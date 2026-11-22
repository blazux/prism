package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// VoxCallMission links only a mission created by this Prism to its origin.
// It contains no gateway credential and never mirrors the phone queue.
type VoxCallMission struct {
	GatewayKey, SessionID, PrismInstance string
	TaskID                               int64
	OwnerID                              *int64
	GroupID                              int64
	SessionExisted                       bool
	PhoneNumber, ContactName, Mission    string
}

func (s *Store) TrackVoxCall(ctx context.Context, call VoxCallMission) error {
	var session string
	err := s.pool.QueryRow(ctx, `INSERT INTO vox_call_missions
		(gateway_key,task_id,session_id,prism_instance,owner_id,group_id,session_existed,phone_number,contact_name,mission)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT(gateway_key,task_id) DO UPDATE SET task_id=EXCLUDED.task_id
		WHERE vox_call_missions.session_id=EXCLUDED.session_id
		AND vox_call_missions.prism_instance=EXCLUDED.prism_instance
		AND vox_call_missions.owner_id IS NOT DISTINCT FROM EXCLUDED.owner_id
		AND vox_call_missions.group_id=EXCLUDED.group_id
		RETURNING session_id`, call.GatewayKey, call.TaskID, call.SessionID, call.PrismInstance, call.OwnerID,
		call.GroupID, call.SessionExisted, call.PhoneNumber, call.ContactName, call.Mission).Scan(&session)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("phone mission is already attached to another conversation")
	}
	return err
}

func (s *Store) PendingVoxCalls(ctx context.Context, gateway string, limit int) ([]VoxCallMission, error) {
	rows, err := s.pool.Query(ctx, `SELECT gateway_key,task_id,session_id,prism_instance,owner_id,group_id,
		session_existed,phone_number,contact_name,mission FROM vox_call_missions
		WHERE gateway_key=$1 AND finished_at IS NULL ORDER BY checked_at,created_at,task_id LIMIT $2`, gateway, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []VoxCallMission
	for rows.Next() {
		var call VoxCallMission
		if err := rows.Scan(&call.GatewayKey, &call.TaskID, &call.SessionID, &call.PrismInstance, &call.OwnerID,
			&call.GroupID, &call.SessionExisted, &call.PhoneNumber, &call.ContactName, &call.Mission); err != nil {
			return nil, err
		}
		result = append(result, call)
	}
	return result, rows.Err()
}

func (s *Store) CheckVoxCall(ctx context.Context, call VoxCallMission, stopped bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE vox_call_missions SET checked_at=NOW(),
		finished_at=CASE WHEN $3 THEN NOW() ELSE finished_at END
		WHERE gateway_key=$1 AND task_id=$2 AND finished_at IS NULL`, call.GatewayKey, call.TaskID, stopped)
	return err
}

type VoxCallCompletion struct {
	NotificationID, MessageID int64
	CreatedAt                 time.Time
	RoomMessage               *RoomMessage
}

// CompleteVoxCall saves the result and notification once, in the same
// transaction as its completion marker, including when several workers retry.
func (s *Store) CompleteVoxCall(ctx context.Context, call VoxCallMission, status, title, message, level, author string) (*VoxCallCompletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var finished *time.Time
	var session string
	err = tx.QueryRow(ctx, `SELECT session_id,finished_at FROM vox_call_missions
		WHERE gateway_key=$1 AND task_id=$2 FOR UPDATE`, call.GatewayKey, call.TaskID).Scan(&session, &finished)
	if err != nil || finished != nil {
		return nil, err
	}
	if session != call.SessionID {
		return nil, fmt.Errorf("phone mission conversation changed")
	}
	result := &VoxCallCompletion{}
	if err := tx.QueryRow(ctx, `INSERT INTO notifications(session_id,title,message,level)
		VALUES($1,$2,$3,$4) RETURNING id,created_at`, session, title, message, level).Scan(&result.NotificationID, &result.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO conversation_history(session_id,role,content)
		VALUES($1,'assistant',$2) RETURNING id`, session, message).Scan(&result.MessageID); err != nil {
		return nil, err
	}
	if call.GroupID > 0 && session == fmt.Sprintf("room-g%d", call.GroupID) {
		row := &RoomMessage{}
		if err := tx.QueryRow(ctx, `INSERT INTO room_messages(group_id,author_name,content,is_agent,status)
			VALUES($1,$2,$3,TRUE,'completed') RETURNING id,group_id,author_name,content,is_agent,created_at,status`,
			call.GroupID, author, message).Scan(&row.ID, &row.GroupID, &row.AuthorName, &row.Content, &row.IsAgent, &row.CreatedAt, &row.Status); err != nil {
			return nil, err
		}
		result.RoomMessage = row
	}
	if _, err := tx.Exec(ctx, `UPDATE vox_call_missions SET finished_at=NOW(),checked_at=NOW(),status=$3
		WHERE gateway_key=$1 AND task_id=$2`, call.GatewayKey, call.TaskID, status); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
