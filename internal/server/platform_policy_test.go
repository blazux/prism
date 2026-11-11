package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/docker"
	"prism/internal/memory"
)

func TestWebexSessionDeliveryNeverFallsBackToAnotherGroup(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	a, _ := ms.CreateGroup(ctx, "source")
	b, _ := ms.CreateGroup(ctx, "other")
	for _, g := range []int64{a.ID, b.ID} {
		if err := ms.SetSecret(ctx, webexKey(webexTokenSecret, g), "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	ms.SetConfig(ctx, webexKey(webexOwnerRoomKey, b.ID), "other-group-room")
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	var room string
	http.DefaultClient = &http.Client{Transport: teamsTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "webexapis.com" {
			t.Fatal("unexpected network destination")
		}
		var data map[string]any
		json.NewDecoder(r.Body).Decode(&data)
		room, _ = data["roomId"].(string)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}
	s := &Server{memStore: ms}
	s.initChannels()
	if err := s.deliverToChannelSession("webex", fmt.Sprintf("room-g%d", a.ID), "private to source group"); err == nil {
		t.Fatal("missing destination must not fall back to another group")
	}
	if room != "" {
		t.Fatalf("message left its group: %q", room)
	}
	ms.SetConfig(ctx, webexKey(webexOwnerRoomKey, a.ID), "source-group-room")
	if err := s.deliverToChannelSession("webex", fmt.Sprintf("room-g%d", a.ID), "hello"); err != nil {
		t.Fatal(err)
	}
	if room != "source-group-room" {
		t.Fatalf("wrong room: %q", room)
	}
	room = ""
	if err := s.deliverToChannelSession("webex", "u7-personal", "private"); err == nil || room != "" {
		t.Fatal("personal session must not choose a group implicitly")
	}
	member, err := ms.CreateUser(ctx, "webex@fixture.test", "unused", "member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, a.ID, member.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	if err := s.deliverToChannelSession("webex", fmt.Sprintf("u%d-personal", member.ID), "explicitly requested announcement"); err != nil || room != "source-group-room" {
		t.Fatalf("own group's personal delivery: room=%q err=%v", room, err)
	}
	room = ""
	// A group-bound script may send exact text, but cannot select another group.
	r := withUser(httptest.NewRequest("POST", "/api/webex/send", strings.NewReader(`{"text":"exact text"}`)), &memory.User{ID: -a.ID, Role: memory.RoleMember})
	r = r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, fmt.Sprintf("room-g%d", a.ID)))
	w := httptest.NewRecorder()
	s.handleWebexSend(w, r)
	if w.Code != 200 || room != "source-group-room" {
		t.Fatalf("bound delivery: %d room=%q", w.Code, room)
	}
	room = ""
	r.URL.RawQuery = fmt.Sprintf("group=%d", b.ID)
	r.Body = io.NopCloser(strings.NewReader(`{"text":"cross-group attempt"}`))
	w = httptest.NewRecorder()
	s.handleWebexSend(w, r)
	if w.Code != 403 || room != "" {
		t.Fatal("group override allowed a cross-group delivery")
	}
	ms.DeleteGroup(ctx, a.ID)
	if err := s.deliverToChannelSession("webex", fmt.Sprintf("room-g%d", a.ID), "stale task"); err == nil || room != "" {
		t.Fatal("deleted group may still send")
	}
}

func TestSharedAgentRespectsModelPolicy(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	g, _ := ms.CreateGroup(ctx, "group")
	u, _ := ms.CreateUser(ctx, "admin@review.test", "unused", "group admin", memory.RoleMember, memory.StatusApproved)
	ms.AddGroupMember(ctx, g.ID, u.ID, memory.GroupRoleAdmin)
	ms.SetConfig(ctx, keyAllowedModels, `["allowed-model"]`)
	ms.AddGroupModel(ctx, g.ID, "allowed-model")
	calls := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			http.NotFound(w, r)
			return
		}
		var data struct{ Model string }
		json.NewDecoder(r.Body).Decode(&data)
		if data.Model != "blocked-model" {
			t.Errorf("model=%q", data.Model)
		}
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"fixture result\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer llm.Close()
	s := New(Config{MultiUser: true, WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), LLMBackend: "openai", OpenAIBaseURL: llm.URL, Model: "allowed-model"})
	s.memStore = ms
	s.docker = docker.WithExecution(nil)
	req := withUser(httptest.NewRequest("POST", fmt.Sprintf("/api/room/config?group=%d", g.ID), strings.NewReader(`{"AgentModel":"blocked-model","AgentPromptProfile":"minimal"}`)), u)
	w := httptest.NewRecorder()
	s.handleRoomConfig(w, req)
	if w.Code != 403 {
		t.Fatalf("forbidden model saved: %d %s", w.Code, w.Body)
	}
	// Also reject previously stored models after the administrator restricts them.
	ms.SetRoomConfig(ctx, memory.RoomConfig{GroupID: g.ID, AgentModel: "blocked-model"})
	principal := &memory.User{ID: -g.ID, Role: memory.RoleMember}
	if s.userCanUseModel(ctx, principal, "blocked-model") {
		t.Fatal("precondition failed")
	}
	_, err := s.runHeadlessChatTap(ctx, fmt.Sprintf("room-g%d", g.ID), "hello", "blocked-model", s.callerContextForGroup(ctx, g.ID), nil, roomLimits(memory.RoomConfig{AgentPromptProfile: "minimal"}))
	if err == nil || calls != 0 {
		t.Fatalf("forbidden provider called: err=%v calls=%d", err, calls)
	}
	// Explicit restrictions do not turn a group's administrator into a global admin.
	ms.SetConfig(ctx, keyAllowedModels, `[]`)
	_, err = s.runHeadlessChatTap(ctx, fmt.Sprintf("room-g%d", g.ID), "hello", "blocked-model", s.callerContextForGroup(ctx, g.ID), nil, roomLimits(memory.RoomConfig{AgentPromptProfile: "minimal"}))
	if err == nil || calls != 0 {
		t.Fatal("group model grant ignored")
	}
	ms.AddGroupModel(ctx, g.ID, "blocked-model")
	_, err = s.runHeadlessChatTap(ctx, fmt.Sprintf("room-g%d", g.ID), "hello", "blocked-model", s.callerContextForGroup(ctx, g.ID), nil, roomLimits(memory.RoomConfig{AgentPromptProfile: "minimal"}))
	if err != nil || calls != 1 {
		t.Fatalf("permitted group model refused: err=%v calls=%d", err, calls)
	}
}

func TestDisabledCustomToolRefusedOverHTTP(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "agent_tools"), 0700)
	os.WriteFile(filepath.Join(dir, "agent_tools", "review.py"), []byte("# TOOL: {\"name\":\"review_tool\",\"description\":\"Fixture\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}\nprint('{}')\n"), 0600)
	s := New(Config{MultiUser: true, WorkspaceDir: dir, PluginDir: t.TempDir()})
	s.memStore = ms
	called := false
	s.docker = docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) {
		called = true
		return `{}`, nil
	})
	if s.customMgr.Get("review_tool") == nil {
		t.Fatal("fixture not discovered")
	}
	ms.SetToolPolicy(ctx, "review_tool", "disabled")
	if !s.hiddenToolsFor(ctx, "default", "")["review_tool"] {
		t.Fatal("precondition")
	}
	r := withUser(httptest.NewRequest("POST", "/api/tool/review_tool", strings.NewReader(`{}`)), serviceUser)
	w := httptest.NewRecorder()
	s.handleToolCall(w, r)
	if w.Code != 403 || called {
		t.Fatalf("disabled tool executed: status=%d executed=%v", w.Code, called)
	}
	guard := s.buildUserGuard(ctx, serviceUser)
	if guard == nil || guard("review_tool", nil) == nil {
		t.Fatal("admin ignored disable")
	}
	if err := guard("place_call", nil); err != nil {
		t.Fatalf("unrelated admin privileges lost: %v", err)
	}
	ms.SetToolPolicy(ctx, "review_tool", "open")
	w = httptest.NewRecorder()
	s.handleToolCall(w, r)
	if w.Code != 200 || !called {
		t.Fatalf("permitted tool refused: %d %s", w.Code, w.Body)
	}
}

func TestGroupHistoryIncludesTeamsAndKeepsOtherGroupsPrivate(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	g, _ := ms.CreateGroup(ctx, "history")
	word := "reviewuniqueword"
	for _, s := range []string{fmt.Sprintf("room-g%d", g.ID), fmt.Sprintf("webex-g%d-space", g.ID), fmt.Sprintf("teams-g%d-thread", g.ID)} {
		if _, err := ms.AppendMessage(ctx, s, "user", word, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []string{fmt.Sprintf("teams-g%d-thread", g.ID+1), "u1-private"} {
		ms.AppendMessage(ctx, session, "user", word, nil)
	}
	hits, err := ms.SearchHistory(ctx, word, 50, fmt.Sprintf("g%d", g.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("want own Room, Webex and Teams histories, got %d: %+v", len(hits), hits)
	}
	for _, hit := range hits {
		if groupScopeFromSessionID(hit.SessionID) != fmt.Sprintf("g%d", g.ID) {
			t.Fatalf("foreign history: %+v", hit)
		}
	}
}
