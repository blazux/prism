package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/agent"
	"prism/internal/memory"
)

// Personal secrets: per-user credentials (e.g. an email password) the agent's
// request_secret/list_secrets/delete_secret tools also read/write via
// ToolExecutor.userStore(). Scoped with s.userStore(r) — the same helper
// every other personal-integration handler (email, calendar, OAuth) already
// uses; falls back to the plain store for the service identity / legacy
// no-DB mode, so single-user deployments keep working unchanged.
//
// GET    /api/user/secrets           → {secrets: [name,...]}
// POST   /api/user/secrets           → {name,value[,group]} create/update
// GET    /api/user/secrets/<name>    → {name,value,source} personal tier, then groups
// POST   /api/user/secrets/<name>    → {group} share: MOVE to the group's scope
// DELETE /api/user/secrets/<name>    → delete
//
// POST with a non-zero "group" is the Settings checkbox "make this a group
// secret": the value is stored under the GROUP's scope instead of the user's,
// making it shared — usable by every member's agent (group secrets are shared
// by design). Any member may do this (membership-gated, not admin-gated);
// reserved integration names are refused so a member can't shadow the group's
// built-in credentials (email, OAuth, MCP tokens).
func (s *Server) handleUserSecrets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.userStore(r)
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "memory store not available")
		return
	}

	switch r.Method {
	case http.MethodGet:
		names, err := ms.ListScopedSecretNames(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if names == nil {
			names = []string{}
		}
		writeJSON(w, map[string]interface{}{"secrets": names})

	case http.MethodPost:
		var b struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Group int64  `json:"group"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" || b.Value == "" {
			writeErr(w, http.StatusBadRequest, "name and value required")
			return
		}
		if b.Group > 0 {
			u := currentUser(r)
			if u == nil || (!u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, b.Group)) {
				writeErr(w, http.StatusForbidden, "not a member of that group")
				return
			}
			if agent.IsReservedSecretName(b.Name) {
				writeErr(w, http.StatusBadRequest, "reserved name — managed by the group's integrations")
				return
			}
			gs := s.store()
			if gs == nil {
				writeErr(w, http.StatusServiceUnavailable, "memory store not available")
				return
			}
			ms = gs.ConfigScope(fmt.Sprintf("g%d", b.Group))
		}
		if err := ms.SetSecret(r.Context(), b.Name, b.Value); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// resolveSecretScopes returns the personal store and the ordered list of group
// scopes ("g<id>") a secret-value GET may read, resolved the same way every
// other scope-aware entry point does (caller_context.go): from the caller's
// identity AND the ?session= it names. It is the authorization gate for the
// value endpoint, so isolation must hold by construction:
//
//   - A real authenticated user is scoped to THEIR OWN id, never the session's
//     "u<id>-" prefix — so passing ?session=u2-… as user u3 can't read u2's
//     personal secrets. Their group tier is exactly their own memberships.
//   - The service identity (Bearer PRISM_TOKEN, user id 0 — cron/widget/custom
//     tool) has no memberships, so it recovers scope from the session id: the
//     numeric user of a "u<id>-<board>" session, or the group of a shared-agent
//     "room-g<id>" / "webex-g<id>-…" session. This is the global-admin identity;
//     letting it read the group whose agent it's driving is the whole point.
//   - A "room-g<id>" group in the session is honored only for the service
//     identity or a real member of that group — never to widen a non-member's
//     reach.
func (s *Server) resolveSecretScopes(r *http.Request) (*memory.Store, []string) {
	sessionID := r.URL.Query().Get("session")
	u := currentUser(r)
	gs := s.store()

	// Effective user for personal scope: a real user is always themselves; the
	// service identity recovers the user the session acts for.
	scopeUID := int64(0)
	if u != nil && u.ID > 0 {
		scopeUID = u.ID
	} else {
		scopeUID = userIDFromSessionID(sessionID)
	}

	personal := s.userStore(r)
	if scopeUID > 0 && gs != nil {
		personal = gs.ConfigScope(fmt.Sprintf("u%d", scopeUID))
	}

	var memberships []string
	if scopeUID > 0 && gs != nil {
		if groups, err := gs.UserGroups(r.Context(), scopeUID); err == nil {
			for _, g := range groups {
				memberships = append(memberships, fmt.Sprintf("g%d", g.GroupID))
			}
		}
	}
	return personal, groupSecretScopes(memberships, sessionID, u == nil || u.ID == 0)
}

// groupSecretScopes is the pure authorization decision for the secret-value
// endpoint's group tier: which "g<id>" scopes may be read. Kept separate from
// the store I/O so the isolation property is unit-testable without a database.
// memberships are the effective user's own groups; a shared-agent session
// ("room-g<id>") adds its group ONLY for the service identity (a room/webex
// cron under the deployment token) — never for a non-member real user.
func groupSecretScopes(memberships []string, sessionID string, isService bool) []string {
	scopes := append([]string(nil), memberships...)
	seen := map[string]bool{}
	for _, s := range scopes {
		seen[s] = true
	}
	if gscope := groupScopeFromSessionID(sessionID); gscope != "" && !seen[gscope] && isService {
		scopes = append(scopes, gscope)
	}
	return scopes
}

func (s *Server) handleUserSecretByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/user/secrets/")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "missing name")
		return
	}
	ms := s.userStore(r)
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "memory store not available")
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Value fetch for scripts — the path a CRON job needs: cron injects no
		// secret env vars (only PRISM_*), and the legacy /api/secrets/<name>
		// only ever serves the unscoped global bucket, so a scoped secret was
		// unreachable from a script at runtime.
		//
		// Scope is resolved from ?session=<id> exactly like /api/builtin and
		// caller_context.go, NOT from the Bearer identity alone — because the
		// case that matters is a shared-agent cron ("PRISM_SESSION=room-g1")
		// firing under the deployment token (the service identity, user id 0,
		// which belongs to no group). Without the session, its group secrets
		// were a hard 404 (measured: the room-g1 morning briefing, which then
		// had to scrape tokens out of /workspace/.crontab to get in). With it,
		// the result mirrors what that session already gets in its sandbox env
		// (ToolExecutor.secretsEnv): personal tier first, then the group's
		// shared tier. See resolveSecretScopes for the authorization gate.
		if agent.IsReservedSecretName(name) {
			writeErr(w, http.StatusForbidden, "reserved name — integration credentials are not served to scripts")
			return
		}
		personalStore, groupScopes := s.resolveSecretScopes(r)
		if val, ok, err := personalStore.GetSecret(r.Context(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		} else if ok {
			writeJSON(w, map[string]interface{}{"name": name, "value": val, "source": "personal"})
			return
		}
		if gs := s.store(); gs != nil {
			for _, scope := range groupScopes {
				if gv, gok, _ := gs.ConfigScope(scope).GetSecret(r.Context(), name); gok {
					writeJSON(w, map[string]interface{}{"name": name, "value": gv, "source": "group"})
					return
				}
			}
		}
		writeErr(w, http.StatusNotFound, "secret not found in your personal or group secrets — see list_secrets for available names")

	case http.MethodDelete:
		if err := ms.DeleteSecret(r.Context(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(204)

	case http.MethodPost:
		// Share an EXISTING personal secret with a group: server-side move to
		// the group's scope, so the value is never read back through the UI
		// (a request_secret-created value can't be retyped — the user may not
		// even know it). A move, not a copy: a stale personal copy would
		// silently shadow the group's value in the member's own env after a
		// rotation, which is exactly the kind of trap secretsEnv's precedence
		// should never hand the agent.
		var b struct {
			Group int64 `json:"group"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Group <= 0 {
			writeErr(w, http.StatusBadRequest, "group required")
			return
		}
		u := currentUser(r)
		if u == nil || (!u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, b.Group)) {
			writeErr(w, http.StatusForbidden, "not a member of that group")
			return
		}
		if agent.IsReservedSecretName(name) {
			writeErr(w, http.StatusBadRequest, "reserved name — integration credentials cannot be shared")
			return
		}
		val, exists, err := ms.GetSecret(r.Context(), name)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !exists {
			writeErr(w, http.StatusNotFound, "secret not found")
			return
		}
		gs := s.store()
		if gs == nil {
			writeErr(w, http.StatusServiceUnavailable, "memory store not available")
			return
		}
		if err := gs.ConfigScope(fmt.Sprintf("g%d", b.Group)).SetSecret(r.Context(), name, val); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := ms.DeleteSecret(r.Context(), name); err != nil {
			// The group copy is written; report the leftover honestly instead
			// of pretending the move completed.
			writeErr(w, http.StatusInternalServerError, "shared with the group, but the personal copy could not be deleted: "+err.Error())
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "moved": true})

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
