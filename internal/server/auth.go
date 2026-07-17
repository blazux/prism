package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/memory"
)

// store returns the memory store. It is written asynchronously once RAG/Postgres
// comes up (see Start), so reads go through the lock rather than touching the
// field directly.
func (s *Server) store() *memory.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memStore
}

// userStore returns the store holding the calling user's data.
//
// Prism is single-user: there is one store, and every request sees the same one —
// so this is store(), and r is ignored. The seam exists anyway, because the
// multi-user build returns a config-scoped view of the same store here ("u<id>").
// Handlers that call userStore(r) instead of touching s.memStore are then written
// once and behave correctly in both, which is the whole point: the mode is decided
// in this function, not in every handler.
func (s *Server) userStore(r *http.Request) *memory.Store { return s.store() }

// pimScopeFor returns the scope for the caller's personal data (notes, tasks,
// calendar). Single-user: always the shared scope. Same seam as userStore — the
// multi-user build answers "u<id>" here.
func (s *Server) pimScopeFor(r *http.Request) string { return pimScope }

func (s *Server) isAuthenticated(r *http.Request) bool {
	if cookie, err := r.Cookie("prism_session"); err == nil && cookie.Value == s.cfg.AuthToken {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ") == s.cfg.AuthToken
	}
	return false
}

func isOAuthCallback(p string) bool {
	return strings.HasPrefix(p, "/api/oauth/") && strings.HasSuffix(p, "/callback")
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The OAuth callback is a cross-site top-level redirect from the provider,
		// so the (SameSite) session cookie may not ride along — and it doesn't
		// need to: it's protected by the unguessable one-time `state`. Let it
		// through so the token exchange can run.
		if s.cfg.AuthToken == "" || r.URL.Path == "/api/auth" || isOAuthCallback(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"unauthorized"}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		authEnabled := s.cfg.AuthToken != ""
		authenticated := !authEnabled || s.isAuthenticated(r)
		fmt.Fprintf(w, `{"authenticated":%v,"auth_enabled":%v}`, authenticated, authEnabled)
	case http.MethodPost:
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token != s.cfg.AuthToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid token"}`)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "prism_session",
			Value:    s.cfg.AuthToken,
			Path:     "/",
			MaxAge:   30 * 24 * 3600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		fmt.Fprint(w, `{"ok":true}`)
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{
			Name:     "prism_session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		fmt.Fprint(w, `{"ok":true}`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
