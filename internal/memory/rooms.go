package memory

// Shared chat rooms (Prism heavy, Phase 4): one room per group, its message log,
// and the group's shared-agent configuration.

import (
	"context"
	"time"
)

type RoomMessage struct {
	ID         int64          `json:"id"`
	GroupID    int64          `json:"groupId"`
	AuthorID   *int64         `json:"authorId,omitempty"`
	AuthorName string         `json:"authorName"`
	Content    string         `json:"content"`
	IsAgent    bool           `json:"isAgent"`
	CreatedAt  time.Time      `json:"createdAt"`
	ReplyTo    *int64         `json:"replyTo,omitempty"`
	Reactions  []RoomReaction `json:"reactions,omitempty"`
}

// RoomReaction is one user's emoji on a message. The client aggregates by emoji
// and marks "mine" against its own user id.
type RoomReaction struct {
	MessageID int64  `json:"messageId"`
	UserID    int64  `json:"userId"`
	Emoji     string `json:"emoji"`
}

type RoomConfig struct {
	GroupID     int64  `json:"groupId"`
	AgentName   string `json:"agentName"`
	AgentPrompt string `json:"agentPrompt"`
	AgentModel  string `json:"agentModel"`
}

// AddRoomMessage appends a message (human or agent) to a group's room. replyTo is
// the id of a message being quoted, or nil.
func (s *Store) AddRoomMessage(ctx context.Context, groupID int64, authorID *int64, authorName, content string, isAgent bool, replyTo *int64) (RoomMessage, error) {
	var m RoomMessage
	err := s.pool.QueryRow(ctx, `
		INSERT INTO room_messages (group_id, author_id, author_name, content, is_agent, reply_to)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, group_id, author_id, author_name, content, is_agent, created_at, reply_to
	`, groupID, authorID, authorName, content, isAgent, replyTo).
		Scan(&m.ID, &m.GroupID, &m.AuthorID, &m.AuthorName, &m.Content, &m.IsAgent, &m.CreatedAt, &m.ReplyTo)
	return m, err
}

// RecentRoomMessages returns the last `limit` messages for a group's room, oldest
// first, each with its reactions attached.
func (s *Store) RecentRoomMessages(ctx context.Context, groupID int64, limit int) ([]RoomMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, group_id, author_id, author_name, content, is_agent, created_at, reply_to
		FROM (
			SELECT * FROM room_messages WHERE group_id = $1 ORDER BY id DESC LIMIT $2
		) t ORDER BY id ASC
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoomMessage
	byID := map[int64]int{}
	for rows.Next() {
		var m RoomMessage
		if err := rows.Scan(&m.ID, &m.GroupID, &m.AuthorID, &m.AuthorName, &m.Content, &m.IsAgent, &m.CreatedAt, &m.ReplyTo); err != nil {
			return nil, err
		}
		byID[m.ID] = len(out)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach reactions for the whole page in one query.
	if len(out) > 0 {
		rr, err := s.pool.Query(ctx, `
			SELECT rr.message_id, rr.user_id, rr.emoji
			FROM room_reactions rr JOIN room_messages m ON m.id = rr.message_id
			WHERE m.group_id = $1
		`, groupID)
		if err == nil {
			defer rr.Close()
			for rr.Next() {
				var x RoomReaction
				if rr.Scan(&x.MessageID, &x.UserID, &x.Emoji) == nil {
					if i, ok := byID[x.MessageID]; ok {
						out[i].Reactions = append(out[i].Reactions, x)
					}
				}
			}
		}
	}
	return out, nil
}

// ─── Reactions ──────────────────────────────────────────────────────────────────

func (s *Store) AddReaction(ctx context.Context, msgID, userID int64, emoji string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO room_reactions (message_id, user_id, emoji) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, msgID, userID, emoji)
	return err
}

func (s *Store) RemoveReaction(ctx context.Context, msgID, userID int64, emoji string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM room_reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3`, msgID, userID, emoji)
	return err
}

// MessageReactions returns all reactions on a message (for broadcasting fresh state).
func (s *Store) MessageReactions(ctx context.Context, msgID int64) ([]RoomReaction, error) {
	rows, err := s.pool.Query(ctx, `SELECT message_id, user_id, emoji FROM room_reactions WHERE message_id = $1`, msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoomReaction
	for rows.Next() {
		var x RoomReaction
		if err := rows.Scan(&x.MessageID, &x.UserID, &x.Emoji); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RoomMessageGroup returns the group a message belongs to (0 if not found), used
// to route a reaction broadcast to the right room.
func (s *Store) RoomMessageGroup(ctx context.Context, msgID int64) int64 {
	var gid int64
	s.pool.QueryRow(ctx, `SELECT group_id FROM room_messages WHERE id = $1`, msgID).Scan(&gid)
	return gid
}

// GetRoomConfig returns a group's shared-agent config, with defaults if unset.
func (s *Store) GetRoomConfig(ctx context.Context, groupID int64) (RoomConfig, error) {
	c := RoomConfig{GroupID: groupID, AgentName: "Assistant"}
	err := s.pool.QueryRow(ctx, `
		SELECT agent_name, agent_prompt, agent_model FROM room_config WHERE group_id = $1
	`, groupID).Scan(&c.AgentName, &c.AgentPrompt, &c.AgentModel)
	if err != nil {
		// No row yet → return defaults (not an error).
		return RoomConfig{GroupID: groupID, AgentName: "Assistant"}, nil
	}
	return c, nil
}

// SetRoomConfig upserts a group's shared-agent config.
func (s *Store) SetRoomConfig(ctx context.Context, c RoomConfig) error {
	if c.AgentName == "" {
		c.AgentName = "Assistant"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO room_config (group_id, agent_name, agent_prompt, agent_model)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id) DO UPDATE SET
			agent_name = EXCLUDED.agent_name,
			agent_prompt = EXCLUDED.agent_prompt,
			agent_model = EXCLUDED.agent_model
	`, c.GroupID, c.AgentName, c.AgentPrompt, c.AgentModel)
	return err
}
