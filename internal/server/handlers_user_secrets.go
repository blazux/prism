package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"prism/internal/agent"
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
	case http.MethodDelete:
		if err := ms.DeleteSecret(r.Context(), name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(204)

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
