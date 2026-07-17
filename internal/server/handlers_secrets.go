package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
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
		all, err := ms.ListSecretNames(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Hide feature-internal scoped secrets ("u<id>:email_password", …) — the
		// Secrets tab manages the team-shared, user-created ones only.
		names := []string{}
		for _, n := range all {
			if !strings.Contains(n, ":") {
				names = append(names, n)
			}
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
		if err := ms.SetSecret(r.Context(), body.Name, body.Value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleSecretByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	name := strings.TrimPrefix(r.URL.Path, "/api/secrets/")
	if name == "" {
		http.Error(w, "missing name", 400)
		return
	}

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
		if err != nil || !ok {
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

// ─── MCP API ──────────────────────────────────────────────────────────────────
