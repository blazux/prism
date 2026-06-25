package memory

// Personal information management: notes, tasks and calendar events. All are
// scoped per session (board) and stored in PostgreSQL alongside the rest of the
// agent's state. Exposed to the agent via tools and to widgets via REST.

import (
	"context"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type Note struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Task struct {
	ID        int64      `json:"id"`
	SessionID string     `json:"sessionId"`
	Title     string     `json:"title"`
	Done      bool       `json:"done"`
	Priority  string     `json:"priority"`
	DueAt     *time.Time `json:"dueAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Event struct {
	ID          int64      `json:"id"`
	SessionID   string     `json:"sessionId"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	StartAt     time.Time  `json:"startAt"`
	EndAt       *time.Time `json:"endAt,omitempty"`
	Location    string     `json:"location"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// ─── Notes ────────────────────────────────────────────────────────────────────

func (s *Store) AddNote(ctx context.Context, session, title, body, tags string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO notes (session_id, title, body, tags)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, session, title, body, tags).Scan(&id)
	return id, err
}

func (s *Store) ListNotes(ctx context.Context, session string) ([]Note, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, title, body, tags, created_at, updated_at
		FROM notes WHERE session_id = $1 ORDER BY updated_at DESC
	`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.SessionID, &n.Title, &n.Body, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UpdateNote(ctx context.Context, session string, id int64, title, body, tags string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE notes SET title = $1, body = $2, tags = $3, updated_at = NOW()
		WHERE id = $4 AND session_id = $5
	`, title, body, tags, id, session)
	return err
}

func (s *Store) DeleteNote(ctx context.Context, session string, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notes WHERE id = $1 AND session_id = $2`, id, session)
	return err
}

// ─── Tasks ────────────────────────────────────────────────────────────────────

func (s *Store) AddTask(ctx context.Context, session, title, priority string, due *time.Time) (int64, error) {
	if priority == "" {
		priority = "normal"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tasks (session_id, title, priority, due_at)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, session, title, priority, due).Scan(&id)
	return id, err
}

func (s *Store) ListTasks(ctx context.Context, session string, includeDone bool) ([]Task, error) {
	q := `SELECT id, session_id, title, done, priority, due_at, created_at
	      FROM tasks WHERE session_id = $1`
	if !includeDone {
		q += ` AND done = FALSE`
	}
	q += ` ORDER BY done ASC, due_at ASC NULLS LAST, created_at ASC`
	rows, err := s.pool.Query(ctx, q, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Title, &t.Done, &t.Priority, &t.DueAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetTaskDone(ctx context.Context, session string, id int64, done bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE tasks SET done = $1 WHERE id = $2 AND session_id = $3`, done, id, session)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, session string, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1 AND session_id = $2`, id, session)
	return err
}

// ─── Calendar ─────────────────────────────────────────────────────────────────

func (s *Store) AddEvent(ctx context.Context, session, title, description, location string, start time.Time, end *time.Time) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO calendar_events (session_id, title, description, location, start_at, end_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
	`, session, title, description, location, start, end).Scan(&id)
	return id, err
}

// ListEvents returns events overlapping [from, to]. Nil bounds are unbounded.
func (s *Store) ListEvents(ctx context.Context, session string, from, to *time.Time) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, title, description, start_at, end_at, location, created_at
		FROM calendar_events
		WHERE session_id = $1
		  AND ($2::timestamptz IS NULL OR COALESCE(end_at, start_at) >= $2)
		  AND ($3::timestamptz IS NULL OR start_at <= $3)
		ORDER BY start_at ASC
	`, session, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Title, &e.Description, &e.StartAt, &e.EndAt, &e.Location, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) DeleteEvent(ctx context.Context, session string, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM calendar_events WHERE id = $1 AND session_id = $2`, id, session)
	return err
}
