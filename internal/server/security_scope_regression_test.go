package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"prism/internal/agent"
	"prism/internal/docker"
	"prism/internal/memory"
)

func scopeFixtureUser(t *testing.T, ms *memory.Store, email, role string) *memory.User {
	t.Helper()
	u, err := ms.CreateUser(t.Context(), email+"@example.test", "unused", email, role, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPersonalWebhookUsesOwnerScopeAndCapability(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u := scopeFixtureUser(t, ms, "hook-owner", memory.RoleMember)
	g, err := ms.CreateGroup(ctx, "Fixture group")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, g.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	s := New(Config{MultiUser: true, AuthToken: "fixture-key"})
	s.memStore = ms
	hook := memory.WebhookRow{ID: "fixture", Scope: fmt.Sprintf("u%d", u.ID)}
	session := webhookSession(hook)
	if session != fmt.Sprintf("u%d-webhook-fixture", u.ID) {
		t.Fatal(session)
	}
	cc := s.webhookCallerContext(ctx, hook, session)
	e := agent.NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
	e.SetSessionID(session)
	cc.apply(e)
	if e.SecretsScope() != hook.Scope || cc.RAGScope != fmt.Sprintf("g%d", g.ID) {
		t.Fatalf("personal=%q group=%q", e.SecretsScope(), cc.RAGScope)
	}
	uid, bound, ok := s.verifyCapToken(s.selfCallToken(session))
	if !ok || uid != u.ID || bound != session {
		t.Fatalf("wrong self-call identity: %d %q %v", uid, bound, ok)
	}
	if _, ok := s.capabilityRequest(httptest.NewRequest("POST", "/api/builtin/notify", nil), uid, bound); !ok {
		t.Fatal("owner capability rejected")
	}
	// The common context pins personal integrations even for older noncanonical
	// channel sessions, without changing their independent group RAG scope.
	legacy := s.callerContextForUser(ctx, u, "webhook-fixture")
	if legacy.PersonalScope != hook.Scope || legacy.RAGScope != cc.RAGScope {
		t.Fatalf("legacy context lost identity: %+v", legacy)
	}
}

func TestOwnedWebhookArchiveMigration(t *testing.T) {
	for _, conflict := range []string{"none", "source", "destination", "webhook", "rollback"} {
		t.Run(conflict, func(t *testing.T) {
			ms, dsn := securityStoreWithDSN(t)
			ctx := t.Context()
			u := scopeFixtureUser(t, ms, "owner", memory.RoleMember)
			other := scopeFixtureUser(t, ms, "other", memory.RoleMember)
			hook := memory.WebhookRow{ID: "fixture", Scope: fmt.Sprintf("u%d", u.ID), Enabled: true}
			if conflict == "webhook" {
				hook.Scope = fmt.Sprintf("u%d", other.ID)
			}
			if err := ms.WebhookUpsert(ctx, hook); err != nil {
				t.Fatal(err)
			}
			oldID, newID := "webhook-fixture", fmt.Sprintf("u%d-webhook-fixture", u.ID)
			var sourceOwner *int64
			if conflict == "source" {
				sourceOwner = &other.ID
			}
			if err := ms.EnsureSession(ctx, oldID, "Legacy feed", sourceOwner); err != nil {
				t.Fatal(err)
			}
			if conflict == "destination" {
				if err := ms.EnsureSession(ctx, newID, "Other owner", &other.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ms.AppendMessage(ctx, oldID, "user", "archive-fixture", nil); err != nil {
				t.Fatal(err)
			}
			if err := ms.SetConfig(ctx, "conversation_summary_"+oldID, "summary-fixture"); err != nil {
				t.Fatal(err)
			}
			if _, err := ms.AddNotification(ctx, oldID, "Legacy", "fixture", "info"); err != nil {
				t.Fatal(err)
			}
			if conflict == "rollback" {
				db, err := pgx.Connect(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close(ctx)
				_, err = db.Exec(ctx, `
					CREATE FUNCTION reject_archive_move() RETURNS trigger LANGUAGE plpgsql AS
					$$ BEGIN RAISE EXCEPTION 'fixture rollback'; END $$;
					CREATE TRIGGER reject_archive_move BEFORE UPDATE ON notifications
					FOR EACH ROW EXECUTE FUNCTION reject_archive_move();
				`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err := ms.MigrateWebhookSession(ctx, hook.ID, u.ID)
			oldHistory, readErr := ms.LoadHistory(ctx, oldID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if conflict != "none" {
				if err == nil || len(oldHistory) != 1 || ms.GetSummary(ctx, oldID) != "summary-fixture" {
					t.Fatalf("ownership failure changed archive: %v %#v", err, oldHistory)
				}
				if history, _ := ms.LoadHistory(ctx, newID); len(history) != 0 {
					t.Fatal("failed migration leaked history")
				}
				return
			}
			if err != nil || len(oldHistory) != 0 {
				t.Fatal(err, oldHistory)
			}
			if history, err := ms.LoadHistory(ctx, newID); err != nil || len(history) != 1 || history[0].Content != "archive-fixture" {
				t.Fatal(err, history)
			}
			if owner, exists, err := ms.SessionOwner(ctx, newID); err != nil || !exists || owner == nil || *owner != u.ID {
				t.Fatal("new session ownership", owner, exists, err)
			}
			if ms.GetSummary(ctx, newID) != "summary-fixture" {
				t.Fatal("summary lost")
			}
			if ns, err := ms.GetRecentNotifications(ctx, newID, 10); err != nil || len(ns) != 1 {
				t.Fatal("notifications lost", ns, err)
			}
			if err := ms.MigrateWebhookSession(ctx, hook.ID, u.ID); err != nil {
				t.Fatal("migration not idempotent", err)
			}
			if history, _ := ms.LoadHistory(ctx, newID); len(history) != 1 {
				t.Fatal("idempotent migration duplicated history")
			}
		})
	}
}

func TestRoomReactionsStayInConnectedGroup(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	a := scopeFixtureUser(t, ms, "a", memory.RoleMember)
	b := scopeFixtureUser(t, ms, "b", memory.RoleMember)
	ga, _ := ms.CreateGroup(ctx, "A")
	gb, _ := ms.CreateGroup(ctx, "B")
	foreign, err := ms.AddRoomMessage(ctx, gb.ID, &b.ID, "B", "private fixture", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ms.AddRoomMessage(ctx, ga.ID, &a.ID, "A", "local fixture", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddReaction(ctx, foreign.ID, b.ID, "b"); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub()}
	c := &roomClient{send: make(chan []byte, 4), groupID: ga.ID, userID: a.ID}
	s.rooms.add(c)
	defer s.rooms.remove(c)
	for _, add := range []bool{true, false} {
		s.handleRoomReaction(c, foreign.ID, "a", add)
		if rs, err := ms.MessageReactions(ctx, foreign.ID); err != nil || len(rs) != 1 || rs[0].UserID != b.ID {
			t.Fatal("foreign mutation", rs, err)
		}
		select {
		case ev := <-c.send:
			t.Fatal("foreign reaction metadata broadcast", string(ev))
		default:
		}
	}
	for _, add := range []bool{true, false} {
		s.handleRoomReaction(c, local.ID, "a", add)
		var ev struct {
			MessageID int64 `json:"messageId"`
			Reactions []memory.RoomReaction
		}
		select {
		case data := <-c.send:
			if err := json.Unmarshal(data, &ev); err != nil || ev.MessageID != local.ID || len(ev.Reactions) != map[bool]int{true: 1, false: 0}[add] {
				t.Fatal(string(data), err)
			}
		default:
			t.Fatal("local reaction missing")
		}
	}
	if err := ms.AddRoomReaction(ctx, ga.ID, foreign.ID, a.ID, "direct"); err != nil {
		t.Fatal(err)
	}
	if err := ms.RemoveRoomReaction(ctx, ga.ID, foreign.ID, b.ID, "b"); err != nil {
		t.Fatal(err)
	}
	if rs, err := ms.MessageReactions(ctx, foreign.ID); err != nil || len(rs) != 1 || rs[0].UserID != b.ID {
		t.Fatal("scoped SQL changed a foreign room", rs, err)
	}
	if rs, err := ms.RoomMessageReactions(ctx, ga.ID, foreign.ID); err != nil || len(rs) != 0 {
		t.Fatal("scoped query exposed foreign room", rs, err)
	}
}

func TestDisabledOwnerCannotUseWebhookOrTelegram(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u := scopeFixtureUser(t, ms, "channel-owner", memory.RoleMember)
	s := New(Config{MultiUser: true, AuthToken: "fixture-key"})
	s.memStore = ms
	us := ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
	if err := us.SetSecret(ctx, tgTokenSecret, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	if ids := (&telegramManager{s: s}).usersWithToken(); len(ids) != 1 || ids[0] != u.ID {
		t.Fatal("approved owner excluded", ids)
	}
	hook := memory.WebhookRow{ID: "fixture", Scope: fmt.Sprintf("u%d", u.ID), Token: "fixture", Enabled: true}
	if err := ms.WebhookUpsert(ctx, hook); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetUserStatus(ctx, u.ID, memory.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if ids := (&telegramManager{s: s}).usersWithToken(); len(ids) != 0 {
		t.Fatal("disabled owner still polled", ids)
	}
	cc := s.webhookCallerContext(ctx, hook, webhookSession(hook))
	if cc.Guard == nil || cc.Guard("read_file", nil) == nil {
		t.Fatal("disabled owner's tools allowed")
	}
	w := httptest.NewRecorder()
	s.handleWebhookIncoming(w, httptest.NewRequest("POST", "/api/webhook/fixture?token=fixture", strings.NewReader("fixture")))
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body)
	}
	var calls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer remote.Close()
	s.handleTelegramMessage(ctx, remote.URL, "fixture-token", u.ID, 42, "/start", nil, nil)
	if calls.Load() != 0 {
		t.Fatal("disabled Telegram account made network calls")
	}
	if _, linked := tgAllowedChat(us); linked {
		t.Fatal("disabled Telegram account linked a chat")
	}
}

func TestNotesValidateConversationBeforeMutation(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	a := scopeFixtureUser(t, ms, "notes-a", memory.RoleMember)
	b := scopeFixtureUser(t, ms, "notes-b", memory.RoleMember)
	root := t.TempDir()
	s := New(Config{MultiUser: true, WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins")})
	s.memStore = ms
	for _, u := range []*memory.User{a, b} {
		sid := fmt.Sprintf("u%d-default", u.ID)
		ag := agent.New(nil, nil, "fixture", ms, "")
		ag.SetSession(sid, "")
		s.clients[&Client{sessionID: sid, user: u, ag: ag}] = struct{}{}
	}
	post := func(session string) *httptest.ResponseRecorder {
		r := withUser(httptest.NewRequest("POST", "/api/notes?session="+session, bytes.NewBufferString(`{"title":"NOTE-FIXTURE","body":"only mine"}`)), a)
		w := httptest.NewRecorder()
		s.handleNotes(w, r)
		return w
	}
	foreign := fmt.Sprintf("u%d-default", b.ID)
	if w := post(foreign); w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body)
	}
	if notes, err := ms.ListNotes(ctx, fmt.Sprintf("u%d", a.ID)); err != nil || len(notes) != 0 {
		t.Fatal("rejected session mutated notes", notes, err)
	}
	w := post("default")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body)
	}
	var saved struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{foreign, "default"} {
		if history, err := ms.LoadHistory(ctx, sid); err != nil || len(history) != 0 {
			t.Fatal("note polluted another conversation", sid, history, err)
		}
	}
	mine := fmt.Sprintf("u%d-default", a.ID)
	if history, err := ms.LoadHistory(ctx, mine); err != nil || len(history) != 1 || !strings.Contains(history[0].Content, "NOTE-FIXTURE") {
		t.Fatal("own session not normalized", history, err)
	}
	for _, session := range []string{foreign, "default"} {
		r := withUser(httptest.NewRequest("DELETE", "/api/notes?session="+session+"&id="+saved.ID, nil), a)
		w := httptest.NewRecorder()
		s.handleNotes(w, r)
		want := http.StatusOK
		if session == foreign {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body)
		}
	}
	if history, _ := ms.LoadHistory(ctx, mine); len(history) != 2 {
		t.Fatal("own delete event missing", history)
	}
}

func TestWebSocketRejectsRevokedAuthenticationSnapshot(t *testing.T) {
	ms := securityStore(t)
	u := scopeFixtureUser(t, ms, "socket-setup", memory.RoleGlobalAdmin)
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	root := t.TempDir()
	s := New(Config{MultiUser: true, WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins"), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: provider.URL})
	s.memStore = ms
	s.docker = docker.WithExecution(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authentication already resolved u, but the administrator disables it
		// before handleWS registers the transport for closeUserSockets.
		r = withUser(r, u)
		if err := ms.SetUserStatus(r.Context(), u.ID, memory.StatusDisabled); err != nil {
			t.Error(err)
			return
		}
		s.closeUserSockets(u.ID)
		s.handleWS(w, r)
	}))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?session=fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, payload, err := conn.ReadMessage(); err == nil {
		t.Fatalf("revoked setup sent dashboard data: %s", payload)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("revoked setup left a passive socket open")
	}
}

func TestExistingWebSocketRevalidatesAccount(t *testing.T) {
	for _, change := range []string{"disable", "demote", "admin-disable"} {
		t.Run(change, func(t *testing.T) {
			ms := securityStore(t)
			ctx := t.Context()
			u := scopeFixtureUser(t, ms, "socket-owner", memory.RoleGlobalAdmin)
			admin := scopeFixtureUser(t, ms, "admin", memory.RoleGlobalAdmin)
			if err := ms.CreateLoginSession(ctx, "fixture-login", u.ID, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			provider := httptest.NewServer(http.NotFoundHandler())
			defer provider.Close()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "dummy.txt"), []byte("fixture-only-content"), 0600); err != nil {
				t.Fatal(err)
			}
			s := New(Config{MultiUser: true, AuthToken: "fixture-key", WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins"), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: provider.URL})
			s.memStore = ms
			s.docker = docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) { return "", nil })
			srv := httptest.NewServer(s.withAuth(http.HandlerFunc(s.handleWS)))
			defer srv.Close()
			header := http.Header{"Cookie": []string{sessionCookie + "=fixture-login"}}
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?session=fixture", header)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.WriteJSON(map[string]string{"type": "file_open", "path": "dummy.txt"}); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				var ev struct{ Type, Content string }
				if err := conn.ReadJSON(&ev); err != nil {
					t.Fatal(err)
				}
				if ev.Type == "file_content" {
					if ev.Content != "fixture-only-content" {
						t.Fatal(ev.Content)
					}
					break
				}
			}
			switch change {
			case "disable":
				err = ms.SetUserStatus(ctx, u.ID, memory.StatusDisabled)
			case "demote":
				err = ms.SetUserRole(ctx, u.ID, memory.RoleMember)
			case "admin-disable":
				w := httptest.NewRecorder()
				s.handleAdminUsers(w, withUser(httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(fmt.Sprintf(`{"id":%d,"action":"disable"}`, u.ID))), admin))
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if change != "admin-disable" {
				_ = conn.WriteJSON(map[string]string{"type": "file_open", "path": "dummy.txt"})
			}
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				var ev struct{ Type string }
				if err := conn.ReadJSON(&ev); err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						t.Fatal("revoked socket remained open")
					}
					break
				}
				if ev.Type == "file_content" {
					t.Fatal("revoked account read an admin-only file")
				}
			}
		})
	}
}
