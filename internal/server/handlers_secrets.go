package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"prism/internal/memory"
)

// Team-shared secrets live unscoped; per-user/group ones are "u<id>:name" /
// "name:g<id>" rows in the same table. Both handlers below talk to the
// unscoped store, so without a gate any signed-in member could read or delete
// another user's mailbox password by asking for it by its full name. Admins
// only (a nil user — single-user or the service identity — passes, as for
// /api/files), and a name carrying a scope separator is refused outright:
// scoped secrets are managed through /api/user/secrets, which scopes itself.
func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminUser(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", 503)
		return
	}

	switch r.Method {
	case "GET":
		names, err := ms.ListScriptSecretNames(r.Context())
		if err != nil {
			writeSecretStoreError(w, err)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"secrets": names})

	case "POST":
		var body struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Value == "" {
			http.Error(w, "name and value required", 400)
			return
		}
		if strings.Contains(body.Name, ":") {
			http.Error(w, "invalid secret name", 400)
			return
		}
		if err := ms.SetScriptSecret(r.Context(), body.Name, body.Value); err != nil {
			writeSecretStoreError(w, err)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleSecretByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !s.requireAdminUser(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
	if name == "" {
		http.Error(w, "missing name", 400)
		return
	}
	if strings.Contains(name, ":") {
		http.Error(w, "secret not found", 404)
		return
	}

	if memory.IsIntegrationSecret(name) {
		http.Error(w, "reserved integration credential; manage it in the integration's settings", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", 503)
		return
	}

	switch r.Method {
	case "GET":
		w.Header().Set("Content-Type", "application/json")
		val, ok, err := ms.GetSecret(r.Context(), name)
		if err != nil {
			writeSecretStoreError(w, err)
			return
		}
		if !ok {
			http.Error(w, "secret not found", 404)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"name": name, "value": val})
	case "DELETE":
		if err := ms.DeleteSecret(r.Context(), name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func writeSecretStoreError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, memory.ErrSecretName) {
		status = http.StatusBadRequest
	}
	writeErr(w, status, err.Error())
}
