package memory

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

type HistoryPage struct {
	Entries    []HistoryEntry
	Before     int64
	HasMore    bool
	ToolCalls  json.RawMessage
	ToolOffset int
}

// Cursor pagination never rereads the whole archive. Seed the first tool's
// metadata from its preceding assistant, even when a page splits a tool batch.
func (s *Store) LoadHistoryPage(ctx context.Context, session string, before int64, limit int) (HistoryPage, error) {
	var page HistoryPage
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,role,content,tool_calls,created_at FROM conversation_history WHERE session_id=$1 AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT $3`, session, before, limit+1)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var e HistoryEntry
		if err = rows.Scan(&e.ID, &e.Role, &e.Content, &e.ToolCalls, &e.CreatedAt); err != nil {
			rows.Close()
			return page, err
		}
		page.Entries = append(page.Entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Entries) > limit {
		page.HasMore = true
		page.Entries = page.Entries[:limit]
	}
	slices.Reverse(page.Entries)
	if len(page.Entries) == 0 {
		return page, nil
	}
	page.Before = page.Entries[0].ID
	if page.Entries[0].Role == "tool" {
		var previous int64
		err = s.pool.QueryRow(ctx, `SELECT id,tool_calls FROM conversation_history WHERE session_id=$1 AND id<$2 AND role IN ('assistant','user') ORDER BY id DESC LIMIT 1`, session, page.Before).Scan(&previous, &page.ToolCalls)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return page, err
		}
		if previous > 0 {
			err = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_history WHERE session_id=$1 AND id>$2 AND id<$3 AND role='tool'`, session, previous, page.Before).Scan(&page.ToolOffset)
			if err != nil {
				return page, err
			}
		}
	}
	return page, nil
}
