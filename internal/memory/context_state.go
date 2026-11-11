package memory

import (
	"context"
	"encoding/json"
)

// ReadContext exposes both summaries with errors for the human inspector.
// Agent loading keeps its existing graceful-degradation helpers.
func (s *Store) ReadContext(ctx context.Context, session string) (*LiveCompaction, string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key,value FROM agent_config WHERE key=ANY($1)`, []string{summaryKey(session), liveCompactionKey(session)})
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var live *LiveCompaction
	var summary string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, "", err
		}
		if key == summaryKey(session) {
			summary = value
		} else {
			var record LiveCompaction
			if err := json.Unmarshal([]byte(value), &record); err != nil {
				return nil, "", err
			}
			if record.Summary != "" {
				live = &record
			}
		}
	}
	return live, summary, rows.Err()
}

func (s *Store) CorrectLiveContext(ctx context.Context, session string, previous LiveCompaction, text string) (bool, error) {
	r, err := s.pool.Exec(ctx, `UPDATE agent_config SET value=jsonb_set(value::jsonb,'{summary}',to_jsonb($4::text))::text,updated_at=NOW() WHERE key=$1 AND value::jsonb->>'summary'=$2 AND (value::jsonb->>'before_id')::bigint=$3`, liveCompactionKey(session), previous.Summary, previous.BeforeID, text)
	return r.RowsAffected() == 1, err
}
