package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// /api/me must report isAdmin wherever requireAdminUser would let the caller into
// /api/terminal — the UI keys the workspace-terminal shortcut off this field, so a
// mismatch either hides the shell from its owner or shows a panel that only 403s.
func TestHandleMeReportsAdmin(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		// Single-user mode: the middleware vetted PRISM_TOKEN before the handler
		// runs, so the caller is the owner.
		{"single-user", Config{MultiUser: false}},
		// Multi-user without a database: the legacy token is the whole deployment.
		{"multi-user, no store", Config{MultiUser: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: tc.cfg}
			w := httptest.NewRecorder()
			s.handleMe(w, httptest.NewRequest("GET", "/api/me", nil))

			var body struct {
				Authenticated, IsAdmin, MultiUser bool
			}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !body.Authenticated || !body.IsAdmin {
				t.Fatalf("got authenticated=%v isAdmin=%v, want both true", body.Authenticated, body.IsAdmin)
			}
			// The frontend gates personal RAG/MCP fallback UI on this field — it must
			// truthfully reflect the deployment's actual mode, not just default false.
			if body.MultiUser != tc.cfg.MultiUser {
				t.Errorf("multiUser=%v, want %v (cfg.MultiUser)", body.MultiUser, tc.cfg.MultiUser)
			}
		})
	}
}
