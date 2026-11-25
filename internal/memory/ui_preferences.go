package memory

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

const uiPreferencePrefix = "ui_preferences:"

var ErrUIPreferencesTooLarge = errors.New("interface preferences exceed the storage limit")

type preferenceQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) readUIPreferences(ctx context.Context, q preferenceQuerier) (map[string]string, error) {
	prefix := s.cfgScope + uiPreferencePrefix
	rows, err := q.Query(ctx, `SELECT key, value FROM agent_config WHERE left(key, length($1::text)) = $1`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[strings.TrimPrefix(key, prefix)] = value
	}
	return values, rows.Err()
}

// UIPreferences reads only interface preferences within this store's config scope.
func (s *Store) UIPreferences(ctx context.Context) (map[string]string, error) {
	return s.readUIPreferences(ctx, s.pool)
}

// SaveUIPreferences merges keys atomically. Insert-only imports never overwrite
// values saved by another browser, including one racing the import itself.
func (s *Store) SaveUIPreferences(ctx context.Context, patch map[string]string, insertOnly bool, maxBytes int) (map[string]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Serialize changes to this owner's preference namespace, rather than to
	// the entire installation or to unrelated connection settings.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, s.cfgScope+uiPreferencePrefix); err != nil {
		return nil, err
	}
	values, err := s.readUIPreferences(ctx, tx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(patch))
	for key, value := range patch {
		if _, exists := values[key]; insertOnly && exists {
			continue
		}
		values[key] = value
		keys = append(keys, key)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxBytes {
		return nil, ErrUIPreferencesTooLarge
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, err := tx.Exec(ctx, `INSERT INTO agent_config (key, value, updated_at) VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, s.cfgScope+uiPreferencePrefix+key, values[key])
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return values, nil
}
