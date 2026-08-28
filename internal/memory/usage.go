package memory

// Usage tracking + audit trail. One narrow event stream in Postgres; writes are
// fire-and-forget (a lost event must never break a chat turn), reads aggregate
// for the Admin → Usage pane. Retention is enforced opportunistically.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var sessionOwnerRe = regexp.MustCompile(`^u(\d+)-`)

// SessionOwnerID extracts the owning user id from a namespaced session
// ("u3-default" → 3), or 0 for un-namespaced (legacy / shared agents).
func SessionOwnerID(session string) int64 {
	m := sessionOwnerRe.FindStringSubmatch(session)
	if m == nil {
		return 0
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id
}

// AddUsage records one event. userID 0 = derive from the session prefix.
// Errors are swallowed by design — telemetry must never break the feature path.
func (s *Store) AddUsage(ctx context.Context, userID int64, session, kind, item string, qty int64, meta map[string]interface{}) {
	if s == nil {
		return
	}
	if userID == 0 {
		userID = SessionOwnerID(session)
	}
	if qty <= 0 {
		qty = 1
	}
	mj := []byte("{}")
	if len(meta) > 0 {
		if b, err := json.Marshal(meta); err == nil {
			mj = b
		}
	}
	s.pool.Exec(ctx, `
		INSERT INTO usage_events (user_id, session, kind, item, qty, meta)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, session, kind, item, qty, mj)
}

// PurgeUsageBefore drops events older than the cutoff (retention).
func (s *Store) PurgeUsageBefore(ctx context.Context, cutoff time.Time) {
	s.pool.Exec(ctx, `DELETE FROM usage_events WHERE ts < $1`, cutoff)
}

// ─── aggregates for the Admin → Usage pane ──────────────────────────────────────

type UsageRow struct {
	Key string `json:"key"`
	N   int64  `json:"n"`
	Qty int64  `json:"qty"`
}

// usageBy aggregates event counts and quantities over a window, grouped by a
// safe column expression (callers pass constants only).
func (s *Store) usageBy(ctx context.Context, group, kind string, since time.Time, limit int) ([]UsageRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+group+` AS k, COUNT(*), COALESCE(SUM(qty),0)
		FROM usage_events WHERE kind = $1 AND ts >= $2
		GROUP BY k ORDER BY COUNT(*) DESC LIMIT $3
	`, kind, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Key, &r.N, &r.Qty); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UsageByItem(ctx context.Context, kind string, since time.Time, limit int) ([]UsageRow, error) {
	return s.usageBy(ctx, "item", kind, since, limit)
}

func (s *Store) UsageByUser(ctx context.Context, kind string, since time.Time, limit int) ([]UsageRow, error) {
	return s.usageBy(ctx, "user_id::text", kind, since, limit)
}

// UsageKindCounts returns event counts per kind since the cutoff.
func (s *Store) UsageKindCounts(ctx context.Context, since time.Time) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, COUNT(*) FROM usage_events WHERE ts >= $1 GROUP BY kind
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ActiveUsers counts distinct users with any activity since the cutoff.
func (s *Store) ActiveUsers(ctx context.Context, since time.Time) int64 {
	var n int64
	s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT user_id) FROM usage_events WHERE ts >= $1 AND user_id > 0
	`, since).Scan(&n)
	return n
}

// UsageEvent is one raw event (audit / error-log listing).
type UsageEvent struct {
	ID      int64           `json:"id"`
	TS      time.Time       `json:"ts"`
	UserID  int64           `json:"userId"`
	Session string          `json:"session"`
	Kind    string          `json:"kind"`
	Item    string          `json:"item"`
	Qty     int64           `json:"qty"`
	Meta    json.RawMessage `json:"meta"`
}

// RecentUsageEvents lists the latest events of a kind (audit trail, error log).
func (s *Store) RecentUsageEvents(ctx context.Context, kind string, limit int) ([]UsageEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, user_id, session, kind, item, qty, meta
		FROM usage_events WHERE kind = $1 ORDER BY id DESC LIMIT $2
	`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageEvents(rows)
}

func scanUsageEvents(rows pgx.Rows) ([]UsageEvent, error) {
	var out []UsageEvent
	for rows.Next() {
		var e UsageEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.UserID, &e.Session, &e.Kind, &e.Item, &e.Qty, &e.Meta); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ActivityFilter selects a slice of the activity feed. Zero value = the
// default human-relevant kinds, newest first, all users.
type ActivityFilter struct {
	Kinds   []string // empty → defaultActivityKinds
	Items   []string // empty → any item (e.g. ["tool_error","tool_denied"] for the errors view)
	UserID  int64    // >0 restricts to one user's events (a member's own feed); 0 = everyone
	Session string   // non-empty restricts to one session/board
	Before  int64    // keyset cursor: only events with id < Before (0 = latest)
	Limit   int
}

// defaultActivityKinds is what "what did my agent do" means: tool calls, the
// audit trail (tool_error / tool_denied / server actions), chat turns and
// webhook fires. Aggregate counters and internal bookkeeping stay out.
var defaultActivityKinds = []string{"tool_call", "audit", "chat_turn", "webhook"}

// ActivityFeed returns a chronological (newest-first) slice of usage_events for
// the human-facing activity log, with keyset pagination by id.
func (s *Store) ActivityFeed(ctx context.Context, f ActivityFilter) ([]UsageEvent, error) {
	if s == nil {
		return nil, nil
	}
	q, args := buildActivityQuery(f)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageEvents(rows)
}

// buildActivityQuery assembles the parameterised SQL for ActivityFeed. Pure, so
// the $N argument indexing (the part that actually breaks) is unit-tested.
func buildActivityQuery(f ActivityFilter) (string, []interface{}) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	kinds := f.Kinds
	if len(kinds) == 0 {
		kinds = defaultActivityKinds
	}
	args := []interface{}{kinds}
	where := "kind = ANY($1)"
	if len(f.Items) > 0 {
		args = append(args, f.Items)
		where += fmt.Sprintf(" AND item = ANY($%d)", len(args))
	}
	if f.UserID > 0 {
		args = append(args, f.UserID)
		where += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if f.Session != "" {
		args = append(args, f.Session)
		where += fmt.Sprintf(" AND session = $%d", len(args))
	}
	if f.Before > 0 {
		args = append(args, f.Before)
		where += fmt.Sprintf(" AND id < $%d", len(args))
	}
	args = append(args, f.Limit)
	q := fmt.Sprintf(`SELECT id, ts, user_id, session, kind, item, qty, meta
		FROM usage_events WHERE %s ORDER BY id DESC LIMIT $%d`, where, len(args))
	return q, args
}
