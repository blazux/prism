package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/memory"
)

// /api/secrets talks to the UNSCOPED store: without a role gate any member could
// read "u1:email_password" by name. These cases run without a database, so an
// admin ends at 503 (no store) — the point is that a member never gets past 403
// and a scoped name never resolves at all.
func TestSecretsEndpointsAreAdminOnly(t *testing.T) {
	s := &Server{}
	member := &memory.User{ID: 2, Role: memory.RoleMember}
	admin := &memory.User{ID: 1, Role: memory.RoleGlobalAdmin}

	for _, path := range []string{"/api/secrets", "/api/secrets/u1:email_password", "/api/secrets/plain"} {
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest("GET", path, nil), member)
		if strings.HasSuffix(path, "secrets") {
			s.handleSecrets(w, r)
		} else {
			s.handleSecretByName(w, r)
		}
		if w.Code != 403 {
			t.Errorf("member GET %s: got %d, want 403", path, w.Code)
		}
	}

	w := httptest.NewRecorder()
	s.handleSecretByName(w, withUser(httptest.NewRequest("GET", "/api/secrets/u1:email_password", nil), admin))
	if w.Code != 404 {
		t.Errorf("admin GET scoped name: got %d, want 404 (unreachable by name)", w.Code)
	}

	w = httptest.NewRecorder()
	s.handleSecretByName(w, withUser(httptest.NewRequest("GET", "/api/secrets/plain", nil), admin))
	if w.Code != 503 {
		t.Errorf("admin GET plain name without store: got %d, want 503 (passed the gate)", w.Code)
	}

	// Single-user / service identity (no user on the request): unrestricted, as before.
	w = httptest.NewRecorder()
	s.handleSecretByName(w, httptest.NewRequest("GET", "/api/secrets/plain", nil))
	if w.Code != 503 {
		t.Errorf("no-user GET: got %d, want 503 (passed the gate)", w.Code)
	}
}
