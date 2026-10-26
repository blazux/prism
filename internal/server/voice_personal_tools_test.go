package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"prism/internal/docker"
	"strings"
	"testing"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

func TestVoiceCallerToolBoundary(t *testing.T) {
	s := &Server{}
	for _, role := range []string{memory.RoleMember, memory.RoleGlobalAdmin} {
		cc := s.callerContextForVoice(context.Background(), &memory.User{ID: 7, Role: role}, "vox-fixture")
		if cc.PersonalScope != "u7" {
			t.Fatal("phone lost its real account scope")
		}
		for _, tool := range []string{"note", "task", "calendar", "email", "prism_help"} {
			if cc.HiddenTools[tool] || cc.Guard(tool, map[string]any{"action": "list"}) != nil {
				t.Errorf("%s cannot use %s", role, tool)
			}
		}
		for _, tool := range []string{"exec_command", "secrets", "pim_source", "agent_settings", "place_call", "list_calls", "cancel_call"} {
			if !cc.HiddenTools[tool] || cc.Guard(tool, nil) == nil {
				t.Errorf("%s exposes forbidden phone tool %s", role, tool)
			}
		}
		for _, action := range []string{"config", "setup", " CONFIG ", "rule_save", "rules_apply", "delete_folder"} {
			if cc.Guard("email", map[string]any{"action": action}) == nil {
				t.Errorf("phone allowed mailbox administration %s", action)
			}
		}
	}
	guest := s.callerContextForVoice(context.Background(), nil, "vox-guest")
	for _, tool := range []string{"note", "task", "calendar", "email", "save_user_info", "prism_help", "place_call"} {
		if !guest.HiddenTools[tool] || guest.Guard(tool, nil) == nil {
			t.Errorf("guest has personal tool %s", tool)
		}
	}
	// Outbound tools used to be advertised dynamically, then rejected by the guard.
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetVox("http://vox.invalid", "", "")
	guest.apply(e)
	for _, tool := range e.AllDynamicTools() {
		t.Errorf("guest advertises unavailable dynamic tool %s", tool.Function.Name)
	}
}

func TestVoicePersonalDataUsesRecognizedAccount(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true}}
	owner, err := ms.CreateUser(ctx, "owner@example.test", "fixture", "Owner", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateUser(ctx, "other@example.test", "fixture", "Other", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	group, err := ms.CreateGroup(ctx, "Phone fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, owner.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	e.SetSessionID("vox-random-session")
	cc := s.callerContextForVoice(ctx, owner, "vox-random-session")
	cc.apply(e)
	for _, call := range []struct{ name, args string }{
		{"note", `{"action":"add","title":"Phone note","body":"Fixture"}`},
		{"task", `{"action":"add","title":"Phone task"}`},
		{"calendar", `{"action":"add","title":"Phone event","start":"2026-11-01T10:00:00-04:00"}`},
	} {
		if result, _, err := e.Execute(ctx, call.name, json.RawMessage(call.args)); err != nil {
			t.Fatalf("%s: %s %v", call.name, result, err)
		}
	}
	for _, u := range []*memory.User{owner, other} {
		scope := fmt.Sprintf("u%d", u.ID)
		notes, err := ms.ListNotes(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := ms.ListTasks(ctx, scope, true)
		if err != nil {
			t.Fatal(err)
		}
		events, err := ms.ListEvents(ctx, scope, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if u.ID == owner.ID {
			want = 1
		}
		if len(notes) != want || len(tasks) != want || len(events) != want {
			t.Fatalf("wrong account data in %s: notes=%d tasks=%d events=%d", scope, len(notes), len(tasks), len(events))
		}
	}
	// Malformed account configuration proves the email tool reads the owner's
	// config without making any IMAP/SMTP connection or sending a real message.
	us := ms.ConfigScope(fmt.Sprintf("u%d", owner.ID))
	if err := us.SetConfig(ctx, "email_config", "fixture malformed email config"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Execute(ctx, "email", json.RawMessage(`{"action":"list"}`)); err == nil || !strings.Contains(err.Error(), "corrupt email config") {
		t.Fatalf("mailbox did not resolve to the recognized account: %v", err)
	}
	// Personal visibility preferences survive the random phone session ID.
	if err := us.SetConfig(ctx, keyHiddenGroupTools, `["note"]`); err != nil {
		t.Fatal(err)
	}
	cc = s.callerContextForVoice(ctx, owner, "vox-another-session")
	if !cc.HiddenTools["note"] {
		t.Fatal("phone ignored the user's hidden tools")
	}
	// Group restrictions apply to members; group admins retain their normal rights.
	if err := ms.SetGroupToolAccess(ctx, group.ID, "task", "admin_only"); err != nil {
		t.Fatal(err)
	}
	cc = s.callerContextForVoice(ctx, owner, "vox-group-member")
	if !cc.HiddenTools["task"] || cc.Guard("task", nil) == nil {
		t.Fatal("group restriction was bypassed")
	}
	if err := ms.AddGroupMember(ctx, group.ID, owner.ID, memory.GroupRoleAdmin); err != nil {
		t.Fatal(err)
	}
	cc = s.callerContextForVoice(ctx, owner, "vox-group-admin")
	if cc.HiddenTools["task"] || cc.Guard("task", nil) != nil {
		t.Fatal("random voice session hid group-admin tools")
	}
	// Use the other account to check a normal member's global policy.
	owner = other
	// Global member restrictions must be both hidden and enforced.
	if err := ms.SetToolPolicy(ctx, "calendar", "admin_only"); err != nil {
		t.Fatal(err)
	}
	cc = s.callerContextForVoice(ctx, owner, "vox-third-session")
	if !cc.HiddenTools["calendar"] || cc.Guard("calendar", map[string]any{"action": "add"}) == nil {
		t.Fatal("phone bypassed the user's tool policy")
	}
}

// Exercise the authenticated Vox WebSocket, model-visible schemas and actual
// calendar execution together. A synthetic model keeps every effect in fixtures.
func TestVoiceSocketCreatesPersonalCalendarEvent(t *testing.T) {
	ms := securityStore(t)
	ctx := context.Background()
	user, err := ms.CreateUser(ctx, "phone@example.test", "fixture", "Phone", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.UpdateProfile(ctx, user.ID, "Phone", "", "", "+12025550123", nil); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		names := map[string]bool{}
		for _, tool := range body.Tools {
			names[tool.Function.Name] = true
		}
		for _, name := range []string{"note", "task", "calendar", "email", "prism_help"} {
			if !names[name] {
				t.Errorf("phone model lacks %s", name)
			}
		}
		for _, name := range []string{"exec_command", "pim_source", "place_call", "list_calls", "cancel_call"} {
			if names[name] {
				t.Errorf("phone model sees forbidden %s", name)
			}
		}
		hasResult := false
		for _, message := range body.Messages {
			hasResult = hasResult || message.Role == "tool"
		}
		delta := map[string]any{"content": "Le rendez-vous est ajouté."}
		finish := "stop"
		if !hasResult {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "calendar-fixture", "type": "function", "function": map[string]any{"name": "calendar", "arguments": `{"action":"add","title":"Voice appointment","start":"2026-11-01T10:00:00-04:00"}`}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
	}))
	defer backend.Close()
	t.Setenv("PATH", t.TempDir())
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, VoxURL: "http://vox.invalid", MultiUser: true})
	s.memStore = ms
	s.docker = docker.WithExecution(nil)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleWS(w, withUser(r, serviceUser)) }))
	defer host.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"?session=vox-calendar-fixture&channel=voice&caller=12025550123", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(map[string]any{"type": "chat", "channel": "voice", "content": "Ajoute le rendez-vous au calendrier."}); err != nil {
		t.Fatal(err)
	}
	for {
		var event struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "error" {
			t.Fatal(event.Content)
		}
		if event.Type == "turn_complete" {
			break
		}
	}
	events, err := ms.ListEvents(ctx, fmt.Sprintf("u%d", user.ID), nil, nil)
	if err != nil || len(events) != 1 || events[0].Title != "Voice appointment" {
		t.Fatalf("calendar action did not reach personal data: %+v %v", events, err)
	}
	global, err := ms.ListEvents(ctx, "global", nil, nil)
	if err != nil || len(global) != 0 {
		t.Fatalf("calendar action used the service account: %+v %v", global, err)
	}
}
