package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"prism/internal/workspace"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"

	"github.com/gorilla/websocket"
)

type Client struct {
	turnDisabled []string

	voice           bool
	historyStale    bool
	pendingSecretID string
	approvals       map[string]chan bool

	runMu        sync.RWMutex
	run          *chatRun
	disconnected bool
	transportMu  sync.RWMutex

	editorPending     map[string]chan json.RawMessage
	editorSequence    uint64
	conn              *websocket.Conn
	send              chan []byte
	ag                *agent.Agent
	cancelFn          context.CancelFunc
	mu                sync.Mutex
	sessionID         string
	user              *memory.User // authenticated user for this connection (nil in legacy/no-DB mode)
	lastNotifID       int64        // last notification ID pushed to this client
	turnDone          chan struct{}
	pendingSecretCh   chan string // non-nil while agent is waiting for secret input
	approvalManual    bool        // manual tool approval toggle (approval_mode message)
	pendingApproval   chan bool   // non-nil while agent is waiting for a tool verdict
	pendingApprovalID string      // tool ID the pending verdict belongs to
	viewContext       string      // what the user is currently looking at (UI -> agent)
}

// wsFileOpsAllowed mirrors requireAdminUser for the editor's WebSocket file
// commands. /api/files and /api/file are admin-only because they expose the
// whole shared workspace (including .secret_key); the same three operations
// over /ws were not gated, which made the REST gate theatre.
func (s *Server) wsFileOpsAllowed(c *Client) bool {
	return c.user == nil || s.isAdminUser(context.Background(), c.user)
}

// wireApproval connects an agent to this client's manual-approval toggle: the
// mode is re-read at every tool call, and the verdict travels back over the
// same WS as an "approval_response" message (see the read pump).
func (c *Client) wireApproval(ag *agent.Agent) { c.wireApprovalPrefix(ag, "") }
func (c *Client) wireApprovalPrefix(ag *agent.Agent, prefix string) {
	ag.SetApprovalFns(
		func() bool {
			if run := c.currentRun(); run != nil {
				return run.approvalManual
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.approvalManual
		},
		func(ctx context.Context, toolID string) bool {
			toolID = prefix + toolID
			c.mu.Lock()
			ch := c.approvals[toolID]
			c.mu.Unlock()
			defer func() {
				c.mu.Lock()
				delete(c.approvals, toolID)
				if c.pendingApproval == ch {
					c.pendingApproval = nil
					c.pendingApprovalID = ""
				}
				c.mu.Unlock()
			}()
			select {
			case ok := <-ch:
				return ok
			case <-ctx.Done():
				return false
			}
		},
		func(toolID string) {
			toolID = prefix + toolID
			ch := make(chan bool, 1)
			c.mu.Lock()
			if c.approvals == nil {
				c.approvals = make(map[string]chan bool)
			}
			c.approvals[toolID] = ch
			c.pendingApproval = ch
			c.pendingApprovalID = toolID
			c.mu.Unlock()
		},
	)
}

// cancelActive cancels the in-flight agent turn (if any) under the client mutex.
func (c *Client) cancelActive() {
	c.mu.Lock()
	cancel, done := c.cancelFn, c.turnDone
	c.cancelFn = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Drain the old turn (including outgoing events and persistence) before a
	// reset or a new turn can reuse this agent.
	if done != nil {
		<-done
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
	// dashboard; "voice" = a phone call docked from Prism Vox, which
	// makes the agent answer in spoken form and skip extended reasoning.
	Channel        string          `json:"channel,omitempty"`
	Content        string          `json:"content,omitempty"`
	GatewayContext []string        `json:"gateway_context,omitempty"`
	Path           string          `json:"path,omitempty"`
	ID             string          `json:"id,omitempty"`
	Locked         bool            `json:"locked,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	Model          string          `json:"model,omitempty"`
	DisabledTools  []string        `json:"disabledTools,omitempty"`
	Images         []string        `json:"images,omitempty"` // base64 image strings for multimodal
	Files          []ChatFile      `json:"files,omitempty"`  // parsed text file attachments
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
	// Bound a complete message, including fragmented frames and base64 images.
	conn.SetReadLimit(16 << 20)
	stopOnCancel := context.AfterFunc(r.Context(), func() { _ = conn.Close() })
	defer stopOnCancel()

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
	s.mkdirManaged(sessionPluginDir)

	ai, err := s.aiConfigFor(r.Context(), requestUserID(r))
	if err != nil {
		conn.WriteJSON(map[string]any{"type": "error", "content": err.Error()})
		conn.Close()
		return
	}
	ollamaClient := ai.newChatBackend()

	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.selfCallToken(sessionID))
	executor.SetLLM(ollamaClient, ai.cfg.Model)
	executor.SetChatBlind(!ai.cfg.ChatVision)
	executor.SetVox(s.cfg.VoxURL, s.cfg.VoxUser, s.cfg.VoxPassword) // enables place_call when docked
	executor.SetRAGProvider(s.acquireRAG)
	executor.SetSessionID(sessionID)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	wsUser := currentUser(r)

	// A phone call docks through Prism Vox with the service token,
	// which auth.go resolves to a GLOBAL ADMIN. Re-identify it, or a stranger who
	// dials the number gets an admin agent — exec_command, docker, mail, secrets,
	// and the owner's whole knowledge base. Until the caller is identified, a call
	// is a guest. See voice.go.
	voiceCall := r.URL.Query().Get("channel") == voiceChannelName && isServiceIdentity(wsUser)

	// A known caller (their number is on their profile) is identified: they get
	// THEIR memory and knowledge — inter-channel continuity — but still only the
	// voice-safe tools. An unknown caller stays a public switchboard guest.
	var voiceUser *memory.User
	var voiceDir []memory.DirEntry // the phone directory = Prism user profiles
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

	// Every branch above configured scope/guard; the help tool is the same for
	// all of them and must not depend on which branch ran.
	executor.SetHelp(s.helpFn(), s.integrationsStatusFor(wsUser))
	executor.SetGlobalAdmin(wsUser != nil && wsUser.IsGlobalAdmin())
	executor.SetRAGReadOnly(!s.canManageRAGScope(r.Context(), wsUser))
	if !voiceCall {
		executor.SetServerTools(s.serverToolsFor(wsUser))
	}
	ragContextFn := s.ragContextFn(ragScope)

	client := &Client{
		voice: voiceCall,
		conn:  conn,
		send:  make(chan []byte, 256),
		user:  wsUser,
	}

	if !voiceCall {
		executor.SetEditor(client.editorTool)
	}

	if !voiceCall {
		tools := s.serverToolsFor(wsUser)
		if tools == nil {
			tools = make(map[string]agent.ServerTool)
		}
		tools["subagent"] = s.subagentTool(client, executor)
		executor.SetServerTools(tools)
	}
	model := ai.cfg.Model

	// Personality per identity:
	//  - voice guest → the switchboard persona (never the owner's assistant, which
	//    would introduce itself to a stranger as managing their mail/servers);
	//  - identified voice caller → THEIR own personality + a note to greet them by
	//    name and resume the conversation (continuity);
	//  - dashboard → this session's personality.
	personality := loadPersonality(r.Context(), ms, sessionID)
	if voiceCall && voiceUser != nil {
		// Their own personality is deliberately NOT used here — see
		// voiceInternalPersonality. Their memory, profile and knowledge still are:
		// that is where the continuity actually lives.
		personality = voiceKnownPersonaNote(voiceUser.DisplayName) +
			s.voiceInternalPersonality(r.Context(), voiceUser)
	} else if voiceCall {
		personality = s.voicePersonality(r.Context())
	}
	if voiceCall {
		// Give the agent the live directory (Prism profiles) so it only offers to
		// transfer to real people; the relay resolves the chosen name to a number.
		personality += voiceDirectoryText(voiceDir)
	}

	client.ag = agent.New(ollamaClient, executor, model, ms, personality)
	client.ag.SetChatBlind(!ai.cfg.ChatVision)
	client.wireApproval(client.ag)
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
		client.pendingSecretID = fmt.Sprintf("secret-%d", time.Now().UnixNano())
		secretID := client.pendingSecretID
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
			"id":          secretID,
			"name":        name,
			"description": description,
		})

		select {
		case val := <-ch:
			if val == "" {
				return fmt.Errorf("secret input cancelled by user")
			}
			return s.saveRequestedSecret(ctx, executor.SecretsScope(), name, val)

		case <-ctx.Done():
			return ctx.Err()
		}
	})

	// Wire plugin/file callbacks to WebSocket events
	executor.SetProgressFn(func(text string) {
		client.sendJSON(map[string]interface{}{"type": "progress", "content": text})
	})

	// On a voice call, expose the telephony tools and relay their calls to
	// Vox over this WS. Vox performs the transfer/message/hang-up with its ARI/SIP
	// machinery, exactly as it does for its own agent.
	if voiceCall {
		executor.SetTelephony(agent.TelephonyTools, func(name string, args map[string]interface{}) (string, error) {
			// transfer_call resolves against the Prism directory (user profiles), not
			// a separate contacts table. Resolve here, pass Vox a pre-resolved number.
			if name == "transfer_call" {
				dest, _ := args["destination"].(string)
				entry, ok := resolveTransferName(voiceDir, dest)
				if !ok {
					// Not in the directory → don't dial; let the agent offer a near
					// match from the list or take a message.
					return fmt.Sprintf("Aucune correspondance unique pour %q : nom absent ou ambigu. Aucun transfert demandé. Demande le nom complet et utilise l'annuaire pour proposer les personnes possibles.", dest), nil
				}
				args["destination"] = entry.Name
				args["dial_number"] = entry.Phone // pre-resolved for Vox
				// How the recipient wants to be reached. Vox holds the announcement
				// machinery; without this it would have to guess, and guessing means
				// putting a stranger straight onto someone's mobile.
				args["transfer_type"] = entry.Transfer
			}
			client.sendJSON(map[string]interface{}{"type": "telephony", "tool": name, "args": args})
			log.Printf("[voice] relaying telephony tool %q to Vox: %v", name, args)
			switch name {
			case "transfer_call":
				return "Demande de transfert transmise à la passerelle. Son résultat n'est pas encore connu ; ne prétends pas que la personne a répondu.", nil
			case "take_message":
				return "Demande de prise de message transmise à la passerelle. Ne promets pas de livraison au destinataire.", nil
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
			content, err := workspace.ReadFile(s.cfg.WorkspaceDir, path)
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
		if voiceCall {
			client.cancelActive()
		} else {
			s.detachRun(client)
		}
	}()

	// Send initial state
	status := s.docker.WorkspaceStatus(r.Context())
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
	if voiceCall {
		s.sendHistory(r.Context(), client, sessionID)
	} else {
		s.attachRun(client)
	}

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
					notifs, err := ms.GetNotificationsAfter(r.Context(), sessionID, lastID)
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
			// Voice stops on hang-up; dashboard execution belongs to the server.
			if voiceCall {
				client.cancelActive()
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			continue
		}

		if !voiceCall && s.routeRunControl(client, msg) {
			continue
		}
		switch msg.Type {
		case "chat":
			client.mu.Lock()
			stale := client.historyStale
			client.historyStale = false
			client.mu.Unlock()
			if stale {
				client.ag.ReloadStoredHistory()
			}
			if !voiceCall && s.runActive(client) {
				client.sendJSON(map[string]any{"type": "error", "content": "This conversation already has a running task. Stop it before starting another."})
				continue
			}
			client.cancelActive()
			nextAI, err := s.aiConfigFor(context.Background(), requestUserID(r))
			if err != nil {
				client.sendJSON(map[string]any{"type": "error", "content": err.Error()})
				continue
			}
			if !sameAIConfig(ai.cfg, nextAI.cfg) {
				ai = nextAI
				executor.SetChatBlind(!ai.cfg.ChatVision)
				client.ag.SetChatBlind(!ai.cfg.ChatVision)
				model = ai.cfg.Model
				be := ai.newChatBackend()
				executor.SetLLM(be, model)
				client.ag.SetBackend(be, model)
				client.sendJSON(map[string]any{"type": "model_set", "model": model})
			}
			if !s.userCanUseModel(context.Background(), client.user, model) {
				client.sendJSON(map[string]any{"type": "error", "content": "model not allowed"})
				continue
			}

			parent := r.Context()
			if !voiceCall {
				parent = s.runtimeContext()
			}
			ctx, cancel := context.WithTimeout(parent, 2*time.Hour)
			client.mu.Lock()
			// Cancel previous if running
			if client.cancelFn != nil {
				client.cancelFn()
			}
			client.cancelFn = cancel
			turnDone := make(chan struct{})
			client.turnDone = turnDone
			client.mu.Unlock()

			client.turnDisabled = append([]string(nil), msg.DisabledTools...)
			client.ag.SetActiveTools(msg.DisabledTools)
			client.ag.SetChannel(msg.Channel)
			content := voiceTurnContent(msg.Content, msg.GatewayContext, voiceCall)
			for _, f := range msg.Files {
				// The browser already ran ingestAttachment via /api/chat/upload and
				// sent back {Text, Path}; here we only build the preamble.
				content = attachmentPreamble(attachment{Name: f.Name, Text: f.Text, Path: f.Path}) + "\n\n" + content
			}
			if voiceCall {
				go func() {
					defer close(turnDone)
					defer cancel()
					s.handleChat(ctx, client, content, msg.Images, msg.Model)
				}()
			} else {
				run, err := s.beginRun(client, cancel, content, msg.Images)
				if err != nil {
					cancel()
					close(turnDone)
					client.sendJSON(map[string]any{"type": "error", "content": err.Error()})
					continue
				}
				if !s.background(func() {
					defer close(turnDone)
					defer cancel()
					defer s.finishRun(run, ctx)
					s.handleChat(ctx, client, content, msg.Images, msg.Model)
				}) {
					cancel()
					close(turnDone)
					s.finishRun(run, ctx)
				}
			}

		case "cancel":
			if voiceCall {
				client.cancelActive()
			} else {
				s.cancelRun(client)
			}

		case "file_open":
			if !s.wsFileOpsAllowed(client) {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "forbidden: administrators only"})
				continue
			}
			_, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "invalid path"})
				continue
			}
			content, err := workspace.ReadFile(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{
					"type": "file_content", "path": msg.Path, "content": string(content),
				})
			}

		case "file_save":
			if !s.wsFileOpsAllowed(client) {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "forbidden: administrators only"})
				continue
			}
			_, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "invalid path"})
				continue
			}
			var payload struct {
				Content string `json:"content"`
			}
			json.Unmarshal(msg.Data, &payload)
			if err := workspace.WriteFile(s.cfg.WorkspaceDir, msg.Path, []byte(payload.Content)); err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
			} else {
				client.sendJSON(map[string]interface{}{"type": "saved", "path": msg.Path})
				tree := s.buildFileTree(s.cfg.WorkspaceDir)
				client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})
			}

		case "file_delete":
			if !s.wsFileOpsAllowed(client) {
				client.sendJSON(map[string]interface{}{"type": "error", "content": "forbidden: administrators only"})
				continue
			}
			_, err := safeWorkspacePath(s.cfg.WorkspaceDir, msg.Path)
			if err != nil {
				continue
			}
			workspace.Remove(s.cfg.WorkspaceDir, msg.Path)
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "refresh_files":
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSON(map[string]interface{}{"type": "file_tree", "files": tree})

		case "remove_plugin":
			if msg.ID != "" {
				if err := s.removeManaged(filepath.Join(sessionPluginDir, msg.ID+".html")); err != nil && !os.IsNotExist(err) {
					log.Printf("[remove_plugin] delete html %s: %v", msg.ID, err)
				}
				if err := s.removeManaged(filepath.Join(sessionPluginDir, msg.ID+".meta.json")); err != nil && !os.IsNotExist(err) {
					log.Printf("[remove_plugin] delete meta %s: %v", msg.ID, err)
				}
				client.sendJSON(map[string]interface{}{"type": "plugin_unload", "id": msg.ID})
				client.ag.InjectNote("[Dashboard] The user removed widget '" + msg.ID + "' from the dashboard. It no longer exists.")
			}

		case "lock_plugin":
			if msg.ID != "" {
				metaPath := filepath.Join(sessionPluginDir, msg.ID+".meta.json")
				s.updatePluginMeta(metaPath, func(m map[string]any) {
					m["locked"] = msg.Locked
				})
			}

		// set_context records what the user is currently viewing so the agent
		// can resolve "this email/note/event" on the next turn.
		case "editor_response":
			client.editorResponse(msg.ID, msg.Data)
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
					s.updatePluginMeta(metaPath, func(m map[string]any) {
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
			if !voiceCall {
				s.cancelRun(client)
			}
			client.cancelActive()
			if err := client.ag.ResetHistory(); err != nil {
				client.sendJSON(map[string]interface{}{"type": "error", "content": err.Error()})
				continue
			}
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

		case "approval_mode":
			client.mu.Lock()
			client.approvalManual = msg.Content == "manual"
			client.mu.Unlock()

		case "approval_response":
			client.mu.Lock()
			ch := client.pendingApproval
			// Ignore a stale verdict (e.g. for a call the cancel already killed).
			if msg.ID != "" && msg.ID != client.pendingApprovalID {
				ch = nil
			} else {
				client.pendingApproval = nil
				client.pendingApprovalID = ""
			}
			client.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg.Content == "approve":
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
			if !voiceCall && s.runActive(client) {
				client.sendJSON(map[string]any{"type": "error", "content": "Stop the running task before changing its model."})
				continue
			}
			client.cancelActive()
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
			chatBE := ai.chatBackendFor(model)
			executor.SetLLM(chatBE, model)
			client.ag = agent.New(chatBE, executor, model, curMS, curPersonality)
			client.ag.SetChatBlind(!ai.cfg.ChatVision)
			client.wireApproval(client.ag)
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
		// "stream" deltas are high-frequency and individually disposable — a
		// dropped one loses a few characters. Everything else carries UI STATE
		// (tool_use/tool_result, plugin add/remove, attachment, stream_end,
		// error…): dropping one strands the UI — e.g. a tool_result lost to a
		// full send buffer leaves its "Running…" spinner up forever, the exact
		// "I never see tool results" bug. So deliver those reliably.
		if ev.Type == "stream" {
			client.sendJSON(ev)
			outChars += len(ev.Content)
		} else {
			client.sendJSONReliable(ev)
		}

		// After file changes, refresh tree
		if ev.Type == "file_changed" {
			tree := s.buildFileTree(s.cfg.WorkspaceDir)
			client.sendJSONReliable(map[string]interface{}{"type": "file_tree", "files": tree})
		}
	}

	// Deterministic end-of-turn marker for non-browser clients (the voice
	// dock reads this to know Prism's reply is complete). The browser ignores it.
	client.sendJSONReliable(map[string]interface{}{"type": "turn_complete"})

	// Legacy chat-turn estimate for existing dashboards. Real main-chat provider
	// usage is stored separately as model_request; never add the two quantities.
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
	if c.publishRun(v) {
		return
	}
	if c.isDisconnected() {
		return
	}
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

// sendJSONReliable is sendJSON for messages that carry UI state rather than a
// disposable stream delta: it applies backpressure (blocks until the writePump
// drains the buffer) instead of dropping on a transient burst — a burst of
// large tool_result payloads must not strand the UI. It still bounds the wait so
// a genuinely dead/stuck client can't hang the turn forever; the write deadline
// in writePump tears such a connection down anyway.
func (c *Client) sendJSONReliable(v interface{}) {
	if c.publishRun(v) {
		return
	}
	if c.isDisconnected() {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-time.After(10 * time.Second):
		log.Printf("client send buffer full for 10s — dropping state message (client stuck?)")
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

// Recheck availability when the user submits: the store may have changed while
// the dialog was open. Never acknowledge a credential that was not persisted.
func (s *Server) saveRequestedSecret(ctx context.Context, scope, name, value string) error {
	ms := s.store()
	if ms == nil {
		return fmt.Errorf("secret store unavailable; secret was not saved")
	}
	if err := ms.ConfigScope(scope).SetScriptSecret(ctx, name, value); err != nil {
		return fmt.Errorf("store secret: %w", err)
	}
	return nil
}
