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
	"unicode/utf8"

	"prism/internal/docker"
	"prism/internal/memory"
)

func roomActivityRequest(s *Server, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/my/groups":
			s.handleMyGroups(w, r)
		case "/api/room/activity":
			s.handleRoomActivity(w, r)
		default:
			s.handleSessionByID(w, r)
		}
	})).ServeHTTP(w, r)
	return w
}

func readRoomActivity(t *testing.T, s *Server, gid int64, token string) roomActivity {
	t.Helper()
	w := roomActivityRequest(s, "GET", fmt.Sprintf("/api/room/activity?group=%d", gid), token)
	if w.Code != 200 {
		t.Fatalf("room activity: %d %s", w.Code, w.Body.String())
	}
	var result roomActivity
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRoomActivityFollowsCurrentMembershipAndKeepsWorkspacesPrivate(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u, err := ms.CreateUser(ctx, "room-activity@example.test", "fixture", "Member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ms.CreateUser(ctx, "room-admin@example.test", "fixture", "Admin", memory.RoleGlobalAdmin, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	group, err := ms.CreateGroup(ctx, "Support activity")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(ctx, "Other activity")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	private := fmt.Sprintf("u%d-project", u.ID)
	if err := ms.EnsureSession(ctx, private, "Private project", &u.ID); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub(), cfg: Config{MultiUser: true, AuthToken: "fixture"}}
	token := s.selfCallToken(fmt.Sprintf("u%d-telegram", u.ID))
	adminToken := s.selfCallToken(fmt.Sprintf("u%d-telegram", admin.ID))
	if got := readRoomActivity(t, s, group.ID, token); got.RunStatus != "idle" {
		t.Fatal("empty Room not idle")
	}
	for _, tc := range []struct {
		method, path, token string
		code                int
	}{
		{"GET", fmt.Sprintf("/api/room/activity?group=%d", other.ID), token, 403},
		{"GET", fmt.Sprintf("/api/room/activity?group=%d", group.ID), "", 401},
		{"GET", "/api/room/activity?group=invalid", token, 400},
		{"GET", fmt.Sprintf("/api/room/activity?group=%d&limit=0", group.ID), token, 400},
		{"POST", fmt.Sprintf("/api/room/activity?group=%d", group.ID), token, 405},
		{"DELETE", fmt.Sprintf("/api/room/activity?group=%d", group.ID), token, 405},
		{"GET", "/api/sessions/" + private + "/activity", adminToken, 404},
		{"GET", fmt.Sprintf("/api/room/activity?group=%d", other.ID), s.selfCallToken(fmt.Sprintf("room-g%d", group.ID)), 403},
	} {
		w := roomActivityRequest(s, tc.method, tc.path, tc.token)
		if w.Code != tc.code {
			t.Errorf("%s %s: %d, want %d: %s", tc.method, tc.path, w.Code, tc.code, w.Body.String())
		}
	}
	readRoomActivity(t, s, other.ID, adminToken) // Existing global-admin Room rights.
	readRoomActivity(t, s, group.ID, s.selfCallToken(fmt.Sprintf("room-g%d", group.ID)))
	w := roomActivityRequest(s, "GET", "/api/my/groups", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Support activity") || strings.Contains(w.Body.String(), "Other activity") {
		t.Fatal("group discovery escaped membership")
	}
	if err := ms.RemoveGroupMember(ctx, group.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if w := roomActivityRequest(s, "GET", fmt.Sprintf("/api/room/activity?group=%d", group.ID), token); w.Code != 403 {
		t.Fatal("old token retained removed membership")
	}
	if err := ms.SetUserStatus(ctx, u.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if w := roomActivityRequest(s, "GET", "/api/my/groups", token); w.Code != 401 {
		t.Fatal("disabled user retained access")
	}
}

func TestRoomActivityBoundsContentAndReportsQueueAndRestart(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	group, err := ms.CreateGroup(ctx, "Queue activity")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub(), cfg: Config{MultiUser: true, AuthToken: "fixture"}}
	token := s.selfCallToken(fmt.Sprintf("room-g%d", group.ID))
	reply, err := ms.AddRoomMessage(ctx, group.ID, nil, "Agent", "Initial reply", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	trace := json.RawMessage(`[{"type":"tool_result","output":"PRIVATE TOOL OUTPUT"}]`)
	if err := ms.UpdateRoomResponse(ctx, reply.ID, "Initial reply", "complete", trace); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		if _, err := ms.AddRoomMessage(ctx, group.ID, nil, "Member", fmt.Sprintf("Message %d ", i)+strings.Repeat("é", 3000), false, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := readRoomActivity(t, s, group.ID, token)
	if got.RunStatus != "completed" || len(got.Messages) != 4 {
		t.Fatalf("lost last agent state: %+v", got)
	}
	for i, m := range got.Messages {
		if len(m.ToolEvents) != 0 || !utf8.ValidString(m.Content) || utf8.RuneCountInString(m.Content) != 1001 || !strings.HasPrefix(m.Content, fmt.Sprintf("Message %d", i+3)) {
			t.Fatal("unbounded or unordered Room excerpt")
		}
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s.rooms.enqueue(group.ID, func() { close(started); <-release })
	<-started
	s.rooms.enqueue(group.ID, func() { close(done) })
	got = readRoomActivity(t, s, group.ID, token)
	if got.RunStatus != "running" || got.Queued != 1 {
		t.Fatalf("worker or queue not detected: %+v", got)
	}
	select {
	case <-done:
		t.Fatal("progress lookup disturbed queue")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queue failed to finish")
	}
	// Observe completion of worker bookkeeping too.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.rooms.mu.Lock()
		busy := s.rooms.executing[group.ID]
		s.rooms.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker remained busy")
		}
		time.Sleep(time.Millisecond)
	}
	for _, tc := range []struct{ saved, want string }{{"complete", "completed"}, {"error", "failed"}, {"stopped", "stopped"}, {"running", "interrupted"}} {
		if err := ms.UpdateRoomResponse(ctx, reply.ID, "Saved reply", tc.saved, trace); err != nil {
			t.Fatal(err)
		}
		if got := readRoomActivity(t, s, group.ID, token); got.RunStatus != tc.want {
			t.Fatalf("saved %s became %s", tc.saved, got.RunStatus)
		}
	}
}

func TestTelegramMultiReadsLiveRoomWithPersonalIdentity(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u, err := ms.CreateUser(ctx, "telegram-room@example.test", "fixture", "Member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	group, err := ms.CreateGroup(ctx, "Support")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("u%d-telegram", u.ID)
	path := fmt.Sprintf("/api/room/activity?group=%d", group.ID)
	roomBusy, releaseRoom, roomDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRoom) })
	var mu sync.Mutex
	roomCalls, telegramCalls := 0, 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		isTelegram := false
		for _, m := range body.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "Current session ID: `"+session+"`") {
				isTelegram = true
			}
		}
		mu.Lock()
		var n int
		if isTelegram {
			n = telegramCalls
			telegramCalls++
		} else {
			n = roomCalls
			roomCalls++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 0 {
			name, args := "note", `{"action":"list"}`
			if isTelegram {
				promptFound := false
				for _, m := range body.Messages {
					if m.Role == "system" && strings.Contains(m.Content, "/api/room/activity?group=<id>") {
						promptFound = true
					}
				}
				if !promptFound {
					t.Error("multi-user Telegram is missing Room instructions")
				}
				name = "exec_command"
				raw, _ := json.Marshal(map[string]string{"command": `curl -sS "$PRISM_URL` + path + `" -H "Authorization: Bearer $PRISM_TOKEN"`})
				args = string(raw)
			}
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "check", "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
			return
		}
		if !isTelegram {
			close(roomBusy)
			select {
			case <-releaseRoom:
			case <-r.Context().Done():
				return
			}
		} else {
			found := false
			for _, m := range body.Messages {
				if m.Role == "tool" && strings.Contains(m.Content, `"runStatus":"running"`) && strings.Contains(m.Content, `"tool":"note"`) {
					found = true
				}
			}
			if !found {
				t.Error("live Room progress did not reach the personal agent")
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Support is working on the request.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	s := New(Config{MultiUser: true, AuthToken: "fixture", WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: model.URL})
	s.memStore = ms
	s.tgScope(u.ID).SetConfig(ctx, tgAllowedChatKey, "42")
	observations := 0
	s.docker = docker.WithExecution(func(_ context.Context, command string, _ []byte, env map[string]string) (string, error) {
		if !strings.Contains(command, path) {
			return "", nil
		}
		observations++
		uid, bound, ok := s.verifyCapToken(env["PRISM_TOKEN"])
		if !ok || uid != u.ID || bound != session {
			t.Error("Telegram impersonated the shared agent")
		}
		w := roomActivityRequest(s, "GET", path, env["PRISM_TOKEN"])
		if w.Code != 200 {
			t.Errorf("Room lookup failed: %d %s", w.Code, w.Body.String())
		}
		return w.Body.String(), nil
	})
	trigger, err := ms.AddRoomMessage(ctx, group.ID, &u.ID, "Member", "@agent Review notes", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ms.GetRoomConfig(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AgentModel = "fixture"
	s.rooms.enqueue(group.ID, func() { defer close(roomDone); s.runRoomAgent(group.ID, cfg, "Member", trigger.Content, trigger.ID) })
	defer func() {
		releaseOnce.Do(func() { close(releaseRoom) })
		select {
		case <-roomDone:
		case <-time.After(5 * time.Second):
			t.Error("Room fixture did not shut down")
		}
	}()
	select {
	case <-roomBusy:
	case <-time.After(5 * time.Second):
		t.Fatal("Room agent did not reach its live tool step")
	}
	var delivered strings.Builder
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			delivered.WriteString(r.FormValue("text"))
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer telegram.Close()
	s.handleTelegramMessage(ctx, telegram.URL+"/botfixture", "fixture", u.ID, 42, "Is Support finished?", nil, nil)
	if observations != 1 || !strings.Contains(delivered.String(), "Support is working") {
		t.Fatalf("Telegram follow-up failed: %q", delivered.String())
	}
	select {
	case <-roomDone:
		t.Fatal("progress check interrupted the shared agent")
	default:
	}
	releaseOnce.Do(func() { close(releaseRoom) })
	select {
	case <-roomDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Room did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if roomCalls != 2 || telegramCalls != 2 {
		t.Fatalf("progress check started extra Room turns: room=%d telegram=%d", roomCalls, telegramCalls)
	}
}
