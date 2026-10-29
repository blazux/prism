package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/memory"
)

func TestTelephonyOutboundOperationsAlwaysKeepPersonalOwnership(t *testing.T) {
	ms := securityStore(t)
	u, err := ms.CreateUser(t.Context(), "outbound-fixture@example.test", "fixture", "Fixture", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := ms.CreateGroup(t.Context(), "Outbound operators")
	if err := ms.AddGroupMember(t.Context(), g.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetConfig(t.Context(), voxWorkspaceGroupsKey, fmt.Sprintf("[%d]", g.ID)); err != nil {
		t.Fatal(err)
	}
	reached := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		var scope map[string]any
		if err := json.Unmarshal([]byte(r.Header.Get("X-Prism-Scope")), &scope); err != nil {
			t.Error(err)
		}
		if scope["owner"] != float64(u.ID) || scope["standard"] != false || scope["admin"] != nil || scope["instance"] == "" {
			t.Errorf("unsafe outbound scope: %v", scope)
		}
		if r.URL.Query().Get("group") != "" || r.URL.Query().Get("session_id") != "" || r.URL.Query().Get("owner") != "" {
			t.Errorf("browser scope leaked: %s", r.URL)
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/calls" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["owner_id"] != nil || body["audience"] != nil || body["max_attempts"] != float64(3) || body["scheduled_at"] != "2030-11-01T09:00:00-04:00" {
				t.Errorf("unsafe call payload: %v", body)
			}
		}
		fmt.Fprint(w, `{"items":[],"total":0}`)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, VoxURL: upstream.URL}}
	request := func(user *memory.User, method, path, body string) int {
		r := withUser(httptest.NewRequest(method, path, strings.NewReader(body)), user)
		r.Header.Set("X-Prism-Scope", `{"admin":true,"owner":999}`)
		w := httptest.NewRecorder()
		s.handleVoxWorkspaceAPI(w, r)
		return w.Code
	}
	for _, op := range []struct{ method, path, body string }{
		{"GET", "/api/vox-workspace/calls?group=admin&owner=999&session_id=victim", ""},
		{"POST", "/api/vox-workspace/calls?group=admin", `{"phone_number":"0596123456","mission":"fixture","scheduled_at":"2030-11-01T09:00:00-04:00","owner_id":999,"audience":"standard"}`},
		{"DELETE", "/api/vox-workspace/calls/42", ""},
		{"POST", "/api/vox-workspace/calls/42/retry", "{}"},
	} {
		if status := request(u, op.method, op.path, op.body); status != 200 {
			t.Fatalf("%s %s: %d", op.method, op.path, status)
		}
	}
	if reached != 4 {
		t.Fatalf("operations forwarded: %d", reached)
	}
	for _, op := range []struct{ method, path, body string }{
		{"POST", "/api/vox-workspace/calls", `{"phone_number":"","mission":"fixture"}`},
		{"DELETE", "/api/vox-workspace/calls/config", ""},
		{"POST", "/api/vox-workspace/calls/42/config", "{}"},
	} {
		if status := request(u, op.method, op.path, op.body); status < 400 {
			t.Fatal("invalid outbound operation accepted")
		}
	}
	if status := request(&memory.User{ID: -g.ID}, "GET", "/api/vox-workspace/calls", ""); status != 403 {
		t.Fatal("group capability gained personal outbound access")
	}
	if status := request(&memory.User{ID: 99999}, "GET", "/api/vox-workspace/calls", ""); status != 403 {
		t.Fatal("user without app access reached outbound operations")
	}
	if reached != 4 {
		t.Fatal("denied operation reached upstream")
	}
}
