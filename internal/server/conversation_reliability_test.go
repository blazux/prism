package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"prism/internal/agent"
	"prism/internal/docker"
	"prism/internal/memory"
	"prism/internal/rag"
	"prism/internal/skills"
)

func waitSocketEvent(t *testing.T, c *websocket.Conn, kind, status string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		var event map[string]any
		if err := c.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event["type"] == kind && (status == "" || event["status"] == status) {
			return event
		}
	}
}

func TestChatReceiptSurvivesDisconnectAndRestart(t *testing.T) {
	ms := securityStore(t)
	var calls atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		count := 0
		for _, m := range body.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "unique delivery request") {
				count++
			}
		}
		if count != 1 {
			t.Errorf("request appeared %d times in model context", count)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	makeServer := func() (*Server, *httptest.Server) {
		s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), LLMBackend: "openai", OpenAIBaseURL: provider.URL, Model: "fixture"})
		s.memStore = ms
		s.docker = docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) { return "", nil })
		return s, httptest.NewServer(http.HandlerFunc(s.handleWS))
	}
	s, httpServer := makeServer()
	defer httpServer.Close()
	dial := func(server *httptest.Server) *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?session=delivery", nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first := dial(httpServer)
	request := WSMessage{Type: "chat", RequestID: "reliable-request-1234", Content: "unique delivery request"}
	if err := first.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	waitSocketEvent(t, first, "chat_receipt", "accepted")
	// A receipt means the user row is already durable, even before inference ends.
	history, err := ms.LoadHistory(t.Context(), "delivery")
	if err != nil || len(history) != 1 {
		t.Fatalf("receipt without durable request: %v %+v", err, history)
	}
	first.Close()
	second := dial(httpServer)
	defer second.Close()
	if err := second.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	if event := waitSocketEvent(t, second, "chat_receipt", ""); event["replayed"] != true {
		t.Fatal("duplicate request was not recognized", event)
	}
	once.Do(func() { close(release) })
	waitSocketEvent(t, second, "run_status", "completed")
	if calls.Load() != 1 {
		t.Fatalf("duplicate provider calls: %d", calls.Load())
	}
	second.Close()
	_, restarted := makeServer()
	defer restarted.Close()
	third := dial(restarted)
	defer third.Close()
	if err := third.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	event := waitSocketEvent(t, third, "chat_receipt", "completed")
	if event["replayed"] != true || calls.Load() != 1 {
		t.Fatal("restart lost receipt", event)
	}
	request.Content = "different content with same ID"
	third.WriteJSON(request)
	waitSocketEvent(t, third, "chat_receipt", "conflict")
	history, err = ms.LoadHistory(t.Context(), "delivery")
	if err != nil || len(history) != 2 {
		t.Fatalf("duplicate archive rows: %+v %v", history, err)
	}
	// A different namespace never inherits another conversation's receipt.
	other := &Client{sessionID: "other", send: make(chan []byte, 3)}
	if s.chatRequestStatus(other, request.RequestID, chatRequestDigest(request)) {
		t.Fatal("cross-session receipt")
	}
}

func TestHistoryPagesPreserveToolMetadataAndBoundaries(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	var calls []map[string]any
	for i := range 120 {
		calls = append(calls, map[string]any{"function": map[string]any{"name": fmt.Sprintf("tool%d", i), "arguments": map[string]any{"index": i}}})
	}
	raw, _ := json.Marshal(calls)
	ms.AppendMessage(ctx, "u1-archive", "user", "archive start", nil)
	ms.AppendMessage(ctx, "u1-archive", "assistant", "", raw)
	for i := range 120 {
		ms.AppendMessage(ctx, "u1-archive", "tool", fmt.Sprintf("result%d", i), nil)
	}
	ms.AppendMessage(ctx, "u1-archive", "assistant", "final answer", nil)
	page, err := ms.LoadHistoryPage(ctx, "u1-archive", 0, 100)
	if err != nil || len(page.Entries) != 100 || !page.HasMore {
		t.Fatalf("unbounded/broken page: %+v %v", page, err)
	}
	if page.ToolOffset != 21 {
		t.Fatalf("lost tool offset at page boundary: %d", page.ToolOffset)
	}
	s := &Server{memStore: ms}
	c := &Client{send: make(chan []byte, 1)}
	s.sendHistory(ctx, c, "u1-archive")
	var shown struct {
		Messages []struct {
			ToolName string `json:"toolName"`
		}
	}
	if err := json.Unmarshal(<-c.send, &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Messages[0].ToolName != "tool21" {
		t.Fatal("wrong first tool on split batch", shown.Messages[0])
	}
	older, err := ms.LoadHistoryPage(ctx, "u1-archive", page.Before, 100)
	if err != nil || older.HasMore || len(older.Entries) != 23 {
		t.Fatalf("older page %+v %v", older, err)
	}
	if older.Entries[len(older.Entries)-1].ID >= page.Entries[0].ID {
		t.Fatal("page overlap")
	}
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	e.SetMultiUserMode(true)
	e.SetSessionID("u1-work")
	e.SetPersonalScope("u1")
	if _, _, err := e.Execute(ctx, "search_history", json.RawMessage(`{"session":"u1-archive","limit":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []string{"u10-archive", "room-g1", "teams-g1-channel"} {
		body, _ := json.Marshal(map[string]any{"session": foreign})
		if _, _, err := e.Execute(ctx, "search_history", body); err == nil {
			t.Fatal("foreign conversation read", foreign)
		}
	}
	// A complete old message can exceed one tool result, including multibyte text.
	long := strings.Repeat("é🟣", 7000) + " exact archive ending"
	id, err := ms.AppendMessage(ctx, "u1-archive", "user", long, nil)
	if err != nil {
		t.Fatal(err)
	}
	var collected strings.Builder
	offset := 0
	for {
		body, _ := json.Marshal(map[string]any{"session": "u1-archive", "message_id": id, "offset": offset})
		text, _, err := e.Execute(ctx, "search_history", body)
		if err != nil {
			t.Fatal(err)
		}
		if len(text) > 24000 || strings.Contains(text, "[truncated:") {
			t.Fatal("history chunk exceeded executor output limit")
		}
		var chunk struct {
			Text       string
			NextOffset *int
		}
		if err := json.Unmarshal([]byte(text), &chunk); err != nil {
			t.Fatal(err)
		}
		collected.WriteString(chunk.Text)
		if chunk.NextOffset == nil {
			break
		}
		if *chunk.NextOffset <= offset {
			t.Fatal("non-advancing cursor")
		}
		offset = *chunk.NextOffset
	}
	var original memory.HistoryEntry
	if err := json.Unmarshal([]byte(collected.String()), &original); err != nil || original.ID != id || original.Content != long {
		t.Fatal("long message was not recovered in full", err)
	}
}

func TestMemoryCurationUsesPersonalScopeAndKeepsContextBoundary(t *testing.T) {
	ms, dsn := securityStoreWithDSN(t)
	ctx := t.Context()
	store, err := rag.NewStore(ctx, dsn, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	u1 := &memory.User{ID: 1, Role: memory.RoleMember}
	u2 := &memory.User{ID: 2, Role: memory.RoleMember}
	for _, scope := range []string{"u1", "u2", "g1"} {
		col := scopeCollection(scope, "user-profile")
		store.EnsureCollection(ctx, col, scope)
		if err := store.UpsertDocument(ctx, col, "location", "", 4, []string{scope + " fact"}, []int{0}, [][]float32{{1, 0, 0}}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{memStore: ms, ragStore: store, cfg: Config{MultiUser: true, WorkspaceDir: t.TempDir()}}
	ms.SetLiveCompaction(ctx, "u1-main", memory.LiveCompaction{Summary: "old summary", BeforeID: 42})
	ms.SetConfig(ctx, "conversation_summary_u1-main", "earlier decisions")
	request := func(method, path, body string, user *memory.User) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user)
		if strings.HasPrefix(path, "/api/chat/context") {
			s.handleConversationContext(w, r)
		} else {
			s.handleMemory(w, r)
		}
		return w
	}
	w := request("GET", "/api/memory?session=main", "", u1)
	if w.Code != 200 || strings.Contains(w.Body.String(), "u2") || strings.Contains(w.Body.String(), "g1") {
		t.Fatalf("foreign memory metadata: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "summary") {
		t.Fatal("settings included conversation context", w.Body)
	}
	w = request("GET", "/api/chat/context?session=main", "", u1)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "earlier decisions") {
		t.Fatal("chat summary missing", w.Code, w.Body)
	}
	w = request("GET", "/api/memory?session=main&kind=profile&name=location", "", u1)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "u1 fact") {
		t.Fatal(w.Code, w.Body)
	}
	var item struct{ Revision string }
	json.Unmarshal(w.Body.Bytes(), &item)
	w = request("PATCH", "/api/chat/context?session=main", `{"kind":"context","content":"corrected summary","original":"old summary"}`, u1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	lc := ms.GetLiveCompaction(ctx, "u1-main")
	if lc.BeforeID != 42 || lc.Summary != "corrected summary" {
		t.Fatal("context correction moved the boundary", lc)
	}
	if w = request("PATCH", "/api/chat/context?session=main", `{"kind":"context","content":"stale overwrite","original":"old summary"}`, u1); w.Code != 409 {
		t.Fatal("stale context overwrote correction")
	}
	if w = request("PATCH", "/api/chat/context?session=main", `{"kind":"archive","content":"corrected earlier decisions","original":"earlier decisions"}`, u1); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if ms.GetLiveCompaction(ctx, "u1-main").BeforeID != 42 || ms.GetSummary(ctx, "u1-main") != "corrected earlier decisions" {
		t.Fatal("summary correction changed the wrong memory")
	}
	previous, err := store.FindDocument(ctx, scopeCollection("u1", "user-profile"), "location")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CorrectDocument(ctx, *previous, "corrected fact", []string{"corrected fact"}, [][]float32{{0, 1, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CorrectDocument(ctx, *previous, "stale overwrite", []string{"stale overwrite"}, [][]float32{{0, 0, 1}}); err != rag.ErrDocumentChanged {
		t.Fatal("concurrent memory correction overwritten", err)
	}
	w = request("GET", "/api/memory?session=main&kind=profile&name=location", "", u1)
	json.Unmarshal(w.Body.Bytes(), &item)
	if w = request("GET", "/api/chat/context?session=u1-main", "", u2); w.Code != 403 {
		t.Fatal("foreign context read")
	}
	deleteBody, _ := json.Marshal(map[string]string{"kind": "profile", "name": "location", "revision": item.Revision})
	if w = request("DELETE", "/api/memory?session=main", string(deleteBody), u1); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if doc, _ := store.FindDocument(ctx, scopeCollection("u2", "user-profile"), "location"); doc == nil {
		t.Fatal("deleted another user's fact")
	}
	// The UI reads the same skill namespace as the agent, including in multi-user mode.
	os.MkdirAll(s.cfg.WorkspaceDir+"/skills", 0700)
	for _, scope := range []string{"u1", "u2", ""} {
		skills.NewManager(s.cfg.WorkspaceDir + "/skills/" + scope).Save(skills.Skill{Name: "fixture-" + scope, Body: "steps"})
	}
	w = httptest.NewRecorder()
	s.handleSkills(w, withUser(httptest.NewRequest("GET", "/api/skills", nil), u1))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture-u1") || strings.Contains(w.Body.String(), "fixture-u2") {
		t.Fatal("skill scope mismatch", w.Body)
	}
}
