package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"prism/internal/agent"
	"strconv"
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

	owner := ownerPtr(currentUser(r))
	switch r.Method {
	case "GET":
		sessions, err := ms.ListSessions(r.Context(), owner)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Hide the reserved sessions: "assistant" (the global apps' agent) and
		// "telegram" (the per-user Telegram bridge). They are not user workspaces
		// and must not look selectable/editable; chat reaches them by id.
		visible := sessions[:0]
		for _, sess := range sessions {
			switch userPrefixRe.ReplaceAllString(sess.ID, "") {
			case "assistant", "telegram":
				continue
			}
			visible = append(visible, sess)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"sessions": visible})

	case "POST":
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, "name required", 400)
			return
		}
		id, ok := s.sessionFor(r, body.Name)
		if !ok || id == "" {
			http.Error(w, "invalid name", 400)
			return
		}
		// Ensure uniqueness within this user's sessions by appending a suffix.
		existing, _ := ms.ListSessions(r.Context(), owner)
		taken := make(map[string]bool)
		for _, s := range existing {
			taken[s.ID] = true
		}
		base := id
		for i := 2; taken[id]; i++ {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		if err := ms.UpsertSession(r.Context(), id, body.Name, owner); err != nil {
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
	rawID := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if rawID == "" {
		http.Error(w, "missing id", 400)
		return
	}
	// Scope to the caller: rejects another user's session id (Phase 3).
	id, ok := s.sessionFor(r, rawID)
	if !ok {
		http.Error(w, "forbidden", 403)
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
		if err := ms.UpsertSession(r.Context(), id, body.Name, ownerPtr(currentUser(r))); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "name": body.Name})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

// loadPersonality returns a session's OWN editable adaptation. The shared base
// personality (memory.KeyPersonalityBase) is layered under it by the agent (see
// agent.loadProfile), so this must NOT include the base.
func loadPersonality(ctx context.Context, ms *memory.Store, sessionID string) string {
	if ms == nil {
		return ""
	}
	if p, ok, err := ms.GetConfig(ctx, memory.KeyPersonality+"_"+sessionID); err == nil && ok {
		return p
	}
	return ""
}

// handlePersonality reads/writes a workspace's editable personality (the same
// value the agent edits via update_system_prompt). GET ?session=ID, POST body
// {personality}. Takes effect on the session's next chat connection.
func (s *Server) handlePersonality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	sessionID, ok := s.sessionFor(r, r.URL.Query().Get("session"))
	if !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	key := memory.KeyPersonality + "_" + sessionID
	switch r.Method {
	case "GET":
		p, _, _ := ms.GetConfig(r.Context(), key)
		json.NewEncoder(w).Encode(map[string]interface{}{"personality": p})
	case "POST":
		var b struct {
			Personality string `json:"personality"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if err := ms.SetConfig(r.Context(), key, b.Personality); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleAgentPersonality reads/writes the caller's base personality (layered
// under every one of their sessions). GET -> {personality}; POST {personality}.
func (s *Server) handleAgentPersonality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.userStore(r)
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		p, _, _ := ms.GetConfig(r.Context(), memory.KeyPersonalityBase)
		json.NewEncoder(w).Encode(map[string]interface{}{"personality": p})
	case "POST":
		var b struct {
			Personality string `json:"personality"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if err := ms.SetConfig(r.Context(), memory.KeyPersonalityBase, b.Personality); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleAgentName reads/writes the caller's agent name. GET -> {name}; POST {name}.
func (s *Server) handleAgentName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.userStore(r)
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		name, _, _ := ms.GetConfig(r.Context(), memory.KeyAgentName)
		json.NewEncoder(w).Encode(map[string]interface{}{"name": name})
	case "POST":
		var b struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if err := ms.SetConfig(r.Context(), memory.KeyAgentName, strings.TrimSpace(b.Name)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// handleAgentLimits reads/writes the caller's agent turn budget (Settings › Agent).
// GET -> {maxIterations, thinking, default, min, max}; POST {maxIterations, thinking}.
// maxIterations 0 = built-in default. Applied by Agent.loadProfile on the next turn.
func (s *Server) handleAgentLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.userStore(r)
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		maxIter := 0
		if v, ok, _ := ms.GetConfig(r.Context(), memory.KeyAgentMaxIterations); ok {
			maxIter, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		thinking := true
		if v, ok, _ := ms.GetConfig(r.Context(), memory.KeyAgentThinking); ok && strings.TrimSpace(v) == "off" {
			thinking = false
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"maxIterations": maxIter, "thinking": thinking,
			"default": agent.DefaultMaxIterations, "min": agent.MinMaxIterations, "max": agent.MaxMaxIterations,
		})
	case "POST":
		var b struct {
			MaxIterations int   `json:"maxIterations"`
			Thinking      *bool `json:"thinking"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		maxIter := agent.ClampIterations(b.MaxIterations)
		mv := ""
		if maxIter > 0 {
			mv = strconv.Itoa(maxIter)
		}
		if err := ms.SetConfig(r.Context(), memory.KeyAgentMaxIterations, mv); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		tv := "on"
		if b.Thinking != nil && !*b.Thinking {
			tv = "off"
		}
		if err := ms.SetConfig(r.Context(), memory.KeyAgentThinking, tv); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "maxIterations": maxIter, "thinking": tv == "on"})
	default:
		http.Error(w, "method not allowed", 405)
	}
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
