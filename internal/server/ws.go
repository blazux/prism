package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"

	"github.com/gorilla/websocket"
)

type Client struct {
	conn            *websocket.Conn
	send            chan []byte
	ag              *agent.Agent
	cancelFn        context.CancelFunc
	mu              sync.Mutex
	sessionID       string
	lastNotifID     int64       // last notification ID pushed to this client
	pendingSecretCh chan string // non-nil while agent is waiting for secret input
	viewContext     string      // what the user is currently looking at (UI -> agent)
}

// cancelActive cancels the in-flight agent turn (if any) under the client mutex.
func (c *Client) cancelActive() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
}

// ─── WebSocket ───────────────────────────────────────────────────────────────

// ChatFile holds a user-uploaded file: its name, extracted text, and the
// workspace-relative path where it was saved (so the agent can read the real file).
type ChatFile struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Path string `json:"path,omitempty"`
}

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
	Files         []ChatFile      `json:"files,omitempty"`  // parsed text file attachments
	// Widget window state (set_plugin_state). Pointers so callers can send a
	// partial update — only the provided fields are written to meta.json.
	Open *bool    `json:"open,omitempty"`
	X    *float64 `json:"x,omitempty"`
	Y    *float64 `json:"y,omitempty"`
	W    *float64 `json:"w,omitempty"`
	H    *float64 `json:"h,omitempty"`
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

	ollamaClient := s.newChatBackend()

	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.cfg.AuthToken)
	executor.SetLLM(ollamaClient, s.cfg.Model)
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
	executor.SetMCPManager(s.mcpMgr, func() {
		s.broadcastMCP(sessionID)
	})

	var ragContextFn func() string
	if s.ragStore != nil {
		ragStore := s.ragStore
		ragContextFn = func() string {
			cols, err := ragStore.ListCollections(context.Background(), sessionID)
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

	// Load personality for this session (falls back to default session, then hardcoded).
	personality := loadPersonality(r.Context(), ms, sessionID)

	client.ag = agent.New(ollamaClient, executor, model, ms, personality)
	client.ag.SetSession(sessionID, personality)
	client.sessionID = sessionID
	if ragContextFn != nil {
		client.ag.SetRAGContextFn(ragContextFn)
	}
	client.ag.SetUserProfileFn(func() string {
		return executor.GetUserProfile(context.Background())
	})
	client.ag.SetSkillsContextFn(executor.SkillsIndex)
	client.ag.SetViewContextFn(func() string {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.viewContext
	})
	if sessionID == assistantSession {
		client.ag.SetGlobalContextFn(s.workspacesOverview)
	}
	client.ag.SetLearningsCtxFn(func(ctx context.Context, query string) string {
		return executor.SearchLearnings(ctx, query)
	})
	mcpMgr := s.mcpMgr
	client.ag.SetMCPContextFn(func() string {
		servers, err := mcpMgr.List(context.Background(), sessionID)
		if err != nil || len(servers) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString("## MCP Servers\n\nYou have access to external tools via MCP servers. Use mcp_list_servers to see current configuration.\n\n")
		for _, srv := range servers {
			if !srv.Enabled || len(srv.Tools) == 0 {
				continue
			}
			fmt.Fprintf(&sb, "- **%s** (%d tools)\n", srv.Name, len(srv.Tools))
		}
		return sb.String()
	})

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
	executor.SetProgressFn(func(text string) {
		client.sendJSON(map[string]interface{}{"type": "progress", "content": text})
	})
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
		client.cancelActive()
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

	mcpServers, _ := s.mcpMgr.List(r.Context(), sessionID)
	client.sendJSON(map[string]interface{}{
		"type":   "tools_list",
		"custom": s.customMgr.All(),
		"mcp":    mcpServers,
	})

	// Restore persisted conversation history for the UI
	if ms != nil {
		if entries, err := ms.LoadHistory(r.Context(), sessionID); err == nil && len(entries) > 0 {
			type toolCallDef struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			}
			type histMsg struct {
				Role      string          `json:"role"`
				Content   string          `json:"content"`
				CreatedAt string          `json:"createdAt,omitempty"`
				ToolName  string          `json:"toolName,omitempty"`
				ToolInput json.RawMessage `json:"toolInput,omitempty"`
			}
			var msgs []histMsg
			var pendingCalls []toolCallDef
			var callIdx int
			for _, e := range entries {
				switch e.Role {
				case "user":
					msgs = append(msgs, histMsg{
						Role:      "user",
						Content:   e.Content,
						CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
					})
					pendingCalls = nil
					callIdx = 0
				case "assistant":
					if strings.TrimSpace(e.Content) != "" {
						msgs = append(msgs, histMsg{
							Role:      "assistant",
							Content:   e.Content,
							CreatedAt: e.CreatedAt.Local().Format(time.RFC3339),
						})
					}
					pendingCalls = nil
					callIdx = 0
					if len(e.ToolCalls) > 0 && string(e.ToolCalls) != "null" {
						_ = json.Unmarshal(e.ToolCalls, &pendingCalls)
					}
				case "tool":
					m := histMsg{Role: "tool", Content: e.Content}
					if callIdx < len(pendingCalls) {
						m.ToolName = pendingCalls[callIdx].Function.Name
						m.ToolInput = pendingCalls[callIdx].Function.Arguments
						callIdx++
					}
					msgs = append(msgs, m)
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
			"open": p.open, "x": p.x, "y": p.y, "w": p.w, "h": p.h,
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
			// WebSocket closed (refresh, tab close, network drop) — cancel any in-flight agent turn.
			client.cancelActive()
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "chat":
			ctx, cancel := context.WithCancel(context.Background())
			client.mu.Lock()
			// Cancel previous if running
			if client.cancelFn != nil {
				client.cancelFn()
			}
			client.cancelFn = cancel
			client.mu.Unlock()

			client.ag.SetActiveTools(msg.DisabledTools)
			content := msg.Content
			for _, f := range msg.Files {
				hdr := "=== Attached file: " + f.Name
				if f.Path != "" {
					hdr += fmt.Sprintf(" (saved in the workspace at %s — read it directly if you need more than the text below, e.g. to OCR a scanned PDF or parse a spreadsheet)", f.Path)
				}
				hdr += " ==="
				body := f.Text
				if strings.TrimSpace(body) == "" {
					if f.Path != "" {
						body = "[No text was extracted automatically — read the file at " + f.Path + " to process it yourself.]"
					} else {
						body = "[No text could be extracted from this file.]"
					}
				}
				content = hdr + "\n" + body + "\n\n" + content
			}
			go s.handleChat(ctx, client, content, msg.Images, msg.Model)

		case "cancel":
			client.cancelActive()

		case "file_open":
			fullPath, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "invalid path"})
				continue
			}
			content, err := os.ReadFile(fullPath)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{
					"type": "file_content", "path": msg.Path, "content": string(content),
				})
			}

		case "file_save":
			fullPath, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "invalid path"})
				continue
			}
			var payload struct {
				Content string `json:"content"`
			}
			json.Unmarshal(msg.Data, &payload)
			os.MkdirAll(filepath.Dir(fullPath), 0755)
			if err := os.WriteFile(fullPath, []byte(payload.Content), 0644); err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{"type": "saved", "path": msg.Path})
				tree := s.buildFileTree(s.cfg.WorkspaceDir)
				client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
			}

		case "file_delete":
			fullPath, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				continue
			}
			os.Remove(fullPath)
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "refresh_files":
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "remove_plugin":
			if msg.ID != "" {
				if err := os.Remove(filepath.Join(sessionPluginDir, msg.ID+".html")); err != nil && !os.IsNotExist(err) {
					log.Printf("[remove_plugin] delete html %s: %v", msg.ID, err)
				}
				if err := os.Remove(filepath.Join(sessionPluginDir, msg.ID+".meta.json")); err != nil && !os.IsNotExist(err) {
					log.Printf("[remove_plugin] delete meta %s: %v", msg.ID, err)
				}
				client.sendJSON(map[string]interface{}{"type": "plugin_unload", "id": msg.ID})
				client.ag.InjectNote("[Dashboard] The user removed widget '" + msg.ID + "' from the dashboard. It no longer exists.")
			}

		case "lock_plugin":
			if msg.ID != "" {
				metaPath := filepath.Join(sessionPluginDir, msg.ID+".meta.json")
				updatePluginMeta(metaPath, func(m map[string]any) {
					m["locked"] = msg.Locked
				})
			}

		// set_context records what the user is currently viewing so the agent
		// can resolve "this email/note/event" on the next turn.
		case "set_context":
			client.mu.Lock()
			client.viewContext = msg.Content
			client.mu.Unlock()

		// set_plugin_state persists window lifecycle without touching the
		// widget content: minimize/restore (open) and the free-window geometry
		// (x/y/w/h). Only the fields the client sent are written.
		case "set_plugin_state":
			if msg.ID != "" {
				metaPath := filepath.Join(sessionPluginDir, msg.ID+".meta.json")
				if _, err := os.Stat(metaPath); err == nil {
					updatePluginMeta(metaPath, func(m map[string]any) {
						if msg.Open != nil {
							m["open"] = *msg.Open
						}
						if msg.X != nil {
							m["x"] = *msg.X
						}
						if msg.Y != nil {
							m["y"] = *msg.Y
						}
						if msg.W != nil {
							m["w"] = *msg.W
						}
						if msg.H != nil {
							m["h"] = *msg.H
						}
					})
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
			client.ag = agent.New(s.newChatBackend(), executor, model, curMS, curPersonality)
			client.ag.SetSession(client.sessionID, curPersonality)
			if ragContextFn != nil {
				client.ag.SetRAGContextFn(ragContextFn)
			}
			client.ag.SetSkillsContextFn(executor.SkillsIndex)
			client.ag.SetViewContextFn(func() string {
				client.mu.Lock()
				defer client.mu.Unlock()
				return client.viewContext
			})
			if client.sessionID == assistantSession {
				client.ag.SetGlobalContextFn(s.workspacesOverview)
			}
			curSessionID := client.sessionID
			client.ag.SetMCPContextFn(func() string {
				servers, err := mcpMgr.List(context.Background(), curSessionID)
				if err != nil || len(servers) == 0 {
					return ""
				}
				var sb strings.Builder
				sb.WriteString("## MCP Servers\n\nYou have access to external tools via MCP servers. Use mcp_list_servers to see current configuration.\n\n")
				for _, srv := range servers {
					if !srv.Enabled || len(srv.Tools) == 0 {
						continue
					}
					fmt.Fprintf(&sb, "- **%s** (%d tools)\n", srv.Name, len(srv.Tools))
				}
				return sb.String()
			})
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

// ─── Workspace proxy ──────────────────────────────────────────────────────────
