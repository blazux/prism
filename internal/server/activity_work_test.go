package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

func readWork(t *testing.T, s *Server, user *memory.User) []activityWorkItem {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleActivityWork(w, withUser(httptest.NewRequest("GET", "/api/activity/work", nil), user))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Items      []activityWorkItem
		ObservedAt time.Time
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ObservedAt.IsZero() {
		t.Fatal("no observation timestamp")
	}
	return result.Items
}
func workByID(items []activityWorkItem, id string) *activityWorkItem {
	for _, item := range items {
		if item.ID == id {
			return &item
		}
	}
	return nil
}

func TestActivityWorkKeepsOwnerScopeAndCurrentRoomMembership(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	a := scopeFixtureUser(t, ms, "work-a", memory.RoleMember)
	b := scopeFixtureUser(t, ms, "work-b", memory.RoleMember)
	admin := scopeFixtureUser(t, ms, "work-admin", memory.RoleGlobalAdmin)
	group, err := ms.CreateGroup(ctx, "Support")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(ctx, "Private group")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, a.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub(), cfg: Config{MultiUser: true}}
	for _, user := range []*memory.User{a, b, admin} {
		sid := fmt.Sprintf("u%d-report", user.ID)
		if err := ms.EnsureSession(ctx, sid, "Report", &user.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if err := s.saveWorkMetadata(ctx, &Client{user: user, sessionID: sid}, workMetadata{Request: fmt.Sprintf("private work of %d", user.ID), RunStatus: "completed", StartedAt: &now, LastActivityAt: &now, FinishedAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	trigger, err := ms.AddRoomMessage(ctx, group.ID, &a.ID, "Member", "Find waiting jobs", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := ms.AddRoomMessage(ctx, group.ID, nil, "Assistant", "Done", true, &trigger.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.UpdateRoomResponse(ctx, reply.ID, "Done", "complete", nil); err != nil {
		t.Fatal(err)
	}
	private, err := ms.AddRoomMessage(ctx, other.ID, nil, "Someone", "PRIVATE GROUP CONTENT", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignReply, err := ms.AddRoomMessage(ctx, other.ID, nil, "Assistant", "Done", true, &private.ID)
	if err != nil {
		t.Fatal(err)
	}
	ms.UpdateRoomResponse(ctx, foreignReply.ID, "Done", "complete", nil)
	items := readWork(t, s, a)
	if len(items) != 2 || workByID(items, fmt.Sprintf("workspace:u%d-report", b.ID)) != nil {
		t.Fatal("another user's work exposed", items)
	}
	room := workByID(items, fmt.Sprintf("room:%d", group.ID))
	if room == nil || room.Request != "Find waiting jobs" || room.RunStatus != "completed" || room.FinishedAt != nil {
		t.Fatal("legacy room fabricated or lost metadata", room)
	}
	adminItems := readWork(t, s, admin)
	if workByID(adminItems, fmt.Sprintf("workspace:u%d-report", a.ID)) != nil || workByID(adminItems, fmt.Sprintf("room:%d", other.ID)) == nil {
		t.Fatal("admin personal/Room scope drifted", adminItems)
	}
	if err := ms.RemoveGroupMember(ctx, group.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if item := workByID(readWork(t, s, a), fmt.Sprintf("room:%d", group.ID)); item != nil {
		t.Fatal("revoked member still sees Room work", item)
	}
	// A bad archived reply reference cannot join across group boundaries.
	ms.AddGroupMember(ctx, group.ID, a.ID, memory.GroupRoleMember)
	cross, err := ms.AddRoomMessage(ctx, group.ID, nil, "Assistant", "Reply", true, &private.ID)
	if err != nil {
		t.Fatal(err)
	}
	ms.UpdateRoomResponse(ctx, cross.ID, "Reply", "complete", nil)
	if room := workByID(readWork(t, s, a), fmt.Sprintf("room:%d", group.ID)); room == nil || room.Request != "" {
		t.Fatal("foreign group request leaked", room)
	}
}

func TestActivityWorkRetainsFactsAcrossCompletionAndRestart(t *testing.T) {
	ms := securityStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := &Server{memStore: ms}
	if err := ms.EnsureSession(ctx, "report", "Report", nil); err != nil {
		t.Fatal(err)
	}
	owner := &Client{sessionID: "report", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, cancel, "Prepare a report", nil)
	if err != nil {
		t.Fatal(err)
	}
	owner.sendJSON(agent.Event{Type: "tool_use", ID: "one", Tool: "read_file", Input: json.RawMessage(`{"path":"PRIVATE ARGUMENT"}`)})
	owner.sendJSON(agent.Event{Type: "tool_result", ID: "one", Output: "PRIVATE RESULT"})
	owner.sendJSON(map[string]any{"type": "secret_request", "name": "PRIVATE SECRET"})
	item := workByID(readWork(t, s, nil), "workspace:report")
	if item == nil || item.RunStatus != "waiting_for_input" || item.StartedAt == nil || item.LastActivityAt == nil || item.FinishedAt != nil {
		t.Fatal(item)
	}
	raw, _ := json.Marshal(item)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("tool inputs/results/secret details exposed", string(raw))
	}
	s.finishRun(run, ctx)
	item = workByID(readWork(t, &Server{memStore: ms}, nil), "workspace:report")
	if item == nil || item.RunStatus != "completed" || item.Request != "Prepare a report" || item.FinishedAt == nil || item.LastActivityAt == nil || item.StartedAt == nil || len(item.RecentTools) != 1 {
		t.Fatal("completion facts lost", item)
	}
	if item.FinishedAt.Before(*item.StartedAt) {
		t.Fatal("invalid task timestamps", item)
	}
	// A persisted running turn without a live worker is interrupted, never busy.
	if err := s.saveWorkMetadata(ctx, owner, workMetadata{RunStatus: "running", Request: "Interrupted request", StartedAt: item.StartedAt, LastActivityAt: item.StartedAt, RecentTools: []activityTool{{ID: "unfinished", Tool: "exec_command", Status: "running"}}}); err != nil {
		t.Fatal(err)
	}
	item = workByID(readWork(t, &Server{memStore: ms}, nil), "workspace:report")
	if item.RunStatus != "interrupted" || item.Request != "Interrupted request" || item.FinishedAt != nil {
		t.Fatal(item)
	}
	if len(item.RecentTools) != 1 || item.RecentTools[0].Status != "result_unavailable" {
		t.Fatal("a saved tool start was mistaken for a live execution after restart", item)
	}
	// Old installations have status only. Do not attach a different request or
	// fabricate missing dates to make the card appear complete.
	ms.SetConfig(ctx, runStateKey(owner), "failed")
	item = workByID(readWork(t, s, nil), "workspace:report")
	if item.Request != "" || item.StartedAt != nil || item.FinishedAt != nil {
		t.Fatal("legacy status inherited old request", item)
	}
}

func TestActivityWorkHeadlessAndDirectToolSemantics(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	ctx := t.Context()
	ms.EnsureSession(ctx, "telegram", "Telegram", nil)
	ms.EnsureSession(ctx, "widget", "Widget", nil)
	_, endWidget, err := s.beginSessionJob(ctx, "widget")
	if err != nil {
		t.Fatal(err)
	}
	defer endWidget()
	if item := workByID(readWork(t, s, nil), "workspace:widget"); item != nil {
		t.Fatal("direct widget marked as agent work", item)
	}
	jobCtx, endJob, err := s.beginAgentJob(ctx, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	track, endWork := s.beginHeadlessWork(jobCtx, "telegram", "Check my mail", 0)
	track(agent.Event{Type: "tool_use", ID: "one", Tool: "email", Input: json.RawMessage(`{"password":"PRIVATE"}`)})
	item := workByID(readWork(t, s, nil), "workspace:telegram")
	if item == nil || item.Request != "Check my mail" || item.RunStatus != "running" || item.BackgroundJobs != 1 {
		t.Fatal(item)
	}
	endWork(nil)
	endJob()
	item = workByID(readWork(t, &Server{memStore: ms}, nil), "workspace:telegram")
	if item == nil || item.RunStatus != "completed" || item.Request != "Check my mail" || item.FinishedAt == nil {
		t.Fatal("headless finish not persisted", item)
	}
	// Older headless work may finish after a newer dashboard task. It must not
	// replace the latter's result metadata in the same conversation.
	jobCtx, endJob, err = s.beginAgentJob(ctx, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	_, endWork = s.beginHeadlessWork(jobCtx, "telegram", "Older request", 0)
	owner := &Client{sessionID: "telegram", send: make(chan []byte, 30)}
	run, err := s.beginRun(owner, func() {}, "Newer request", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.finishRun(run, ctx)
	endWork(nil)
	endJob()
	item = workByID(readWork(t, s, nil), "workspace:telegram")
	if item == nil || item.Request != "Newer request" {
		t.Fatal("late headless finish overwrote newer request", item)
	}
}
