package memory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type ChatReceipt struct {
	Digest string
	Status string
}

func (s *Store) ChatReceipt(ctx context.Context, session, id string) (*ChatReceipt, error) {
	var receipt ChatReceipt
	err := s.pool.QueryRow(ctx, `SELECT digest, status FROM chat_requests WHERE session_id=$1 AND request_id=$2`, session, id).Scan(&receipt.Digest, &receipt.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &receipt, err
}

func (s *Store) ClaimChatRequest(ctx context.Context, session, id, digest, content string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	r, err := tx.Exec(ctx, `INSERT INTO chat_requests(session_id,request_id,digest) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, session, id, digest)
	if err != nil {
		return 0, err
	}
	if r.RowsAffected() != 1 {
		return 0, nil
	}
	var messageID int64
	err = tx.QueryRow(ctx, `INSERT INTO conversation_history(session_id,role,content) VALUES($1,'user',$2) RETURNING id`, session, content).Scan(&messageID)
	if err != nil {
		return 0, err
	}
	return messageID, tx.Commit(ctx)
}

func (s *Store) FinishChatRequest(ctx context.Context, session, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_requests SET status=$3 WHERE session_id=$1 AND request_id=$2`, session, id, status)
	return err
}
