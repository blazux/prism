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
	"os"
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
	queues  map[int64][]func()
	cancels map[int64]context.CancelFunc
}

func newRoomHub() *roomHub {
	return &roomHub{clients: make(map[*roomClient]struct{}), queues: make(map[int64][]func()), cancels: make(map[int64]context.CancelFunc)}
}

// enqueue preserves arrival order and has one worker per group. This also
// serializes context maintenance with model turns.
func (h *roomHub) enqueue(group int64, fn func()) {
	h.mu.Lock()
	_, running := h.queues[group]
	h.queues[group] = append(h.queues[group], fn)
	h.mu.Unlock()
	if running {
		return
	}
	go func() {
		for {
			h.mu.Lock()
			q := h.queues[group]
			if len(q) == 0 {
				delete(h.queues, group)
				h.mu.Unlock()
				return
			}
			work := q[0]
			h.queues[group] = q[1:]
			h.mu.Unlock()
			work()
		}
	}()
}
func (h *roomHub) setCancel(group int64, cancel context.CancelFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancel == nil {
		delete(h.cancels, group)
	} else {
		h.cancels[group] = cancel
	}
}
func (h *roomHub) stop(group int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancel := h.cancels[group]; cancel != nil {
		cancel()
	}
}
func (h *roomHub) activity(group int64) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{"type": "activity", "running": h.cancels[group] != nil, "queued": len(h.queues[group])}
}

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
	default: // Reconnect and replay persisted state instead of losing final events.
		_ = c.conn.Close()
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
		default: // A slow client reconnects and replays persisted state.
			_ = c.conn.Close()
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
	stopOnCancel := context.AfterFunc(r.Context(), func() { _ = conn.Close() })
	defer stopOnCancel()

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
	c.sendJSON(s.rooms.activity(groupID))
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
		case "stop":
			s.rooms.stop(groupID)
		case "typing":
			// Relay to the room (clients ignore their own id).
			s.rooms.broadcast(c.groupID, map[string]interface{}{"type": "typing", "userId": c.userID, "name": c.userName})
		case "react":
			s.handleRoomReaction(c, in.MessageID, in.Emoji, true)
		case "unreact":
			s.handleRoomReaction(c, in.MessageID, in.Emoji, false)
		default:
			if len(in.Content) > 16000 {
				c.sendJSON(map[string]any{"type": "error", "content": "Message too long (maximum 16000 characters)."})
				continue
			}
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
	s.background(func() { s.notifyRoomMentions(c.groupID, c.userID, c.userName, content) })

	cfg, _ := ms.GetRoomConfig(ctx, c.groupID)
	if !mentionsAgent(content, cfg.AgentName) {
		return
	}
	s.rooms.enqueue(c.groupID, func() { s.runRoomAgent(c.groupID, cfg, c.userName, content, msg.ID) })
	s.rooms.broadcast(c.groupID, s.rooms.activity(c.groupID))
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
func (s *Server) runRoomAgent(groupID int64, cfg memory.RoomConfig, fromName, content string, triggerID int64) {
	ms := s.store()
	if ms == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.runtimeContext(), 10*time.Minute)
	defer cancel()
	s.rooms.setCancel(groupID, cancel)
	defer func() { s.rooms.setCancel(groupID, nil); s.rooms.broadcast(groupID, s.rooms.activity(groupID)) }()
	s.rooms.broadcast(groupID, s.rooms.activity(groupID))
	sessionID := fmt.Sprintf("room-g%d", groupID)
	cc := s.callerContextForGroup(ctx, groupID)
	// Emptying the configured prompt must clear a previously saved override too.
	_ = ms.SetConfig(ctx, memory.KeyPersonality+"_"+sessionID, cfg.AgentPrompt+fmt.Sprintf("\nRoom archive: use exec_command to GET $PRISM_URL/api/room/history?group=%d&before=<message_id> with Bearer $PRISM_TOKEN. Results are chronological pages; before=0 reads the latest. search_history finds your previous agent exchanges.", groupID))
	message := fmt.Sprintf("[%s]: %s", fromName, stripMention(content, cfg.AgentName))
	seenKey := fmt.Sprintf("room_seen_%d", groupID)
	raw, _, _ := ms.GetConfig(ctx, seenKey)
	seen, _ := strconv.ParseInt(raw, 10, 64)
	if recent, err := ms.RoomMessagesBefore(ctx, groupID, triggerID, 40); err == nil {
		message = roomPrompt(message, recent, seen, 6000)
	}
	agentMsg, err := ms.AddRoomMessage(ctx, groupID, nil, cfg.AgentName, "", true, &triggerID)
	if err != nil {
		log.Printf("[room] start reply: %v", err)
		return
	}
	agentMsg.Status = "running"
	_ = ms.UpdateRoomResponse(ctx, agentMsg.ID, "", "running", nil)
	s.rooms.broadcast(groupID, roomEvent(agentMsg))
	traces := []agent.Event{}
	var preview strings.Builder
	inThink := false
	persist := func(status, content string) {
		raw, _ := json.Marshal(traces)
		// Cancellation stops generation, not the final durable transcript write.
		saveCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := ms.UpdateRoomResponse(saveCtx, agentMsg.ID, content, status, raw); err != nil {
			log.Printf("[room] persist response: %v", err)
		}
		agentMsg.Content = content
		agentMsg.Status = status
		agentMsg.ToolEvents = raw
	}
	tap := func(ev agent.Event) {
		switch ev.Type {
		case "stream":
			if ev.Content == "<think>" {
				inThink = true
				return
			}
			if ev.Content == "</think>" {
				inThink = false
				return
			}
			if inThink {
				return
			}
			preview.WriteString(ev.Content)
			s.rooms.broadcast(groupID, map[string]any{"type": "agent_stream", "messageId": agentMsg.ID, "content": ev.Content})
		case "tool_use", "tool_result":
			// Preserve the full bounded tool result emitted by the executor.
			traces = append(traces, ev)
			persist("running", preview.String())
			s.rooms.broadcast(groupID, map[string]any{"type": "agent_event", "messageId": agentMsg.ID, "event": ev})
		}
	}
	reply, runErr := s.runHeadlessChatTap(ctx, sessionID, message, cfg.AgentModel, cc, tap, roomLimits(cfg))
	status := "complete"
	if runErr != nil {
		status = "error"
		if ctx.Err() != nil {
			status = "stopped"
		}
		if strings.TrimSpace(reply) == "" {
			reply = preview.String()
		}
	}
	persist(status, reply)
	_ = ms.SetConfig(context.Background(), seenKey, strconv.FormatInt(triggerID, 10))
	s.rooms.broadcast(groupID, roomEvent(agentMsg))
}

// Ambient chatter is optional context, so retain the most recent complete
// messages that fit rather than letting an inactive room inflate the prefill.
func roomPrompt(message string, recent []memory.RoomMessage, seen int64, budget int) string {
	lines := []string{}
	used := 0
	for i := len(recent) - 1; i >= 0; i-- {
		m := recent[i]
		if m.ID <= seen || m.IsAgent {
			continue
		}
		line := fmt.Sprintf("[%s] [%s]: %s", m.CreatedAt.Format("2006-01-02 15:04"), m.AuthorName, m.Content)
		if len(line) > budget-used {
			break
		}
		used += len(line)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return message
	}
	var b strings.Builder
	b.WriteString("Recent room messages (background context; answer the request below). Older messages remain in the room archive.\n")
	for i := len(lines) - 1; i >= 0; i-- {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	b.WriteString("\nRequest:\n")
	b.WriteString(message)
	return b.String()
}

// Room reads and maintenance share one membership gate.
func (s *Server) roomAccess(w http.ResponseWriter, r *http.Request) (int64, bool) {
	u := currentUser(r)
	group, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || group <= 0 {
		writeErr(w, 400, "group required")
		return 0, false
	}
	if !(u.ID == -group && boundSession(r) == fmt.Sprintf("room-g%d", group) && r.Method == http.MethodGet) && !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, group) {
		writeErr(w, 403, "not a member of this group")
		return 0, false
	}
	if s.store() == nil {
		writeErr(w, 503, "database unavailable")
		return 0, false
	}
	return group, true
}
func (s *Server) handleRoomHistory(w http.ResponseWriter, r *http.Request) {
	group, ok := s.roomAccess(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "GET required")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	msgs, err := s.store().RoomMessagesBefore(r.Context(), group, before, 50)
	if err != nil {
		writeErr(w, 500, "could not load room history")
		return
	}
	writeJSON(w, map[string]any{"messages": msgs, "hasMore": len(msgs) == 50})
}
func (s *Server) handleRoomContext(w http.ResponseWriter, r *http.Request) {
	group, ok := s.roomAccess(w, r)
	if !ok {
		return
	}
	session := fmt.Sprintf("room-g%d", group)
	if r.Method == http.MethodPost {
		// Reject concurrent maintenance instead of holding an HTTP request in a queue.
		s.rooms.mu.Lock()
		_, busy := s.rooms.queues[group]
		s.rooms.mu.Unlock()
		if busy {
			writeErr(w, 409, "wait for the current response to finish")
			return
		}
		done := make(chan error, 1)
		s.rooms.enqueue(group, func() {
			cfg, _ := s.store().GetRoomConfig(r.Context(), group)
			ctx := context.WithValue(r.Context(), compactContextKey{}, true)
			_, err := s.runHeadlessChatTap(ctx, session, "", cfg.AgentModel, s.callerContextForGroup(ctx, group), nil, roomLimits(cfg))
			done <- err
		})
		select {
		case err := <-done:
			if err != nil {
				writeErr(w, 502, "context summary failed; archive retained")
				return
			}
		case <-r.Context().Done():
			return
		}
	} else if r.Method != http.MethodGet {
		writeErr(w, 405, "GET or POST required")
		return
	}
	state, err := s.store().SessionContext(r.Context(), session)
	if err != nil {
		writeErr(w, 500, "could not read context")
		return
	}
	state["historyBudgetChars"] = roomLimits(memory.RoomConfig{}).HistoryBudgetChars
	writeJSON(w, state)
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
			AgentPromptProfile                 *string
			AgentLean                          *bool  // absent = guided profile
			AgentReasoning                     string // "" = server default
			AgentVoicePrompt                   string // "" = built-in internal-call text
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		old, _ := ms.GetRoomConfig(r.Context(), groupID)
		profile := memory.ResolvePromptProfile(old.AgentPromptProfile, old.AgentLean)
		if b.AgentPromptProfile != nil {
			if !memory.ValidPromptProfile(*b.AgentPromptProfile) {
				writeErr(w, 400, "invalid prompt profile")
				return
			}
			profile = *b.AgentPromptProfile
		} else if b.AgentLean != nil {
			profile = memory.ResolvePromptProfile("", *b.AgentLean)
		}
		thinking := b.AgentThinking == nil || *b.AgentThinking
		if err := ms.SetRoomConfig(r.Context(), memory.RoomConfig{
			GroupID: groupID, AgentName: b.AgentName, AgentPrompt: b.AgentPrompt, AgentModel: b.AgentModel,
			AgentMaxIter: agent.ClampIterations(b.AgentMaxIter), AgentThinking: thinking, AgentLean: profile != "guided", AgentPromptProfile: profile,
			AgentReasoning:   agent.NormalizeReasoningEffort(b.AgentReasoning),
			AgentVoicePrompt: strings.TrimSpace(b.AgentVoicePrompt),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// channelContextCharBudget bounds how much conversation a channel agent (a
// Webex space, an in-app group room) replays to the model on every message.
//
// Deliberately far below the dashboard's budget: a channel session is never
// reset and rebuilds its agent per message, so it sits permanently at whatever
// ceiling it is given — every message then pays a full prefill of that much
// context, with no warm agent and no prompt cache to amortize it. That is what
// made @mentioning the Webex bot take minutes in a busy space. A chat channel
// trades recall for latency here: a short window, plus the compaction note that
// summarizes what fell out of it. Override via CHANNEL_CONTEXT_CHAR_BUDGET (0
// restores the deployment-wide budget).
var channelContextCharBudget = func() int {
	if v := os.Getenv("CHANNEL_CONTEXT_CHAR_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 16_000
}()

// roomLimits maps a group's shared-agent budget onto the agent's turn limits.
func roomLimits(cfg memory.RoomConfig) agent.Limits {
	th, ln := cfg.AgentThinking, cfg.AgentLean
	return agent.Limits{
		MaxIterations:      cfg.AgentMaxIter,
		Thinking:           &th,
		LeanPrompt:         &ln,
		PromptProfile:      memory.ResolvePromptProfile(cfg.AgentPromptProfile, cfg.AgentLean),
		ReasoningEffort:    cfg.AgentReasoning,
		HistoryBudgetChars: channelContextCharBudget,
	}
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
