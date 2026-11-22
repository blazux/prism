package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"prism/internal/agent"
	"prism/internal/memory"
)

func TestVoxMissionMonitorResumesOnceAndKeepsOriginAndGateway(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	user, err := ms.CreateUser(ctx, "mission-owner@example.test", "fixture", "Owner", memory.RoleGlobalAdmin, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateUser(ctx, "mission-other@example.test", "fixture", "Other", memory.RoleGlobalAdmin, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("u%d-main", user.ID)
	otherSession := fmt.Sprintf("u%d-main", other.ID)
	if err := ms.EnsureSession(ctx, session, "Main", &user.ID); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		var scope missionScope
		if json.Unmarshal([]byte(r.Header.Get("X-Prism-Scope")), &scope) != nil || scope.Owner == nil || *scope.Owner != user.ID || scope.Admin {
			t.Error("monitor used another principal")
		}
		if r.URL.Path != "/api/calls/77" && r.URL.Path != "/api/calls/78" {
			t.Error("monitor scanned the queue instead of reading a followed ID")
		}
		id := 77
		if strings.HasSuffix(r.URL.Path, "78") {
			id = 78
		}
		fmt.Fprintf(w, `{"id":%d,"status":"done","result_summary":"Appointment remains unconfirmed."}`, id)
	}))
	t.Cleanup(upstream.Close)
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL}}
	scope, _, err := s.voxMissionScope(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(scope)
	call := agent.QueuedCall{ID: 77, VoxURL: upstream.URL, Scope: string(raw), PhoneNumber: "+596596123456", Mission: "Ask for an appointment"}
	if err := s.trackVoxCall(ctx, session, call); err != nil {
		t.Fatal(err)
	}
	otherScope, _, _ := s.voxMissionScope(ctx, otherSession)
	otherRaw, _ := json.Marshal(otherScope)
	otherCall := call
	otherCall.Scope = string(otherRaw)
	if err := s.trackVoxCall(ctx, otherSession, otherCall); err == nil {
		t.Fatal("an existing mission was rebound to another user")
	}
	// A fresh Server recovers the durable link. Concurrent observations still
	// produce exactly one result and one notification.
	restarted := &Server{memStore: ms, cfg: s.cfg}
	var workers sync.WaitGroup
	for _, server := range []*Server{s, restarted} {
		workers.Add(1)
		go func() { defer workers.Done(); server.pollVoxCallMissions(ctx) }()
	}
	workers.Wait()
	restarted.pollVoxCallMissions(ctx)
	notices, _ := ms.GetRecentNotifications(ctx, session, 10)
	history, _ := ms.LoadHistory(ctx, session)
	foreign, _ := ms.GetRecentNotifications(ctx, otherSession, 10)
	if len(notices) != 1 || len(history) != 1 || len(foreign) != 0 || !strings.Contains(history[0].Content, "Appointment remains unconfirmed") {
		t.Fatalf("wrong durable delivery: notices=%d history=%d other=%d", len(notices), len(history), len(foreign))
	}
	call.ID = 78
	if err := s.trackVoxCall(ctx, session, call); err != nil {
		t.Fatal(err)
	}
	var wrongGatewayReads atomic.Int32
	otherGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wrongGatewayReads.Add(1) }))
	t.Cleanup(otherGateway.Close)
	restarted.cfg.VoxURL = otherGateway.URL
	restarted.pollVoxCallMissions(ctx)
	if wrongGatewayReads.Load() != 0 {
		t.Fatal("old mission ID was queried on another phone installation")
	}
	restarted.cfg.VoxURL = upstream.URL
	restarted.pollVoxCallMissions(ctx)
	notices, _ = ms.GetRecentNotifications(ctx, session, 10)
	if len(notices) != 2 {
		t.Fatal("pending mission was lost while disconnected")
	}
}

func TestVoxMissionMonitorRechecksRemovedOwnersAndGroups(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	user, _ := ms.CreateUser(ctx, "revoked-mission@example.test", "fixture", "Owner", memory.RoleMember, memory.StatusApproved)
	group, _ := ms.CreateGroup(ctx, "Reception")
	if err := ms.SetConfig(ctx, voxWorkspaceGroupsKey, fmt.Sprintf("[%d]", group.ID)); err != nil {
		t.Fatal(err)
	}
	var reached atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	t.Cleanup(upstream.Close)
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL}}
	for id, session := range []string{fmt.Sprintf("u%d-main", user.ID), fmt.Sprintf("room-g%d", group.ID)} {
		scope, _, err := s.voxMissionScope(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(scope)
		if err := s.trackVoxCall(ctx, session, agent.QueuedCall{ID: int64(id + 1), VoxURL: upstream.URL, Scope: string(raw), Mission: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ms.SetUserStatus(ctx, user.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetConfig(ctx, voxWorkspaceGroupsKey, "[]"); err != nil {
		t.Fatal(err)
	}
	s.pollVoxCallMissions(ctx)
	if reached.Load() != 0 {
		t.Fatal("monitor read private results after rights were revoked")
	}
	pending, _ := ms.PendingVoxCalls(ctx, voxGatewayKey(upstream.URL), 10)
	if len(pending) != 0 {
		t.Fatal("revoked missions remain active")
	}
}

func TestKnownPhoneArchiveAndMissionsBindTheRecognizedAccount(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	first, _ := ms.CreateUser(ctx, "phone-first@example.test", "fixture", "First", memory.RoleGlobalAdmin, memory.StatusApproved)
	second, _ := ms.CreateUser(ctx, "phone-second@example.test", "fixture", "Second", memory.RoleGlobalAdmin, memory.StatusApproved)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var scope missionScope
		if json.Unmarshal([]byte(r.Header.Get("X-Prism-Scope")), &scope) != nil || scope.Owner == nil || scope.Admin {
			t.Error("phone call has no personal scope")
			http.Error(w, "bad scope", 403)
			return
		}
		if r.URL.Path == "/api/history" {
			fmt.Fprintf(w, `{"total":1,"items":[{"summary":"owner-%d"}],"next_offset":null}`, *scope.Owner)
			return
		}
		if r.Method == http.MethodPost {
			fmt.Fprintf(w, `{"id":%d,"status":"pending"}`, 100+*scope.Owner)
			return
		}
		fmt.Fprintf(w, `{"id":%d,"status":"done","result_summary":"private-owner-%d"}`, 100+*scope.Owner, *scope.Owner)
	}))
	t.Cleanup(upstream.Close)
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL}}
	for _, user := range []*memory.User{first, second} {
		e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
		e.SetMemoryStore(ms)
		e.SetSessionID("vox-random-session")
		s.configureVoxExecutor(e, "vox-random-session")
		s.configureVoiceVoxExecutor(e, user)
		s.callerContextForVoice(ctx, user, "vox-random-session").apply(e)
		text, _, err := e.Execute(ctx, "call_history", json.RawMessage(`{}`))
		if err != nil || !strings.Contains(text, fmt.Sprintf("owner-%d", user.ID)) {
			t.Fatalf("phone archive mixed identities: %v %s", err, text)
		}
		text, _, err = e.Execute(ctx, "place_call", json.RawMessage(`{"phone_number":"+596596123456","mission":"fixture"}`))
		if err != nil || !strings.Contains(text, `"automatic_result_tracking": true`) {
			t.Fatal(err, text)
		}
	}
	s.pollVoxCallMissions(ctx)
	for _, user := range []*memory.User{first, second} {
		session := fmt.Sprintf("u%d-%s", user.ID, assistantSession)
		notices, _ := ms.GetRecentNotifications(ctx, session, 10)
		if len(notices) != 1 || !strings.Contains(notices[0].Message, fmt.Sprintf("private-owner-%d", user.ID)) {
			t.Fatalf("phone outcome delivered to wrong owner: %d %v", user.ID, notices)
		}
	}
}

func TestVoxCallResultWaitsForTheCurrentToolBatch(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	s := &Server{memStore: ms, cfg: Config{VoxURL: "http://phone.fixture"}}
	scope, _, err := s.voxMissionScope(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(scope)
	if err := s.trackVoxCall(ctx, "default", agent.QueuedCall{ID: 91, VoxURL: s.voxURL(), Scope: string(raw), Mission: "fixture"}); err != nil {
		t.Fatal(err)
	}
	calls, err := ms.PendingVoxCalls(ctx, voxGatewayKey(s.voxURL()), 10)
	if err != nil || len(calls) != 1 {
		t.Fatal(err, calls)
	}
	_, finish, err := s.beginAgentJob(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish() })
	batch := json.RawMessage(`[{"function":{"name":"note","arguments":{"action":"list"}}}]`)
	if _, err := ms.AppendMessage(ctx, "default", "assistant", "", batch); err != nil {
		t.Fatal(err)
	}
	s.publishVoxCallCompletion(ctx, calls[0], monitoredCall{ID: 91, Status: "done"})
	history, _ := ms.LoadHistory(ctx, "default")
	if len(history) != 1 {
		t.Fatal("phone result split a still-running tool batch")
	}
	if _, err := ms.AppendMessage(ctx, "default", "tool", "No notes", nil); err != nil {
		t.Fatal(err)
	}
	finish()
	// Make the registered cleanup harmless after ending the job explicitly.
	finish = func() {}
	s.publishVoxCallCompletion(ctx, calls[0], monitoredCall{ID: 91, Status: "done"})
	history, _ = ms.LoadHistory(ctx, "default")
	if len(history) != 3 || history[1].Role != "tool" || history[2].Role != "assistant" || !strings.Contains(history[2].Content, "mission #91") {
		t.Fatal("saved result did not follow the complete tool batch")
	}
}
