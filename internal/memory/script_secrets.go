package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrSecretName = errors.New("invalid secret name")

// SecretEnvName is the shared mapping used by storage validation and execution.
func SecretEnvName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func IsIntegrationSecret(name string) bool {
	n := SecretEnvName(name)
	switch n {
	case "PRISM_AI_PROFILE", "EMAIL_PASSWORD", "CALDAV_PASSWORD", "TODOIST_TOKEN", "TELEGRAM_BOT_TOKEN", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN":
		return true
	}
	return strings.HasPrefix(n, "WEBEX_BOT_TOKEN_") || strings.HasPrefix(n, "MCP_OAUTH_") || strings.HasPrefix(strings.ToLower(name), "webex_bot_token:") || strings.HasPrefix(strings.ToLower(name), "mcp_oauth_")
}

func ValidateScriptSecretName(name string) error {
	n := SecretEnvName(name)
	if name != strings.TrimSpace(name) || strings.Contains(name, ":") || n == "" || n[0] < 'A' || n[0] > 'Z' {
		return fmt.Errorf("%w: use a name starting with a letter, such as MY_API_KEY; ':' is reserved", ErrSecretName)
	}
	if IsIntegrationSecret(name) {
		return fmt.Errorf("%w: %q belongs to a built-in integration; manage it in that integration's settings", ErrSecretName, name)
	}
	if strings.HasPrefix(n, "PRISM_") {
		return fmt.Errorf("%w: PRISM_* environment variables are managed by Prism", ErrSecretName)
	}
	return nil
}

type secretNameQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) checkScriptSecretName(ctx context.Context, q secretNameQuerier, name string) error {
	if err := ValidateScriptSecretName(name); err != nil {
		return err
	}
	rows, err := q.Query(ctx, `SELECT name FROM secrets WHERE left(name, length($1)) = $1`, s.cfgScope)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			return err
		}
		stored = strings.TrimPrefix(stored, s.cfgScope)
		if strings.Contains(stored, ":") {
			continue
		}
		if err := checkSecretAlias(name, stored); err != nil {
			return err
		}
	}
	return rows.Err()
}

func checkSecretAlias(name, stored string) error {
	if stored != name && SecretEnvName(stored) == SecretEnvName(name) {
		return fmt.Errorf("%w: %q and existing secret %q both map to %s; use the existing name or choose a different one", ErrSecretName, name, stored, SecretEnvName(name))
	}
	return nil
}

// Check before prompting; SetScriptSecret repeats the check atomically on save.
func (s *Store) CheckScriptSecretName(ctx context.Context, name string) error {
	return s.checkScriptSecretName(ctx, s.pool, name)
}

// SetScriptSecret is for user-created credentials only. Built-in integrations
// retain SetSecret and their existing names and access controls.
func (s *Store) SetScriptSecret(ctx context.Context, name, value string) error {
	if err := ValidateScriptSecretName(name); err != nil {
		return err
	}
	encrypted, err := encryptValue(s.encKey, value)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize alias checking + upsert within this scope, including concurrent
	// UI and agent submissions. No schema migration or renaming of existing keys.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "prism-script-secrets:"+s.cfgScope); err != nil {
		return err
	}
	if err := s.checkScriptSecretName(ctx, tx, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO secrets (name,value,created_at) VALUES ($1,$2,NOW()) ON CONFLICT (name) DO UPDATE SET value=EXCLUDED.value, created_at=NOW()`, s.cfgScope+name, encrypted); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListScriptSecretNames also handles the unscoped mono-user store, without
// exposing scoped rows or built-in credentials in the generic Secrets UI.
func (s *Store) ListScriptSecretNames(ctx context.Context) ([]string, error) {
	var names []string
	var err error
	if s.cfgScope == "" {
		names, err = s.ListSecretNames(ctx)
	} else {
		names, err = s.ListScopedSecretNames(ctx)
	}
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, name := range names {
		if !strings.Contains(name, ":") && !IsIntegrationSecret(name) {
			result = append(result, name)
		}
	}
	return result, nil
}
