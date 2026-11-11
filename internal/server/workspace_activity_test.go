package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"prism/internal/agent"
	"prism/internal/docker"
	"prism/internal/memory"
)

func readActivity(t *testing.T, handler http.Handler, path, token string) workspaceActivity {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("activity failed: %d %s", w.Code, w.Body.String())
	}
	var result workspaceActivity
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWorkspaceActivityReadsLiveRunWithoutChangingIt(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms, cfg: Config{AuthToken: "fixture"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := ms.EnsureSession(ctx, "report", "Report", nil); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		if _, err := ms.AppendMessage(ctx, "report", "assistant", fmt.Sprintf("Step %d ", i)+strings.Repeat("é", 3000), nil); err != nil {
			t.Fatal(err)
		}
	}
	ms.AppendMessage(ctx, "report", "tool", "PRIVATE TOOL PAYLOAD", nil)
	owner := &Client{user: serviceUser, sessionID: "report", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, cancel, "Prepare the report", nil)
	if err != nil {
		t.Fatal(err)
	}
	run.children = map[string]*childTask{"child": {id: "child", status: "running", cancel: func() {}}}
	for i := range 10 {
		owner.sendJSON(agent.Event{Type: "tool_use", ID: fmt.Sprint(i), Tool: "read_file", Input: json.RawMessage(`{"path":"PRIVATE ARGUMENT"}`)})
		owner.sendJSON(agent.Event{Type: "tool_result", ID: fmt.Sprint(i), Output: "PRIVATE RESULT", IsError: i == 9})
	}
	owner.sendJSON(map[string]any{"type": "secret_request", "name": "PRIVATE SECRET NAME"})
	handler := s.withAuth(http.HandlerFunc(s.handleSessionByID))
	result := readActivity(t, handler, "/api/sessions/report/activity", s.selfCallToken("telegram"))
	if result.RunStatus != "waiting_for_input" || result.Request != "Prepare the report" || len(result.Subagents) != 1 || len(result.RecentTools) != 8 || len(result.Messages) != 4 {
		t.Fatalf("incomplete progress: %+v", result)
	}
	if result.RecentTools[0].ID != "2" || result.RecentTools[7].Status != "failed" {
		t.Fatalf("tool states lost: %+v", result.RecentTools)
	}
	for i, message := range result.Messages {
		if utf8.RuneCountInString(message.Content) != 1001 || !utf8.ValidString(message.Content) || !strings.HasPrefix(message.Content, fmt.Sprintf("Step %d", i+3)) {
			t.Fatal("transcript is unbounded or not chronological")
		}
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "tool_calls") {
		t.Fatal("raw tool payload or secret dialog included in progress")
	}
	if owner.currentRun() != run || ctx.Err() != nil {
		t.Fatal("progress check changed or cancelled the task")
	}
	owner.sendJSON(map[string]any{"type": "approval_request", "id": "approval", "input": "PRIVATE APPROVAL"})
	if got := readActivity(t, handler, "/api/sessions/report/activity?limit=1", "fixture"); got.RunStatus != "waiting_for_approval" || len(got.Messages) != 1 {
		t.Fatal("approval state or message limit lost")
	}
	s.finishRun(run, ctx)
	if got := readActivity(t, handler, "/api/sessions/report/activity", "fixture"); got.RunStatus != "completed" || got.Request != "" || len(got.Subagents) != 0 {
		t.Fatalf("finished task still looks active: %+v", got)
	}
	ms.SetConfig(ctx, runStateKey(owner), "running")
	if got := readActivity(t, handler, "/api/sessions/report/activity", "fixture"); got.RunStatus != "interrupted" {
		t.Fatal("stale running state not detected")
	}
	_, finish, err := s.beginSessionJob(ctx, "report")
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if got := readActivity(t, handler, "/api/sessions/report/activity", "fixture"); got.RunStatus != "running" || got.BackgroundJobs != 1 {
		t.Fatal("headless task not detected")
	}
}

func TestWorkspaceActivityKeepsPersonalOwnershipAndMutationScopes(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	users := []*memory.User{}
	for i := range 2 {
		u, err := ms.CreateUser(ctx, fmt.Sprintf("progress%d@example.test", i), "fixture", "Owner", memory.RoleMember, memory.StatusApproved)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
		id := fmt.Sprintf("u%d-project", u.ID)
		if err := ms.EnsureSession(ctx, id, "Project", &u.ID); err != nil {
			t.Fatal(err)
		}
		ms.AppendMessage(ctx, id, "assistant", fmt.Sprintf("Owner %d result", u.ID), nil)
	}
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, AuthToken: "fixture"}}
	handler := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			s.handleChatHTTP(w, r)
		case "/api/sessions":
			s.handleSessions(w, r)
		default:
			s.handleSessionByID(w, r)
		}
	}))
	uid := users[0].ID
	session := fmt.Sprintf("u%d-project", uid)
	token := s.selfCallToken(fmt.Sprintf("u%d-telegram", uid))
	if got := readActivity(t, handler, "/api/sessions/"+session+"/activity", token); len(got.Messages) != 1 || got.Messages[0].Content != fmt.Sprintf("Owner %d result", uid) {
		t.Fatal("Telegram capability cannot read its owner's workspace")
	}
	group, err := ms.CreateGroup(ctx, "Progress group")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, token, body string
		code                      int
	}{
		{"GET", fmt.Sprintf("/api/sessions/u%d-project/activity", users[1].ID), token, "", 404},
		{"GET", "/api/sessions/missing/activity", token, "", 404},
		{"GET", "/api/sessions/" + session + "/activity", "", "", 401},
		{"GET", "/api/sessions/" + session + "/activity", s.selfCallToken(fmt.Sprintf("room-g%d", group.ID)), "", 401},
		{"GET", "/api/sessions/" + session + "/activity?limit=100000", token, "", 400},
		{"POST", "/api/sessions/" + session + "/activity", token, "{}", 405},
		{"DELETE", "/api/sessions/" + session + "/activity", token, "", 405},
		{"POST", "/api/chat", token, `{"session":"` + session + `","message":"new task"}`, 403},
		{"PATCH", "/api/sessions/" + session, token, `{"name":"Renamed"}`, 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("%s %s: got %d, want %d: %s", tc.method, tc.path, w.Code, tc.code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), fmt.Sprintf("u%d-project", users[1].ID)) || !strings.Contains(w.Body.String(), session) {
		t.Fatalf("workspace discovery scope incorrect: %d %s", w.Code, w.Body.String())
	}
}

func TestTelegramFollowsDashboardTaskThroughExistingExecutionTool(t *testing.T) {
	ms := securityStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ms.EnsureSession(ctx, "report", "Report", nil)
	ms.SetConfig(ctx, tgAllowedChatKey, "42")
	var delivered strings.Builder
	requests := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		json.NewDecoder(r.Body).Decode(&body)
		if requests == 0 {
			found := false
			for _, m := range body.Messages {
				if m.Role == "system" && strings.Contains(m.Content, "/api/sessions/<id>/activity") {
					found = true
				}
			}
			if !found {
				t.Error("Telegram model has no workspace follow-up instructions")
			}
		} else {
			found := false
			for _, m := range body.Messages {
				if m.Role == "tool" && strings.Contains(m.Content, `"runStatus":"running"`) && strings.Contains(m.Content, "read_file") {
					found = true
				}
			}
			if !found {
				t.Error("workspace observation did not reach the Telegram agent")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 0 {
			args, _ := json.Marshal(map[string]string{"command": `curl -sS "$PRISM_URL/api/sessions/report/activity" -H "Authorization: Bearer $PRISM_TOKEN"`})
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "progress", "type": "function", "function": map[string]string{"name": "exec_command", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The report is running and reading a file.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
		requests++
	}))
	defer model.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), AuthToken: "fixture", Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: model.URL})
	s.memStore = ms
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			delivered.WriteString(r.FormValue("text"))
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer telegram.Close()
	owner := &Client{user: serviceUser, sessionID: "report", send: make(chan []byte, 10)}
	run, err := s.beginRun(owner, cancel, "Prepare report", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.finishRun(run, ctx)
	owner.sendJSON(agent.Event{Type: "tool_use", ID: "read", Tool: "read_file"})
	observations := 0
	s.docker = docker.WithExecution(func(_ context.Context, command string, _ []byte, env map[string]string) (string, error) {
		if !strings.Contains(command, "/api/sessions/report/activity") {
			return "", nil // unrelated workspace/git bookkeeping
		}
		observations++
		if env["PRISM_SESSION"] != "telegram" {
			t.Error("workspace check lost the Telegram execution session")
		}
		r := httptest.NewRequest("GET", "/api/sessions/report/activity", nil)
		r.Header.Set("Authorization", "Bearer "+env["PRISM_TOKEN"])
		w := httptest.NewRecorder()
		s.withAuth(http.HandlerFunc(s.handleSessionByID)).ServeHTTP(w, r)
		return w.Body.String(), nil
	})
	s.handleTelegramMessage(ctx, telegram.URL+"/botfixture", "fixture", 0, 42, "How is Report progressing?", nil, nil)
	if requests != 2 || observations != 1 || !strings.Contains(delivered.String(), "report is running") || owner.currentRun() != run || ctx.Err() != nil {
		t.Fatalf("Telegram follow-up failed or disturbed work: requests=%d reply=%q", requests, delivered.String())
	}
}
