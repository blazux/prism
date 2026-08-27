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
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	models, err := s.chatModels(ctx)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"models": []string{}, "error": err.Error()})
		return
	}
	// Platform allow-list applies to everyone — the admin pane manages the full
	// list via /api/admin/platform (unfiltered), so the picker can honestly show
	// what the platform offers. Per-group grants then tighten further.
	u := currentUser(r)
	if set, unrestricted := s.platformAllowedModels(ctx); !unrestricted {
		kept := models[:0]
		for _, m := range models {
			if set[m] {
				kept = append(kept, m)
			}
		}
		models = kept
	}
	models = s.filterModelsForUser(ctx, u, models)
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

	// RBAC: custom tools obey the same policy as in chat.
	if guard := s.buildUserGuard(r.Context(), currentUser(r)); guard != nil {
		if err := guard(name, nil); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	sessionID, sessOK := s.sessionFor(r, r.URL.Query().Get("session"))
	if !sessOK {
		http.Error(w, "forbidden", 403)
		return
	}
	if sessionID == "" {
		sessionID = "default"
	}

	// Shed load before spawning a docker exec: a widget with a button stuck in a
	// fetch() loop would otherwise launch unbounded execs and OOM the host.
	release, ok := s.toolLimiter.acquire(sessionID)
	if !ok {
		http.Error(w, "too many concurrent tool calls — slow down", http.StatusTooManyRequests)
		return
	}
	defer release()

	env := map[string]string{
		"PRISM_SESSION": sessionID,
		"PRISM_URL":     "http://prism-server:8080",
		"PRISM_TOKEN":   s.selfCallToken(sessionID),
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
	w.Write(toolResponseBody(out, execErr))
}

// toolResponseBody shapes a custom tool's stdout into the HTTP response body a
// widget receives. Any valid JSON — object OR array — is returned verbatim so
// the widget consumes it directly without a double-parse. Anything else (plain
// text, or a failed exec) is wrapped in {"output":...,"error":...}.
//
// The array case is the fix for a real trap: a tool that prints a top-level JSON
// array (e.g. a list of tickets) used to fail json.Unmarshal into a map and got
// stringified into {"output":"[...]"}, so a widget iterating the list silently
// saw nothing — while the same tool called from chat (raw stdout) looked fine.
func toolResponseBody(out string, execErr error) []byte {
	trimmed := strings.TrimSpace(out)
	if execErr == nil && json.Valid([]byte(trimmed)) {
		return []byte(trimmed)
	}
	resp := map[string]interface{}{"output": out}
	if execErr != nil {
		resp["error"] = execErr.Error()
	}
	b, _ := json.Marshal(resp)
	return b
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

	sessionID, sessOK := s.sessionFor(r, r.URL.Query().Get("session"))
	if !sessOK {
		http.Error(w, "forbidden", 403)
		return
	}
	if sessionID == "" {
		sessionID = "default"
	}

	// Same load-shedding as /api/tool: don't let a runaway caller spawn unbounded
	// tool executions and OOM the host.
	release, ok := s.toolLimiter.acquire(sessionID)
	if !ok {
		http.Error(w, "too many concurrent tool calls — slow down", http.StatusTooManyRequests)
		return
	}
	defer release()

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.cfg.AuthToken)
	executor.SetLLM(s.newChatBackend(), s.cfg.Model)
	executor.SetChatBlind(!s.cfg.ChatVision)
	executor.SetVox(s.cfg.VoxURL, s.cfg.VoxUser, s.cfg.VoxPassword) // enables place_call when docked
	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder, s.ragCaptioner)
	}
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	// RBAC + tenant scoping: this endpoint runs tools with the caller's
	// permissions and RAG scope, exactly like the WebSocket chat path — see
	// callerContextForUser for why this also resolves scope correctly for
	// service-token self-calls (cron, custom tools, widgets).
	s.callerContextForUser(r.Context(), currentUser(r), sessionID).apply(executor)
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
