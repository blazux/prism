package server

// Single-user mode: the whole of it.
//
// Prism is a personal dashboard first. MULTI_USER off (the default) means there are
// no accounts, no login page and no signup — PRISM_TOKEN is the entire authentication
// story, exactly as it was before accounts existed.
//
// The trick that makes one codebase serve both: every request here carries the
// service identity (serviceUser, ID 0), and every scope helper already reads ID 0 as
// "global, no namespacing" — userStore(r) returns the unscoped store, pimScopeFor(r)
// returns "global", sessionFor(r, id) skips the "u<id>-" prefix, ownerPtr(u) is nil.
// So the handlers, tools and channels are written once, for the multi-user case, and
// degrade to the personal one without knowing it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// tokenAuthed reports whether the request carries PRISM_TOKEN, as a Bearer header
// or as the session cookie the token-login sets.
func (s *Server) tokenAuthed(r *http.Request) bool {
	return bearerToken(r) == s.cfg.AuthToken || s.legacyCookieAuthed(r)
}

// singleUserAuth is the pre-accounts behaviour, unchanged — including its one
// subtlety: a browser hitting a page without the token still gets the page. The
// frontend then asks /api/auth and shows its own token prompt. Only /api/* and /ws
// are refused outright, because there is no human on the other end to prompt.
func (s *Server) singleUserAuth(next http.Handler, w http.ResponseWriter, r *http.Request) {
	// The OAuth callback is a cross-site top-level redirect from the provider, so the
	// SameSite cookie may not ride along — and it doesn't need to: the one-time
	// `state` protects it. Let it through so the token exchange can run.
	if s.cfg.AuthToken == "" || r.URL.Path == "/api/auth" || isOAuthCallback(r.URL.Path) || webhookPublicPath(r.URL.Path) {
		next.ServeHTTP(w, withUser(r, serviceUser))
		return
	}
	if s.tokenAuthed(r) {
		next.ServeHTTP(w, withUser(r, serviceUser))
		return
	}
	if tok := capTokenFromRequest(r); tok != "" {
		if _, _, ok := s.verifyCapToken(tok); ok {
			next.ServeHTTP(w, withUser(r, serviceUser))
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
		return
	}
	next.ServeHTTP(w, r)
}

// singleUserHandleAuth is the token login the dashboard talks to when there are no
// accounts: GET reports whether the token is needed and given, POST exchanges it for
// the session cookie, DELETE clears it. The multi-user build answers /api/login and
// /api/signup instead, and this endpoint reports the session there.
func (s *Server) singleUserHandleAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		authEnabled := s.cfg.AuthToken != ""
		fmt.Fprintf(w, `{"authenticated":%v,"auth_enabled":%v}`, !authEnabled || s.tokenAuthed(r), authEnabled)
	case http.MethodPost:
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token != s.cfg.AuthToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid token"}`)
			return
		}
		setSessionCookie(w, s.cfg.AuthToken, 30*24*3600)
		fmt.Fprint(w, `{"ok":true}`)
	case http.MethodDelete:
		setSessionCookie(w, "", -1)
		fmt.Fprint(w, `{"ok":true}`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
