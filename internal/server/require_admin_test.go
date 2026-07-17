package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/memory"
)

// A shell in the tools container is an admin capability: the workspace holds every
// group's files and the agent's secret key. These cases run without a database, so
// they pin the decision the middleware alone would get wrong (it only proves the
// caller is signed in).
func TestRequireAdminUser(t *testing.T) {
	s := &Server{}

	cases := []struct {
		name  string
		user  *memory.User
		allow bool
	}{
		// Legacy single-owner mode and the internal service identity: no user is
		// attached to the request, and the deployment has always been unrestricted.
		{"no user (legacy / service)", nil, true},
		{"global admin", &memory.User{ID: 1, Role: memory.RoleGlobalAdmin}, true},
		// Without a store there is no group_members table to consult, so an ordinary
		// member cannot be a group admin either.
		{"plain member", &memory.User{ID: 2, Role: memory.RoleMember}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/terminal", nil)
			if tc.user != nil {
				r = withUser(r, tc.user)
			}
			w := httptest.NewRecorder()

			got := s.requireAdminUser(w, r)
			if got != tc.allow {
				t.Fatalf("requireAdminUser = %v, want %v", got, tc.allow)
			}
			if !tc.allow && w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d (the caller must see why)", w.Code, http.StatusForbidden)
			}
			if tc.allow && w.Code != http.StatusOK {
				t.Errorf("status = %d, want the handler to be left untouched", w.Code)
			}
		})
	}
}
