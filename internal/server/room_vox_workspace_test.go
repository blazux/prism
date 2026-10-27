package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

func TestRoomQueueSerializesAndStops(t *testing.T) {
	hub := newRoomHub()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	order := []int{}
	hub.enqueue(1, func() { close(started); <-release; mu.Lock(); order = append(order, 1); mu.Unlock() })
	<-started
	hub.enqueue(1, func() { mu.Lock(); order = append(order, 2); mu.Unlock(); close(done) })
	ctx, cancel := context.WithCancel(context.Background())
	hub.setCancel(1, cancel)
	hub.stop(1)
	if ctx.Err() == nil {
		t.Fatal("stop did not cancel the active run")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("queue stalled")
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(order) != "[1 2]" {
		t.Fatalf("order %v", order)
	}
}
func TestRoomPromptBoundsAmbientMessages(t *testing.T) {
	messages := []memory.RoomMessage{{ID: 1, Content: strings.Repeat("x", 5000)}, {ID: 2, Content: "newer context"}}
	result := roomPrompt("request", messages, 0, 200)
	if len(result) > 500 || !strings.Contains(result, "newer context") || strings.Contains(result, "xxx") {
		t.Fatalf("unbounded ambient context: %d", len(result))
	}
	if got := roomPrompt("request", messages, 2, 200); got != "request" {
		t.Fatal("replayed handled messages")
	}
}
func TestRoomArchiveKeepsToolResultsAndGroupIsolation(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	group, _ := ms.CreateGroup(ctx, "room fixtures")
	user, _ := ms.CreateUser(ctx, "room@example.test", "fixture", "Member", memory.RoleMember, memory.StatusApproved)
	_ = ms.AddGroupMember(ctx, group.ID, user.ID, memory.GroupRoleMember)
	msg, err := ms.AddRoomMessage(ctx, group.ID, nil, "Agent", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	trace := json.RawMessage(`[{"type":"tool_use","id":"t1","tool":"note","input":{"action":"list"}},{"type":"tool_result","id":"t1","output":"fixture result"}]`)
	if err := ms.UpdateRoomResponse(ctx, msg.ID, "reply", "complete", trace); err != nil {
		t.Fatal(err)
	}
	page, err := ms.RoomMessagesBefore(ctx, group.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || string(page[0].ToolEvents) != string(trace) && !strings.Contains(string(page[0].ToolEvents), "fixture result") {
		t.Fatal("lost tool trace on reload")
	}
	previous, _ := ms.RoomMessagesBefore(ctx, group.ID, msg.ID, 50)
	if len(previous) != 0 {
		t.Fatal("pagination repeats boundary")
	}
	s := &Server{memStore: ms}
	req := withUser(httptest.NewRequest("GET", fmt.Sprintf("/api/room/history?group=%d", group.ID), nil), &memory.User{ID: 999, Role: memory.RoleMember})
	w := httptest.NewRecorder()
	s.handleRoomHistory(w, req)
	if w.Code != 403 {
		t.Fatalf("other user read room: %d", w.Code)
	}
}
func TestPersonalVoiceUsesOwnerProfileAndOrganizer(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	s := &Server{memStore: ms}
	_ = ms.SetConfig(ctx, cfgProfilePhone, "+596596123456")
	_ = ms.SetConfig(ctx, cfgProfileDisplay, "Owner fixture")
	req := withUser(httptest.NewRequest("POST", "/api/profile", strings.NewReader(`{"displayName":"Owner fixture","phone":"+596596123456","transfer":"attended"}`)), serviceUser)
	response := httptest.NewRecorder()
	s.handleProfile(response, req)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	directory := s.voiceDirectory(ctx)
	if len(directory) != 1 || directory[0].Transfer != memory.TransferAttended {
		t.Fatal("personal transfer preference was ignored")
	}

	u := s.resolveVoiceCaller(ctx, "0596123456")
	if u == nil || u.ID != 0 || u.DisplayName != "Owner fixture" {
		t.Fatalf("owner not recognized: %+v", u)
	}
	if s.resolveVoiceCaller(ctx, "0596654321") != nil || s.resolveVoiceCaller(ctx, "123") != nil {
		t.Fatal("stranger identified as owner")
	}
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	e.SetSessionID("vox-owner-call")
	cc := s.callerContextForVoice(ctx, u, "vox-owner-call")
	cc.apply(e)
	_, _, err := e.Execute(ctx, "calendar", json.RawMessage(`{"action":"add","title":"Owner phone event","start":"2026-11-01T10:00:00-04:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	events, err := ms.ListEvents(ctx, "global", nil, nil)
	if err != nil || len(events) != 1 {
		t.Fatalf("phone wrote a different organizer: %v %d", err, len(events))
	}
	if !cc.HiddenTools["exec_command"] || cc.Guard("exec_command", nil) == nil {
		t.Fatal("owner call got host tools")
	}
	if serviceUser.DisplayName == "Owner fixture" {
		t.Fatal("mutated shared principal")
	}
}
func TestVoxWorkspaceScopeAndProxyAllowList(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	g, _ := ms.CreateGroup(ctx, "reception")
	other, _ := ms.CreateGroup(ctx, "other")
	u, _ := ms.CreateUser(ctx, "reception@example.test", "fixture", "Reception", memory.RoleMember, memory.StatusApproved)
	_ = ms.AddGroupMember(ctx, g.ID, u.ID, memory.GroupRoleMember)
	raw, _ := json.Marshal([]int64{g.ID})
	_ = ms.SetConfig(ctx, voxWorkspaceGroupsKey, string(raw))
	var gotScope map[string]any
	gotPath := ""
	gotSession := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSession = r.URL.Query().Get("session_id")
		_ = json.Unmarshal([]byte(r.Header.Get("X-Prism-Scope")), &gotScope)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[],"total":0}`)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL}}
	request := func(path string) *httptest.ResponseRecorder {
		r := withUser(httptest.NewRequest("GET", path, nil), u)
		r.Header.Set("X-Prism-Scope", `{"admin":true}`)
		w := httptest.NewRecorder()
		s.handleVoxWorkspaceAPI(w, r)
		return w
	}
	if w := request(fmt.Sprintf("/api/vox-workspace/history?group=%d&session_id=victim", g.ID)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if gotPath != "/api/history" || gotSession != fmt.Sprintf("group-%d", g.ID) || gotScope["admin"] != nil || gotScope["owner"] != nil || gotScope["standard"] != true {
		t.Fatalf("unsafe scope/target: %s %s %v", gotPath, gotSession, gotScope)
	}
	if w := request(fmt.Sprintf("/api/vox-workspace/history?group=%d", other.ID)); w.Code != 403 {
		t.Fatal("cross-group access")
	}
	if w := request("/api/vox-workspace/history?group=admin"); w.Code != 403 {
		t.Fatal("member read administrator archive")
	}
	if w := request("/api/vox-workspace/config"); w.Code != 405 {
		t.Fatal("configuration reached through workspace proxy")
	}
	// The group capability can read its archive, but cannot reach admin routes.
	if !groupCapabilityPath(httptest.NewRequest("GET", "/api/vox/history", nil)) || groupCapabilityPath(httptest.NewRequest("GET", "/api/vox/config", nil)) {
		t.Fatal("capability route boundary")
	}
	personal, _ := s.voxReadScope(ctx, u, false)
	if personal["owner"] != u.ID {
		t.Fatal("personal archive lost its owner")
	}
}

func TestSharedCompactionNeverMovesBoundaryBackwards(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	for _, boundary := range []int64{100, 150, 120} {
		if err := ms.AdvanceLiveCompaction(ctx, "room-g1", memory.LiveCompaction{Summary: fmt.Sprint(boundary), BeforeID: boundary}); err != nil {
			t.Fatal(err)
		}
	}
	lc := ms.GetLiveCompaction(ctx, "room-g1")
	if lc == nil || lc.BeforeID != 150 || lc.Summary != "150" {
		t.Fatalf("stale compaction won: %+v", lc)
	}
}

func TestRoomDirectLinkUsesCanonicalApp(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.handleRoomPage(w, httptest.NewRequest("GET", "/room?group=1", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/apps/room.html?group=1" {
		t.Fatalf("Room must use the same UI as the shell: %d %q", w.Code, w.Header().Get("Location"))
	}
}
