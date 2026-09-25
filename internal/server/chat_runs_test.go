package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"prism/internal/docker"
	"prism/internal/memory"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readRunEvent(t *testing.T, c *websocket.Conn, kind string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var m map[string]any
		if err := c.ReadJSON(&m); err != nil {
			t.Fatal(err)
		}
		if m["type"] == kind {
			return m
		}
	}
}
func TestDashboardRunSurvivesDisconnectAndReplays(t *testing.T) {
	t.Run("legacy", func(t *testing.T) { checkDashboardRunReplay(t, nil) })
	t.Run("namespaced-long-workspace", func(t *testing.T) { checkDashboardRunReplay(t, &memory.User{ID: 42}) })
}
func checkDashboardRunReplay(t *testing.T, user *memory.User) {
	t.Setenv("PATH", t.TempDir())
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Beginning. \"}}]}\n\n")
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != nil {
			r = withUser(r, user)
		}
		s.handleWS(w, r)
	}))
	defer httpServer.Close()
	dial := func(session string) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"?session="+session, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	session := "a-workspace-name-with-thirty-two-characters"
	c := dial(session)
	readRunEvent(t, c, "run_status")
	if err := c.WriteJSON(map[string]any{"type": "chat", "content": "Do the fixture task"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model not called")
	}
	c.Close()
	other := dial("other")
	readRunEvent(t, other, "run_status")
	other.Close()
	stored, ok := s.sessionFor(reqAs(user), session)
	if !ok {
		t.Fatal("session rejected")
	}
	resumed := dial(stored)
	defer resumed.Close()
	replay := readRunEvent(t, resumed, "run_replay")
	if replay["status"] != "running" {
		t.Fatal(replay)
	}
	close(release)
	for {
		m := readRunEvent(t, resumed, "run_status")
		if m["status"] == "completed" {
			break
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("reconnection started %d model calls", calls.Load())
	}
	resumed.Close()
	final := dial(stored)
	defer final.Close()
	m := readRunEvent(t, final, "run_replay")
	raw, _ := json.Marshal(m)
	if m["status"] != "completed" || !strings.Contains(string(raw), "Finished.") {
		t.Fatal("result lost after offline completion")
	}
}
func TestRunIsolationAndDetachedApprovals(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &Client{user: &memory.User{ID: 1}, sessionID: "fixture", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, cancel, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	stranger := &Client{user: &memory.User{ID: 2}, sessionID: "fixture", send: make(chan []byte, 30)}
	s.attachRun(stranger)
	if stranger.currentRun() != nil {
		t.Fatal("cross-user attachment")
	}
	ch := make(chan bool, 1)
	owner.approvals = map[string]chan bool{"child/tool_1": ch}
	owner.sendJSON(map[string]any{"type": "approval_request", "id": "child/tool_1"})
	s.detachRun(owner)
	same := &Client{user: &memory.User{ID: 1}, sessionID: "fixture", send: make(chan []byte, 30)}
	s.attachRun(same)
	if same.currentRun() != run {
		t.Fatal("run was lost")
	}
	if s.routeRunControl(stranger, WSMessage{Type: "approval_response", ID: "child/tool_1", Content: "approve"}) {
		t.Fatal("stranger routed")
	}
	s.routeRunControl(same, WSMessage{Type: "approval_response", ID: "wrong", Content: "approve"})
	select {
	case <-ch:
		t.Fatal("stale approval accepted")
	default:
	}
	s.routeRunControl(same, WSMessage{Type: "approval_response", ID: "child/tool_1", Content: "approve"})
	if !<-ch {
		t.Fatal("approval lost")
	}
	s.finishRun(run, ctx)
}

func TestVoiceDisconnectStillCancels(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	entered, cancelled := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Speaking\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleWS(w, withUser(r, serviceUser)) }))
	defer host.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"?session=voice&channel=voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteJSON(map[string]any{"type": "chat", "content": "Hello", "channel": "voice"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("voice did not start")
	}
	c.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("voice kept running after hangup")
	}
}

func TestMultiplePendingApprovalsAndSecretSurviveReconnect(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &Client{sessionID: "fixture", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, cancel, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := make(chan bool, 1), make(chan bool, 1)
	owner.approvals = map[string]chan bool{"a/tool_1": a, "b/tool_1": b}
	for id := range owner.approvals {
		owner.sendJSON(map[string]any{"type": "approval_request", "id": id})
	}
	s.detachRun(owner)
	peer := &Client{sessionID: "fixture", send: make(chan []byte, 30)}
	s.attachRun(peer)
	s.routeRunControl(peer, WSMessage{Type: "approval_mode", Content: "manual"})
	if !peer.approvalManual || run.approvalManual {
		t.Fatal("reconnect changed current policy or lost next task policy")
	}
	pending := 0
	for len(peer.send) > 0 {
		var m map[string]any
		json.Unmarshal(<-peer.send, &m)
		if m["type"] == "approval_request" {
			pending++
		}
	}
	if pending != 2 {
		t.Fatalf("replayed %d approvals", pending)
	}
	s.routeRunControl(peer, WSMessage{Type: "approval_response", ID: "a/tool_1", Content: "approve"})
	if !<-a {
		t.Fatal("wrong approval verdict")
	}
	if run.status != "waiting_for_approval" {
		t.Fatal("remaining approval lost")
	}
	s.routeRunControl(peer, WSMessage{Type: "approval_response", ID: "b/tool_1", Content: "reject"})
	if <-b {
		t.Fatal("rejection lost")
	}
	owner.pendingSecretCh = make(chan string, 1)
	owner.pendingSecretID = "secret-fixture"
	owner.sendJSON(map[string]any{"type": "secret_request", "id": "secret-fixture", "name": "fixture"})
	secret := owner.pendingSecretCh
	s.routeRunControl(peer, WSMessage{Type: "secret_response", ID: "stale", Content: "wrong"})
	select {
	case <-secret:
		t.Fatal("stale secret accepted")
	default:
	}
	s.routeRunControl(peer, WSMessage{Type: "secret_response", ID: "secret-fixture", Content: "fixture-value"})
	if <-secret != "fixture-value" {
		t.Fatal("secret response lost")
	}
	raw, _ := json.Marshal(run.events)
	if strings.Contains(string(raw), "fixture-value") {
		t.Fatal("secret retained in replay journal")
	}
	s.finishRun(run, ctx)
}

func TestPersistedRunReconnectAndRestart(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &Client{sessionID: "persisted-fixture", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, cancel, "request", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ms.AppendMessage(ctx, owner.sessionID, "assistant", "Saved offline result", nil); err != nil {
		t.Fatal(err)
	}
	// A fresh server cannot resume side effects from a task left running by a crash.
	restarted := &Server{memStore: ms}
	peer := &Client{sessionID: owner.sessionID, send: make(chan []byte, 30)}
	restarted.attachRun(peer)
	var history, status map[string]any
	json.Unmarshal(<-peer.send, &history)
	json.Unmarshal(<-peer.send, &status)
	raw, _ := json.Marshal(history)
	if status["status"] != "interrupted" || !strings.Contains(string(raw), "Saved offline result") {
		t.Fatalf("restart lost state: %v %s", status, raw)
	}
	s.finishRun(run, ctx)
	peer = &Client{sessionID: owner.sessionID, send: make(chan []byte, 30)}
	restarted.attachRun(peer)
	json.Unmarshal(<-peer.send, &history)
	json.Unmarshal(<-peer.send, &status)
	if status["status"] != "completed" {
		t.Fatal(status)
	}
	if len(s.completedRuns) != 0 {
		t.Fatal("database-backed run retained a duplicate memory journal")
	}
}
