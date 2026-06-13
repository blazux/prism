package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"prism/internal/agent"
	"prism/internal/ollama"
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	client := ollama.NewClient(s.cfg.OllamaURL)
	models, err := client.ListModels(ctx)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"models": []string{}, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"models": models})
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"custom": s.customMgr.All(),
	})
}

func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/tool/")
	if name == "" {
		http.Error(w, "tool name required", http.StatusBadRequest)
		return
	}

	tool := s.customMgr.Get(name)
	if tool == nil {
		http.Error(w, "tool not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}
	env := map[string]string{
		"PRISM_SESSION": sessionID,
		"PRISM_URL":     "http://prism-server:8080",
	}

	// Pass the JSON payload via stdin to avoid shell argument-length limits (ARG_MAX).
	// The one-liner shim injects stdin into sys.argv[1] so tools are unchanged.
	escape := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	cmd := fmt.Sprintf(
		"python3 -c 'import sys,runpy;sys.argv=[sys.argv[1],open(\"/dev/stdin\").read()];runpy.run_path(sys.argv[0],run_name=\"__main__\")' %s",
		escape("/workspace/agent_tools/"+tool.Filename),
	)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	out, execErr := s.docker.ExecWithStdin(ctx, cmd, body, 2*time.Minute, env)
	// If the tool printed valid JSON, return it directly so widgets don't need a double-parse.
	// Otherwise wrap in {"output": "..."} for plain-text results.
	var resp map[string]interface{}
	if execErr == nil && json.Unmarshal([]byte(strings.TrimSpace(out)), &resp) == nil {
		// valid JSON — resp is already set
	} else {
		resp = map[string]interface{}{"output": out}
		if execErr != nil {
			resp["error"] = execErr.Error()
		}
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) broadcastTools() {
	tools := s.customMgr.All()
	msg, _ := json.Marshal(map[string]interface{}{
		"type":   "tools_updated",
		"custom": tools,
	})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for client := range s.clients {
		select {
		case client.send <- msg:
		default:
		}
	}
}

// handleBuiltinTool exposes built-in agent tools as an HTTP endpoint so custom
// Python tools and cron scripts can call them.
// POST /api/builtin/<tool_name>?session=<id>  body: JSON args
func (s *Server) handleBuiltinTool(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/builtin/")
	if name == "" {
		http.Error(w, "tool name required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.cfg.AuthToken)
	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder, s.ragCaptioner)
	}
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	executor.SetCustomTools(s.customMgr, func() {
		s.customMgr.Reload()
		s.broadcastTools()
	})
	executor.SetMCPManager(s.mcpMgr, func() { s.broadcastMCP(sessionID) })

	if ms != nil {
		executor.SetNotificationCallback(func(title, msg, level string) {
			id, err := ms.AddNotification(context.Background(), sessionID, title, msg, level)
			if err != nil {
				return
			}
			s.pushNotificationToSession(sessionID, id, title, msg, level)
		})
	}

	executor.SetCallbacks(
		func(id, title, content string, cols, height int) {
			s.pushJSONToSession(sessionID, map[string]interface{}{
				"type": "plugin_load", "id": id, "title": title,
				"content": content, "cols": cols, "height": height,
			})
		},
		func(id string) {
			s.pushJSONToSession(sessionID, map[string]interface{}{"type": "plugin_unload", "id": id})
		},
		func(path string) {}, // open_file: UI-only, no-op
		func() {},            // file_changed: UI-only, no-op
	)
	executor.SetSecretRequestCallback(func(ctx context.Context, name, description string) error {
		return fmt.Errorf("request_secret unavailable from builtin HTTP endpoint")
	})

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	result, images, execErr := executor.Execute(ctx, name, json.RawMessage(body))

	resp := map[string]interface{}{"result": result}
	if len(images) > 0 {
		resp["images"] = images
	}
	if execErr != nil {
		resp["error"] = execErr.Error()
	}
	json.NewEncoder(w).Encode(resp)
}
