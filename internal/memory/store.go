package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"prism/internal/ollama"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	KeyPersonality = "system_prompt_personality"
)

// HistoryEntry is one row from conversation_history.
type HistoryEntry struct {
	ID        int64
	Role      string
	Content   string
	ToolCalls json.RawMessage
	CreatedAt time.Time
}

type Store struct {
	pool   *pgxpool.Pool
	encKey []byte // AES-256 key for encrypting secret values
}

func NewStore(ctx context.Context, connStr string, encKey []byte) (*Store, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	s := &Store{pool: pool, encKey: encKey}
	if err := s.initSchema(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`INSERT INTO sessions (id, name) VALUES ('default', 'Main') ON CONFLICT DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS agent_config (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS conversation_history (
			id         BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT 'default',
			role       TEXT NOT NULL,
			content    TEXT NOT NULL DEFAULT '',
			tool_calls JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS conv_history_session_idx ON conversation_history(session_id, id)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id         BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT 'default',
			title      TEXT NOT NULL,
			message    TEXT NOT NULL DEFAULT '',
			level      TEXT NOT NULL DEFAULT 'info',
			read       BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS notifications_session_idx ON notifications(session_id, id DESC)`,
		`CREATE TABLE IF NOT EXISTS secrets (
			name       TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id          TEXT NOT NULL,
			session_id  TEXT NOT NULL,
			name        TEXT NOT NULL,
			url         TEXT NOT NULL,
			auth_secret TEXT NOT NULL DEFAULT '',
			enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			tools_json  JSONB NOT NULL DEFAULT '[]',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (session_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS mcp_servers_session_idx ON mcp_servers(session_id)`,
		`CREATE TABLE IF NOT EXISTS notes (
			id         BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT 'default',
			title      TEXT NOT NULL DEFAULT '',
			body       TEXT NOT NULL DEFAULT '',
			tags       TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS notes_session_idx ON notes(session_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id         BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT 'default',
			title      TEXT NOT NULL,
			done       BOOLEAN NOT NULL DEFAULT FALSE,
			priority   TEXT NOT NULL DEFAULT 'normal',
			due_at     TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS tasks_session_idx ON tasks(session_id, done, due_at)`,
		`CREATE TABLE IF NOT EXISTS calendar_events (
			id          BIGSERIAL PRIMARY KEY,
			session_id  TEXT NOT NULL DEFAULT 'default',
			title       TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			start_at    TIMESTAMPTZ NOT NULL,
			end_at      TIMESTAMPTZ,
			location    TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS calendar_session_idx ON calendar_events(session_id, start_at)`,
		`CREATE TABLE IF NOT EXISTS documents (
			id         BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT 'default',
			title      TEXT NOT NULL DEFAULT '',
			body       TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS documents_session_idx ON documents(session_id, updated_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}
	return nil
}

// ─── Notifications ────────────────────────────────────────────────────────────

type Notification struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Level     string    `json:"level"` // info | success | warning | error
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) AddNotification(ctx context.Context, sessionID, title, message, level string) (int64, error) {
	if level == "" {
		level = "info"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO notifications (session_id, title, message, level)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, sessionID, title, message, level).Scan(&id)
	return id, err
}

func (s *Store) GetNotificationsAfter(ctx context.Context, sessionID string, afterID int64) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, title, message, level, read, created_at
		FROM notifications
		WHERE session_id = $1 AND id > $2
		ORDER BY id ASC
	`, sessionID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notifs []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.SessionID, &n.Title, &n.Message, &n.Level, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		notifs = append(notifs, n)
	}
	return notifs, rows.Err()
}

func (s *Store) GetRecentNotifications(ctx context.Context, sessionID string, limit int) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, title, message, level, read, created_at
		FROM notifications
		WHERE session_id = $1
		ORDER BY id DESC LIMIT $2
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notifs []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.SessionID, &n.Title, &n.Message, &n.Level, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		notifs = append(notifs, n)
	}
	// Return in chronological order
	for i, j := 0, len(notifs)-1; i < j; i, j = i+1, j-1 {
		notifs[i], notifs[j] = notifs[j], notifs[i]
	}
	return notifs, rows.Err()
}

func (s *Store) MarkNotificationsRead(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET read = TRUE WHERE session_id = $1`, sessionID)
	return err
}

func (s *Store) DeleteNotification(ctx context.Context, sessionID string, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notifications WHERE id = $1 AND session_id = $2`, id, sessionID)
	return err
}

// ─── Session management ───────────────────────────────────────────────────────

type Session struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, created_at FROM sessions ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.Name, &sess.CreatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// EnsureSession creates the session row if it doesn't exist, without touching the name if it does.
func (s *Store) EnsureSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name) VALUES ($1, $1)
		ON CONFLICT DO NOTHING
	`, id)
	return err
}

func (s *Store) UpsertSession(ctx context.Context, id, name string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name) VALUES ($1, $2)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, id, name)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if id == "default" {
		return fmt.Errorf("cannot delete the default session")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM conversation_history WHERE session_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_config WHERE key LIKE $1`, "%_"+id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mcp_servers WHERE session_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetConfig retrieves a config value by key. Returns ("", false, nil) if not found.
func (s *Store) GetConfig(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM agent_config WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SetConfig upserts a config value.
func (s *Store) SetConfig(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_config (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, key, value)
	return err
}

// LoadHistory returns all messages for a session in insertion order.
func (s *Store) LoadHistory(ctx context.Context, sessionID string) ([]HistoryEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, role, content, tool_calls, created_at
		FROM conversation_history
		WHERE session_id = $1
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.Role, &e.Content, &e.ToolCalls, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// AppendMessage inserts a single message into conversation_history.
func (s *Store) AppendMessage(ctx context.Context, sessionID, role, content string, toolCalls json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO conversation_history (session_id, role, content, tool_calls)
		VALUES ($1, $2, $3, $4)
	`, sessionID, role, content, toolCalls)
	return err
}

// ClearHistory deletes all messages and the summary for a session.
func (s *Store) ClearHistory(ctx context.Context, sessionID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM conversation_history WHERE session_id = $1`, sessionID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_config WHERE key = $1`, summaryKey(sessionID))
	return err
}

// GetSummary returns the accumulated conversation summary for a session.
func (s *Store) GetSummary(ctx context.Context, sessionID string) string {
	var value string
	_ = s.pool.QueryRow(ctx, `SELECT value FROM agent_config WHERE key = $1`, summaryKey(sessionID)).Scan(&value)
	return value
}

// MaybeSummarize summarizes old history if user+assistant message count exceeds maxMessages.
// It keeps the keepRecent most recent messages and summarizes the rest.
// Runs the LLM call with the provided client and model.
func (s *Store) MaybeSummarize(ctx context.Context, sessionID string, ollamaClient ollama.Backend, model string, maxMessages, keepRecent int) {
	var count int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM conversation_history
		WHERE session_id = $1 AND role IN ('user', 'assistant')
	`, sessionID).Scan(&count); err != nil || count <= maxMessages {
		return
	}

	// Fetch all messages oldest-first
	rows, err := s.pool.Query(ctx, `
		SELECT id, role, content FROM conversation_history
		WHERE session_id = $1
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		log.Printf("[memory] MaybeSummarize query: %v", err)
		return
	}

	type row struct {
		id      int64
		role    string
		content string
	}
	var msgs []row
	for rows.Next() {
		var m row
		if err := rows.Scan(&m.id, &m.role, &m.content); err != nil {
			rows.Close()
			log.Printf("[memory] MaybeSummarize scan: %v", err)
			return
		}
		msgs = append(msgs, m)
	}
	rows.Close()

	if len(msgs) <= keepRecent {
		return
	}

	toSummarize := msgs[:len(msgs)-keepRecent]

	// Consolidation prompt: instead of appending a fresh summary to the old one
	// (which lets obsolete facts pile up forever), we feed the existing running
	// summary back in and ask the model to rewrite a single, up-to-date summary
	// that reflects the CURRENT state — dropping anything later messages have
	// undone. This keeps the summary bounded and reconciled with reality.
	existing := s.GetSummary(ctx, sessionID)

	var sb strings.Builder
	sb.WriteString("You maintain a running summary of an ongoing conversation between a user and an AI assistant. ")
	sb.WriteString("Produce an updated, self-contained summary that reflects the CURRENT state of things.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Integrate the new messages into the existing summary. Do NOT simply append them.\n")
	sb.WriteString("- If newer messages show something was undone, deleted, renamed, completed or changed, UPDATE or REMOVE the now-obsolete fact. The summary must describe what is true now, not the full history.\n")
	sb.WriteString("- Preserve durable facts, decisions, user preferences and still-pending tasks.\n")
	sb.WriteString("- Stay concise and factual. Output ONLY the summary text, with no preamble or commentary.\n\n")
	if existing != "" {
		sb.WriteString("=== Current running summary ===\n")
		sb.WriteString(existing)
		sb.WriteString("\n\n")
	}
	sb.WriteString("=== New messages to integrate ===\n")
	for _, m := range toSummarize {
		if m.role == "tool" {
			continue
		}
		sb.WriteString(m.role)
		sb.WriteString(": ")
		content := m.content
		if len(content) > 600 {
			content = content[:600] + "..."
		}
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	ch := make(chan ollama.StreamEvent, 200)
	req := ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "user", Content: sb.String()},
		},
	}
	go func() {
		ollamaClient.Chat(ctx, req, ch)
		close(ch)
	}()

	var summary strings.Builder
	for ev := range ch {
		if ev.Err != nil {
			log.Printf("[memory] summarize LLM error: %v", ev.Err)
			return
		}
		summary.WriteString(ev.Content)
	}
	summaryText := stripThinking(strings.TrimSpace(summary.String()))
	if summaryText == "" {
		return
	}

	// Collect IDs to delete
	ids := make([]int64, len(toSummarize))
	for i, m := range toSummarize {
		ids[i] = m.id
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("[memory] summarize begin tx: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM conversation_history WHERE id = ANY($1)`, ids); err != nil {
		log.Printf("[memory] summarize delete: %v", err)
		return
	}

	// Replace (not append): the consolidation prompt already folded the previous
	// summary into summaryText, so the stored summary stays bounded and current.
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_config (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, summaryKey(sessionID), summaryText); err != nil {
		log.Printf("[memory] summarize upsert summary: %v", err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[memory] summarize commit: %v", err)
		return
	}

	log.Printf("[memory] summarized %d messages for session %q", len(toSummarize), sessionID)
}

// stripThinking removes <think>…</think> (and <thinking>/<thought>) blocks from
// a model's output, so reasoning a model inlines into content never leaks into a
// stored summary. Mirrors the agent's stripThinkingBlocks. Unclosed tags drop
// everything from the opening tag onward.
func stripThinking(s string) string {
	for _, tag := range []string{"think", "thinking", "thought"} {
		open, close := "<"+tag+">", "</"+tag+">"
		for {
			start := strings.Index(s, open)
			if start < 0 {
				break
			}
			end := strings.Index(s[start:], close)
			if end < 0 {
				s = s[:start] // unclosed: drop the trailing reasoning
				break
			}
			s = s[:start] + s[start+end+len(close):]
		}
	}
	return strings.TrimSpace(s)
}

func summaryKey(sessionID string) string {
	return "conversation_summary_" + sessionID
}

// ─── Secrets ──────────────────────────────────────────────────────────────────

func (s *Store) SetSecret(ctx context.Context, name, value string) error {
	encrypted, err := encryptValue(s.encKey, value)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO secrets (name, value, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, created_at = NOW()
	`, name, encrypted)
	return err
}

func (s *Store) GetSecret(ctx context.Context, name string) (string, bool, error) {
	var encrypted string
	err := s.pool.QueryRow(ctx, `SELECT value FROM secrets WHERE name = $1`, name).Scan(&encrypted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	value, err := decryptValue(s.encKey, encrypted)
	if err != nil {
		return "", false, fmt.Errorf("decrypt secret %q: %w", name, err)
	}
	return value, true, nil
}

func (s *Store) ListSecretNames(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name FROM secrets ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func (s *Store) DeleteSecret(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM secrets WHERE name = $1`, name)
	return err
}

func (s *Store) GetAllSecrets(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, value FROM secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var name, encrypted string
		if err := rows.Scan(&name, &encrypted); err != nil {
			return nil, err
		}
		value, err := decryptValue(s.encKey, encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %q: %w", name, err)
		}
		result[name] = value
	}
	return result, rows.Err()
}

// ─── MCP servers ──────────────────────────────────────────────────────────────

// MCPServerRow is the raw DB record for an MCP server configuration.
type MCPServerRow struct {
	ID         string
	SessionID  string
	Name       string
	URL        string
	AuthSecret string
	Enabled    bool
	ToolsJSON  []byte
	CreatedAt  time.Time
}

func (s *Store) MCPUpsertServer(ctx context.Context, sessionID, id, name, url, authSecret string, enabled bool, toolsJSON []byte) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mcp_servers (id, session_id, name, url, auth_secret, enabled, tools_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (session_id, id) DO UPDATE
		SET name        = EXCLUDED.name,
		    url         = EXCLUDED.url,
		    auth_secret = EXCLUDED.auth_secret,
		    enabled     = EXCLUDED.enabled,
		    tools_json  = EXCLUDED.tools_json,
		    created_at  = NOW()
	`, id, sessionID, name, url, authSecret, enabled, toolsJSON)
	return err
}

func (s *Store) MCPDeleteServer(ctx context.Context, sessionID, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM mcp_servers WHERE session_id = $1 AND id = $2`, sessionID, id)
	return err
}

func (s *Store) MCPListServers(ctx context.Context, sessionID string) ([]MCPServerRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, name, url, auth_secret, enabled, tools_json, created_at
		FROM mcp_servers
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MCPServerRow
	for rows.Next() {
		var r MCPServerRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Name, &r.URL, &r.AuthSecret, &r.Enabled, &r.ToolsJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) MCPSetEnabled(ctx context.Context, sessionID, id string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mcp_servers SET enabled = $1 WHERE session_id = $2 AND id = $3
	`, enabled, sessionID, id)
	return err
}
