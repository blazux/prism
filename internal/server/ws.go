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
	user            *memory.User // authenticated user for this connection (nil in legacy/no-DB mode)
	lastNotifID     int64        // last notification ID pushed to this client
	pendingSecretCh chan string  // non-nil while agent is waiting for secret input
	viewContext     string       // what the user is currently looking at (UI -> agent)
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
	Type string `json:"type"`
	// Channel names the surface the message comes from. Empty = the browser
	// dashboard; "voice" = a phone call docked from Vox (Vortex megazord), which
	// makes the agent answer in spoken form and skip extended reasoning.
	Channel       string          `json:"channel,omitempty"`
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

	// Determine session from query param, scoped to the connected user so each
	// user gets their own isolated sessions (Phase 3).
	clientSession := r.URL.Query().Get("session")
	sessionID, ok := s.sessionFor(r, clientSession)
	if !ok {
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"forbidden session"}`))
		conn.Close()
		return
	}

	// Ensure session exists in DB (owned by the connecting user)
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms != nil {
		if err := ms.EnsureSession(r.Context(), sessionID, sessionDisplayName(clientSession), ownerPtr(currentUser(r))); err != nil {
			log.Printf("[session] ensure %q: %v", sessionID, err)
		}
	}

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	os.MkdirAll(sessionPluginDir, 0755)

	ollamaClient := s.newChatBackend()

	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.cfg.AuthToken)
	executor.SetLLM(ollamaClient, s.cfg.Model)
	executor.SetChatBlind(!s.cfg.ChatVision)
	executor.SetVox(s.cfg.VoxURL, s.cfg.VoxUser, s.cfg.VoxPassword) // enables place_call when docked
	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder, s.ragCaptioner)
	}
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	wsUser := currentUser(r)

	// Vortex (megazord): a phone call docks through Vox with the service token,
	// which auth.go resolves to a GLOBAL ADMIN. Re-identify it, or a stranger who
	// dials the number gets an admin agent — exec_command, docker, mail, secrets,
	// and the owner's whole knowledge base. Until the caller is identified, a call
	// is a guest. See voice.go.
	voiceCall := r.URL.Query().Get("channel") == voiceChannelName && isServiceIdentity(wsUser)

	// A known caller (their number is on their profile) is identified: they get
	// THEIR memory and knowledge — inter-channel continuity — but still only the
	// voice-safe tools. An unknown caller stays a public switchboard guest.
	var voiceUser *memory.User
	var voiceDir []memory.DirEntry // the phone directory = Cortex user profiles
	if voiceCall {
		voiceUser = s.resolveVoiceCaller(r.Context(), r.URL.Query().Get("caller"))
		voiceDir = s.voiceDirectory(r.Context())
	}

	var ragScope string
	if voiceCall && voiceUser != nil {
		ragScope = s.ragScopeFor(r.Context(), voiceUser)
		log.Printf("[voice] caller %q identified as user %d (%s) → own scope %q, voice-safe tools",
			r.URL.Query().Get("caller"), voiceUser.ID, voiceUser.DisplayName, ragScope)
		CallerContext{
			Guard:         voiceGuard(voiceKnownAllowedTools),
			RAGScope:      ragScope,
			PersonalScope: fmt.Sprintf("u%d", voiceUser.ID), // their memory/profile
			HiddenTools:   voiceHiddenTools(voiceKnownAllowedTools),
			MultiUser:     s.cfg.MultiUser,
		}.apply(executor)
	} else if voiceCall {
		ragScope = s.voiceRAGScope(r.Context())
		log.Printf("[voice] inbound call from %q → guest identity (tools deny-by-default, rag scope %q)",
			r.URL.Query().Get("caller"), ragScope)
		CallerContext{
			Guard:    voiceGuard(voiceGuestAllowedTools),
			RAGScope: ragScope,
			// Pin an isolated personal scope: without it personalScope() falls back
			// to the rag scope and a caller could read the owner's profile / learnings.
			PersonalScope: voiceGuestScope,
			HiddenTools:   voiceHiddenTools(voiceGuestAllowedTools),
			MultiUser:     s.cfg.MultiUser,
		}.apply(executor)
		// Deliberately no custom tools and no MCP for a guest: built-ins only, and
		// only the allow-listed ones survive.
	} else {
		executor.SetCustomTools(s.customMgr, func() {
			s.customMgr.Reload()
			s.broadcastTools()
		})
		executor.SetMCPManager(s.mcpMgr, func() {
			s.broadcastMCP(sessionID)
		})
		// RBAC: gate this personal agent's tool calls by the connected user's
		// permissions (nil user / admin → unrestricted). Resolved once at connect;
		// the guard closure captures the policy snapshot.
		cc := s.callerContextForUser(r.Context(), wsUser, sessionID)
		ragScope = cc.RAGScope
		// Personal knowledge (profile, learnings) is per-user, not per-group: the
		// browser session id doesn't carry "u<id>-", so pin it explicitly or a grouped
		// user reads their profile under the group scope and finds nothing.
		if wsUser != nil && wsUser.ID > 0 {
			cc.PersonalScope = fmt.Sprintf("u%d", wsUser.ID)
		}
		cc.apply(executor)
	}

	ragContextFn := s.ragContextFn(ragScope)

	client := &Client{
		conn: conn,
		send: make(chan []byte, 256),
		user: wsUser,
	}

	model := s.cfg.Model

	// Personality per identity:
	//  - voice guest → the switchboard persona (never the owner's assistant, which
	//    would introduce itself to a stranger as managing their mail/servers);
	//  - identified voice caller → THEIR own personality + a note to greet them by
	//    name and resume the conversation (continuity);
	//  - dashboard → this session's personality.
	personality := loadPersonality(r.Context(), ms, sessionID)
	if voiceCall && voiceUser != nil {
		personality = voiceKnownPersonaNote(voiceUser.DisplayName) +
			loadPersonality(r.Context(), ms, fmt.Sprintf("u%d-default", voiceUser.ID))
	} else if voiceCall {
		personality = s.voicePersonality(r.Context())
	}
	if voiceCall {
		// Give the agent the live directory (Cortex profiles) so it only offers to
		// transfer to real people; the relay resolves the chosen name to a number.
		personality += voiceDirectoryText(voiceDir)
	}

	client.ag = agent.New(ollamaClient, executor, model, ms, personality)
	client.ag.SetSession(sessionID, personality)
	client.sessionID = sessionID
	if ragContextFn != nil {
		client.ag.SetRAGContextFn(ragContextFn)
	}
	client.ag.SetViewContextFn(func() string {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.viewContext
	})
	mcpMgr := s.mcpMgr

	// An identified caller gets their own profile + past learnings (that IS the
	// continuity), scoped to them by the personal scope set above. A guest gets
	// none. The dashboard gets everything.
	if !voiceCall || voiceUser != nil {
		client.ag.SetUserProfileFn(func() string {
			return executor.GetUserProfile(context.Background())
		})
		client.ag.SetLearningsCtxFn(func(ctx context.Context, query string) string {
			return executor.SearchLearnings(ctx, query)
		})
	}

	// The rest is owner infrastructure (skills, running services, workspaces, MCP
	// servers) — irrelevant and unsafe to expose on a phone call. Dashboard only.
	if !voiceCall {
		client.ag.SetSkillsContextFn(executor.SkillsIndex)
		client.ag.SetServicesContextFn(s.servicesContext)
		if sessionID == assistantSession {
			client.ag.SetGlobalContextFn(s.workspacesOverview)
		}
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
				if err := curMS.ConfigScope(executor.SecretsScope()).SetSecret(context.Background(), name, val); err != nil {
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

	// Vortex: on a voice call, expose the telephony tools and relay their calls to
	// Vox over this WS. Vox performs the transfer/message/hang-up with its ARI/SIP
	// machinery, exactly as it does for its own agent.
	if voiceCall {
		executor.SetTelephony(agent.TelephonyTools, func(name string, args map[string]interface{}) (string, error) {
			// transfer_call resolves against the Cortex directory (user profiles), not
			// a separate contacts table. Resolve here, pass Vox a pre-resolved number.
			if name == "transfer_call" {
				dest, _ := args["destination"].(string)
				cname, phone, ok := resolveTransferName(voiceDir, dest)
				if !ok {
					// Not in the directory → don't dial; let the agent offer a near
					// match from the list or take a message.
					return fmt.Sprintf("Échec : %q ne figure pas dans l'annuaire, le transfert n'a pas eu lieu. Ne réessaie pas sans l'accord de l'appelant : propose un nom proche de la liste, ou de prendre un message.", dest), nil
				}
				args["destination"] = cname
				args["dial_number"] = phone // pre-resolved for Vox
			}
			client.sendJSON(map[string]interface{}{"type": "telephony", "tool": name, "args": args})
			log.Printf("[voice] relaying telephony tool %q to Vox: %v", name, args)
			switch name {
			case "transfer_call":
				return "Le transfert est en cours.", nil
			case "take_message":
				return "Le message est bien noté.", nil
			case "end_call":
				return "L'appel va se terminer.", nil
			}
			return "OK", nil
		})
	}
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
			client.ag.SetChannel(msg.Channel)
			content := msg.Content
			for _, f := range msg.Files {
				// The browser already ran ingestAttachment via /api/chat/upload and
				// sent back {Text, Path}; here we only build the preamble.
				content = attachmentPreamble(attachment{Name: f.Name, Text: f.Text, Path: f.Path}) + "\n\n" + content
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
			// RBAC: refuse a model this user isn't allowed to use.
			if !s.userCanUseModel(context.Background(), client.user, msg.Model) {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "You are not allowed to use model " + msg.Model})
				continue
			}
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
			// Route the picked model to the backend that serves it (vLLM or Ollama).
			chatBE := s.chatBackendFor(model)
			executor.SetLLM(chatBE, model)
			client.ag = agent.New(chatBE, executor, model, curMS, curPersonality)
			client.ag.SetSession(client.sessionID, curPersonality)
			if ragContextFn != nil {
				client.ag.SetRAGContextFn(ragContextFn)
			}
			client.ag.SetSkillsContextFn(executor.SkillsIndex)
			client.ag.SetServicesContextFn(s.servicesContext)
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

	outChars := 0
	for ev := range events {
		client.sendJSON(ev)
		if ev.Type == "stream" {
			outChars += len(ev.Content)
		}

		// After file changes, refresh tree
		if ev.Type == "file_changed" {
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
		}
	}

	// Deterministic end-of-turn marker for non-browser clients (the Vortex voice
	// dock reads this to know Cortex's reply is complete). The browser ignores it.
	client.sendJSON(map[string]interface{}{"type": "turn_complete"})

	// Usage: one chat turn, tokens estimated (chars/4 in+out) until backend
	// counters are wired.
	if ms := s.store(); ms != nil {
		model := modelOverride
		if model == "" && client.ag != nil {
			model = client.ag.Model()
		}
		ms.AddUsage(context.Background(), 0, client.sessionID, "chat_turn", model,
			int64((len(content)+outChars)/4), map[string]interface{}{"origin": "ws"})
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
