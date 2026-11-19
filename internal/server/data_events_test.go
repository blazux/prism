package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

func dataTestClient(s *Server, user *memory.User) *Client {
	c := &Client{user: user, send: make(chan []byte, 32)}
	s.clients[c] = struct{}{}
	return c
}

func dataTestReceive(t *testing.T, c *Client, topic string) dataChange {
	t.Helper()
	select {
	case raw := <-c.send:
		var event dataChange
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "data_changed" || event.Topic != topic {
			t.Fatalf("unexpected invalidation: %s", raw)
		}
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		for key := range fields {
			if key != "type" && key != "topic" && key != "scope" && key != "group" && key != "reason" {
				t.Fatalf("record payload in invalidation: %s", raw)
			}
		}
		return event
	default:
		t.Fatalf("missing %s invalidation", topic)
		return dataChange{}
	}
}

func dataTestEmpty(t *testing.T, clients ...*Client) {
	t.Helper()
	for _, client := range clients {
		select {
		case event := <-client.send:
			t.Fatalf("unexpected event for user %d: %s", dataUserID(client.user), event)
		default:
		}
	}
}

func TestDataEventsRespectPersonalAndLiveGroupRights(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{MultiUser: true})
	s.memStore = ms
	a := scopeFixtureUser(t, ms, "data-a", memory.RoleMember)
	b := scopeFixtureUser(t, ms, "data-b", memory.RoleMember)
	c := scopeFixtureUser(t, ms, "data-c", memory.RoleMember)
	admin := scopeFixtureUser(t, ms, "data-admin", memory.RoleGlobalAdmin)
	g, err := ms.CreateGroup(t.Context(), "event group")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(t.Context(), "other group")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*memory.User{a, b} {
		if err := ms.AddGroupMember(t.Context(), g.ID, u.ID, memory.GroupRoleMember); err != nil {
			t.Fatal(err)
		}
	}
	ca, cb, cc, cad := dataTestClient(s, a), dataTestClient(s, b), dataTestClient(s, c), dataTestClient(s, admin)
	cg := dataTestClient(s, &memory.User{ID: -g.ID})
	cother := dataTestClient(s, &memory.User{ID: -other.ID})
	cservice := dataTestClient(s, serviceUser)
	s.emitDataChange(fmt.Sprintf("u%d", a.ID), "notes")
	if event := dataTestReceive(t, ca, "notes"); event.Scope != "personal" || event.Group != 0 {
		t.Fatal(event)
	}
	dataTestEmpty(t, cb, cc, cad, cg, cother, cservice)

	scope := fmt.Sprintf("g%d", g.ID)
	s.emitDataChange(scope, "todo.shared")
	for _, client := range []*Client{ca, cb, cad, cg, cservice} {
		if event := dataTestReceive(t, client, "todo.shared"); event.Scope != "group" || event.Group != g.ID {
			t.Fatal(event)
		}
	}
	dataTestEmpty(t, cc, cother)
	if err := ms.RemoveGroupMember(t.Context(), g.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetUserStatus(t.Context(), a.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	s.emitDataChange(scope, "todo.shared")
	for _, client := range []*Client{cad, cg, cservice} {
		dataTestReceive(t, client, "todo.shared")
	}
	dataTestEmpty(t, ca, cb, cc, cother)
	if err := ms.DeleteGroup(t.Context(), g.ID); err != nil {
		t.Fatal(err)
	}
	s.emitDataChange(scope, "todo.shared")
	dataTestEmpty(t, ca, cb, cc, cad, cg, cother, cservice)
}

func TestDataEventsPublicationUsesSignedCallerScope(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{MultiUser: true, AuthToken: "fixture-data-events"})
	s.memStore = ms
	u := scopeFixtureUser(t, ms, "data-owner", memory.RoleMember)
	otherUser := scopeFixtureUser(t, ms, "data-stranger", memory.RoleMember)
	g, err := ms.CreateGroup(t.Context(), "publisher group")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(t.Context(), "stranger group")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(t.Context(), g.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	owner, stranger := dataTestClient(s, u), dataTestClient(s, otherUser)
	group := dataTestClient(s, &memory.User{ID: -g.ID})
	session := fmt.Sprintf("u%d-work", u.ID)
	groupSession := fmt.Sprintf("room-g%d", g.ID)
	post := func(sid, query, body string) int {
		r := httptest.NewRequest("POST", "/api/data-events"+query, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.selfCallToken(sid))
		w := httptest.NewRecorder()
		s.withAuth(http.HandlerFunc(s.handleDataEvents)).ServeHTTP(w, r)
		return w.Code
	}
	if code := post(session, "?session="+session, `{"topic":"todo.shared"}`); code != 200 {
		t.Fatal(code)
	}
	dataTestReceive(t, owner, "todo.shared")
	dataTestEmpty(t, stranger, group)
	if code := post(session, "", fmt.Sprintf(`{"topic":"todo.shared","scope":"group","group":%d}`, g.ID)); code != 200 {
		t.Fatal(code)
	}
	dataTestReceive(t, owner, "todo.shared")
	dataTestReceive(t, group, "todo.shared")
	if code := post(groupSession, "", `{"topic":"todo.shared"}`); code != 200 {
		t.Fatal(code)
	}
	dataTestReceive(t, owner, "todo.shared")
	dataTestReceive(t, group, "todo.shared")
	for _, test := range []struct {
		sid, query, body string
		code             int
	}{
		{session, "", fmt.Sprintf(`{"topic":"todo.shared","scope":"group","group":%d}`, other.ID), 403},
		{groupSession, "", fmt.Sprintf(`{"topic":"todo.shared","scope":"group","group":%d}`, other.ID), 403},
		{groupSession, "", `{"topic":"todo.shared","scope":"personal"}`, 403},
		{session, "?session=" + groupSession, `{"topic":"todo.shared"}`, 401},
		{session, "", `{"topic":"Notes"}`, 400},
		{session, "", `{"topic":"notes","record":"secret"}`, 400},
		{session, "", `{"topic":"notes"}{}`, 400},
		{session, "", `{"topic":"notes"}` + strings.Repeat(" ", 1100), 400},
	} {
		if got := post(test.sid, test.query, test.body); got != test.code {
			t.Errorf("%s %s %s: status %d want %d", test.sid, test.query, test.body, got, test.code)
		}
	}
	dataTestEmpty(t, owner, stranger, group)
}

func TestDataEventsNativeAndHTTPWritesEmitOnlyAfterSuccess(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{MultiUser: true})
	s.memStore = ms
	u := scopeFixtureUser(t, ms, "data-native", memory.RoleGlobalAdmin)
	client := dataTestClient(s, u)
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	session := fmt.Sprintf("u%d-work", u.ID)
	e.SetSessionID(session)
	s.callerContextForUser(t.Context(), u, session).apply(e)
	for _, native := range []struct{ name, args, topic string }{
		{"note", `{"action":"add","title":"private note","body":"private body"}`, "notes"},
		{"task", `{"action":"add","title":"private task"}`, "tasks"},
		{"calendar", `{"action":"add","title":"private event","start":"2026-11-18T10:00:00-04:00"}`, "calendar"},
	} {
		if _, _, err := e.Execute(t.Context(), native.name, json.RawMessage(native.args)); err != nil {
			t.Fatalf("%s: %v", native.name, err)
		}
		dataTestReceive(t, client, native.topic)
		if _, _, err := e.Execute(t.Context(), native.name, json.RawMessage(`{"action":"list"}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.Execute(t.Context(), native.name, json.RawMessage(`{"action":"update","id":"nonexistent"}`)); err == nil {
			t.Fatal("invalid mutation accepted", native.name)
		}
		dataTestEmpty(t, client)
	}
	for _, direct := range []struct {
		path, topic, body string
		handler           http.HandlerFunc
	}{
		{"/api/notes", "notes", `{"title":"http note","body":"body"}`, s.handleNotes},
		{"/api/tasks", "tasks", `{"title":"http task"}`, s.handleTasks},
		{"/api/events", "calendar", `{"title":"http event","start":"2026-11-18T10:00:00-04:00"}`, s.handleEvents},
	} {
		w := httptest.NewRecorder()
		direct.handler(w, withUser(httptest.NewRequest("POST", direct.path, strings.NewReader(direct.body)), u))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", direct.path, w.Code, w.Body)
		}
		dataTestReceive(t, client, direct.topic)
		for _, method := range []string{"GET", "POST"} {
			w := httptest.NewRecorder()
			direct.handler(w, withUser(httptest.NewRequest(method, direct.path, strings.NewReader("bad JSON")), u))
			dataTestEmpty(t, client)
		}
	}
}

func TestDataEventsDoNotEnterConversationReplay(t *testing.T) {
	s := New(Config{})
	owner, peer := dataTestClient(s, nil), dataTestClient(s, nil)
	run := &chatRun{owner: owner, subscribers: map[*Client]bool{owner: true, peer: true}}
	owner.setRun(run)
	s.emitDataChange("global", "notes")
	dataTestReceive(t, owner, "notes")
	dataTestReceive(t, peer, "notes")
	dataTestEmpty(t, owner, peer)
	if len(run.events) != 0 || run.bytes != 0 {
		t.Fatal("invalidation retained in conversation replay")
	}
}

func TestDataEventsTrustedMonoHeadlessCallerPublishesNativeWrites(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{})
	s.memStore = ms
	client := dataTestClient(s, nil)
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	e.SetSessionID("slack")
	s.trustedCallerContext().apply(e)
	if _, _, err := e.Execute(t.Context(), "note", json.RawMessage(`{"action":"add","title":"channel note"}`)); err != nil {
		t.Fatal(err)
	}
	dataTestReceive(t, client, "notes")
}

func TestDataEventsSharedNotesNotifyOnlyGroupAndOnlyAfterCommit(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{MultiUser: true})
	s.memStore = ms
	owner := scopeFixtureUser(t, ms, "data-share-owner", memory.RoleMember)
	peer := scopeFixtureUser(t, ms, "data-share-peer", memory.RoleMember)
	stranger := scopeFixtureUser(t, ms, "data-share-stranger", memory.RoleMember)
	g, err := ms.CreateGroup(t.Context(), "shared notes events")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*memory.User{owner, peer} {
		if err := ms.AddGroupMember(t.Context(), g.ID, u.ID, memory.GroupRoleMember); err != nil {
			t.Fatal(err)
		}
	}
	co, cp, cs := dataTestClient(s, owner), dataTestClient(s, peer), dataTestClient(s, stranger)
	personalID, err := ms.AddNote(t.Context(), fmt.Sprintf("u%d", owner.ID), "source", "body", "")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/notes/share?id=%d", personalID)
	w := httptest.NewRecorder()
	s.handleNoteShare(w, withUser(httptest.NewRequest("POST", path, nil), owner))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, c := range []*Client{co, cp} {
		if event := dataTestReceive(t, c, "notes"); event.Scope != "group" || event.Group != g.ID {
			t.Fatal(event)
		}
	}
	dataTestEmpty(t, cs)
	var shared struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &shared); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	body := fmt.Sprintf(`{"id":%q,"scope":"group","title":"not permitted"}`, shared.ID)
	s.handleNotes(w, withUser(httptest.NewRequest("POST", "/api/notes", strings.NewReader(body)), peer))
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	dataTestEmpty(t, co, cp, cs)
	// The native tool path uses the same scope callback in headless callers.
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetMemoryStore(ms)
	session := fmt.Sprintf("u%d-headless", owner.ID)
	e.SetSessionID(session)
	s.callerContextForUser(t.Context(), owner, session).apply(e)
	args := fmt.Sprintf(`{"action":"share","id":"%d"}`, personalID)
	if _, _, err := e.Execute(t.Context(), "note", json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	dataTestReceive(t, co, "notes")
	dataTestReceive(t, cp, "notes")
	dataTestEmpty(t, cs)
}

func TestDataEventsSignedGroupStreamCannotSelectAnotherGroup(t *testing.T) {
	ms := securityStore(t)
	s := New(Config{MultiUser: true, AuthToken: "fixture-group-sse"})
	s.memStore = ms
	g, err := ms.CreateGroup(t.Context(), "stream group")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(t.Context(), "other stream group")
	if err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("room-g%d", g.ID)
	token := s.selfCallToken(session)
	for _, query := range []string{"?scope=personal", fmt.Sprintf("?scope=group&group=%d", other.ID)} {
		request := httptest.NewRequest("GET", "/api/data-events"+query, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.withAuth(http.HandlerFunc(s.handleDataEvents)).ServeHTTP(w, request)
		if w.Code != 403 {
			t.Fatal(query, w.Code)
		}
	}
	remote := httptest.NewServer(s.withAuth(http.HandlerFunc(s.handleDataEvents)))
	defer remote.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", remote.URL+"/api/data-events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	readDataStream(t, scanner)
	s.emitDataChange(fmt.Sprintf("g%d", other.ID), "forbidden.topic")
	s.emitDataChange("u42", "forbidden.personal")
	s.emitDataChange(fmt.Sprintf("g%d", g.ID), "todo.shared")
	if event := readDataStream(t, scanner); event.Topic != "todo.shared" || event.Group != g.ID {
		t.Fatal(event)
	}
}

func readDataStream(t *testing.T, scanner *bufio.Scanner) dataChange {
	t.Helper()
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
			var event dataChange
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			return event
		}
	}
	t.Fatal("stream ended before event", scanner.Err())
	return dataChange{}
}

func TestDataEventsScriptStreamReconnectAndUnsubscribe(t *testing.T) {
	// The service token is also a valid global publisher/subscriber in multi.
	s := New(Config{AuthToken: "fixture-sse", MultiUser: true})
	s.memStore = securityStore(t)
	remote := httptest.NewServer(s.withAuth(http.HandlerFunc(s.handleDataEvents)))
	defer remote.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithCancel(t.Context())
		req, _ := http.NewRequestWithContext(ctx, "GET", remote.URL+"?topic=todo.shared", nil)
		req.Header.Set("Authorization", "Bearer fixture-sse")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
			resp.Body.Close()
			cancel()
			t.Fatal("not an event stream", resp.StatusCode)
		}
		scanner := bufio.NewScanner(resp.Body)
		if event := readDataStream(t, scanner); event.Topic != "*" || event.Reason != "connected" {
			t.Fatal("reconnect must request an authoritative refresh", event)
		}
		s.emitDataChange("global", "ignored.topic")
		s.emitDataChange("global", "todo.shared")
		if event := readDataStream(t, scanner); event.Topic != "todo.shared" {
			t.Fatal("topic filter not applied", event)
		}
		resp.Body.Close()
		cancel()
		deadline := time.Now().Add(time.Second)
		for {
			s.dataEvents.mu.Lock()
			remaining := len(s.dataEvents.subs)
			s.dataEvents.mu.Unlock()
			if remaining == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("cancelled stream kept its subscription")
			}
			time.Sleep(time.Millisecond)
		}
	}
	for _, query := range []string{"?topic=bad%20topic", "?scope=group&group=banana", "?scope=personal&group=1"} {
		w := httptest.NewRecorder()
		s.handleDataEvents(w, withUser(httptest.NewRequest("GET", "/api/data-events"+query, nil), serviceUser))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
}

func TestDataEventsBoundSlowSubscribers(t *testing.T) {
	s := New(Config{})
	sub := &dataSubscription{ch: make(chan dataChange, 1)}
	if !s.dataEvents.subscribe(sub) {
		t.Fatal("first subscription refused")
	}
	s.emitDataChange("global", "notes")
	s.emitDataChange("global", "notes") // Drop the slow stream; its reconnect reloads state.
	if _, ok := <-sub.ch; !ok {
		t.Fatal("first signal lost")
	}
	if _, ok := <-sub.ch; ok {
		t.Fatal("slow stream left open")
	}
	s.dataEvents.remove(sub) // Removal after overflow must remain safe.
	for i := 0; i < 8; i++ {
		if !s.dataEvents.subscribe(&dataSubscription{ch: make(chan dataChange, 1)}) {
			t.Fatal(i)
		}
	}
	if s.dataEvents.subscribe(&dataSubscription{ch: make(chan dataChange, 1)}) {
		t.Fatal("unbounded subscriptions")
	}
}
