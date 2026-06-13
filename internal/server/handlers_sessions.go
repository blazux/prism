package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"prism/internal/memory"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "unavailable"
	if s.docker.IsDockerAvailable() {
		status = s.docker.Status(r.Context())
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"container": status,
		"model":     s.cfg.Model,
	})
}

// ─── Sessions API ─────────────────────────────────────────────────────────────

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
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
		sessions, err := ms.ListSessions(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"sessions": sessions})

	case "POST":
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		id := sanitizeSessionID(body.Name)
		if id == "" {
			http.Error(w, "invalid name", 400)
			return
		}
		// Ensure uniqueness by appending suffix if needed
		existing, _ := ms.ListSessions(r.Context())
		taken := make(map[string]bool)
		for _, s := range existing {
			taken[s.ID] = true
		}
		base := id
		for i := 2; taken[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		if err := ms.UpsertSession(r.Context(), id, body.Name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		os.MkdirAll(filepath.Join(s.cfg.PluginDir, id), 0755)
		json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "name": body.Name})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		http.Error(w, "missing id", 400)
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
	case "DELETE":
		if err := ms.DeleteSession(r.Context(), id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Remove session plugin dir
		os.RemoveAll(filepath.Join(s.cfg.PluginDir, id))
		w.WriteHeader(204)

	case "PATCH":
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		if err := ms.UpsertSession(r.Context(), id, body.Name); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "name": body.Name})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// loadPersonality returns the personality for a session:
// 1. session-specific personality_<sessionID>
// 2. default session's personality (personality_default) — acts as a global default
// 3. empty string → agent.New falls back to the hardcoded default
func loadPersonality(ctx context.Context, ms *memory.Store, sessionID string) string {
	if ms == nil {
		return ""
	}
	if p, ok, err := ms.GetConfig(ctx, memory.KeyPersonality+"_"+sessionID); err == nil && ok {
		return p
	}
	if sessionID != "default" {
		if p, ok, err := ms.GetConfig(ctx, memory.KeyPersonality+"_default"); err == nil && ok {
			return p
		}
	}
	// Legacy fallback: global key written by older versions.
	if p, ok, err := ms.GetConfig(ctx, memory.KeyPersonality); err == nil && ok {
		return p
	}
	return ""
}

// ─── Secrets API ──────────────────────────────────────────────────────────────

// sanitizeSessionID turns a string into a safe session ID slug.
func sanitizeSessionID(s string) string {
	var sb strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen && sb.Len() > 0 {
			sb.WriteRune('-')
			prevHyphen = true
		}
	}
	id := strings.TrimRight(sb.String(), "-")
	if len(id) > 32 {
		id = id[:32]
	}
	return id
}

// ─── Plugin restore ───────────────────────────────────────────────────────────
