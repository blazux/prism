package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SharedItem is a widget or a whole dashboard a member published to one of
// their groups. Payload holds the actual widget content(s):
// {"widgets":[{"title","content","cols","height"}, ...]} — one entry for a
// "widget", many for a "dashboard".
type SharedItem struct {
	ID        int64           `json:"id"`
	GroupID   int64           `json:"groupId"`
	GroupName string          `json:"groupName,omitempty"`
	Kind      string          `json:"kind"` // "widget" | "dashboard"
	Title     string          `json:"title"`
	OwnerID   int64           `json:"ownerId"`
	OwnerName string          `json:"ownerName"`
	Count     int             `json:"count"`             // number of widgets (gallery convenience)
	Payload   json.RawMessage `json:"payload,omitempty"` // omitted from list responses
	CreatedAt time.Time       `json:"createdAt"`
}

func (s *Store) ShareItem(ctx context.Context, it SharedItem) (int64, error) {
	if s == nil {
		return 0, errors.New("no store")
	}
	if len(it.Payload) == 0 {
		it.Payload = json.RawMessage("{}")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shared_items (group_id, kind, title, owner_id, owner_name, payload)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		it.GroupID, it.Kind, it.Title, it.OwnerID, it.OwnerName, it.Payload).Scan(&id)
	return id, err
}

// SharedItemsForGroups lists items shared to any of the given groups, newest
// first, WITHOUT the payload (kept light for the gallery). kind filters to
// "widget"/"dashboard" when non-empty.
func (s *Store) SharedItemsForGroups(ctx context.Context, groupIDs []int64, kind string) ([]SharedItem, error) {
	if s == nil || len(groupIDs) == 0 {
		return nil, nil
	}
	q, args := sharedListQuery(groupIDs, kind)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedItem
	for rows.Next() {
		var it SharedItem
		if err := rows.Scan(&it.ID, &it.GroupID, &it.GroupName, &it.Kind, &it.Title,
			&it.OwnerID, &it.OwnerName, &it.Count, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// sharedListQuery is pure so the parameter indexing is unit-tested.
func sharedListQuery(groupIDs []int64, kind string) (string, []interface{}) {
	args := []interface{}{groupIDs}
	where := "si.group_id = ANY($1)"
	if kind != "" {
		args = append(args, kind)
		where += fmt.Sprintf(" AND si.kind = $%d", len(args))
	}
	q := `SELECT si.id, si.group_id, g.name, si.kind, si.title, si.owner_id, si.owner_name,
		COALESCE(jsonb_array_length(si.payload->'widgets'),0), si.created_at
		FROM shared_items si JOIN groups g ON g.id = si.group_id
		WHERE ` + where + ` ORDER BY si.id DESC`
	return q, args
}

func (s *Store) SharedItemByID(ctx context.Context, id int64) (SharedItem, bool, error) {
	var it SharedItem
	if s == nil {
		return it, false, nil
	}
	err := s.pool.QueryRow(ctx, `
		SELECT id, group_id, kind, title, owner_id, owner_name, payload, created_at
		FROM shared_items WHERE id = $1`, id).Scan(
		&it.ID, &it.GroupID, &it.Kind, &it.Title, &it.OwnerID, &it.OwnerName, &it.Payload, &it.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, false, nil
	}
	return it, err == nil, err
}

func (s *Store) DeleteSharedItem(ctx context.Context, id int64) error {
	if s == nil {
		return errors.New("no store")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM shared_items WHERE id = $1`, id)
	return err
}
