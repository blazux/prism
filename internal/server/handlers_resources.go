package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"prism/internal/agent"
)

// This executor needs no AI provider or embedding initialization. Maintenance
// must work when a model/provider is unavailable, using the caller's normal
// tool policies and the same lifecycle implementation as agent calls.
func (s *Server) resourceExecutor(r *http.Request, session string) *agent.ToolExecutor {
	e := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, filepath.Join(s.cfg.PluginDir, session), s.cfg.SearxngURL, s.selfCallToken(session))
	e.SetRawResults(true)
	e.SetSessionID(session)
	e.SetMemoryStore(s.store())
	s.callerContextForUser(r.Context(), currentUser(r), session).apply(e)
	e.SetCustomTools(s.customMgr, func() {
		if s.customMgr != nil {
			s.customMgr.Reload()
			s.broadcastTools()
		}
	})
	e.SetCallbacks(nil, func(id string) { s.pushJSONToSession(session, map[string]any{"type": "plugin_unload", "id": id}) }, nil, nil)
	return e
}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	session, ok := s.sessionFor(r, r.URL.Query().Get("session"))
	if !ok {
		http.Error(w, "forbidden", 403)
		return
	}
	args := map[string]any{}
	switch r.Method {
	case "GET":
		args["action"] = "list"
		if widget := r.URL.Query().Get("widget"); widget != "" {
			args = map[string]any{"action": "cleanup", "widget": widget, "dry_run": true}
		}
		if r.URL.Query().Get("board") == "true" {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			result, err := s.resourceExecutor(r, session).CleanupResources(ctx, "", true, nil, true)
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			json.NewEncoder(w).Encode(result)
			return
		}
	case "POST":
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&args); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
	default:
		http.Error(w, "method not allowed", 405)
		return
	}
	e := s.resourceExecutor(r, session)
	b, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, _, err := e.Execute(ctx, "resources", b)
	if err != nil {
		w.WriteHeader(409)
		var cleanup any
		_ = json.Unmarshal([]byte(result), &cleanup)
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "cleanup": cleanup})
		return
	}
	w.Write([]byte(result))
}
