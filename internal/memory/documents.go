package memory

// Documents: longer, writing-first Markdown documents (the Documents app). Like
// notes but without tags and with full-body editing. Scoped per session, used as
// "global" by the UI so documents are available across workspaces.

import (
	"context"
	"time"
)

type Document struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (s *Store) AddDocument(ctx context.Context, session, title, body string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO documents (session_id, title, body)
		VALUES ($1, $2, $3) RETURNING id
	`, session, title, body).Scan(&id)
	return id, err
}

func (s *Store) ListDocuments(ctx context.Context, session string) ([]Document, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, title, body, created_at, updated_at
		FROM documents WHERE session_id = $1 ORDER BY updated_at DESC
	`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Title, &d.Body, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDocument(ctx context.Context, session string, id int64, title, body string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE documents SET title = $1, body = $2, updated_at = NOW()
		WHERE id = $3 AND session_id = $4
	`, title, body, id, session)
	return err
}

func (s *Store) DeleteDocument(ctx context.Context, session string, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND session_id = $2`, id, session)
	return err
}
