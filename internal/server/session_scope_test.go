package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

func reqAs(u *memory.User) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	if u != nil {
		r = withUser(r, u)
	}
	return r
}

func TestSessionFor(t *testing.T) {
	s := &Server{}
	u2 := &memory.User{ID: 2, Role: memory.RoleMember}

	if id, ok := s.sessionFor(reqAs(u2), "default"); !ok || id != "u2-default" {
		t.Errorf(`default → got (%q,%v), want ("u2-default",true)`, id, ok)
	}
	// Idempotent: a round-tripped storage id isn't double-prefixed.
	if id, ok := s.sessionFor(reqAs(u2), "u2-default"); !ok || id != "u2-default" {
		t.Errorf(`u2-default → got (%q,%v), want ("u2-default",true)`, id, ok)
	}
	// A different user's namespace is refused (the key isolation property).
	if _, ok := s.sessionFor(reqAs(u2), "u3-secret"); ok {
		t.Error("u2 must not be able to address u3's session")
	}
	// A user-named session that merely starts with 'u' but not u<digits>- is fine.
	if id, ok := s.sessionFor(reqAs(u2), "underground"); !ok || id != "u2-underground" {
		t.Errorf(`underground → got (%q,%v), want ("u2-underground",true)`, id, ok)
	}
	// Legacy (nil user) and the internal service identity pass through unchanged.
	if id, ok := s.sessionFor(reqAs(nil), "default"); !ok || id != "default" {
		t.Errorf(`nil user → got (%q,%v), want ("default",true)`, id, ok)
	}
	if id, ok := s.sessionFor(reqAs(serviceUser), "foo"); !ok || id != "foo" {
		t.Errorf(`service → got (%q,%v), want ("foo",true)`, id, ok)
	}
}
