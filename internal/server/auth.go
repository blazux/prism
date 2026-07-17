package server

// Multi-user authentication (Prism heavy). Accounts live in Postgres; login
// creates a server-side session whose opaque token rides in the `prism_session`
// cookie. The first account to sign up becomes the global admin (auto-approved);
// later signups are 'pending' until an admin approves them.
//
// Two compatibility paths are preserved:
//   - Service token: a request bearing `Authorization: Bearer <PRISM_TOKEN>` is
//     treated as a synthetic global admin. The agent uses this for its own
//     internal API self-calls (http_request tool, widget data, …).
//   - Legacy single-token mode: if there is no database (no POSTGRES_URL), auth
//     falls back to the old shared-token behaviour so local/dev still works.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"prism/internal/memory"
)

type ctxKey int

const userCtxKey ctxKey = 0

const sessionCookie = "prism_session"
const sessionTTL = 30 * 24 * time.Hour

// serviceUser is the identity attached to internal service-token calls.
var serviceUser = &memory.User{ID: 0, Email: "service@prism", Role: memory.RoleGlobalAdmin, Status: memory.StatusApproved}

// currentUser returns the authenticated user for a request, or nil.
func currentUser(r *http.Request) *memory.User {
	u, _ := r.Context().Value(userCtxKey).(*memory.User)
	return u
}

func withUser(r *http.Request, u *memory.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
}

func (s *Server) store() *memory.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memStore
}

// userStore returns a config-scoped view of the store for the calling user
// ("u<id>:" prefix on config keys and secret names), so external integrations —
// email, calendar providers, OAuth, notes vault, agent identity — are per-user
// instead of deployment-global. Falls back to the plain store for the service
// identity / legacy no-DB mode.
func (s *Server) userStore(r *http.Request) *memory.Store {
	ms := s.store()
	if u := currentUser(r); u != nil && u.ID > 0 {
		return ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
	}
	return ms
}

func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func isOAuthCallback(p string) bool {
	return strings.HasPrefix(p, "/api/oauth/") && strings.HasSuffix(p, "/callback")
}

// authPublicPath lists the endpoints reachable without a session (the login /
// signup surface itself).
func authPublicPath(p string) bool {
	switch p {
	case "/login", "/signup", "/api/login", "/api/signup", "/api/me":
		return true
	}
	return false
}

// userFromCookie resolves the session cookie to its user, or nil.
func (s *Server) userFromCookie(r *http.Request, ms *memory.Store) *memory.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := ms.UserByLoginToken(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return u
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The one place the two modes part company. Single-user is the default and
		// never reaches the account machinery below — see auth_singleuser.go.
		if !s.cfg.MultiUser {
			s.singleUserAuth(next, w, r)
			return
		}

		if authPublicPath(r.URL.Path) || isOAuthCallback(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ms := s.store()

		// Legacy single-token mode when there is no database.
		if ms == nil {
			if s.cfg.AuthToken == "" || bearerToken(r) == s.cfg.AuthToken || s.legacyCookieAuthed(r) {
				next.ServeHTTP(w, r)
				return
			}
			s.deny(w, r)
			return
		}

		// Internal service token → synthetic global admin. Accepted via Bearer
		// (agent self-calls) or via the session cookie (the widget-preview browser
		// sets it so the whole page context — including the widget's /data fetches —
		// is authenticated instead of bouncing to /login).
		if s.cfg.AuthToken != "" && (bearerToken(r) == s.cfg.AuthToken || cookieValue(r, sessionCookie) == s.cfg.AuthToken) {
			next.ServeHTTP(w, withUser(r, serviceUser))
			return
		}

		// Real user session.
		if u := s.userFromCookie(r, ms); u != nil {
			next.ServeHTTP(w, withUser(r, u))
			return
		}

		s.deny(w, r)
	})
}

// legacyCookieAuthed supports the old shared-token cookie in no-DB mode.
func (s *Server) legacyCookieAuthed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value == s.cfg.AuthToken
}

// deny returns 401 for API/WS and redirects browser navigation to the login page.
func (s *Server) deny(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ─── session helpers ────────────────────────────────────────────────────────

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) startLoginSession(w http.ResponseWriter, r *http.Request, ms *memory.Store, userID int64) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	if err := ms.CreateLoginSession(r.Context(), token, userID, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	setSessionCookie(w, token, int(sessionTTL/time.Second))
	return nil
}

// ─── handlers ───────────────────────────────────────────────────────────────

// GET /api/me → the current user (or {authenticated:false}).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// /api/me is public, so resolve the cookie here directly.
	ms := s.store()
	if ms == nil {
		json.NewEncoder(w).Encode(map[string]any{"authenticated": s.cfg.AuthToken == "" || s.legacyCookieAuthed(r), "legacy": true})
		return
	}
	u := s.userFromCookie(r, ms)
	if u == nil {
		json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
		return
	}
	// isAdmin = global admin OR admin of any group — the very predicate that guards
	// /api/terminal and /api/exec. The UI hides the terminal on it; the server never
	// trusts it.
	json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "user": u, "isAdmin": s.isAdminUser(r.Context(), u)})
}

// POST /api/signup {email, password, displayName}
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "signup requires a database (POSTGRES_URL)")
		return
	}
	var b struct{ Email, Password, DisplayName string }
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeErr(w, http.StatusBadRequest, "valid email required")
		return
	}
	if len(b.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if _, _, err := ms.GetUserByEmail(r.Context(), email); err == nil {
		writeErr(w, http.StatusConflict, "an account with that email already exists")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	name := strings.TrimSpace(b.DisplayName)
	if name == "" {
		name = email
	}

	// First account bootstraps the global admin (auto-approved).
	n, err := ms.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	role, status := memory.RoleMember, memory.StatusPending
	if n == 0 {
		role, status = memory.RoleGlobalAdmin, memory.StatusApproved
	}

	u, err := ms.CreateUser(r.Context(), email, string(hash), name, role, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if status == memory.StatusApproved {
		if err := s.startLoginSession(w, r, ms, u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "loggedIn": true, "user": u})
		return
	}
	// Tell the admins someone is waiting at the door — a DB notification (feed
	// history) plus a live toast on every workspace they have open.
	go s.notifyAdminsOfSignup(u.DisplayName, u.Email)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "pending": true})
}

// notifyAdminsOfSignup notifies every approved global admin that a signup is
// pending approval (Admin → Users).
func (s *Server) notifyAdminsOfSignup(displayName, email string) {
	ms := s.store()
	if ms == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	users, err := ms.ListUsers(ctx)
	if err != nil {
		return
	}
	title := "New signup pending approval"
	msg := fmt.Sprintf("%s (%s) signed up and is waiting for approval — Admin → Users.", displayName, email)
	for _, u := range users {
		if u.Role != memory.RoleGlobalAdmin || u.Status != memory.StatusApproved {
			continue
		}
		sess := fmt.Sprintf("u%d-default", u.ID)
		if id, err := ms.AddNotification(ctx, sess, title, msg, "info"); err == nil {
			s.pushNotificationToUser(u.ID, id, title, msg, "info")
		}
	}
}

// POST /api/login {email, password}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "login requires a database (POSTGRES_URL)")
		return
	}
	var b struct{ Email, Password string }
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(b.Email))
	u, hash, err := ms.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	switch u.Status {
	case memory.StatusPending:
		writeErr(w, http.StatusForbidden, "your account is awaiting admin approval")
		return
	case memory.StatusDisabled:
		writeErr(w, http.StatusForbidden, "your account has been disabled")
		return
	}
	if err := s.startLoginSession(w, r, ms, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ms.AddUsage(r.Context(), u.ID, "", "login", "", 1, nil)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": u})
}

// POST /api/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if ms := s.store(); ms != nil {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			ms.DeleteLoginSession(r.Context(), c.Value)
		}
	}
	setSessionCookie(w, "", -1)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}

// ─── admin: user approval ───────────────────────────────────────────────────

// GET /api/admin/users → list; POST {id, action} → approve|disable|make_admin|make_member.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u == nil || !u.IsGlobalAdmin() {
		writeErr(w, http.StatusForbidden, "global admin only")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := ms.ListUsers(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"users": users})
	case http.MethodPost:
		var b struct {
			ID     int64  `json:"id"`
			Action string `json:"action"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.ID == 0 {
			writeErr(w, http.StatusBadRequest, "id and action required")
			return
		}
		// Lockout guard: never disable or demote the last approved global admin —
		// the platform would be left with no one able to administer it.
		if b.Action == "disable" || b.Action == "make_member" {
			if target, err := ms.GetUserByID(r.Context(), b.ID); err == nil && target != nil && target.IsGlobalAdmin() {
				if n, err := ms.CountGlobalAdmins(r.Context()); err == nil && n <= 1 {
					writeErr(w, http.StatusBadRequest, "cannot disable or demote the last global admin — promote someone else first")
					return
				}
			}
		}
		var err error
		switch b.Action {
		case "approve":
			err = ms.SetUserStatus(r.Context(), b.ID, memory.StatusApproved)
		case "disable":
			err = ms.SetUserStatus(r.Context(), b.ID, memory.StatusDisabled)
		case "make_admin":
			err = ms.SetUserRole(r.Context(), b.ID, memory.RoleGlobalAdmin)
		case "make_member":
			err = ms.SetUserRole(r.Context(), b.ID, memory.RoleMember)
		default:
			writeErr(w, http.StatusBadRequest, "unknown action")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "user_"+b.Action, map[string]interface{}{"targetUserId": b.ID})
		writeJSON(w, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAuth is a compatibility shim for the existing frontend, which still
// calls /api/auth (GET to check state, DELETE to log out). Reached only when
// already authenticated (the middleware gates it); the token-POST login is
// obsolete and points callers at the new flow. Removed when the UI is rewired
// for multi-user (Phase 6).
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.MultiUser {
		s.singleUserHandleAuth(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"authenticated": currentUser(r) != nil, "auth_enabled": true})
	case http.MethodDelete:
		s.handleLogout(w, r)
	default:
		writeErr(w, http.StatusGone, "token login removed — use /api/login")
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
