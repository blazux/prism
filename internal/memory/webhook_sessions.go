package memory

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MigrateWebhookSession moves only the archive and conversation metadata of an
// owned webhook's old dedicated session. Ownership comes from the exact webhook
// row, never from a caller-supplied legacy session id.
func (s *Store) MigrateWebhookSession(ctx context.Context, webhookID string, ownerID int64) error {
	if ownerID <= 0 || webhookID == "" {
		return fmt.Errorf("invalid webhook session owner")
	}
	scope := fmt.Sprintf("u%d", ownerID)
	oldID, newID := "webhook-"+webhookID, scope+"-webhook-"+webhookID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var storedScope, configuredSession string
	if err := tx.QueryRow(ctx, `SELECT scope, session_id FROM webhooks WHERE id = $1 FOR UPDATE`, webhookID).Scan(&storedScope, &configuredSession); err != nil {
		return err
	}
	if storedScope != scope || configuredSession != "" {
		return fmt.Errorf("webhook session ownership changed")
	}
	// Neither an explicitly owned source nor an existing destination belonging
	// to someone else may be claimed, even if the legacy id matches the hook.
	for _, id := range []string{oldID, newID} {
		var owner *int64
		err := tx.QueryRow(ctx, `SELECT owner_id FROM sessions WHERE id = $1 FOR UPDATE`, id).Scan(&owner)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if owner != nil && *owner != ownerID {
			return fmt.Errorf("webhook archive belongs to another user")
		}
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO sessions (id, name, owner_id, created_at)
		VALUES ($2, COALESCE((SELECT name FROM sessions WHERE id = $1), $2), $3,
		        COALESCE((SELECT created_at FROM sessions WHERE id = $1), NOW()))
		ON CONFLICT (id) DO UPDATE SET owner_id = EXCLUDED.owner_id
		WHERE sessions.owner_id IS NULL OR sessions.owner_id = EXCLUDED.owner_id
	`, oldID, newID, ownerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("webhook archive belongs to another user")
	}
	if _, err := tx.Exec(ctx, `UPDATE conversation_history SET session_id = $2 WHERE session_id = $1`, oldID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET session_id = $2 WHERE session_id = $1`, oldID, newID); err != nil {
		return err
	}
	for _, keys := range [][2]string{
		{KeyPersonality + "_" + oldID, KeyPersonality + "_" + newID},
		{summaryKey(oldID), summaryKey(newID)},
		{liveCompactionKey(oldID), liveCompactionKey(newID)},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_config (key, value, updated_at)
			SELECT $2, value, updated_at FROM agent_config WHERE key = $1
			ON CONFLICT (key) DO NOTHING
		`, keys[0], keys[1]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM agent_config WHERE key = $1`, keys[0]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, oldID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
