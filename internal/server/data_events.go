package server

// Data events invalidate a view; they never carry application records. The
// source of truth remains the file, provider or tool the subscriber already
// knows how to read. Dashboard browsers use their existing WebSocket; scripts
// and standalone previews can use the same signals over SSE.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/memory"
)

var dataTopicRE = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,79}$`)

type dataChange struct {
	Type   string `json:"type"`
	Topic  string `json:"topic"`
	Scope  string `json:"scope,omitempty"`
	Group  int64  `json:"group,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type dataSubscription struct {
	user  *memory.User
	scope string // empty = all of this principal's authorized scopes
	topic string
	ch    chan dataChange
}

type dataEventHub struct {
	mu   sync.Mutex
	subs map[*dataSubscription]struct{}
}

func (h *dataEventHub) subscribe(sub *dataSubscription) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= 64 {
		return false
	}
	var count int
	for other := range h.subs {
		if dataUserID(other.user) == dataUserID(sub.user) {
			count++
		}
	}
	if count >= 8 {
		return false
	}
	if h.subs == nil {
		h.subs = make(map[*dataSubscription]struct{})
	}
	h.subs[sub] = struct{}{}
	return true
}

func dataUserID(u *memory.User) int64 {
	if u != nil {
		return u.ID
	}
	return 0
}

func (h *dataEventHub) remove(sub *dataSubscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sub]; ok {
		delete(h.subs, sub)
		close(sub.ch)
	}
}

func (s *Server) dataScopeAllowed(ctx context.Context, user *memory.User, scope string) bool {
	if !s.cfg.MultiUser {
		return scope == "global"
	}
	user, err := s.freshUser(ctx, user)
	if err != nil || user == nil {
		return false
	}
	if scope == fmt.Sprintf("u%d", user.ID) && user.ID > 0 {
		return true
	}
	if scope == "global" {
		return user.ID == 0
	}
	gid, err := strconv.ParseInt(strings.TrimPrefix(scope, "g"), 10, 64)
	if err != nil || gid <= 0 || scope != fmt.Sprintf("g%d", gid) {
		return false
	}
	ms := s.store()
	if ms == nil {
		return false
	}
	if user.ID < 0 && user.ID != -gid {
		return false
	}
	if user.ID < 0 || user.IsGlobalAdmin() {
		groups, err := ms.ListGroups(ctx)
		if err != nil {
			return false
		}
		for _, group := range groups {
			if group.ID == gid {
				return true
			}
		}
		return false
	}
	return s.userInGroup(ctx, user.ID, gid)
}

func (s *Server) dataScope(r *http.Request, kind string, group int64) (string, error) {
	u := currentUser(r)
	if _, err := s.freshUser(r.Context(), u); err != nil {
		return "", err
	}
	if kind == "" && u != nil && u.ID < 0 {
		kind, group = "group", -u.ID
	}
	switch kind {
	case "", "personal":
		if group != 0 || (u != nil && u.ID < 0) {
			return "", fmt.Errorf("personal events require a personal identity")
		}
		scope := s.pimScopeFor(r)
		if !s.dataScopeAllowed(r.Context(), u, scope) {
			return "", fmt.Errorf("event scope unavailable")
		}
		return scope, nil
	case "group":
		if group == 0 {
			if u != nil && u.ID < 0 {
				group = -u.ID
			} else if _, id, ok := s.pimGroupScopeFor(r); ok {
				group = id
			}
		}
		scope := fmt.Sprintf("g%d", group)
		if !s.dataScopeAllowed(r.Context(), u, scope) {
			return "", fmt.Errorf("not a member of this group")
		}
		return scope, nil
	default:
		return "", fmt.Errorf("scope must be personal or group")
	}
}

// emitDataChange is shared by native tools, HTTP writes and explicit custom
// data signals. A slow/disconnected view cannot block the mutation. Membership
// is checked at delivery, so passive clients do not retain removed access.
func (s *Server) emitDataChange(scope, topic string) {
	if scope == "" || !dataTopicRE.MatchString(topic) {
		return
	}
	ctx, cancel := context.WithTimeout(s.runtimeContext(), 2*time.Second)
	defer cancel()
	event := dataChange{Type: "data_changed", Topic: topic, Scope: "personal", Reason: "change"}
	if strings.HasPrefix(scope, "g") && scope != "global" {
		event.Scope = "group"
		event.Group, _ = strconv.ParseInt(scope[1:], 10, 64)
	}
	allowed := map[int64]bool{}
	checked := map[int64]bool{}
	canReceive := func(user *memory.User) bool {
		id := dataUserID(user)
		if !checked[id] {
			allowed[id] = s.dataScopeAllowed(ctx, user, scope)
			checked[id] = true
		}
		return allowed[id]
	}
	s.mu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		if !c.voice {
			clients = append(clients, c)
		}
	}
	s.mu.RUnlock()
	raw, _ := json.Marshal(event)
	for _, client := range clients {
		if canReceive(client.user) {
			client.sendRaw(raw) // Invalidation is transport state, never run replay.
		}
	}
	s.dataEvents.mu.Lock()
	subs := make([]*dataSubscription, 0, len(s.dataEvents.subs))
	for sub := range s.dataEvents.subs {
		subs = append(subs, sub)
	}
	s.dataEvents.mu.Unlock()
	for _, sub := range subs {
		if (sub.scope != "" && sub.scope != scope) || (sub.topic != "" && sub.topic != topic) || !canReceive(sub.user) {
			continue
		}
		s.dataEvents.mu.Lock()
		if _, exists := s.dataEvents.subs[sub]; exists {
			select {
			case sub.ch <- event:
			default:
				delete(s.dataEvents.subs, sub)
				close(sub.ch)
			}
		}
		s.dataEvents.mu.Unlock()
	}
}

func (s *Server) handleDataEvents(w http.ResponseWriter, r *http.Request) {
	if session := r.URL.Query().Get("session"); session != "" {
		if _, ok := s.sessionFor(r, session); !ok {
			writeErr(w, http.StatusForbidden, "forbidden session")
			return
		}
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Topic string `json:"topic"`
			Scope string `json:"scope"`
			Group int64  `json:"group"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !dataTopicRE.MatchString(body.Topic) {
			writeErr(w, http.StatusBadRequest, "topic must be 1-80 lowercase letters, digits, dots, colons, underscores or hyphens, starting with a letter")
			return
		}
		scope, err := s.dataScope(r, body.Scope, body.Group)
		if err != nil {
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
		s.emitDataChange(scope, body.Topic)
		writeJSON(w, map[string]bool{"accepted": true})
	case http.MethodGet:
		s.streamDataEvents(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

func (s *Server) streamDataEvents(w http.ResponseWriter, r *http.Request) {
	u, err := s.freshUser(r.Context(), currentUser(r))
	if err != nil || (s.cfg.MultiUser && u == nil) {
		writeErr(w, http.StatusForbidden, "account unavailable")
		return
	}
	kind := r.URL.Query().Get("scope")
	scope := ""
	groupValue := r.URL.Query().Get("group")
	group := int64(0)
	if groupValue != "" {
		group, err = strconv.ParseInt(groupValue, 10, 64)
		if err != nil || group <= 0 || kind != "group" {
			writeErr(w, http.StatusBadRequest, "group requires scope=group and a positive group id")
			return
		}
	}
	if kind != "" && kind != "all" {
		scope, err = s.dataScope(r, kind, group)
		if err != nil {
			writeErr(w, http.StatusForbidden, err.Error())
			return
		}
	}
	topic := r.URL.Query().Get("topic")
	if topic != "" && !dataTopicRE.MatchString(topic) {
		writeErr(w, http.StatusBadRequest, "invalid topic")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unavailable")
		return
	}
	sub := &dataSubscription{user: u, scope: scope, topic: topic, ch: make(chan dataChange, 32)}
	if !s.dataEvents.subscribe(sub) {
		writeErr(w, http.StatusTooManyRequests, "too many event subscriptions")
		return
	}
	defer s.dataEvents.remove(sub)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(event dataChange) bool {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
		raw, _ := json.Marshal(event)
		_, err := fmt.Fprintf(w, "event: change\ndata: %s\n\n", raw)
		if err == nil {
			flusher.Flush()
		}
		return err == nil
	}
	if !write(dataChange{Type: "data_changed", Topic: "*", Reason: "connected"}) {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-sub.ch:
			if !ok {
				return
			}
			eventScope := "global"
			if s.cfg.MultiUser {
				if uid := dataUserID(u); uid > 0 {
					eventScope = fmt.Sprintf("u%d", uid)
				}
				if event.Scope == "group" {
					eventScope = fmt.Sprintf("g%d", event.Group)
				}
			}
			if !s.dataScopeAllowed(r.Context(), u, eventScope) {
				continue
			}
			if !write(event) {
				return
			}
		case <-heartbeat.C:
			if _, err := s.freshUser(r.Context(), u); err != nil {
				return
			}
			if scope != "" && !s.dataScopeAllowed(r.Context(), u, scope) {
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.runtimeContext().Done():
			return
		}
	}
}

// observeDataMutation observes only handlers that perform native data writes;
// it does not infer whether an arbitrary script or shell command changed data.
func (s *Server) observeDataMutation(w http.ResponseWriter, r *http.Request, topic string, scope *string) (http.ResponseWriter, func()) {
	writer := &dataMutationWriter{ResponseWriter: w}
	return writer, func() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && writer.status >= 200 && writer.status < 300 {
			s.emitDataChange(*scope, topic)
		}
	}
}

type dataMutationWriter struct {
	http.ResponseWriter
	status int
}

func (w *dataMutationWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *dataMutationWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}
