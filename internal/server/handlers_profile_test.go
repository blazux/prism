package server

import (
	"encoding/json"
	"net/http/httptest"
	"prism/internal/memory"
	"strings"
	"testing"
)

func TestProfileContactEmail(t *testing.T) {
	ms := securityStore(t)
	u, err := ms.CreateUser(t.Context(), "login@example.test", "unchanged-hash", "Fixture", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{memStore: ms}
	for _, user := range []*memory.User{u, {ID: 0}} {
		request := func(method, body string, status int) memory.Profile {
			t.Helper()
			w := httptest.NewRecorder()
			s.handleProfile(w, withUser(httptest.NewRequest(method, "/api/profile", strings.NewReader(body)), user))
			if w.Code != status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var p memory.Profile
			if status == 200 {
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
			}
			return p
		}
		request("POST", `{"email":" contact@example.test "}`, 200)
		if p := request("GET", "", 200); p.Email != "contact@example.test" {
			t.Fatalf("not persisted: %q", p.Email)
		}
		request("POST", `{"displayName":"Updated"}`, 200)
		request("POST", `{"email":"not-an-email"}`, 400)
		request("POST", `{"email":"Name <contact@example.test>"}`, 400)
		if p := request("GET", "", 200); p.Email != "contact@example.test" {
			t.Fatal("email changed unexpectedly")
		}
		request("POST", `{"email":""}`, 200)
		if p := request("GET", "", 200); p.Email != "" {
			t.Fatal("cannot clear email")
		}
	}
	got, hash, err := ms.GetUserByEmail(t.Context(), "login@example.test")
	if err != nil || got.ID != u.ID || hash != "unchanged-hash" {
		t.Fatal("contact edit changed authentication")
	}
}

func TestProfileTimezoneIsolation(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	users := []*memory.User{{ID: 0}}
	for _, name := range []string{"paris", "martinique"} {
		u, err := ms.CreateUser(t.Context(), name, "hash", name, memory.RoleMember, memory.StatusApproved)
		if err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	request := func(u *memory.User, method, body string, status int) memory.Profile {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleProfile(w, withUser(httptest.NewRequest(method, "/api/profile", strings.NewReader(body)), u))
		if w.Code != status {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
		var p memory.Profile
		if status == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	zones := []string{"UTC", "Europe/Paris", "America/Martinique"}
	for i, u := range users {
		request(u, "POST", `{"timezone":"`+zones[i]+`"}`, 200)
	}
	for i, u := range users {
		request(u, "POST", `{"timezone":"invalid"}`, 400)
		request(u, "POST", `{"displayName":"Updated"}`, 200)
		p := request(u, "GET", "", 200)
		if p.Timezone != zones[i] || p.EffectiveTimezone != zones[i] {
			t.Fatalf("user %d: %+v", u.ID, p)
		}
	}
	request(users[1], "POST", `{"timezone":""}`, 200)
	if p := request(users[1], "GET", "", 200); p.Timezone != "" || p.EffectiveTimezone == "" {
		t.Fatal("fallback failed")
	}
}
