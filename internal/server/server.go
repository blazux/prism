package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/agent"
	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/internal/memory"
	"prism/internal/ollama"
	"prism/internal/rag"

	"github.com/gorilla/websocket"
)


type Config struct {
	Port           string
	WorkspaceDir   string
	PluginDir      string
	OllamaURL      string
	Model          string
	AgentContainer string
	SearxngURL     string
	PostgresURL    string
	EmbedModel     string
	WebFS          embed.FS
}

type Server struct {
	cfg         Config
	docker      *docker.Manager
	upgrader    websocket.Upgrader
	clients     map[*Client]struct{}
	mu          sync.RWMutex
	ragStore    *rag.Store
	ragEmbedder *rag.Embedder
	customMgr   *customtools.Manager
	memStore    *memory.Store
}

type Client struct {
	conn            *websocket.Conn
	send            chan []byte
	ag              *agent.Agent
	cancelFn        context.CancelFunc
	mu              sync.Mutex
	sessionID       string
	lastNotifID     int64  // last notification ID pushed to this client
	pendingSecretCh chan string // non-nil while agent is waiting for secret input
}

func New(cfg Config) *Server {
	customToolsDir := filepath.Join(cfg.WorkspaceDir, "agent_tools")
	return &Server{
		cfg:       cfg,
		docker:    docker.NewManager(cfg.AgentContainer, cfg.WorkspaceDir),
		clients:   make(map[*Client]struct{}),
		customMgr: customtools.NewManager(customToolsDir),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Start() error {
	// Initialize memory store (agent config + conversation history)
	if s.cfg.PostgresURL != "" {
		go func() {
			for attempt := 1; ; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				ms, err := memory.NewStore(ctx, s.cfg.PostgresURL)
				cancel()
				if err == nil {
					s.mu.Lock()
					s.memStore = ms
					s.mu.Unlock()
					log.Printf("[memory] store initialized")
					return
				}
				log.Printf("[memory] init failed (attempt %d): %v", attempt, err)
				time.Sleep(3 * time.Second)
			}
		}()
	}

	// Initialize RAG in background (embedding probe can take a moment)
	go s.initRAG(context.Background())

	// Initialize Docker workspace in background
	go func() {
		if s.docker.IsDockerAvailable() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := s.docker.EnsureRunning(ctx); err != nil {
				log.Printf("[docker] workspace container unavailable: %v", err)
				log.Printf("[docker] commands will fail — start Docker and restart the server")
			}
		} else {
			log.Printf("[docker] Docker not available — exec tools will be disabled")
		}
	}()

	mux := http.NewServeMux()

	// Embedded web files
	webSub, err := fs.Sub(s.cfg.WebFS, "web")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	// Dynamic plugin files
	os.MkdirAll(s.cfg.PluginDir, 0755)
	mux.Handle("/plugins/", http.StripPrefix("/plugins/", http.FileServer(http.Dir(s.cfg.PluginDir))))

	// Widget data directory — writable by cron/tools, readable by widgets via /data/<file>
	widgetDataDir := filepath.Join(s.cfg.WorkspaceDir, "widget_data")
	os.MkdirAll(widgetDataDir, 0755)
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir(widgetDataDir))))

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// REST API
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/file", s.handleFile)
	mux.HandleFunc("/api/exec", s.handleExec)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/tools", s.handleTools)
	mux.HandleFunc("/api/tool/", s.handleToolCall)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)
	mux.HandleFunc("/api/notify", s.handleExternalNotify)
	mux.HandleFunc("/api/chat", s.handleChatHTTP)
	mux.HandleFunc("/api/secrets", s.handleSecrets)
	mux.HandleFunc("/api/secrets/", s.handleSecretByName)
	s.registerRAGRoutes(mux)

	addr := ":" + s.cfg.Port
	log.Printf("Listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// ─── WebSocket ───────────────────────────────────────────────────────────────

type WSMessage struct {
	Type          string          `json:"type"`
	Content       string          `json:"content,omitempty"`
	Path          string          `json:"path,omitempty"`
	ID            string          `json:"id,omitempty"`
	Locked        bool            `json:"locked,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
	Model         string          `json:"model,omitempty"`
	DisabledTools []string        `json:"disabledTools,omitempty"`
	Images        []string        `json:"images,omitempty"` // base64 image strings for multimodal
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	// Determine session from query param (default: "default")
	sessionID := sanitizeSessionID(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = "default"
	}

	// Ensure session exists in DB
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms != nil {
		if err := ms.EnsureSession(r.Context(), sessionID); err != nil {
			log.Printf("[session] ensure %q: %v", sessionID, err)
		}
	}

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	os.MkdirAll(sessionPluginDir, 0755)

	ollamaClient := ollama.NewClient(s.cfg.OllamaURL)

	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL)
	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder)
	}
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	executor.SetCustomTools(s.customMgr, func() {
		s.customMgr.Reload()
		s.broadcastTools()
	})

	var ragContextFn func() string
	if s.ragStore != nil {
		ragStore := s.ragStore
		ragContextFn = func() string {
			cols, err := ragStore.ListCollections(context.Background())
			if err != nil || len(cols) == 0 {
				return ""
			}
			var sb strings.Builder
			sb.WriteString("## Knowledge Base (RAG)\n\n")
			sb.WriteString("You have access to document collections via `rag_search`. Call it whenever the user's question might be answered by these documents — don't guess, search first.\n\n")
			for _, c := range cols {
				if c.Description != "" {
					fmt.Fprintf(&sb, "- **%s** — %s (%d docs)\n", c.Name, c.Description, c.DocCount)
				} else {
					fmt.Fprintf(&sb, "- **%s** (%d docs, %d chunks)\n", c.Name, c.DocCount, c.ChunkCount)
				}
			}
			return sb.String()
		}
	}

	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
	}

	model := s.cfg.Model

	// Load personality from DB for this session (best-effort)
	var personality string
	if ms != nil {
		if p, ok, err := ms.GetConfig(r.Context(), memory.KeyPersonality+"_"+sessionID); err == nil && ok {
			personality = p
		} else if p, ok, err := ms.GetConfig(r.Context(), memory.KeyPersonality); err == nil && ok {
			// fallback to global personality key (legacy)
			personality = p
		}
	}

	client.ag = agent.New(ollamaClient, executor, model, ms, personality)
	client.ag.SetSession(sessionID, personality)
	client.sessionID = sessionID
	if ragContextFn != nil {
		client.ag.SetRAGContextFn(ragContextFn)
	}

	// Wire notification callback: inserts into DB and pushes to this client immediately
	if ms != nil {
		executor.SetNotificationCallback(func(title, msg, level string) {
			id, err := ms.AddNotification(context.Background(), sessionID, title, msg, level)
			if err != nil {
				log.Printf("[notify] insert: %v", err)
				return
			}
			client.mu.Lock()
			if id > client.lastNotifID {
				client.lastNotifID = id
			}
			client.mu.Unlock()
			client.sendJSON(map[string]interface{}{
				"type":      "notification",
				"id":        id,
				"title":     title,
				"message":   msg,
				"level":     level,
				"read":      false,
				"createdAt": time.Now().Format(time.RFC3339),
			})
		})
	}

	// Wire secret request callback: pauses agent, shows password dialog in browser, stores result in DB
	executor.SetSecretRequestCallback(func(ctx context.Context, name, description string) error {
		ch := make(chan string, 1)
		client.mu.Lock()
		client.pendingSecretCh = ch
		client.mu.Unlock()
		defer func() {
			client.mu.Lock()
			if client.pendingSecretCh == ch {
				client.pendingSecretCh = nil
			}
			client.mu.Unlock()
		}()

		client.sendJSON(map[string]interface{}{
			"type":        "secret_request",
			"name":        name,
			"description": description,
		})

		select {
		case val := <-ch:
			if val == "" {
				return fmt.Errorf("secret input cancelled by user")
			}
			s.mu.RLock()
			curMS := s.memStore
			s.mu.RUnlock()
			if curMS != nil {
				if err := curMS.SetSecret(context.Background(), name, val); err != nil {
					return fmt.Errorf("store secret: %w", err)
				}
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Minute):
			return fmt.Errorf("timed out waiting for secret input (5 min)")
		}
	})

	// Wire plugin/file callbacks to WebSocket events
	executor.SetCallbacks(
		func(id, title, content string, cols, height int) {
			client.sendJSON(map[string]interface{}{
				"type": "plugin_load", "id": id, "title": title, "content": content,
				"cols": cols, "height": height,
			})
		},
		func(id string) {
			client.sendJSON(map[string]interface{}{"type": "plugin_unload", "id": id})
		},
		func(path string) {
			fullPath := filepath.Join(s.cfg.WorkspaceDir, filepath.Clean(path))
			content, err := os.ReadFile(fullPath)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "open_file: " + err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{"type": "file_content", "path": path, "content": string(content)})
			}
		},
		func() {
			// File changed — refresh tree
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
		},
	)

	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
		conn.Close()
		if client.cancelFn != nil {
			client.cancelFn()
		}
	}()

	// Send initial state
	status := "unavailable"
	if s.docker.IsDockerAvailable() {
		status = s.docker.Status(r.Context())
	}
	client.sendJSON(map[string]interface{}{
		"type":      "container_status",
		"status":    status,
		"model":     model,
		"sessionID": sessionID,
	})
	tree := s.buildFileTree(s.cfg.WorkspaceDir)
	client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

	client.sendJSON(map[string]interface{}{
		"type":   "tools_list",
		"custom": s.customMgr.All(),
	})

	// Restore persisted conversation history for the UI
	if ms != nil {
		if entries, err := ms.LoadHistory(r.Context(), sessionID); err == nil && len(entries) > 0 {
			type histMsg struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				CreatedAt string `json:"createdAt"` // ISO 8601
			}
			var msgs []histMsg
			for i, e := range entries {
				if e.Role == "user" {
					msgs = append(msgs, histMsg{
						Role:      e.Role,
						Content:   e.Content,
						CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
					})
				} else if e.Role == "assistant" {
					// Skip intermediate assistant turns (those followed by a tool message).
					// These are mid-loop iterations with tool calls; only the final
					// assistant message (not followed by a tool) should appear in the UI.
					if i+1 < len(entries) && entries[i+1].Role == "tool" {
						continue
					}
					msgs = append(msgs, histMsg{
						Role:      e.Role,
						Content:   e.Content,
						CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
					})
				}
			}
			if len(msgs) > 0 {
				client.sendJSON(map[string]interface{}{
					"type":     "chat_history",
					"messages": msgs,
				})
			}
		}
	}

	// Restore persisted widgets for this session
	for _, p := range s.loadPlugins(sessionPluginDir) {
		client.sendJSON(map[string]interface{}{
			"type": "plugin_load", "id": p.id, "title": p.title, "content": p.content,
			"cols": p.cols, "height": p.height, "locked": p.locked,
		})
	}

	// Send recent notification history
	if ms != nil {
		if notifs, err := ms.GetRecentNotifications(r.Context(), sessionID, 50); err == nil && len(notifs) > 0 {
			client.lastNotifID = notifs[len(notifs)-1].ID
			client.sendJSON(map[string]interface{}{
				"type":          "notifications_history",
				"notifications": notifs,
			})
		}
	}

	go client.writePump()

	// Background poller: pushes notifications created by cron scripts (outside of chat turns)
	done := make(chan struct{})
	defer close(done)
	if ms != nil {
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					client.mu.Lock()
					lastID := client.lastNotifID
					client.mu.Unlock()
					notifs, err := ms.GetNotificationsAfter(context.Background(), sessionID, lastID)
					if err != nil || len(notifs) == 0 {
						continue
					}
					for _, n := range notifs {
						client.sendJSON(map[string]interface{}{
							"type":      "notification",
							"id":        n.ID,
							"title":     n.Title,
							"message":   n.Message,
							"level":     n.Level,
							"read":      n.Read,
							"createdAt": n.CreatedAt.Format(time.RFC3339),
						})
					}
					client.mu.Lock()
					client.lastNotifID = notifs[len(notifs)-1].ID
					client.mu.Unlock()
				}
			}
		}()
	}

	// Read pump
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "chat":
			// Cancel previous if running
			if client.cancelFn != nil {
				client.cancelFn()
			}
			ctx, cancel := context.WithCancel(context.Background())
			client.mu.Lock()
			client.cancelFn = cancel
			client.mu.Unlock()

			client.ag.SetActiveTools(msg.DisabledTools)
			go s.handleChat(ctx, client, msg.Content, msg.Images, msg.Model)

		case "cancel":
			if client.cancelFn != nil {
				client.cancelFn()
				client.cancelFn = nil
			}

		case "file_open":
			content, err := os.ReadFile(filepath.Join(s.cfg.WorkspaceDir, filepath.Clean(msg.Path)))
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{
					"type": "file_content", "path": msg.Path, "content": string(content),
				})
			}

		case "file_save":
			var payload struct {
				Content string `json:"content"`
			}
			json.Unmarshal(msg.Data, &payload)
			path := filepath.Join(s.cfg.WorkspaceDir, filepath.Clean(msg.Path))
			os.MkdirAll(filepath.Dir(path), 0755)
			if err := os.WriteFile(path, []byte(payload.Content), 0644); err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{"type": "saved", "path": msg.Path})
				tree := s.buildFileTree(s.cfg.WorkspaceDir)
				client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
			}

		case "file_delete":
			path := filepath.Join(s.cfg.WorkspaceDir, filepath.Clean(msg.Path))
			os.Remove(path)
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "refresh_files":
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "remove_plugin":
			if msg.ID != "" {
				os.Remove(filepath.Join(sessionPluginDir, msg.ID+".html"))
				os.Remove(filepath.Join(sessionPluginDir, msg.ID+".meta.json"))
				client.sendJSON(map[string]interface{}{"type": "plugin_unload", "id": msg.ID})
			}

		case "lock_plugin":
			if msg.ID != "" {
				metaPath := filepath.Join(sessionPluginDir, msg.ID+".meta.json")
				var m struct {
					Title  string `json:"title"`
					Cols   int    `json:"cols"`
					Height int    `json:"height"`
					Locked bool   `json:"locked,omitempty"`
				}
				if b, err := os.ReadFile(metaPath); err == nil {
					json.Unmarshal(b, &m)
				}
				m.Locked = msg.Locked
				if b, err := json.Marshal(m); err == nil {
					os.WriteFile(metaPath, b, 0644)
				}
			}

		case "reset_chat":
			client.ag.ResetHistory()
			client.sendJSON(map[string]interface{}{"type": "chat_reset"})

		case "secret_response":
			client.mu.Lock()
			ch := client.pendingSecretCh
			client.pendingSecretCh = nil
			client.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg.Content:
				default:
				}
			}

		case "mark_notifications_read":
			if ms != nil {
				ms.MarkNotificationsRead(context.Background(), client.sessionID)
			}
			client.sendJSON(map[string]interface{}{"type": "notifications_read"})

		case "delete_notification":
			if ms != nil && msg.ID != "" {
				if id, err := strconv.ParseInt(msg.ID, 10, 64); err == nil {
					ms.DeleteNotification(context.Background(), client.sessionID, id)
				}
			}
			client.sendJSON(map[string]interface{}{"type": "notification_deleted", "id": msg.ID})

		case "set_model":
			model = msg.Model
			var curPersonality string
			s.mu.RLock()
			curMS := s.memStore
			s.mu.RUnlock()
			if curMS != nil {
				if p, ok, err := curMS.GetConfig(context.Background(), memory.KeyPersonality+"_"+client.sessionID); err == nil && ok {
					curPersonality = p
				}
			}
			client.ag = agent.New(ollama.NewClient(s.cfg.OllamaURL), executor, model, curMS, curPersonality)
			client.ag.SetSession(client.sessionID, curPersonality)
			if ragContextFn != nil {
				client.ag.SetRAGContextFn(ragContextFn)
			}
			client.sendJSON(map[string]interface{}{"type": "model_set", "model": model})
		}
	}
}

func (s *Server) handleChat(ctx context.Context, client *Client, content string, images []string, modelOverride string) {
	events := make(chan agent.Event, 100)

	go func() {
		client.ag.Chat(ctx, content, images, events)
		close(events)
	}()

	for ev := range events {
		client.sendJSON(ev)

		// After file changes, refresh tree
		if ev.Type == "file_changed" {
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
		}
	}
}

// ─── REST API ─────────────────────────────────────────────────────────────────

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	tree := s.buildFileTree(s.cfg.WorkspaceDir)
	json.NewEncoder(w).Encode(map[string]interface{}{"files": tree})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		http.Error(w, "invalid path", 400)
		return
	}
	fullPath := filepath.Join(s.cfg.WorkspaceDir, cleanPath)

	switch r.Method {
	case "GET":
		data, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	case "POST", "PUT":
		var body struct{ Content string }
		json.NewDecoder(r.Body).Decode(&body)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err := os.WriteFile(fullPath, []byte(body.Content), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var body struct{ Command string }
	json.NewDecoder(r.Body).Decode(&body)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	out, err := s.docker.Exec(ctx, "cd /workspace && "+body.Command, 30*time.Second)
	resp := map[string]interface{}{"output": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	json.NewEncoder(w).Encode(resp)
}

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
		"IDE_SESSION": sessionID,
		"IDE_URL":     "http://server:8080",
	}

	escape := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	cmd := fmt.Sprintf("python3 /workspace/agent_tools/%s %s", escape(tool.Filename), escape(string(body)))

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	out, execErr := s.docker.ExecWithEnv(ctx, cmd, 30*time.Second, env)
	resp := map[string]interface{}{"output": out}
	if execErr != nil {
		resp["error"] = execErr.Error()
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

// handleExternalNotify allows cron scripts (running in Docker) to push notifications
// via: curl -s -X POST http://server:8080/api/notify -H "Content-Type: application/json" \
//      -d '{"session":"default","title":"Backup OK","level":"success"}'
func (s *Server) handleExternalNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var body struct {
		Session string `json:"session"`
		Title   string `json:"title"`
		Message string `json:"message"`
		Level   string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		http.Error(w, "title required", 400)
		return
	}
	if body.Session == "" {
		body.Session = "default"
	}
	if body.Level == "" {
		body.Level = "info"
	}

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", 503)
		return
	}

	id, err := ms.AddNotification(r.Context(), body.Session, body.Title, body.Message, body.Level)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"id": id})
}

// ─── HTTP Chat API ────────────────────────────────────────────────────────────
//
// POST /api/chat — trigger the agent from a cron script or external tool.
//
//	{"session":"default","message":"Analyse les CVE…","model":"llama3"}
//
// The agent runs to completion (max 10 min), saves the exchange to DB history,
// fires any send_notification calls normally, and returns the final response.
// Secrets (request_secret) are not available in this headless mode.
func (s *Server) handleChatHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var body struct {
		Session string `json:"session"`
		Message string `json:"message"`
		Model   string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		http.Error(w, "message required", 400)
		return
	}
	if body.Session == "" {
		body.Session = "default"
	}

	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()

	model := body.Model
	if model == "" {
		model = s.cfg.Model
	}

	sessionID := sanitizeSessionID(body.Session)
	if sessionID == "" {
		sessionID = "default"
	}

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	os.MkdirAll(sessionPluginDir, 0755)

	ollamaClient := ollama.NewClient(s.cfg.OllamaURL)
	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL)

	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder)
	}
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	executor.SetCustomTools(s.customMgr, func() {
		s.customMgr.Reload()
		s.broadcastTools()
	})

	// Notification: insert to DB + push to any live WS client for this session
	if ms != nil {
		executor.SetNotificationCallback(func(title, msg, level string) {
			id, err := ms.AddNotification(context.Background(), sessionID, title, msg, level)
			if err != nil {
				log.Printf("[chat-http] notification: %v", err)
				return
			}
			s.pushNotificationToSession(sessionID, id, title, msg, level)
		})
	}

	// WS-only features are no-ops in headless mode
	executor.SetCallbacks(
		func(id, title, content string, cols, height int) {},
		func(id string) {},
		func(path string) {},
		func() {},
	)
	executor.SetSecretRequestCallback(func(ctx context.Context, name, description string) error {
		return fmt.Errorf("request_secret non disponible en mode headless (cron)")
	})

	var personality string
	if ms != nil {
		if p, ok, err := ms.GetConfig(r.Context(), memory.KeyPersonality+"_"+sessionID); err == nil && ok {
			personality = p
		} else if p, ok, err := ms.GetConfig(r.Context(), memory.KeyPersonality); err == nil && ok {
			personality = p
		}
	}

	ag := agent.New(ollamaClient, executor, model, ms, personality)
	ag.SetSession(sessionID, personality)

	if s.ragStore != nil {
		ragStore := s.ragStore
		ag.SetRAGContextFn(func() string {
			cols, err := ragStore.ListCollections(context.Background())
			if err != nil || len(cols) == 0 {
				return ""
			}
			var sb strings.Builder
			sb.WriteString("## Knowledge Base (RAG)\n\nYou have access to document collections via `rag_search`.\n\n")
			for _, c := range cols {
				if c.Description != "" {
					fmt.Fprintf(&sb, "- **%s** — %s (%d docs)\n", c.Name, c.Description, c.DocCount)
				} else {
					fmt.Fprintf(&sb, "- **%s** (%d docs, %d chunks)\n", c.Name, c.DocCount, c.ChunkCount)
				}
			}
			return sb.String()
		})
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	events := make(chan agent.Event, 200)
	go func() {
		ag.Chat(ctx, body.Message, nil, events)
		close(events)
	}()

	// Collect the final assistant text, skipping <think>…</think> blocks
	var response strings.Builder
	inThink := false
	for ev := range events {
		if ev.Type == "stream" {
			switch ev.Content {
			case "<think>":
				inThink = true
			case "</think>":
				inThink = false
			default:
				if !inThink {
					response.WriteString(ev.Content)
				}
			}
		}
	}

	log.Printf("[chat-http] session=%q response=%d chars", sessionID, response.Len())
	json.NewEncoder(w).Encode(map[string]interface{}{
		"response": strings.TrimSpace(response.String()),
		"session":  sessionID,
	})
}

// pushNotificationToSession delivers a notification to all live WS clients for a session.
func (s *Server) pushNotificationToSession(sessionID string, id int64, title, message, level string) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type":      "notification",
		"id":        id,
		"title":     title,
		"message":   message,
		"level":     level,
		"read":      false,
		"createdAt": time.Now().Format(time.RFC3339),
	})
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.clients {
		if c.sessionID == sessionID {
			select {
			case c.send <- msg:
			default:
			}
		}
	}
}

// ─── Secrets API ──────────────────────────────────────────────────────────────

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

	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}

	names, err := ms.ListSecretNames(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if names == nil {
		names = []string{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"secrets": names})
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

	if r.Method != "DELETE" {
		http.Error(w, "method not allowed", 405)
		return
	}

	if err := ms.DeleteSecret(r.Context(), name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

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

type pluginEntry struct {
	id, title, content string
	cols, height       int
	locked             bool
}

func (s *Server) loadPlugins(dir string) []pluginEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var plugins []pluginEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".html" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".html")
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		title := id
		cols := 1
		height := 280
		locked := false
		if metaBytes, err := os.ReadFile(filepath.Join(dir, id+".meta.json")); err == nil {
			var m struct {
				Title  string `json:"title"`
				Cols   int    `json:"cols"`
				Height int    `json:"height"`
				Locked bool   `json:"locked"`
			}
			if json.Unmarshal(metaBytes, &m) == nil {
				if m.Title != "" {
					title = m.Title
				}
				if m.Cols > 0 {
					cols = m.Cols
				}
				if m.Height > 0 {
					height = m.Height
				}
				locked = m.Locked
			}
		}
		plugins = append(plugins, pluginEntry{id: id, title: title, content: string(content), cols: cols, height: height, locked: locked})
	}
	return plugins
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	Children []FileNode  `json:"children,omitempty"`
	Size     int64       `json:"size,omitempty"`
}

func (s *Server) buildFileTree(root string) []FileNode {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var nodes []FileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, _ := entry.Info()
		node := FileNode{
			Name:  entry.Name(),
			Path:  entry.Name(),
			IsDir: entry.IsDir(),
		}
		if !entry.IsDir() && info != nil {
			node.Size = info.Size()
		}
		if entry.IsDir() {
			node.Children = s.buildSubTree(filepath.Join(root, entry.Name()), entry.Name(), 0)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *Server) buildSubTree(dir, prefix string, depth int) []FileNode {
	if depth > 4 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var nodes []FileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, _ := entry.Info()
		relPath := prefix + "/" + entry.Name()
		node := FileNode{
			Name:  entry.Name(),
			Path:  relPath,
			IsDir: entry.IsDir(),
		}
		if !entry.IsDir() && info != nil {
			node.Size = info.Size()
		}
		if entry.IsDir() {
			node.Children = s.buildSubTree(filepath.Join(dir, entry.Name()), relPath, depth+1)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (c *Client) sendJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		log.Printf("client send buffer full, dropping message")
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer c.conn.Close()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

