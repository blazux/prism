package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) isAuthenticated(r *http.Request) bool {
	if cookie, err := r.Cookie("prism_session"); err == nil && cookie.Value == s.cfg.AuthToken {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ") == s.cfg.AuthToken
	}
	return false
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" || r.URL.Path == "/api/auth" {
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
			SameSite: http.SameSiteStrictMode,
		})
		fmt.Fprint(w, `{"ok":true}`)
	case http.MethodDelete:
		http.SetCookie(w, &http.Cookie{
			Name:     "prism_session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		fmt.Fprint(w, `{"ok":true}`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
