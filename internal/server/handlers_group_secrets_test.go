package server

import (
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

// handleGroupSecrets must reject a request with no group param (400) and,
// once a group is given, a request against a store-less server (503) — the
// only two paths reachable without a real Postgres (no DB-free mock/fake
// store exists anywhere in the repo, so the membership/admin-gated branches
// aren't unit-testable here; see rbac_test.go for the same constraint).
func TestHandleGroupSecrets_DenyPaths(t *testing.T) {
	s := &Server{}
	member := &memory.User{ID: 5, Role: memory.RoleMember, Status: memory.StatusApproved}

	// No group param → bad request, regardless of user.
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest("GET", "/api/group/secrets", nil), member)
	s.handleGroupSecrets(w, r)
	if w.Code != 400 {
		t.Errorf("missing group param: status=%d, want 400", w.Code)
	}

	// No store at all → 503 (this Server has no store configured).
	w = httptest.NewRecorder()
	r = withUser(httptest.NewRequest("GET", "/api/group/secrets?group=1", nil), member)
	s.handleGroupSecrets(w, r)
	if w.Code != 503 {
		t.Errorf("no store: status=%d, want 503", w.Code)
	}
}
