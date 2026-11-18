package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func TestRoomMessageReceiptIsDurableAndScoped(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	u := scopeFixtureUser(t, ms, "room-receipt", memory.RoleMember)
	other := scopeFixtureUser(t, ms, "room-other", memory.RoleMember)
	g, _ := ms.CreateGroup(ctx, "Room A")
	gb, _ := ms.CreateGroup(ctx, "Room B")
	quote, _ := ms.AddRoomMessage(ctx, g.ID, &u.ID, "Member", "quote", false, nil)
	const requestID = "room-request-00000001"
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			m, created, err := ms.AddRoomMessageOnce(ctx, g.ID, u.ID, "Member", "hello", &quote.ID, requestID)
			if err != nil || m.Content != "hello" || m.ReplyTo == nil || *m.ReplyTo != quote.ID {
				t.Errorf("concurrent admission: %+v %v", m, err)
			}
			if created {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d messages", admitted.Load())
	}
	for _, tc := range []struct {
		content string
		reply   *int64
	}{{"changed", &quote.ID}, {" hello ", &quote.ID}, {"hello", nil}} {
		if _, _, err := ms.AddRoomMessageOnce(ctx, g.ID, u.ID, "Member", tc.content, tc.reply, requestID); !errors.Is(err, memory.ErrRoomRequestConflict) {
			t.Fatalf("request content/reply conflict accepted: %v", err)
		}
	}
	for _, scope := range [][2]int64{{g.ID, other.ID}, {gb.ID, u.ID}} {
		if _, created, err := ms.AddRoomMessageOnce(ctx, scope[0], scope[1], "Member", "hello", nil, requestID); err != nil || !created {
			t.Fatalf("request id escaped author/group scope: %v %v", created, err)
		}
	}
	if _, _, err := ms.AddRoomMessageOnce(ctx, gb.ID, u.ID, "Member", "foreign quote", &quote.ID, "room-foreign-000001"); !errors.Is(err, memory.ErrRoomReplyNotFound) {
		t.Fatalf("foreign reply accepted: %v", err)
	}
	missing := quote.ID + 10000
	if _, _, err := ms.AddRoomMessageOnce(ctx, g.ID, u.ID, "Member", "missing quote", &missing, "room-missing-000001"); !errors.Is(err, memory.ErrRoomReplyNotFound) {
		t.Fatalf("missing reply accepted: %v", err)
	}
	for range 2 {
		if _, created, err := ms.AddRoomMessageOnce(ctx, g.ID, u.ID, "Member", "legacy", nil, ""); err != nil || !created {
			t.Fatalf("legacy message: %v %v", created, err)
		}
	}
}

type roomReceiptFixture struct {
	Type      string             `json:"type"`
	Status    string             `json:"status"`
	RequestID string             `json:"requestId"`
	Replayed  bool               `json:"replayed"`
	Message   memory.RoomMessage `json:"message"`
}

func readRoomReceipt(t *testing.T, c *websocket.Conn) roomReceiptFixture {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var frame roomReceiptFixture
		if err := c.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == "room_receipt" {
			return frame
		}
	}
}

func TestRoomWebSocketAdmissionReplaysWithoutAgentDuplicate(t *testing.T) {
	ms, dsn := securityStoreWithDSN(t)
	ctx := t.Context()
	u := scopeFixtureUser(t, ms, "room-ws", memory.RoleMember)
	g, _ := ms.CreateGroup(ctx, "Room delivery")
	if err := ms.AddGroupMember(ctx, g.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub()}
	// Keep model work queued: these tests exercise admission only.
	s.rooms.queues[g.ID] = []func(){}
	t.Cleanup(func() { s.life.workers.Wait() })
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleRoomWS(w, withUser(r, u)) }))
	defer h.Close()
	connect := func() *websocket.Conn {
		t.Helper()
		c, _, err := websocket.DefaultDialer.Dial(strings.Replace(h.URL, "http", "ws", 1)+fmt.Sprintf("/wsroom?group=%d", g.ID), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c := connect()
	frame := map[string]any{"type": "message", "requestId": "room-ws-request-00001", "content": "@agent once"}
	if err := c.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
	first := readRoomReceipt(t, c)
	if first.Status != "accepted" || first.Replayed || first.Message.ID == 0 {
		t.Fatalf("missing durable receipt: %+v", first)
	}
	c.Close()
	c = connect()
	_ = c.WriteJSON(frame)
	replay := readRoomReceipt(t, c)
	if replay.Status != "accepted" || !replay.Replayed || replay.Message.ID != first.Message.ID {
		t.Fatalf("replay receipt: %+v", replay)
	}
	// The accepted receipt precedes side effects; wait for the first handler's
	// queue insertion without invoking a model or depending on socket timing.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.rooms.mu.Lock()
		queued := len(s.rooms.queues[g.ID])
		s.rooms.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued %d agent turns", queued)
		}
		time.Sleep(time.Millisecond)
	}
	frame["content"] = "changed"
	_ = c.WriteJSON(frame)
	if receipt := readRoomReceipt(t, c); receipt.Status != "rejected" {
		t.Fatalf("changed retry accepted: %+v", receipt)
	}
	for _, n := range []int{16000, 16001} {
		_ = c.WriteJSON(map[string]any{"type": "message", "requestId": fmt.Sprintf("unicode-message-%d", n), "content": strings.Repeat("🚀", n)})
		receipt := readRoomReceipt(t, c)
		want := "accepted"
		if n > 16000 {
			want = "rejected"
		}
		if receipt.Status != want {
			t.Fatalf("%d Unicode codepoints: %s", n, receipt.Status)
		}
	}
	// An actual PostgreSQL write failure sends an uncertain receipt; retrying
	// the same id after recovery produces exactly one persisted message.
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	_, err = db.Exec(ctx, `CREATE FUNCTION reject_room_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.content='database-failure' THEN RAISE EXCEPTION 'fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_room_insert BEFORE INSERT ON room_messages FOR EACH ROW EXECUTE FUNCTION reject_room_insert()`)
	if err != nil {
		t.Fatal(err)
	}
	failed := map[string]any{"type": "message", "requestId": "room-db-request-00001", "content": "database-failure"}
	_ = c.WriteJSON(failed)
	if receipt := readRoomReceipt(t, c); receipt.Status != "unavailable" {
		t.Fatalf("database error silently lost: %+v", receipt)
	}
	if _, err := db.Exec(ctx, `DROP TRIGGER reject_room_insert ON room_messages`); err != nil {
		t.Fatal(err)
	}
	_ = c.WriteJSON(failed)
	if receipt := readRoomReceipt(t, c); receipt.Status != "accepted" || receipt.Replayed {
		t.Fatalf("database recovery: %+v", receipt)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_messages WHERE request_id=$1`, failed["requestId"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry wrote %d rows: %v", count, err)
	}
}

func TestRoomWebSocketRechecksMembershipAndIdentity(t *testing.T) {
	for _, change := range []string{"membership", "disabled", "admin-demoted"} {
		t.Run(change, func(t *testing.T) {
			ms := securityStore(t)
			ctx := t.Context()
			role := memory.RoleMember
			if change == "admin-demoted" {
				role = memory.RoleGlobalAdmin
			}
			u := scopeFixtureUser(t, ms, "room-revoke", role)
			g, _ := ms.CreateGroup(ctx, "Room revocation")
			if role == memory.RoleMember {
				_ = ms.AddGroupMember(ctx, g.ID, u.ID, memory.GroupRoleMember)
			}
			s := &Server{memStore: ms, rooms: newRoomHub()}
			var stopped atomic.Bool
			s.rooms.cancels[g.ID] = func() { stopped.Store(true) }
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleRoomWS(w, withUser(r, u)) }))
			defer h.Close()
			c, _, err := websocket.DefaultDialer.Dial(strings.Replace(h.URL, "http", "ws", 1)+fmt.Sprintf("/wsroom?group=%d", g.ID), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			switch change {
			case "membership":
				err = ms.RemoveGroupMember(ctx, g.ID, u.ID)
			case "disabled":
				err = ms.SetUserStatus(ctx, u.ID, memory.StatusDisabled)
			case "admin-demoted":
				err = ms.SetUserRole(ctx, u.ID, memory.RoleMember)
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = c.WriteJSON(map[string]any{"type": "stop"})
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			for {
				_, _, err := c.ReadMessage()
				if err != nil {
					break
				}
			}
			if stopped.Load() {
				t.Fatal("revoked stale socket stopped the room")
			}
		})
	}
}

func TestRoomAdminRemovalClosesPassiveSubscriptions(t *testing.T) {
	for _, action := range []string{"remove_member", "delete"} {
		t.Run(action, func(t *testing.T) {
			ms := securityStore(t)
			ctx := t.Context()
			u := scopeFixtureUser(t, ms, "passive-room-member", memory.RoleMember)
			peer := scopeFixtureUser(t, ms, "passive-room-peer", memory.RoleMember)
			group, _ := ms.CreateGroup(ctx, "Room membership revocation")
			other, _ := ms.CreateGroup(ctx, "Unaffected room")
			// Exercise both filters with an existing multi-group membership, even
			// though the current admin UI only permits one primary group.
			for _, membership := range [][2]int64{{group.ID, u.ID}, {group.ID, peer.ID}, {other.ID, u.ID}} {
				if err := ms.AddGroupMember(ctx, membership[0], membership[1], memory.GroupRoleMember); err != nil {
					t.Fatal(err)
				}
			}
			s := &Server{memStore: ms, rooms: newRoomHub()}
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/admin/groups" {
					s.handleAdminGroups(w, withUser(r, serviceUser))
					return
				}
				user := u
				if r.URL.Query().Get("who") == "peer" {
					user = peer
				}
				s.handleRoomWS(w, withUser(r, user))
			}))
			defer h.Close()
			connect := func(gid int64, who string) *websocket.Conn {
				t.Helper()
				c, _, err := websocket.DefaultDialer.Dial(strings.Replace(h.URL, "http", "ws", 1)+fmt.Sprintf("/wsroom?group=%d&who=%s", gid, who), nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				for {
					var frame struct{ Type string }
					if err := c.ReadJSON(&frame); err != nil {
						t.Fatal(err)
					}
					if frame.Type == "presence" {
						break
					}
				}
				return c
			}
			target := connect(group.ID, "member")
			remaining := connect(group.ID, "peer")
			unaffected := connect(other.ID, "member")
			readUntil := func(c *websocket.Conn, want string) {
				t.Helper()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				for {
					var frame struct{ Type string }
					if err := c.ReadJSON(&frame); err != nil {
						t.Fatal(err)
					}
					if frame.Type == want {
						return
					}
				}
			}
			// Drain initial history/presence before revocation. The subscribed
			// clients send no frames at any point in this test.
			s.rooms.broadcast(group.ID, map[string]any{"type": "fixture_ready"})
			s.rooms.broadcast(other.ID, map[string]any{"type": "fixture_ready"})
			for _, c := range []*websocket.Conn{target, remaining, unaffected} {
				readUntil(c, "fixture_ready")
			}
			body := fmt.Sprintf(`{"action":%q,"groupId":%d,"userId":%d}`, action, group.ID, u.ID)
			resp, err := h.Client().Post(h.URL+"/api/admin/groups", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s returned HTTP %d", action, resp.StatusCode)
			}
			s.rooms.broadcast(group.ID, map[string]any{"type": "fixture_secret"})
			expectClosed := func(c *websocket.Conn) {
				t.Helper()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				for {
					var frame struct{ Type string }
					err := c.ReadJSON(&frame)
					if err != nil {
						var networkError net.Error
						if errors.As(err, &networkError) && networkError.Timeout() {
							t.Fatal("revoked passive room subscription remained open")
						}
						return
					}
					if frame.Type == "fixture_secret" {
						t.Fatal("revoked passive room subscription received a broadcast")
					}
				}
			}
			expectClosed(target)
			if action == "delete" {
				expectClosed(remaining)
			} else {
				readUntil(remaining, "fixture_secret")
			}
			s.rooms.broadcast(other.ID, map[string]any{"type": "fixture_unaffected"})
			readUntil(unaffected, "fixture_unaffected")
		})
	}
}

func TestRoomMembershipRevokedDuringHandshake(t *testing.T) {
	ms := securityStore(t)
	u := scopeFixtureUser(t, ms, "room-handshake-member", memory.RoleMember)
	group, err := ms.CreateGroup(t.Context(), "Room handshake revocation")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(t.Context(), group.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms, rooms: newRoomHub()}
	handshakeEntered, releaseHandshake := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandshake) }) }
	// Upgrade invokes CheckOrigin after the initial membership check. Pause it
	// until removal has committed and attempted to close all registered clients.
	s.upgrader.CheckOrigin = func(*http.Request) bool {
		close(handshakeEntered)
		<-releaseHandshake
		return true
	}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/groups" {
			s.handleAdminGroups(w, withUser(r, serviceUser))
			return
		}
		s.handleRoomWS(w, withUser(r, u))
	}))
	defer func() { release(); h.Close() }()
	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	dialed := make(chan dialResult, 1)
	go func() {
		dialer := *websocket.DefaultDialer
		dialer.HandshakeTimeout = 3 * time.Second
		c, _, err := dialer.Dial(strings.Replace(h.URL, "http", "ws", 1)+fmt.Sprintf("/wsroom?group=%d", group.ID), nil)
		dialed <- dialResult{conn: c, err: err}
	}()
	select {
	case <-handshakeEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("room handshake never reached the authorization barrier")
	}
	body := fmt.Sprintf(`{"action":"remove_member","groupId":%d,"userId":%d}`, group.ID, u.ID)
	resp, err := h.Client().Post(h.URL+"/api/admin/groups", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove_member returned HTTP %d", resp.StatusCode)
	}
	release()
	var result dialResult
	select {
	case result = <-dialed:
	case <-time.After(3 * time.Second):
		t.Fatal("room handshake did not resume")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.conn.Close()
	_ = result.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, data, err := result.conn.ReadMessage(); err == nil {
		t.Fatalf("revoked handshake delivered room data: %s", data)
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("revoked handshake left a passive room subscription open")
		}
	}
}
