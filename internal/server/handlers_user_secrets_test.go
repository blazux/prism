package server

import (
	"net/http/httptest"
	"testing"
)

// handleUserSecrets/handleUserSecretByName must 503 without a store — the
// only path reachable without a real Postgres (userStore(r)'s fallback
// behavior for a real user is otherwise covered indirectly wherever
// userStore itself is exercised).
func TestHandleUserSecrets_NoStore(t *testing.T) {
	s := &Server{}

	w := httptest.NewRecorder()
	s.handleUserSecrets(w, httptest.NewRequest("GET", "/api/user/secrets", nil))
	if w.Code != 503 {
		t.Errorf("handleUserSecrets, no store: status=%d, want 503", w.Code)
	}

	w = httptest.NewRecorder()
	s.handleUserSecretByName(w, httptest.NewRequest("DELETE", "/api/user/secrets/foo", nil))
	if w.Code != 503 {
		t.Errorf("handleUserSecretByName, no store: status=%d, want 503", w.Code)
	}
}
