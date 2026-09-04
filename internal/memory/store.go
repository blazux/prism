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
	KeyPersonality     = "system_prompt_personality"
	KeyAgentName       = "agent_name"
	KeyPersonalityBase = "system_prompt_personality_base" // global base persona, layered under every session
	// Turn budget of the caller's agent (Settings › Agent). Both re-read each
	// turn by Agent.loadProfile, scoped like agent_name in multi-user mode.
	KeyAgentMaxIterations   = "agent_max_iterations"   // integer; empty = built-in default
	KeyAgentThinking        = "agent_thinking"         // "on" / "off"; empty = on
	KeyAgentLeanPrompt      = "agent_lean_prompt"      // "on" / "off"; empty = off (guided profile)
	KeyAgentReasoningEffort = "agent_reasoning_effort" // "low"/"medium"/"high"/"xhigh"; empty = server default (OPENAI_REASONING_EFFORT)
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
	// cfgScope, when set ("u<id>:"), prefixes config keys and secret names so
	// external integrations (email, calendar, vault, OAuth…) are per-user instead
	// of deployment-global. Obtained via ConfigScope; zero value = legacy global.
	cfgScope string
}

// ConfigScope returns a view of the store whose GetConfig/SetConfig and
// GetSecret/SetSecret/DeleteSecret operate under "<scope>:" — per-user (or
// per-group) isolation of integration settings with no changes to provider
// code, which simply receives the scoped view. Empty or "global" scope returns
// the store unchanged (legacy single-user behaviour).
func (s *Store) ConfigScope(scope string) *Store {
	if s == nil || scope == "" || scope == "global" {
		return s
	}
	c := *s
	c.cfgScope = scope + ":"
	return &c
}

// NewStore opens the pool, applies the schema and runs the one-off migrations.
//
// multiUser gates migrateUserScopedConfig, and that gate matters: the migration
// RENAMES this deployment's config keys and secrets to "u<id>:…" the moment a
// global admin exists. A personal Prism must never run it — its own guard (no
// admin ⇒ no-op) would hold today, but one MULTI_USER=1 boot and one signup would
// be enough to move email_password out from under the single-user build, which
// reads unprefixed keys and would silently see nothing.
func NewStore(ctx context.Context, connStr string, encKey []byte, multiUser bool) (*Store, error) {
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
	s.migratePersonalityBase(ctx)
	if multiUser {
		s.migrateUserScopedConfig(ctx)
	}
	return s, nil
}

// migrateUserScopedConfig moves legacy single-user integration settings (email,
// calendar providers, OAuth, notes vault, agent identity) under the first global
// admin's scope ("u<id>:key"), matching the per-user ConfigScope reads that
// replaced the global ones. Runs until an admin exists, then never again; each
// move is guarded so it never overwrites an already-scoped value.
func (s *Store) migrateUserScopedConfig(ctx context.Context) {
	if _, ok, _ := s.GetConfig(ctx, "user_scoped_config_migrated_v2"); ok {
		return
	}
	var adminID int64
	err := s.pool.QueryRow(ctx, `SELECT MIN(id) FROM users WHERE role = 'global_admin'`).Scan(&adminID)
	if err != nil || adminID == 0 {
		return // no admin yet (fresh install / no-DB mode) — retry next boot
	}
	prefix := fmt.Sprintf("u%d:", adminID)
	cfgKeys := []string{
		"email_config", "notes_provider", "notes_vault_path",
		"caldav_config", "tasks_provider", "calendar_provider",
		"agent_name", KeyPersonalityBase, KeyAgentMaxIterations, KeyAgentThinking, KeyAgentLeanPrompt, KeyAgentReasoningEffort,
		"oauth_google_client_id", "oauth_microsoft_client_id",
		"telegram_allowed_chat",
	}
	for _, k := range cfgKeys {
		s.pool.Exec(ctx, `
			UPDATE agent_config SET key = $1 WHERE key = $2
			AND NOT EXISTS (SELECT 1 FROM agent_config WHERE key = $1)
		`, prefix+k, k)
	}
	secretNames := []string{
		"email_password", "todoist_token", "caldav_password",
		"oauth_google_client_secret", "oauth_google_token",
		"oauth_microsoft_client_secret", "oauth_microsoft_token",
		"telegram_bot_token",
	}
	for _, n := range secretNames {
		s.pool.Exec(ctx, `
			UPDATE secrets SET name = $1 WHERE name = $2
			AND NOT EXISTS (SELECT 1 FROM secrets WHERE name = $1)
		`, prefix+n, n)
	}
	s.SetConfig(ctx, "user_scoped_config_migrated_v2", "1")
}

// migratePersonalityBase moves the old "default = base" personality into the
// dedicated base key, so the default workspace becomes a normal workspace whose
// own adaptation layers on top of the shared base (like every other workspace).
// Runs once; guarded by a flag config key.
func (s *Store) migratePersonalityBase(ctx context.Context) {
	if _, ok, _ := s.GetConfig(ctx, "personality_base_migrated"); ok {
		return
	}
	old := ""
	if v, ok, _ := s.GetConfig(ctx, KeyPersonality+"_default"); ok && v != "" {
		old = v
	} else if v, ok, _ := s.GetConfig(ctx, KeyPersonality); ok && v != "" {
		old = v
	}
	if old != "" {
		s.SetConfig(ctx, KeyPersonalityBase, old)
		// Clear the default session's own value so it isn't applied twice.
		s.SetConfig(ctx, KeyPersonality+"_default", "")
	}
	s.SetConfig(ctx, "personality_base_migrated", "1")
}

// HistoryHit is one match from a cross-session conversation search.
type HistoryHit struct {
	SessionID string    `json:"sessionId"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

// SearchHistory full-text searches user/assistant messages across all sessions.
func (s *Store) SearchHistory(ctx context.Context, query string, limit int) ([]HistoryHit, error) {
	if limit <= 0 || limit > 50 {
		limit = 15
	}
	rows, err := s.pool.Query(ctx, `
		SELECT session_id, role, content, created_at
		FROM conversation_history
		WHERE role IN ('user', 'assistant')
		  AND content_tsv @@ websearch_to_tsquery('simple', $1)
		ORDER BY ts_rank(content_tsv, websearch_to_tsquery('simple', $1)) DESC, created_at DESC
		LIMIT $2
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryHit
	for rows.Next() {
		var h HistoryHit
		if err := rows.Scan(&h.SessionID, &h.Role, &h.Content, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
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
		// Multi-user (Phase 3): each session belongs to a user. NULL owner =
		// legacy/shared session (pre-migration, or the single-user default).
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS owner_id BIGINT`,
		`CREATE INDEX IF NOT EXISTS sessions_owner_idx ON sessions(owner_id)`,
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
		// Full-text search across all conversations (cross-session recall).
		`ALTER TABLE conversation_history ADD COLUMN IF NOT EXISTS content_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED`,
		`CREATE INDEX IF NOT EXISTS conv_history_tsv_idx ON conversation_history USING GIN(content_tsv)`,
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
		// Group-shared notes (Spectrum): a note shared to a group is a copy stored
		// under the group scope ("g<id>"). owner_id is the sharer (edit/delete rights),
		// origin_id links back to the author's personal note so re-sharing updates it.
		`ALTER TABLE notes ADD COLUMN IF NOT EXISTS owner_id BIGINT`,
		`ALTER TABLE notes ADD COLUMN IF NOT EXISTS origin_id BIGINT`,
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
		// ─── Multi-user identity (Prism heavy) ──────────────────────────────────
		// Users authenticate with email + bcrypt password. The first user to sign
		// up becomes the global admin (auto-approved); everyone after is 'pending'
		// until an admin approves them.
		`CREATE TABLE IF NOT EXISTS users (
			id            BIGSERIAL PRIMARY KEY,
			email         TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			display_name  TEXT NOT NULL DEFAULT '',
			role          TEXT NOT NULL DEFAULT 'member',   -- 'global_admin' | 'member'
			status        TEXT NOT NULL DEFAULT 'pending',  -- 'pending' | 'approved' | 'disabled'
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS groups (
			id         BIGSERIAL PRIMARY KEY,
			name       TEXT UNIQUE NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS group_members (
			group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			group_role TEXT NOT NULL DEFAULT 'member',       -- 'admin' | 'member'
			PRIMARY KEY (group_id, user_id)
		)`,
		// Server-side sessions: the cookie holds an opaque random token that maps
		// to a user here, so logout / disable revokes access immediately.
		`CREATE TABLE IF NOT EXISTS user_sessions (
			token      TEXT PRIMARY KEY,
			user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS user_sessions_user_idx ON user_sessions(user_id)`,
		// ─── RBAC (Prism heavy, Phase 2) ────────────────────────────────────────
		// Global per-tool policy. A row overrides the code default; tools with no
		// row use the built-in default (dangerous tools admin-only, rest open).
		`CREATE TABLE IF NOT EXISTS tool_policy (
			tool   TEXT PRIMARY KEY,
			access TEXT NOT NULL           -- 'open' | 'admin_only'
		)`,
		// Per-group model grants. A group with zero rows = all models allowed;
		// adding rows restricts that group to the listed models.
		`CREATE TABLE IF NOT EXISTS group_models (
			group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			model    TEXT NOT NULL,
			PRIMARY KEY (group_id, model)
		)`,
		// ─── Shared chat rooms (Prism heavy, Phase 4) ───────────────────────────
		// One shared room per group: members chat with each other and with a
		// shared agent that responds when @mentioned. The agent's prompt/model are
		// configured per group (Phase 5).
		`CREATE TABLE IF NOT EXISTS room_config (
			group_id     BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
			agent_name   TEXT NOT NULL DEFAULT 'Assistant',
			agent_prompt TEXT NOT NULL DEFAULT '',
			agent_model  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS room_messages (
			id          BIGSERIAL PRIMARY KEY,
			group_id    BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			author_id   BIGINT,        -- NULL for the agent
			author_name TEXT NOT NULL,
			content     TEXT NOT NULL,
			is_agent    BOOLEAN NOT NULL DEFAULT FALSE,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS room_messages_group_idx ON room_messages(group_id, id)`,
		// Per-group tool restrictions (Phase 5). A group admin may only *tighten*
		// within the global ceiling, so rows here always mean 'admin_only' for that
		// group's members; removing a row reverts to the global policy.
		`CREATE TABLE IF NOT EXISTS group_tool_policy (
			group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			tool     TEXT NOT NULL,
			access   TEXT NOT NULL,   -- 'admin_only' (restrict)
			PRIMARY KEY (group_id, tool)
		)`,
		// ─── Profiles, avatars & richer room chat (Spectrum) ───────────────────
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS first_name TEXT NOT NULL DEFAULT ''`,
		// Shared-agent turn budget, set by the group admin (Admin › Shared agent).
		`ALTER TABLE room_config ADD COLUMN IF NOT EXISTS agent_max_iter INT NOT NULL DEFAULT 0`,
		`ALTER TABLE room_config ADD COLUMN IF NOT EXISTS agent_thinking BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE room_config ADD COLUMN IF NOT EXISTS agent_lean BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE room_config ADD COLUMN IF NOT EXISTS agent_reasoning TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_name  TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS phone      TEXT NOT NULL DEFAULT ''`,
		// Avatars for users and agents, keyed by scope: "u<id>" (user),
		// "agent-u<id>" (a user's personal agent), "agent-g<id>" (a group's shared
		// agent). Stored as bytes + mime; served via /api/avatar, versioned by updated_at.
		`CREATE TABLE IF NOT EXISTS avatars (
			scope      TEXT PRIMARY KEY,
			mime       TEXT NOT NULL DEFAULT 'image/png',
			data       BYTEA NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Usage tracking + audit trail (Spectrum): one narrow event stream.
		// kind: chat_turn | tool_call | rag_upload | channel_msg | login | audit | error_log
		// item: model / tool name / channel / action. qty: count or est. tokens.
		`CREATE TABLE IF NOT EXISTS usage_events (
			id      BIGSERIAL PRIMARY KEY,
			ts      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			user_id BIGINT NOT NULL DEFAULT 0,
			session TEXT NOT NULL DEFAULT '',
			kind    TEXT NOT NULL,
			item    TEXT NOT NULL DEFAULT '',
			qty     BIGINT NOT NULL DEFAULT 1,
			meta    JSONB NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS usage_events_ts_idx ON usage_events(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS usage_events_kind_idx ON usage_events(kind, ts DESC)`,
		// Shared widgets/dashboards: a group member publishes a widget (or a whole
		// board) to their group; other members add it to their own dashboard.
		`CREATE TABLE IF NOT EXISTS shared_items (
			id         BIGSERIAL PRIMARY KEY,
			group_id   BIGINT NOT NULL,
			kind       TEXT NOT NULL,
			title      TEXT NOT NULL DEFAULT '',
			owner_id   BIGINT NOT NULL DEFAULT 0,
			owner_name TEXT NOT NULL DEFAULT '',
			payload    JSONB NOT NULL DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS shared_items_group_idx ON shared_items(group_id, id DESC)`,
		// Inbound webhooks: an external HTTP call becomes an agent turn wrapped in a
		// stored prompt. The id is the URL segment and must be unique on its own —
		// the inbound request cannot tell us which scope to look in.
		`CREATE TABLE IF NOT EXISTS webhooks (
			id           TEXT PRIMARY KEY,
			scope        TEXT NOT NULL DEFAULT 'global',
			name         TEXT NOT NULL DEFAULT '',
			token        TEXT NOT NULL,
			prompt       TEXT NOT NULL DEFAULT '',
			session_id   TEXT NOT NULL DEFAULT '',
			model        TEXT NOT NULL DEFAULT '',
			deliver      TEXT NOT NULL DEFAULT '',
			respond      BOOLEAN NOT NULL DEFAULT FALSE,
			enabled      BOOLEAN NOT NULL DEFAULT TRUE,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_call_at TIMESTAMPTZ,
			last_status  TEXT NOT NULL DEFAULT '',
			call_count   BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS webhooks_scope_idx ON webhooks(scope)`,
		// Reply/quote + emoji reactions for room messages.
		`ALTER TABLE room_messages ADD COLUMN IF NOT EXISTS reply_to BIGINT`,
		`CREATE TABLE IF NOT EXISTS room_reactions (
			message_id BIGINT NOT NULL REFERENCES room_messages(id) ON DELETE CASCADE,
			user_id    BIGINT NOT NULL,
			emoji      TEXT NOT NULL,
			PRIMARY KEY (message_id, user_id, emoji)
		)`,
		`CREATE INDEX IF NOT EXISTS room_reactions_msg_idx ON room_reactions(message_id)`,
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

// ListSessions returns sessions owned by ownerID; pass nil to list every session
// (admin / internal use). Multi-user callers pass the current user's id so each
// user only ever sees their own sessions.
func (s *Store) ListSessions(ctx context.Context, ownerID *int64) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, created_at FROM sessions
		WHERE ($1::bigint IS NULL OR owner_id = $1)
		ORDER BY created_at ASC
	`, ownerID)
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

// EnsureSession creates the session row (with owner + name) if it doesn't exist,
// leaving an existing row untouched. ownerID may be nil (legacy/shared).
func (s *Store) EnsureSession(ctx context.Context, id, name string, ownerID *int64) error {
	if name == "" {
		name = id
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name, owner_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, id, name, ownerID)
	return err
}

// UpsertSession creates or renames a session; owner is set on create and left
// unchanged on rename.
func (s *Store) UpsertSession(ctx context.Context, id, name string, ownerID *int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name, owner_id) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name
	`, id, name, ownerID)
	return err
}

// SessionOwner returns the owner of a session (nil if none/legacy), and whether
// the session exists.
func (s *Store) SessionOwner(ctx context.Context, id string) (ownerID *int64, exists bool, err error) {
	var owner *int64
	err = s.pool.QueryRow(ctx, `SELECT owner_id FROM sessions WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return owner, true, nil
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
	// Explicit per-session keys. The previous `LIKE '%_' || id` treated `_` as a
	// single-char wildcard: deleting a session named "id" also wiped
	// oauth_google_client_id, and "b" took system_prompt_personality_ab with it.
	if _, err := tx.Exec(ctx, `DELETE FROM agent_config WHERE key = ANY($1)`,
		[]string{KeyPersonality + "_" + id, summaryKey(id)}); err != nil {
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
	err := s.pool.QueryRow(ctx, `SELECT value FROM agent_config WHERE key = $1`, s.cfgScope+key).Scan(&value)
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
	`, s.cfgScope+key, value)
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
	`, s.cfgScope+name, encrypted)
	return err
}

func (s *Store) GetSecret(ctx context.Context, name string) (string, bool, error) {
	var encrypted string
	err := s.pool.QueryRow(ctx, `SELECT value FROM secrets WHERE name = $1`, s.cfgScope+name).Scan(&encrypted)
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
	_, err := s.pool.Exec(ctx, `DELETE FROM secrets WHERE name = $1`, s.cfgScope+name)
	return err
}

// ListScopedSecretNames lists secret names visible under this store's
// ConfigScope ("u<id>:" or "g<id>:"), prefix stripped — e.g. a "u42:"-scoped
// store lists "email_password" for the row named "u42:email_password".
// Unlike ListSecretNames (the legacy global bucket, kept unmodified since
// /api/secrets still uses it directly), an empty cfgScope returns nothing
// rather than the whole table: this method is only ever called on an
// already-scoped store (the new group/personal secrets endpoints), so a
// caller can never accidentally enumerate every legacy global secret name by
// forgetting to scope first. cfgScope is always built as "u%d:"/"g%d:"
// (digits + colon), never containing a LIKE wildcard, so no escaping is
// needed — same assumption the existing scope cleanup in permissions.go makes.
func (s *Store) ListScopedSecretNames(ctx context.Context) ([]string, error) {
	if s.cfgScope == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT name FROM secrets WHERE name LIKE $1 ORDER BY name ASC`, s.cfgScope+"%")
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
		names = append(names, strings.TrimPrefix(n, s.cfgScope))
	}
	return names, rows.Err()
}

// ScopedSecrets returns the secrets visible to a script running under scope:
// the team-shared bucket (names with no ':', e.g. request_secret in
// single-user/legacy mode) plus scope's own secrets, with the "<scope>:"
// prefix stripped from the returned keys. Selecting the whole table gives no
// way to tell one scope's entries apart from another's by name alone.
func (s *Store) ScopedSecrets(ctx context.Context, scope string) (map[string]string, error) {
	result := make(map[string]string)
	if err := s.collectSecrets(ctx, `SELECT name, value FROM secrets WHERE name NOT LIKE '%:%'`, nil, "", result); err != nil {
		return nil, err
	}
	if scope != "" && scope != "global" {
		prefix := scope + ":"
		if err := s.collectSecrets(ctx, `SELECT name, value FROM secrets WHERE name LIKE $1`, []interface{}{prefix + "%"}, prefix, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) collectSecrets(ctx context.Context, query string, args []interface{}, stripPrefix string, into map[string]string) error {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, encrypted string
		if err := rows.Scan(&name, &encrypted); err != nil {
			return err
		}
		value, err := decryptValue(s.encKey, encrypted)
		if err != nil {
			return fmt.Errorf("decrypt secret %q: %w", name, err)
		}
		into[strings.TrimPrefix(name, stripPrefix)] = value
	}
	return rows.Err()
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
