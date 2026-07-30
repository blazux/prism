package memory

import (
	"context"
	"time"
)

// A webhook turns an inbound HTTP call from anything (CI, Grafana, a form, a
// smart plug) into an agent turn: the caller's payload is wrapped in a stored
// prompt and handed to the agent, which then has the whole toolset to act on it.
//
// Unlike mcp_servers, the id is globally unique rather than scoped: the inbound
// request knows only the URL, so the row has to be findable by id alone. The
// scope it was created in rides along on the row and decides what the resulting
// agent turn is allowed to touch.
type WebhookRow struct {
	ID    string // unguessable slug, appears in the URL
	Scope string // config scope that owns it (single-user: "global")
	Name  string
	Token string // shared secret checked on every inbound call
	// Prompt wraps the payload. "{{content}}" is replaced by it; with no
	// placeholder the payload is appended after the prompt.
	Prompt string
	// SessionID is the chat session the turn runs in. Empty means a dedicated
	// "webhook-<id>" session, so an automated feed never pollutes a human chat.
	SessionID string
	Model     string // empty = deployment default
	Deliver   string // "", "telegram", "slack", "webex": push the answer there too
	// Respond makes the call synchronous, returning the agent's answer as the
	// HTTP response. Off by default: agent turns routinely outlive a caller's
	// timeout, and most senders discard the body anyway.
	Respond    bool
	Enabled    bool
	CreatedAt  time.Time
	LastCallAt *time.Time
	LastStatus string
	CallCount  int64
}

func (s *Store) WebhookUpsert(ctx context.Context, w WebhookRow) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO webhooks (id, scope, name, token, prompt, session_id, model, deliver, respond, enabled, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, NOW())
		ON CONFLICT (id) DO UPDATE
		SET name       = EXCLUDED.name,
		    token      = EXCLUDED.token,
		    prompt     = EXCLUDED.prompt,
		    session_id = EXCLUDED.session_id,
		    model      = EXCLUDED.model,
		    deliver    = EXCLUDED.deliver,
		    respond    = EXCLUDED.respond,
		    enabled    = EXCLUDED.enabled
	`, w.ID, w.Scope, w.Name, w.Token, w.Prompt, w.SessionID, w.Model, w.Deliver, w.Respond, w.Enabled)
	return err
}

const webhookCols = `id, scope, name, token, prompt, session_id, model, deliver, respond, enabled, created_at, last_call_at, last_status, call_count`

func scanWebhook(row interface{ Scan(...any) error }) (WebhookRow, error) {
	var w WebhookRow
	err := row.Scan(&w.ID, &w.Scope, &w.Name, &w.Token, &w.Prompt, &w.SessionID, &w.Model,
		&w.Deliver, &w.Respond, &w.Enabled, &w.CreatedAt, &w.LastCallAt, &w.LastStatus, &w.CallCount)
	return w, err
}

// WebhookByID looks a webhook up across every scope — the inbound request has
// only the URL to go on. The caller must still check the token.
func (s *Store) WebhookByID(ctx context.Context, id string) (WebhookRow, bool, error) {
	w, err := scanWebhook(s.pool.QueryRow(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE id = $1`, id))
	if err != nil {
		return WebhookRow{}, false, nil
	}
	return w, true, nil
}

func (s *Store) WebhookList(ctx context.Context, scope string) ([]WebhookRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+webhookCols+` FROM webhooks WHERE scope = $1 ORDER BY created_at ASC`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookRow
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) WebhookDelete(ctx context.Context, scope, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM webhooks WHERE scope = $1 AND id = $2`, scope, id)
	return err
}

// WebhookRecordCall stamps the outcome so the settings page can show whether a
// webhook has ever fired — the first question anyone asks when wiring one up.
func (s *Store) WebhookRecordCall(ctx context.Context, id, status string) {
	_, _ = s.pool.Exec(ctx, `
		UPDATE webhooks SET last_call_at = NOW(), last_status = $2, call_count = call_count + 1 WHERE id = $1
	`, id, status)
}
