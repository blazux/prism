package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/memory"
)

func TestObjectLinksReadOnlyAuthenticatedScope(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u := scopeFixtureUser(t, ms, "links-owner", memory.RoleMember)
	other := scopeFixtureUser(t, ms, "links-other", memory.RoleMember)
	group, err := ms.CreateGroup(ctx, "Links team")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	s := New(Config{MultiUser: true})
	s.memStore = ms
	scope, foreignScope := fmt.Sprintf("u%d", u.ID), fmt.Sprintf("u%d", other.ID)
	mustID := func(id int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownNote := mustID(ms.AddNote(ctx, scope, "Own note", "Own body", ""))
	foreignNote := mustID(ms.AddNote(ctx, foreignScope, "Private foreign note", "Foreign body", ""))
	sharedNote, err := ms.ShareNote(ctx, fmt.Sprintf("g%d", group.ID), u.ID, ownNote, "Shared note", "Shared body", "")
	if err != nil {
		t.Fatal(err)
	}
	ownTask := mustID(ms.AddTask(ctx, scope, "Completed task", "normal", nil))
	if err := ms.SetTaskDone(ctx, scope, ownTask, true); err != nil {
		t.Fatal(err)
	}
	foreignTask := mustID(ms.AddTask(ctx, foreignScope, "Private foreign task", "normal", nil))
	future := time.Date(2099, 6, 15, 10, 0, 0, 0, time.UTC)
	ownEvent := mustID(ms.AddEvent(ctx, scope, "Far future event", "", "", future, nil))
	foreignEvent := mustID(ms.AddEvent(ctx, foreignScope, "Private foreign event", "", "", future, nil))
	for _, tc := range []struct {
		name, endpoint, content string
		id                      int64
		handler                 http.HandlerFunc
	}{
		{"note", "/api/notes", "Own note", ownNote, s.handleNotes},
		{"completed task", "/api/tasks", "Completed task", ownTask, s.handleTasks},
		{"future event", "/api/events", "Far future event", ownEvent, s.handleEvents},
		{"shared note", "/api/notes", "Shared note", sharedNote, s.handleNotes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := withUser(httptest.NewRequest("GET", fmt.Sprintf("%s?id=%d", tc.endpoint, tc.id), nil), u)
			tc.handler(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.content) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		endpoint string
		id       int64
		handler  http.HandlerFunc
	}{
		{"/api/notes", foreignNote, s.handleNotes},
		{"/api/tasks", foreignTask, s.handleTasks},
		{"/api/events", foreignEvent, s.handleEvents},
		{"/api/notes", -1, s.handleNotes},
		{"/api/tasks", -1, s.handleTasks},
		{"/api/events", -1, s.handleEvents},
	} {
		w := httptest.NewRecorder()
		// URL hints cannot select another account, group or workspace's data.
		url := fmt.Sprintf("%s?id=%d&user=%d&scope=%s&session=%s-main", tc.endpoint, tc.id, other.ID, foreignScope, foreignScope)
		tc.handler(w, withUser(httptest.NewRequest("GET", url, nil), u))
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Private foreign") {
			t.Fatalf("foreign/missing %s: %d %s", url, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.handleNotes(w, withUser(httptest.NewRequest("GET", fmt.Sprintf("/api/notes?id=%d", sharedNote), nil), other))
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Shared body") {
		t.Fatalf("nonmember shared note: %d %s", w.Code, w.Body.String())
	}
}
