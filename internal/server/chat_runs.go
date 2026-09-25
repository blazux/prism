package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// chatRun owns execution; browser Clients are replaceable subscribers. All maps
// belong to one Server (one hosted tenant). Keys additionally include user ID.
type chatRun struct {
	approvalManual bool // immutable policy snapshot for this task
	children       map[string]*childTask
	childrenDone   sync.WaitGroup

	mu          sync.Mutex
	owner       *Client
	subscribers map[*Client]bool
	history     json.RawMessage
	events      []json.RawMessage
	bytes       int
	cancel      context.CancelFunc
	failed      bool
	status      string
	secret      json.RawMessage
	approvals   map[string]json.RawMessage
}

func runKey(c *Client) string {
	var uid int64
	if c.user != nil {
		uid = c.user.ID
	}
	return fmt.Sprintf("%d:%s", uid, c.sessionID)
}
func runStateKey(c *Client) string {
	return fmt.Sprintf("chat_run_%x", sha256.Sum256([]byte(runKey(c))))
}
func (c *Client) currentRun() *chatRun { c.runMu.RLock(); defer c.runMu.RUnlock(); return c.run }
func (c *Client) setRun(r *chatRun)    { c.runMu.Lock(); c.run = r; c.runMu.Unlock() }
func (c *Client) isDisconnected() bool {
	c.transportMu.RLock()
	defer c.transportMu.RUnlock()
	return c.disconnected
}
func (c *Client) sendRaw(b []byte) {
	if c.isDisconnected() {
		return
	}
	select {
	case c.send <- b:
	default:
		if c.conn != nil {
			_ = c.conn.Close()
		}
	} // reconnect gets an authoritative snapshot
}
func (r *chatRun) broadcastLocked(v any) {
	b, _ := json.Marshal(v)
	for c := range r.subscribers {
		c.sendRaw(b)
	}
}
func (c *Client) publishRun(v any) bool {
	r := c.currentRun()
	if r == nil || r.owner != c {
		return false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return true
	}
	var kind struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal(b, &kind)
	// Browser editor RPCs belong strictly to the initiating tab, never a reattached one.
	if kind.Type == "editor_request" || kind.Type == "file_content" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch kind.Type {
	case "secret_request":
		r.secret = b
		r.status = "waiting_for_input"
	case "approval_request":
		if r.approvals == nil {
			r.approvals = make(map[string]json.RawMessage)
		}
		r.approvals[kind.ID] = b
		r.status = "waiting_for_approval"
	case "error":
		r.failed = true
	}
	if kind.Type != "secret_request" && kind.Type != "approval_request" {
		if r.bytes+len(b) > 16<<20 {
			r.failed = true
			r.cancel()
			return true
		}
		r.events = append(r.events, b)
		r.bytes += len(b)
	}
	for peer := range r.subscribers {
		peer.sendRaw(b)
	}
	return true
}
func (s *Server) historySnapshot(c *Client) json.RawMessage {
	tmp := &Client{send: make(chan []byte, 1)}
	s.sendHistory(s.runtimeContext(), tmp, c.sessionID)
	select {
	case b := <-tmp.send:
		return b
	default:
		return json.RawMessage(`{"type":"chat_history","messages":[]}`)
	}
}
func (s *Server) runActive(c *Client) bool {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	return s.runs[runKey(c)] != nil
}
func (s *Server) beginRun(c *Client, cancel context.CancelFunc, content string, images []string) (*chatRun, error) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	if s.runs == nil {
		s.runs = make(map[string]*chatRun)
	}
	if s.runs[runKey(c)] != nil {
		return nil, fmt.Errorf("a task is already running in this conversation")
	}
	active := 0
	for _, r := range s.runs {
		if (r.owner.user == nil && c.user == nil) || (r.owner.user != nil && c.user != nil && r.owner.user.ID == c.user.ID) {
			active++
		}
	}
	if active >= 4 || len(s.runs) >= 16 {
		return nil, fmt.Errorf("too many running tasks; wait or stop one first")
	}
	if ms := s.store(); ms != nil {
		if err := ms.SetConfig(s.runtimeContext(), runStateKey(c), "running"); err != nil {
			return nil, fmt.Errorf("could not record task state")
		}
	}
	delete(s.completedRuns, runKey(c))
	c.mu.Lock()
	manual := c.approvalManual
	c.mu.Unlock()
	r := &chatRun{approvalManual: manual, owner: c, subscribers: map[*Client]bool{c: true}, history: s.historySnapshot(c), cancel: cancel, status: "running"}
	user, _ := json.Marshal(map[string]any{"type": "run_user", "content": content, "images": images})
	r.events = append(r.events, user)
	r.bytes = len(user)
	c.setRun(r)
	s.mu.RLock()
	peers := make([]*Client, 0, len(s.clients))
	for peer := range s.clients {
		if peer != c && !peer.voice && runKey(peer) == runKey(c) {
			peers = append(peers, peer)
		}
	}
	s.mu.RUnlock()
	for _, peer := range peers {
		peer.setRun(r)
		r.subscribers[peer] = true
		peer.sendJSON(map[string]any{"type": "run_replay", "history": r.history, "events": r.events, "status": "running"})
	}
	s.runs[runKey(c)] = r
	r.broadcastLocked(map[string]any{"type": "run_status", "status": "running"})
	return r, nil
}
func (s *Server) attachRun(c *Client) {
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	if r := s.runs[runKey(c)]; r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		c.setRun(r)
		r.subscribers[c] = true
		c.sendJSON(map[string]any{"type": "run_replay", "history": r.history, "events": r.events, "status": r.status})
		r.owner.mu.Lock()
		secret := r.owner.pendingSecretCh != nil
		pending := make(map[string]bool)
		for id := range r.owner.approvals {
			pending[id] = true
		}
		r.owner.mu.Unlock()
		if secret && r.secret != nil {
			c.sendRaw(r.secret)
		}
		for id, b := range r.approvals {
			if pending[id] {
				c.sendRaw(b)
			}
		}
	} else {
		if completed := s.completedRuns[runKey(c)]; completed != nil {
			c.sendJSON(map[string]any{"type": "run_replay", "history": completed.history, "events": completed.events, "status": completed.status})
			return
		}
		s.sendHistory(s.runtimeContext(), c, c.sessionID)
		status := "idle"
		if ms := s.store(); ms != nil {
			if v, ok, _ := ms.GetConfig(s.runtimeContext(), runStateKey(c)); ok {
				status = v
				if v == "running" {
					status = "interrupted"
				}
			}
		}
		c.sendJSON(map[string]any{"type": "run_status", "status": status})
	}
}
func (s *Server) detachRun(c *Client) {
	c.transportMu.Lock()
	c.disconnected = true
	c.transportMu.Unlock()
	if r := c.currentRun(); r != nil {
		r.mu.Lock()
		delete(r.subscribers, c)
		r.mu.Unlock()
	}
}
func (s *Server) finishRun(r *chatRun, ctx context.Context) {
	ms := s.store()
	shuttingDown := s.runtimeContext().Err() != nil
	r.mu.Lock()
	for _, child := range r.children {
		child.cancel()
	}
	r.mu.Unlock()
	r.childrenDone.Wait()
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	status := "completed"
	if r.failed {
		status = "failed"
	}
	if ctx.Err() != nil {
		status = "stopped"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timed_out"
		}
	}
	if shuttingDown {
		status = "interrupted"
	}
	// Use a bounded persistence context even while shutdown cancels execution.
	save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ms != nil {
		_ = ms.SetConfig(save, runStateKey(r.owner), status)
	}
	r.broadcastLocked(map[string]any{"type": "run_status", "status": status})
	for c := range r.subscribers {
		if c != r.owner {
			c.mu.Lock()
			c.historyStale = true
			c.mu.Unlock()
		}
		c.setRun(nil)
	}
	r.owner.setRun(nil)
	if ms == nil {
		if s.completedRuns == nil {
			s.completedRuns = make(map[string]*chatRun)
		}
		if len(s.completedRuns) >= 8 {
			for key := range s.completedRuns {
				delete(s.completedRuns, key)
				break
			}
		}
		s.completedRuns[runKey(r.owner)] = &chatRun{history: r.history, events: r.events, status: status}
	}
	delete(s.runs, runKey(r.owner))
}
func (s *Server) cancelRun(c *Client) {
	s.runsMu.Lock()
	r := s.runs[runKey(c)]
	s.runsMu.Unlock()
	if r != nil {
		r.owner.cancelActive()
	}
}
func (s *Server) routeRunControl(c *Client, msg WSMessage) bool {
	switch msg.Type {
	case "secret_response", "approval_response", "approval_mode":
	default:
		return false
	}
	r := c.currentRun()
	if r == nil {
		return false
	}
	if msg.Type == "approval_mode" {
		c.mu.Lock()
		c.approvalManual = msg.Content == "manual" // the next task; this run keeps its snapshot
		c.mu.Unlock()
		return true
	}
	owner := r.owner
	owner.mu.Lock()
	switch msg.Type {
	case "secret_response":
		if msg.ID == "" || msg.ID != owner.pendingSecretID {
			owner.mu.Unlock()
			return true
		}
		ch := owner.pendingSecretCh
		owner.pendingSecretCh = nil
		owner.mu.Unlock()
		if ch != nil {
			select {
			case ch <- msg.Content:
			default:
			}
		}
	case "approval_response":
		ch := owner.approvals[msg.ID]
		if ch == nil {
			owner.mu.Unlock()
			return true
		}
		// Keep the registered channel until the waiter consumes the verdict.
		if msg.ID == owner.pendingApprovalID {
			owner.pendingApproval = nil
			owner.pendingApprovalID = ""
		}
		owner.mu.Unlock()
		if ch != nil {
			select {
			case ch <- msg.Content == "approve":
			default:
			}
		}
	}
	r.mu.Lock()
	if msg.Type == "secret_response" {
		r.secret = nil
	}
	if msg.Type == "approval_response" {
		delete(r.approvals, msg.ID)
	}
	r.status = "running"
	if r.secret != nil {
		r.status = "waiting_for_input"
	}
	if len(r.approvals) > 0 {
		r.status = "waiting_for_approval"
	}
	r.broadcastLocked(map[string]any{"type": "run_status", "status": r.status})
	r.mu.Unlock()
	return true
}
