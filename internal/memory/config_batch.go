package memory

import (
	"context"
	"strings"
)

// GetConfigs fetches a known set of configuration keys without an N+1 query.
// The scoped view applies exactly the same owner prefix as GetConfig.
func (s *Store) GetConfigs(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	qualified := make([]string, len(keys))
	for i, key := range keys {
		qualified[i] = s.cfgScope + key
	}
	rows, err := s.pool.Query(ctx, `SELECT key,value FROM agent_config WHERE key=ANY($1)`, qualified)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(key, s.cfgScope)] = value
	}
	return out, rows.Err()
}

// SetConfigs commits related keys together, including a task's legacy status
// and its compact display metadata. It does not persist streaming events.
func (s *Store) SetConfigs(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	contents := make([]string, 0, len(values))
	for key, value := range values {
		keys = append(keys, s.cfgScope+key)
		contents = append(contents, value)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agent_config(key,value) SELECT * FROM unnest($1::text[],$2::text[]) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, keys, contents)
	return err
}
