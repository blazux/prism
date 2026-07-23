package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"prism/internal/agent"
)

// handleChatFileUpload parses an uploaded file and returns its text content so
// the frontend can include it in the next chat message.
func (s *Server) handleChatFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "file too large (max 10 MB)", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Extract text if we can, blank it for files the agent must open itself, and
	// save the bytes to the workspace — shared with the chat channels.
	a := s.ingestAttachment(data, header.Filename)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"name": a.Name, "text": a.Text, "path": a.Path})
}

// saveChatUpload copies a chat upload into the workspace uploads/ dir and returns
// its workspace-relative path (empty on failure — extraction-only still works).
func (s *Server) saveChatUpload(srcPath, origName string) string {
	uploadsDir := filepath.Join(s.cfg.WorkspaceDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return ""
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return ""
	}
	dest := uniqueUploadPath(uploadsDir, sanitizeUploadName(origName))
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return ""
	}
	rel, err := filepath.Rel(s.cfg.WorkspaceDir, dest)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func sanitizeUploadName(name string) string {
	name = strings.NewReplacer("/", "-", "\\", "-", "\x00", "").Replace(strings.TrimSpace(filepath.Base(name)))
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	return name
}

func uniqueUploadPath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
}

// ─── REST API ─────────────────────────────────────────────────────────────────

// ─── HTTP Chat API ────────────────────────────────────────────────────────────
//
// POST /api/chat — trigger the agent from a cron script or external tool.
//
//	{"session":"default","message":"Analyse les CVE…","model":"llama3"}
//
// From a cron job, "session" MUST be $PRISM_SESSION (the session the cron was
// created from), not a hardcoded literal — a made-up session id has none of
// that chat's RAG/MCP scope, so the agent silently loses its tools.
//
// The agent runs to completion (max 10 min), saves the exchange to DB history,
// fires any notify calls normally, and returns the final response.
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
		Deliver string `json:"deliver"` // optional: "telegram" or "slack" to push the reply to that channel's owner
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		http.Error(w, "message required", 400)
		return
	}
	if body.Session == "" {
		body.Session = "default"
	}

	sessionID, ok := s.sessionFor(r, body.Session)
	if !ok {
		writeErr(w, http.StatusForbidden, "forbidden session")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	// RBAC: gate tool calls by the caller's permissions and reject a model the
	// caller isn't allowed to use.
	user := currentUser(r)
	if body.Model != "" && !s.userCanUseModel(ctx, user, body.Model) {
		writeErr(w, http.StatusForbidden, "you are not allowed to use model "+body.Model)
		return
	}
	resp, err := s.runHeadlessChat(ctx, sessionID, body.Message, body.Model, s.callerContextForUser(ctx, user, sessionID))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	delivered := false
	if body.Deliver != "" && strings.TrimSpace(resp) != "" {
		if err := s.deliverToChannelSession(body.Deliver, sessionID, resp); err != nil {
			log.Printf("[chat-http] deliver to %s: %v", body.Deliver, err)
		} else {
			delivered = true
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"response":  resp,
		"session":   sessionID,
		"delivered": delivered,
	})
}

// runHeadlessChat runs a single agent turn for a session outside the WebSocket
// (used by /api/chat and the Telegram bridge). sessionID must already be
// sanitized. Returns the assistant's final text (thinking blocks stripped).
//
// cc's guard, if non-nil, authorizes each tool call before it runs — the
// Webex channel passes a per-sender guard so untrusted space members can only
// reach the tools they've been granted. Trusted callers (browser, Telegram,
// /api/chat) get a nil guard (see callerContextForUser) to allow every tool.
func (s *Server) runHeadlessChat(ctx context.Context, sessionID, message, model string, cc CallerContext) (string, error) {
	return s.runHeadlessChatTap(ctx, sessionID, message, model, cc, nil)
}

// runHeadlessChatTap is runHeadlessChat with an optional event tap, so callers
// (the group room) can surface tool activity live while the turn runs.
func (s *Server) runHeadlessChatTap(ctx context.Context, sessionID, message, model string, cc CallerContext, tap func(agent.Event)) (string, error) {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()

	if model == "" {
		model = s.cfg.Model
	}

	sessionPluginDir := filepath.Join(s.cfg.PluginDir, sessionID)
	os.MkdirAll(sessionPluginDir, 0755)

	ollamaClient := s.chatBackendFor(model)
	executor := agent.NewToolExecutor(s.docker, s.cfg.WorkspaceDir, sessionPluginDir, s.cfg.SearxngURL, s.cfg.AuthToken)
	executor.SetLLM(ollamaClient, model)
	executor.SetChatBlind(!s.cfg.ChatVision)
	executor.SetVox(s.cfg.VoxURL, s.cfg.VoxUser, s.cfg.VoxPassword) // enables place_call when docked

	if s.ragStore != nil {
		executor.SetRAG(s.ragStore, s.ragEmbedder, s.ragCaptioner)
	}
	executor.SetSessionID(sessionID)
	executor.SetHeadless(true) // no dashboard: widget tools refuse cleanly
	cc.apply(executor)
	if ms != nil {
		executor.SetMemoryStore(ms)
	}
	// MCP tools must be available in headless mode too (the Webex channel relies
	// on them). The WS path wires this the same way.
	executor.SetMCPManager(s.mcpMgr, func() { s.broadcastMCP(sessionID) })
	executor.SetCustomTools(s.customMgr, func() {
		s.customMgr.Reload()
		s.broadcastTools()
	})

	// Notification: insert to DB + push to any live WS client for this session
	if ms != nil {
		executor.SetNotificationCallback(func(title, msg, level string) {
			id, err := ms.AddNotification(context.Background(), sessionID, title, msg, level)
			if err != nil {
				log.Printf("[chat-headless] notification: %v", err)
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
		return fmt.Errorf("request_secret unavailable in headless mode")
	})

	personality := loadPersonality(ctx, ms, sessionID)

	ag := agent.New(ollamaClient, executor, model, ms, personality)
	ag.SetSession(sessionID, personality)

	if s.ragStore != nil {
		ragStore := s.ragStore
		ag.SetRAGContextFn(func() string {
			if s.ragPersonalFallbackBlocked(cc.RAGScope) {
				return ""
			}
			cols, err := ragStore.ListCollections(context.Background(), cc.RAGScope)
			if err != nil || len(cols) == 0 {
				return ""
			}
			var sb strings.Builder
			sb.WriteString("## Knowledge Base (RAG)\n\nYou have access to document collections via `rag_search`.\n\n")
			for _, c := range cols {
				name := unscopeCollection(cc.RAGScope, c.Name)
				if c.Description != "" {
					fmt.Fprintf(&sb, "- **%s** — %s (%d docs)\n", name, c.Description, c.DocCount)
				} else {
					fmt.Fprintf(&sb, "- **%s** (%d docs, %d chunks)\n", name, c.DocCount, c.ChunkCount)
				}
			}
			return sb.String()
		})
	}
	ag.SetSkillsContextFn(executor.SkillsIndex)
	ag.SetUserProfileFn(func() string {
		return executor.GetUserProfile(context.Background())
	})
	ag.SetLearningsCtxFn(func(ctx context.Context, query string) string {
		return executor.SearchLearnings(ctx, query)
	})

	events := make(chan agent.Event, 200)
	go func() {
		ag.Chat(ctx, message, nil, events)
		close(events)
	}()

	// Collect only the FINAL assistant message. Reset on each tool call so the
	// step-by-step planning narration the agent emits between tools doesn't get
	// concatenated into one delivered message (e.g. a Telegram reply). Also skip
	// <think>…</think> blocks.
	var response strings.Builder
	inThink := false
	for ev := range events {
		if tap != nil {
			tap(ev)
		}
		switch ev.Type {
		case "error":
			// Otherwise silent: headless only collects "tool_use"/"stream" events,
			// so a backend error (bad status, stream parse failure) previously
			// vanished with no trace — the caller just saw an empty response.
			log.Printf("[chat-headless] session=%q agent error: %s", sessionID, ev.Content)
		case "tool_use":
			response.Reset()
			inThink = false
		case "stream":
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

	log.Printf("[chat-headless] session=%q response=%d chars", sessionID, response.Len())
	// Usage: one chat turn, tokens estimated (chars/4 in+out) until backend
	// counters are wired.
	if ms != nil {
		ms.AddUsage(context.Background(), 0, sessionID, "chat_turn", model,
			int64((len(message)+response.Len())/4), map[string]interface{}{"origin": "headless"})
	}
	return strings.TrimSpace(response.String()), nil
}

// looksBinary reports whether extracted "text" is really raw bytes. rag.ParseFile
// falls back to reading a file whole for any extension it doesn't handle, so a
// pcap, a zip or an executable arrives here as a string of noise.
//
// A NUL byte is the giveaway — no text format we accept contains one — and
// invalid UTF-8 catches the rest. Only the head is examined: a 100 MB capture
// should not be scanned to learn what its first bytes already say.
func looksBinary(text string) bool {
	head := text
	if len(head) > 8192 {
		head = head[:8192]
	}
	if strings.ContainsRune(head, 0) {
		return true
	}
	// A multi-byte rune can straddle the cut, so only trust invalid UTF-8 when the
	// text is short enough to be read whole.
	return len(text) <= 8192 && !utf8.ValidString(text)
}
