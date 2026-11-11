package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
)

// migrateSingleOwnerMCP consolidates legacy board/personal connections into the
// installation's registry. Group connections are never imported. Credentials
// are copied under distinct names; existing configurations are not overwritten.
func (s *Store) migrateSingleOwnerMCP(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE mcp_servers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT session_id, id, auth_secret FROM mcp_servers
		WHERE session_id <> 'global' AND session_id !~ '^g[0-9]+$' ORDER BY created_at, session_id, id`)
	if err != nil {
		return err
	}
	type legacy struct{ scope, id, secret string }
	var previous []legacy
	for rows.Next() {
		var item legacy
		if err = rows.Scan(&item.scope, &item.id, &item.secret); err != nil {
			rows.Close()
			return err
		}
		previous = append(previous, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range previous {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(item.scope+":"+item.id)))
		id := item.id
		for n := 0; ; n++ {
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_servers WHERE session_id='global' AND id=$1)`, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				break
			}
			id = fmt.Sprintf("%s-%s-%d", item.id, digest[:12], n)
		}
		secret := item.secret
		if secret != "" && item.scope != "" {
			var encrypted string
			err := tx.QueryRow(ctx, `SELECT value FROM secrets WHERE name=$1`, item.scope+":"+secret).Scan(&encrypted)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				prefix := "mcp_legacy_"
				if strings.HasPrefix(secret, "mcp_oauth_") {
					prefix = "mcp_oauth_migrated_"
				}
				for n := 0; ; n++ {
					name := fmt.Sprintf("%s%s_%d", prefix, digest, n)
					result, err := tx.Exec(ctx, `INSERT INTO secrets(name,value) VALUES($1,$2) ON CONFLICT(name) DO NOTHING`, name, encrypted)
					if err != nil {
						return err
					}
					if result.RowsAffected() > 0 {
						secret = name
						break
					}
				}
			}
		}
		// Existing installation servers keep precedence when legacy connections
		// expose the same tool name. Treat the promotion as a new registration.
		if _, err = tx.Exec(ctx, `UPDATE mcp_servers SET session_id='global',id=$1,auth_secret=$2,created_at=NOW() WHERE session_id=$3 AND id=$4`, id, secret, item.scope, item.id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
