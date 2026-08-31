package server

// Shared group chat rooms (Prism heavy, Phase 4). Each group has one room where
// members chat with each other and with a shared agent that replies when
// @mentioned. Unlike the personal-agent WebSocket (one client ↔ one agent), a
// room fans a message out to every connected member.
//
// The shared agent runs on a per-group session ("room-g<id>") so it keeps
// continuity across mentions (its own exchanges live in that session's history).
// On each mention it is also handed the room messages posted SINCE its last reply
// — the multi-speaker chatter it missed while not addressed — so it has the full
// context without re-dumping the whole log (see runRoomAgent). It uses the group's
// RAG scope and a member-level tool guard (dangerous tools stay admin-only).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"

	"github.com/gorilla/websocket"
)

// ─── hub ──────────────────────────────────────────────────────────────────────

type roomClient struct {
	conn     *websocket.Conn
	send     chan []byte
	groupID  int64
	userID   int64
	userName string
}

type roomHub struct {
	mu      sync.Mutex
	clients map[*roomClient]struct{}
}

func newRoomHub() *roomHub { return &roomHub{clients: make(map[*roomClient]struct{})} }

func (h *roomHub) add(c *roomClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *roomHub) remove(c *roomClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// sendJSON queues a frame for this client through the writer pump. gorilla's
// Conn panics on concurrent writes, and the pump is already running by the
// time the join handler wants to send history — so the handler must never
// call conn.WriteJSON itself.
func (c *roomClient) sendJSON(payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // slow client: drop rather than block
	}
}

// broadcast delivers a payload to every client connected to a group's room.
func (h *roomHub) broadcast(groupID int64, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.groupID != groupID {
			continue
		}
		select {
		case c.send <- data:
		default: // slow client: drop rather than block the whole room
		}
	}
}

// presence returns the distinct users currently connected to a group's room.
func (h *roomHub) presence(groupID int64) []map[string]interface{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[int64]bool{}
	out := []map[string]interface{}{}
	for c := range h.clients {
		if c.groupID == groupID && !seen[c.userID] {
			seen[c.userID] = true
			out = append(out, map[string]interface{}{"userId": c.userID, "name": c.userName})
		}
	}
	return out
}

// ─── membership ────────────────────────────────────────────────────────────────

// userInGroup reports whether the user belongs to the group (room access gate).
func (s *Server) userInGroup(ctx context.Context, userID, groupID int64) bool {
	ms := s.store()
	if ms == nil {
		return false
	}
	groups, err := ms.UserGroups(ctx, userID)
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g.GroupID == groupID {
			return true
		}
	}
	return false
}

// ─── WebSocket endpoint ─────────────────────────────────────────────────────────

// handleRoomWS serves /wsroom?group=<id> for a group member.
func (s *Server) handleRoomWS(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if err != nil || groupID <= 0 {
		http.Error(w, "group required", http.StatusBadRequest)
		return
	}
	if !user.IsGlobalAdmin() && !s.userInGroup(r.Context(), user.ID, groupID) {
		http.Error(w, "not a member of this group", http.StatusForbidden)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &roomClient{conn: conn, send: make(chan []byte, 64), groupID: groupID, userID: user.ID, userName: user.DisplayName}
	s.rooms.add(c)
	defer func() { s.rooms.remove(c); conn.Close(); s.pushPresence(groupID) }()

	// Writer pump.
	go func() {
		for data := range c.send {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}()

	// Send recent history + config + who's here to the newcomer, then announce join.
	if ms := s.store(); ms != nil {
		if msgs, err := ms.RecentRoomMessages(r.Context(), groupID, 50); err == nil {
			c.sendJSON(map[string]interface{}{"type": "history", "messages": msgs})
		}
		if cfg, err := ms.GetRoomConfig(r.Context(), groupID); err == nil {
			c.sendJSON(map[string]interface{}{"type": "room_config", "agentName": cfg.AgentName, "groupId": groupID})
		}
	}
	c.sendJSON(map[string]interface{}{"type": "presence", "users": s.rooms.presence(groupID)})
	s.pushPresence(groupID)

	// Read pump.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var in struct {
			Type      string `json:"type"`
			Content   string `json:"content"`
			ReplyTo   *int64 `json:"replyTo"`
			MessageID int64  `json:"messageId"`
			Emoji     string `json:"emoji"`
		}
		if json.Unmarshal(data, &in) != nil {
			continue
		}
		switch in.Type {
		case "typing":
			// Relay to the room (clients ignore their own id).
			s.rooms.broadcast(c.groupID, map[string]interface{}{"type": "typing", "userId": c.userID, "name": c.userName})
		case "react":
			s.handleRoomReaction(c, in.MessageID, in.Emoji, true)
		case "unreact":
			s.handleRoomReaction(c, in.MessageID, in.Emoji, false)
		default:
			if strings.TrimSpace(in.Content) == "" {
				continue
			}
			s.handleRoomMessage(context.Background(), c, strings.TrimSpace(in.Content), in.ReplyTo)
		}
	}
}

// pushPresence broadcasts the current set of connected users to a group's room.
func (s *Server) pushPresence(groupID int64) {
	s.rooms.broadcast(groupID, map[string]interface{}{"type": "presence", "users": s.rooms.presence(groupID)})
}

// handleRoomReaction toggles a reaction and broadcasts the message's fresh set.
func (s *Server) handleRoomReaction(c *roomClient, msgID int64, emoji string, add bool) {
	ms := s.store()
	if ms == nil || msgID == 0 || strings.TrimSpace(emoji) == "" {
		return
	}
	ctx := context.Background()
	if add {
		ms.AddReaction(ctx, msgID, c.userID, emoji)
	} else {
		ms.RemoveReaction(ctx, msgID, c.userID, emoji)
	}
	rs, _ := ms.MessageReactions(ctx, msgID)
	if rs == nil {
		rs = []memory.RoomReaction{}
	}
	s.rooms.broadcast(c.groupID, map[string]interface{}{"type": "reaction", "messageId": msgID, "reactions": rs})
}

// handleRoomMessage persists a human message, broadcasts it, and — if the shared
// agent is @mentioned — runs it and broadcasts the reply.
func (s *Server) handleRoomMessage(ctx context.Context, c *roomClient, content string, replyTo *int64) {
	ms := s.store()
	if ms == nil {
		return
	}
	authorID := c.userID
	msg, err := ms.AddRoomMessage(ctx, c.groupID, &authorID, c.userName, content, false, replyTo)
	if err != nil {
		log.Printf("[room] add message: %v", err)
		return
	}
	s.rooms.broadcast(c.groupID, roomEvent(msg))
	ms.AddUsage(ctx, c.userID, fmt.Sprintf("room-g%d", c.groupID), "channel_msg", "room", 1, nil)

	// Notify any @mentioned members (in their personal notification feed).
	go s.notifyRoomMentions(c.groupID, c.userID, c.userName, content)

	cfg, _ := ms.GetRoomConfig(ctx, c.groupID)
	if !mentionsAgent(content, cfg.AgentName) {
		return
	}
	go s.runRoomAgent(c.groupID, cfg, c.userName, content)
}

// notifyRoomMentions delivers a notification to each group member @mentioned by
// display name (spaces stripped), except the author.
func (s *Server) notifyRoomMentions(groupID, authorID int64, authorName, content string) {
	ms := s.store()
	if ms == nil {
		return
	}
	ctx := context.Background()
	members, err := ms.GroupMembers(ctx, groupID)
	if err != nil {
		return
	}
	lc := strings.ToLower(content)
	for _, mem := range members {
		if mem.UserID == authorID || mem.DisplayName == "" {
			continue
		}
		handle := "@" + strings.ToLower(strings.ReplaceAll(mem.DisplayName, " ", ""))
		if !strings.Contains(lc, handle) {
			continue
		}
		title := fmt.Sprintf("%s mentioned you in the room", authorName)
		sess := fmt.Sprintf("u%d-default", mem.UserID)
		id, err := ms.AddNotification(ctx, sess, title, content, "info")
		if err == nil {
			// Live toast on every workspace the user has open, not just default.
			s.pushNotificationToUser(mem.UserID, id, title, content, "info")
		}
	}
}

// runRoomAgent runs the shared agent for a mention and broadcasts its reply.
func (s *Server) runRoomAgent(groupID int64, cfg memory.RoomConfig, fromName, content string) {
	ms := s.store()
	if ms == nil {
		return
	}
	// Signal "the agent is typing" to the room.
	s.rooms.broadcast(groupID, map[string]interface{}{"type": "agent_typing"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sessionID := fmt.Sprintf("room-g%d", groupID)
	// The shared agent runs with the group-admin-defined rights: global policy +
	// this group's restrictions, at member level (no admin-only tools by default).
	cc := s.callerContextForGroup(ctx, groupID)

	// Apply the group admin's configured system prompt for the shared agent.
	if cfg.AgentPrompt != "" {
		ms.SetConfig(ctx, memory.KeyPersonality+"_"+sessionID, cfg.AgentPrompt)
	}

	// Label the speaker so the agent knows who addressed it.
	prompt := stripMention(content, cfg.AgentName)
	message := fmt.Sprintf("[%s]: %s", fromName, prompt)

	// Give the agent the room chatter it missed: only the messages posted SINCE its
	// last reply (members talking among themselves without @mentioning it). Its own
	// session history already carries the exchanges it took part in, so we add just
	// this delta — not the whole log — to keep the context tight, not giant.
	if recent, err := ms.RecentRoomMessages(ctx, groupID, 80); err == nil {
		start := 0
		for i := len(recent) - 1; i >= 0; i-- {
			if recent[i].IsAgent {
				start = i + 1 // everything after the agent's last message
				break
			}
		}
		missed := recent[start:]
		if len(missed) > 0 {
			missed = missed[:len(missed)-1] // drop the triggering message (it's the prompt)
		}
		if len(missed) > 0 {
			var sb strings.Builder
			sb.WriteString("Messages posted in the room since your last reply (context — do not answer these directly):\n")
			for _, m := range missed {
				// Send time included so the agent can tell a minute-old reply
				// from an hour-old aside (the triggering message is timestamped
				// by Agent.Chat itself).
				sb.WriteString(fmt.Sprintf("[%s] [%s]: %s\n", m.CreatedAt.In(time.Local).Format("2006-01-02 15:04"), m.AuthorName, m.Content))
			}
			sb.WriteString("\nNow reply to the message addressed to you:\n")
			message = sb.String() + message
		}
	}

	model := cfg.AgentModel // empty → server default
	// Surface tool activity live: members see what the agent is doing ("using
	// web_search…") instead of a silent pause; the tool list is attached to the
	// reply client-side.
	tap := func(ev agent.Event) {
		if ev.Type == "tool_use" && ev.Tool != "" {
			s.rooms.broadcast(groupID, map[string]interface{}{"type": "agent_tool", "tool": ev.Tool})
		}
	}
	reply, err := s.runHeadlessChatTap(ctx, sessionID, message, model, cc, tap, roomLimits(cfg))
	if err != nil || strings.TrimSpace(reply) == "" {
		reply = "⚠️ Sorry, I couldn't produce a response."
	}
	agentMsg, err := ms.AddRoomMessage(ctx, groupID, nil, cfg.AgentName, reply, true, nil)
	if err != nil {
		log.Printf("[room] add agent message: %v", err)
		return
	}
	s.rooms.broadcast(groupID, roomEvent(agentMsg))
}

func roomEvent(m memory.RoomMessage) map[string]interface{} {
	return map[string]interface{}{"type": "message", "message": m}
}

// ─── REST: my groups + room config ──────────────────────────────────────────────

// GET /api/my/groups → the caller's groups (all groups for a global admin), so
// the room UI can offer a picker.
func (s *Server) handleMyGroups(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	if u.IsGlobalAdmin() {
		groups, _ := ms.ListGroups(r.Context())
		writeJSON(w, map[string]interface{}{"groups": groups, "displayName": u.DisplayName, "isGlobalAdmin": true})
		return
	}
	groups, _ := ms.UserGroups(r.Context(), u.ID)
	writeJSON(w, map[string]interface{}{"groups": groups, "displayName": u.DisplayName, "isGlobalAdmin": false})
}

// GET /api/group/members?group=<id> → members (userId, name, avatarVer) plus the
// shared agent's name, for @mention autocomplete and avatars. Any member may read.
func (s *Server) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	if !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, groupID) {
		writeErr(w, http.StatusForbidden, "not a member")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	members, _ := ms.GroupMembers(r.Context(), groupID)
	out := make([]map[string]interface{}, 0, len(members))
	for _, m := range members {
		out = append(out, map[string]interface{}{
			"userId": m.UserID, "name": m.DisplayName, "role": m.Role,
			"avatarVer": ms.AvatarVer(r.Context(), fmt.Sprintf("u%d", m.UserID)),
		})
	}
	cfg, _ := ms.GetRoomConfig(r.Context(), groupID)
	writeJSON(w, map[string]interface{}{"members": out, "agentName": cfg.AgentName})
}

// isGroupAdminOf reports whether the user administers the given group (or is global admin).
func (s *Server) isGroupAdminOf(ctx context.Context, u *memory.User, groupID int64) bool {
	if u == nil {
		return false
	}
	if u.IsGlobalAdmin() {
		return true
	}
	ms := s.store()
	if ms == nil {
		return false
	}
	groups, _ := ms.UserGroups(ctx, u.ID)
	for _, g := range groups {
		if g.GroupID == groupID && g.Role == memory.GroupRoleAdmin {
			return true
		}
	}
	return false
}

// GET/POST /api/room/config?group=<id> — the group's shared-agent config.
// Any member may read it; only a group admin (or global admin) may change it.
func (s *Server) handleRoomConfig(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, groupID) {
			writeErr(w, http.StatusForbidden, "not a member")
			return
		}
		cfg, _ := ms.GetRoomConfig(r.Context(), groupID)
		writeJSON(w, cfg)
	case http.MethodPost:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		var b struct {
			AgentName, AgentPrompt, AgentModel string
			AgentMaxIter                       int
			AgentThinking                      *bool // absent = keep reasoning on
			AgentLean                          bool  // absent = guided profile
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		thinking := b.AgentThinking == nil || *b.AgentThinking
		if err := ms.SetRoomConfig(r.Context(), memory.RoomConfig{
			GroupID: groupID, AgentName: b.AgentName, AgentPrompt: b.AgentPrompt, AgentModel: b.AgentModel,
			AgentMaxIter: agent.ClampIterations(b.AgentMaxIter), AgentThinking: thinking, AgentLean: b.AgentLean,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// roomLimits maps a group's shared-agent budget onto the agent's turn limits.
func roomLimits(cfg memory.RoomConfig) agent.Limits {
	th, ln := cfg.AgentThinking, cfg.AgentLean
	return agent.Limits{MaxIterations: cfg.AgentMaxIter, Thinking: &th, LeanPrompt: &ln}
}

// ─── mention parsing ────────────────────────────────────────────────────────────

// mentionsAgent reports whether a message addresses the shared agent — either the
// generic "@agent" or "@<AgentName>".
func mentionsAgent(content, agentName string) bool {
	lc := strings.ToLower(content)
	if strings.Contains(lc, "@agent") {
		return true
	}
	if agentName != "" {
		return strings.Contains(lc, "@"+strings.ToLower(strings.ReplaceAll(agentName, " ", "")))
	}
	return false
}

// stripMention removes @mention token(s) so the agent sees a clean prompt.
func stripMention(content, agentName string) string {
	out := content
	tokens := []string{"@agent"}
	if agentName != "" {
		tokens = append(tokens, "@"+strings.ReplaceAll(agentName, " ", ""), "@"+agentName)
	}
	for _, tok := range tokens {
		for {
			idx := strings.Index(strings.ToLower(out), strings.ToLower(tok))
			if idx < 0 {
				break
			}
			out = out[:idx] + out[idx+len(tok):]
		}
	}
	return strings.TrimSpace(out)
}
