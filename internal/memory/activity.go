package memory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// LoadActivityMessages reads a small user-visible transcript tail. Clip content
// in PostgreSQL and exclude tool payloads and image blobs before transferring
// it: progress checks must not load the full conversation into either agent.
func (s *Store) LoadActivityMessages(ctx context.Context, sessionID string, limit int) ([]HistoryEntry, error) {
	if limit < 1 || limit > 20 {
		limit = 4
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, role, content, created_at FROM (
			SELECT id, role,
				CASE WHEN length(content) > 1000 THEN left(content, 1000) || '…' ELSE content END AS content,
				created_at
			FROM conversation_history
			WHERE session_id = $1 AND role IN ('user', 'assistant', 'error') AND content <> ''
			ORDER BY id DESC LIMIT $2
		) t ORDER BY id ASC
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.Role, &entry.Content, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// LoadRoomActivityMessages excludes tool payloads and reactions before reading
// from PostgreSQL. Full traces remain in the existing room archive.
func (s *Store) LoadRoomActivityMessages(ctx context.Context, groupID int64, limit int) ([]RoomMessage, error) {
	if limit < 1 || limit > 20 {
		limit = 4
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, author_name, content, is_agent, created_at, reply_to, status FROM (
			SELECT id, author_name,
				CASE WHEN length(content) > 1000 THEN left(content, 1000) || '…' ELSE content END AS content,
				is_agent, created_at, reply_to, status
			FROM room_messages WHERE group_id = $1 ORDER BY id DESC LIMIT $2
		) t ORDER BY id ASC
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []RoomMessage
	for rows.Next() {
		message := RoomMessage{GroupID: groupID}
		if err := rows.Scan(&message.ID, &message.AuthorName, &message.Content, &message.IsAgent, &message.CreatedAt, &message.ReplyTo, &message.Status); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) LastRoomResponseStatus(ctx context.Context, groupID int64) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM room_messages WHERE group_id=$1 AND is_agent ORDER BY id DESC LIMIT 1`, groupID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return status, err
}
