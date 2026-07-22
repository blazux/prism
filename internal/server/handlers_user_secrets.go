package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Personal secrets: per-user credentials (e.g. an email password) the agent's
// request_secret/list_secrets/delete_secret tools also read/write via
// ToolExecutor.userStore(). Scoped with s.userStore(r) — the same helper
// every other personal-integration handler (email, calendar, OAuth) already
// uses; falls back to the plain store for the service identity / legacy
// no-DB mode, so single-user deployments keep working unchanged.
//
// GET    /api/user/secrets           → {secrets: [name,...]}
// POST   /api/user/secrets           → {name,value} create/update
// DELETE /api/user/secrets/<name>    → delete
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
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Name == "" || b.Value == "" {
			writeErr(w, http.StatusBadRequest, "name and value required")
			return
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
