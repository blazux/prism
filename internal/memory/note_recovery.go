package memory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// initNoteRecoverySchema follows the base notes schema. Existing notes remain
// active; their first subsequent change captures the previous version.
func (s *Store) initNoteRecoverySchema(ctx context.Context) error {
	for _, q := range []string{
		`ALTER TABLE notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`,
		`ALTER TABLE notes ADD COLUMN IF NOT EXISTS aliases TEXT[] NOT NULL DEFAULT '{}'`,
		`CREATE TABLE IF NOT EXISTS note_versions (id BIGSERIAL PRIMARY KEY, note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE, session_id TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, tags TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS note_versions_scope_idx ON note_versions(session_id,note_id,id DESC)`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

type NoteVersion struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      string    `json:"tags"`
	CreatedAt time.Time `json:"createdAt"`
}

const noteColumns = `id,session_id,title,body,tags,created_at,updated_at,COALESCE(owner_id,0),COALESCE(origin_id,0),aliases,deleted_at`

func scanNote(row pgx.Row) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.SessionID, &n.Title, &n.Body, &n.Tags, &n.CreatedAt, &n.UpdatedAt, &n.OwnerID, &n.OriginID, &n.Aliases, &n.DeletedAt)
	return n, err
}

func captureNote(ctx context.Context, tx pgx.Tx, n Note) error {
	_, err := tx.Exec(ctx, `INSERT INTO note_versions(note_id,session_id,title,body,tags,created_at) VALUES($1,$2,$3,$4,$5,$6)`, n.ID, n.SessionID, n.Title, n.Body, n.Tags, n.UpdatedAt)
	return err
}

func noteAliases(aliases []string, oldTitle, newTitle string) []string {
	if oldTitle == "" || oldTitle == newTitle {
		return aliases
	}
	for _, a := range aliases {
		if a == oldTitle {
			return aliases
		}
	}
	return append(aliases, oldTitle)
}

func (s *Store) NoteHistory(ctx context.Context, session string, id, before int64) ([]NoteVersion, error) {
	if _, err := s.GetNoteIncludingTrash(ctx, session, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,title,tags,created_at FROM note_versions WHERE session_id=$1 AND note_id=$2 AND ($3::bigint=0 OR id<$3) ORDER BY id DESC LIMIT 50`, session, id, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteVersion{}
	for rows.Next() {
		var v NoteVersion
		if err := rows.Scan(&v.ID, &v.Title, &v.Tags, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReadNoteVersion(ctx context.Context, session string, id, version int64) (NoteVersion, error) {
	var v NoteVersion
	err := s.pool.QueryRow(ctx, `SELECT id,title,body,tags,created_at FROM note_versions WHERE session_id=$1 AND note_id=$2 AND id=$3`, session, id, version).Scan(&v.ID, &v.Title, &v.Body, &v.Tags, &v.CreatedAt)
	return v, err
}

func (s *Store) GetNoteIncludingTrash(ctx context.Context, session string, id int64) (Note, error) {
	return scanNote(s.pool.QueryRow(ctx, `SELECT `+noteColumns+` FROM notes WHERE id=$1 AND session_id=$2`, id, session))
}

func (s *Store) TrashedNotes(ctx context.Context, session string) ([]Note, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,session_id,title,'' AS body,tags,created_at,updated_at,COALESCE(owner_id,0),COALESCE(origin_id,0),aliases,deleted_at FROM notes WHERE session_id=$1 AND deleted_at IS NOT NULL ORDER BY deleted_at DESC`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RestoreNote either restores the deleted current note or a historical version.
// Restoring a version first preserves the present body as another version.
func (s *Store) RestoreNote(ctx context.Context, session string, id, version int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	n, err := scanNote(tx.QueryRow(ctx, `SELECT `+noteColumns+` FROM notes WHERE session_id=$1 AND id=$2 FOR UPDATE`, session, id))
	if err != nil {
		return err
	}
	title, body, tags := n.Title, n.Body, n.Tags
	if version > 0 {
		err = tx.QueryRow(ctx, `SELECT title,body,tags FROM note_versions WHERE id=$1 AND note_id=$2 AND session_id=$3`, version, id, session).Scan(&title, &body, &tags)
		if err != nil {
			return err
		}
	}
	if n.DeletedAt == nil && title == n.Title && body == n.Body && tags == n.Tags {
		return tx.Commit(ctx)
	}
	if err := captureNote(ctx, tx, n); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE notes SET title=$1,body=$2,tags=$3,aliases=$4,deleted_at=NULL,updated_at=NOW() WHERE id=$5 AND session_id=$6`, title, body, tags, noteAliases(n.Aliases, n.Title, title), id, session)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) updateNote(ctx context.Context, session string, id int64, title, body, tags string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	n, err := scanNote(tx.QueryRow(ctx, `SELECT `+noteColumns+` FROM notes WHERE session_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, session, id))
	if err != nil {
		return err
	}
	if n.Title == title && n.Body == body && n.Tags == tags {
		return tx.Commit(ctx)
	}
	if err := captureNote(ctx, tx, n); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE notes SET title=$1,body=$2,tags=$3,aliases=$4,updated_at=NOW() WHERE id=$5 AND session_id=$6`, title, body, tags, noteAliases(n.Aliases, n.Title, title), id, session)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) trashNote(ctx context.Context, session string, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	n, err := scanNote(tx.QueryRow(ctx, `SELECT `+noteColumns+` FROM notes WHERE session_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, session, id))
	if err != nil {
		return err
	}
	if err := captureNote(ctx, tx, n); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE notes SET deleted_at=NOW(),updated_at=NOW() WHERE session_id=$1 AND id=$2`, session, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
