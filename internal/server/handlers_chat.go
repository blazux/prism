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

	"prism/internal/agent"
	"prism/internal/rag"
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

	ext := strings.ToLower(filepath.Ext(header.Filename))
	tmp, err := os.CreateTemp("", "prism-chat-upload-*"+ext)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tmp.Close()

	// Primary path: extract the text and inline it (works well for most docs).
	text, err := rag.ParseFile(tmp.Name())
	if err != nil {
		text = ""
	}

	// Guard against enormous files that would blow out the model context window.
	const maxChars = 100_000
	if len(text) > maxChars {
		text = text[:maxChars] + "\n[... truncated at 100 000 characters ...]"
	}

	// Fallback only: when nothing could be extracted (scanned/image PDF,
	// unsupported type), persist the file into the workspace so the agent can
	// read/OCR/parse the real file itself.
	var savedRel string
	if strings.TrimSpace(text) == "" {
		savedRel = s.saveChatUpload(tmp.Name(), header.Filename)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"name": header.Filename, "text": text, "path": savedRel})
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

	sessionID := sanitizeSessionID(body.Session)
	if sessionID == "" {
		sessionID = "default"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	resp, err := s.runHeadlessChat(ctx, sessionID, body.Message, body.Model)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	delivered := false
	if body.Deliver != "" && strings.TrimSpace(resp) != "" {
		if err := s.deliverToChannel(body.Deliver, resp); err != nil {
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
func (s *Server) runHeadlessChat(ctx context.Context, sessionID, message, model string) (string, error) {
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
			cols, err := ragStore.ListCollections(context.Background(), sessionID)
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
		switch ev.Type {
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
	return strings.TrimSpace(response.String()), nil
}
