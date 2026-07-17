package server

// Observability (Spectrum): a log ring buffer + the Admin → Usage / Logs API.
// No external stack — logs stay in memory (last logRingCap lines, still written
// to stderr for docker logs), usage lives in the usage_events table, and the
// admin console renders both.

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/memory"
)

// ─── log ring buffer ────────────────────────────────────────────────────────────

const logRingCap = 5000

type logRing struct {
	mu    sync.Mutex
	lines []string
	part  string // trailing partial line between Write calls
	// onError, when set, receives lines that look like failures (throttled).
	onError   func(line string)
	lastError time.Time
}

var ring = &logRing{}

func (rb *logRing) Write(p []byte) (int, error) {
	os.Stderr.Write(p) // keep docker logs intact
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.part += string(p)
	for {
		i := strings.IndexByte(rb.part, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(rb.part[:i], "\r")
		rb.part = rb.part[i+1:]
		if line == "" {
			continue
		}
		rb.lines = append(rb.lines, line)
		if len(rb.lines) > logRingCap {
			rb.lines = rb.lines[len(rb.lines)-logRingCap:]
		}
		// Persist failures (throttled to 1/s so a crash loop can't flood the DB).
		if rb.onError != nil && looksLikeError(line) && time.Since(rb.lastError) > time.Second {
			rb.lastError = time.Now()
			go rb.onError(line)
		}
	}
	return len(p), nil
}

func looksLikeError(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "error") || strings.Contains(l, "failed") || strings.Contains(l, "panic")
}

// Tail returns up to n most recent lines containing filter (case-insensitive).
func (rb *logRing) Tail(n int, filter string) []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	f := strings.ToLower(filter)
	out := []string{}
	for i := len(rb.lines) - 1; i >= 0 && len(out) < n; i-- {
		if f == "" || strings.Contains(strings.ToLower(rb.lines[i]), f) {
			out = append(out, rb.lines[i])
		}
	}
	// reverse to chronological order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// installLogRing hooks the ring into the standard logger and wires error
// persistence once the store exists. Call from Start after the store is up.
func (s *Server) installLogRing() {
	ring.onError = func(line string) {
		if ms := s.store(); ms != nil {
			item := ""
			if i := strings.Index(line, "["); i >= 0 {
				if j := strings.Index(line[i:], "]"); j > 0 {
					item = line[i+1 : i+j]
				}
			}
			ms.AddUsage(context.Background(), 0, "", "error_log", item, 1,
				map[string]interface{}{"line": line})
		}
	}
}

// ─── audit helper ────────────────────────────────────────────────────────────────

// audit records an admin/security-relevant action in the usage stream.
func (s *Server) audit(r *http.Request, action string, meta map[string]interface{}) {
	ms := s.store()
	if ms == nil {
		return
	}
	uid := int64(0)
	if u := currentUser(r); u != nil {
		uid = u.ID
	}
	ms.AddUsage(r.Context(), uid, "", "audit", action, 1, meta)
}

// ─── Admin API ───────────────────────────────────────────────────────────────────

// GET /api/admin/logs?limit=500&filter=webex — global admin only.
func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !u.IsGlobalAdmin() {
		writeErr(w, http.StatusForbidden, "global admin only")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	writeJSON(w, map[string]interface{}{
		"lines": ring.Tail(limit, r.URL.Query().Get("filter")),
	})
}

// GET /api/admin/usage?days=7 — aggregated usage + audit trail, global admin only.
func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || !u.IsGlobalAdmin() {
		writeErr(w, http.StatusForbidden, "global admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 7
	}
	ctx := r.Context()
	since := time.Now().AddDate(0, 0, -days)
	day := time.Now().Add(-24 * time.Hour)

	// Opportunistic retention: keep 90 days.
	go ms.PurgeUsageBefore(context.Background(), time.Now().AddDate(0, 0, -90))

	kinds, _ := ms.UsageKindCounts(ctx, since)
	kindsDay, _ := ms.UsageKindCounts(ctx, day)
	models, _ := ms.UsageByItem(ctx, "chat_turn", since, 10)
	tools, _ := ms.UsageByItem(ctx, "tool_call", since, 15)
	channels, _ := ms.UsageByItem(ctx, "channel_msg", since, 5)
	byUserChat, _ := ms.UsageByUser(ctx, "chat_turn", since, 20)
	byUserTools, _ := ms.UsageByUser(ctx, "tool_call", since, 20)
	audit, _ := ms.RecentUsageEvents(ctx, "audit", 30)
	errs, _ := ms.RecentUsageEvents(ctx, "error_log", 20)

	// Resolve user ids to display names for the pane.
	names := map[string]string{}
	if users, err := ms.ListUsers(ctx); err == nil {
		for _, us := range users {
			names[strconv.FormatInt(us.ID, 10)] = us.DisplayName
		}
	}

	writeJSON(w, map[string]interface{}{
		"days": days, "kinds": kinds, "kindsDay": kindsDay,
		"activeDay": ms.ActiveUsers(ctx, day), "activeWeek": ms.ActiveUsers(ctx, time.Now().AddDate(0, 0, -7)),
		"models": nz(models), "tools": nz(tools), "channels": nz(channels),
		"byUserChat": nz(byUserChat), "byUserTools": nz(byUserTools),
		"audit": audit, "errors": errs, "userNames": names,
	})
}

func nz(rows []memory.UsageRow) []memory.UsageRow {
	if rows == nil {
		return []memory.UsageRow{}
	}
	return rows
}
