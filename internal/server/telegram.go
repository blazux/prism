package server

// Telegram bridge, per-user (Spectrum): every user can connect their own bot
// and chat with their PERSONAL agent from Telegram. Each configured user gets a
// long-poll loop on their token; messages run on the user's dedicated session
// ("u<id>-telegram") with their guard, RAG scope and personal knowledge — the
// same identity as their browser chat. Security: each bot is locked to a single
// chat — the first /start claims it (trust on first use).
//
// Config lives in the user's scope ("u<id>:telegram_bot_token" secret,
// "u<id>:telegram_allowed_chat"). A legacy un-scoped token (no-DB single-user
// deployments) still runs the old global loop on the "telegram" session.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/memory"
)

const tgTokenSecret = "telegram_bot_token"
const tgAllowedChatKey = "telegram_allowed_chat"

// tgScope returns the store view for one user's telegram config.
func (s *Server) tgScope(userID int64) *memory.Store {
	ms := s.store()
	if ms == nil || userID <= 0 {
		return ms
	}
	return ms.ConfigScope(fmt.Sprintf("u%d", userID))
}

func tgToken(ms *memory.Store) string {
	if ms == nil {
		return ""
	}
	tok, _, _ := ms.GetSecret(context.Background(), tgTokenSecret)
	return strings.TrimSpace(tok)
}

func tgAllowedChat(ms *memory.Store) (int64, bool) {
	if ms == nil {
		return 0, false
	}
	v, ok, _ := ms.GetConfig(context.Background(), tgAllowedChatKey)
	if !ok || v == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// ─── manager: one loop per configured user ──────────────────────────────────────

type telegramManager struct{ s *Server }

func (m *telegramManager) Name() string { return "telegram" }

// usersWithToken lists the ids of users who connected their own bot.
func (m *telegramManager) usersWithToken() []int64 {
	ms := m.s.store()
	if ms == nil {
		return nil
	}
	users, err := ms.ListUsers(context.Background())
	if err != nil {
		return nil
	}
	var out []int64
	for _, u := range users {
		if tgToken(m.s.tgScope(u.ID)) != "" {
			out = append(out, u.ID)
		}
	}
	return out
}

func (m *telegramManager) Configured() bool {
	return len(m.usersWithToken()) > 0 || tgToken(m.s.store()) != ""
}

func (m *telegramManager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, uid := range m.usersWithToken() {
		wg.Add(1)
		go func(uid int64) {
			defer wg.Done()
			log.Printf("[telegram-u%d] bot started", uid)
			m.s.telegramLoop(ctx, tgToken(m.s.tgScope(uid)), uid)
		}(uid)
	}
	// Legacy un-scoped token (no-DB / pre-multi-user): the old global loop.
	if tok := tgToken(m.s.store()); tok != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("[telegram] legacy bridge started")
			m.s.telegramLoop(ctx, tok, 0)
		}()
	}
	wg.Wait()
}

// SendToOwner is the legacy single-owner delivery: the global token if set,
// else the only configured user. Ambiguous with several users — use SendToUser.
func (m *telegramManager) SendToOwner(text string) error {
	if tok := tgToken(m.s.store()); tok != "" {
		if chatID, ok := tgAllowedChat(m.s.store()); ok {
			m.s.tgSend("https://api.telegram.org/bot"+tok, chatID, text)
			return nil
		}
	}
	uids := m.usersWithToken()
	if len(uids) == 1 {
		return m.SendToUser(uids[0], text)
	}
	return fmt.Errorf("telegram delivery needs a user context (several bots configured)")
}

// SendToUser delivers to one user's linked chat.
func (m *telegramManager) SendToUser(userID int64, text string) error {
	ms := m.s.tgScope(userID)
	tok := tgToken(ms)
	if tok == "" {
		return fmt.Errorf("telegram not configured for this user")
	}
	chatID, ok := tgAllowedChat(ms)
	if !ok {
		return fmt.Errorf("no linked telegram chat — send /start to your bot first")
	}
	m.s.tgSend("https://api.telegram.org/bot"+tok, chatID, text)
	return nil
}

// ─── receive loop ─────────────────────────────────────────────────────────────

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text     string `json:"text"`
		Caption  string `json:"caption"` // text sent alongside a file
		Document *struct {
			FileID   string `json:"file_id"`
			FileName string `json:"file_name"`
		} `json:"document"`
		Photo []struct {
			FileID string `json:"file_id"`
		} `json:"photo"` // sizes, smallest→largest
	} `json:"message"`
}

// telegramLoop long-polls one bot. userID > 0 routes to that user's personal
// agent; 0 is the legacy single-owner mode (global config, "telegram" session).
func (s *Server) telegramLoop(ctx context.Context, token string, userID int64) {
	api := "https://api.telegram.org/bot" + token
	client := &http.Client{Timeout: 70 * time.Second}
	tag := "telegram"
	if userID > 0 {
		tag = fmt.Sprintf("telegram-u%d", userID)
	}
	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}
		u := fmt.Sprintf("%s/getUpdates?timeout=60&offset=%d", api, offset)
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[%s] getUpdates: %v", tag, err)
			time.Sleep(5 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			OK     bool       `json:"ok"`
			Result []tgUpdate `json:"result"`
		}
		if err := json.Unmarshal(body, &out); err != nil || !out.OK {
			log.Printf("[%s] bad response: %.200s", tag, string(body))
			time.Sleep(5 * time.Second)
			continue
		}
		for _, up := range out.Result {
			offset = up.UpdateID + 1
			if up.Message == nil {
				continue
			}
			m := up.Message
			// A file message carries its text in `caption`; text-only in `text`.
			text := strings.TrimSpace(m.Text)
			if text == "" {
				text = strings.TrimSpace(m.Caption)
			}
			// Collect attachment file_ids: a document, or the largest photo size.
			var fileIDs, names []string
			if m.Document != nil && m.Document.FileID != "" {
				fileIDs = append(fileIDs, m.Document.FileID)
				names = append(names, m.Document.FileName)
			}
			if n := len(m.Photo); n > 0 {
				fileIDs = append(fileIDs, m.Photo[n-1].FileID)
				names = append(names, "photo.jpg")
			}
			if len(fileIDs) > 0 {
				text = s.attachTelegramFiles(ctx, token, fileIDs, names, text)
			}
			if text == "" {
				continue
			}
			s.handleTelegramMessage(ctx, api, userID, m.Chat.ID, text)
		}
	}
}

func (s *Server) handleTelegramMessage(ctx context.Context, api string, userID, chatID int64, text string) {
	var ms *memory.Store
	if userID > 0 {
		ms = s.tgScope(userID)
	} else {
		ms = s.store()
	}
	if ms == nil {
		return
	}

	allowed, hasOwner := tgAllowedChat(ms)
	if !hasOwner {
		// Trust on first use: the first /start links this chat.
		if strings.HasPrefix(text, "/start") {
			ms.SetConfig(context.Background(), tgAllowedChatKey, strconv.FormatInt(chatID, 10))
			s.tgSend(api, chatID, "✅ This chat is now linked to your Prism agent. Send me anything.")
		} else {
			s.tgSend(api, chatID, "Send /start to link this chat to your Prism agent.")
		}
		return
	}
	if chatID != allowed {
		s.tgSend(api, chatID, "⛔ This Prism agent is linked to another chat.")
		return
	}
	if text == "/start" {
		s.tgSend(api, chatID, "Already linked — just send me a message.")
		return
	}

	// Per-user: run on the user's own telegram session with their permissions and
	// scopes — same identity as their browser chat, so their agent, their RAG,
	// their knowledge. Legacy (userID 0): the old global "telegram" session.
	session := "telegram"
	var guard func(string, map[string]interface{}) error
	ragScope := ""
	if userID > 0 {
		session = fmt.Sprintf("u%d-telegram", userID)
		if u, err := s.store().GetUserByID(ctx, userID); err == nil && u != nil {
			guard = s.buildUserGuard(ctx, u)
			ragScope = s.ragScopeFor(ctx, u)
		}
	}

	s.tgAction(api, chatID, "typing")
	s.store().AddUsage(ctx, userID, session, "channel_msg", "telegram", 1, nil)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := s.runHeadlessChat(runCtx, session, text, "", guard, ragScope)
	if err != nil {
		log.Printf("[telegram] chat: %v", err)
		s.tgSend(api, chatID, "⚠️ Sorry, something went wrong.")
		return
	}
	if strings.TrimSpace(resp) == "" {
		resp = "(no response)"
	}
	s.tgSend(api, chatID, resp)
}

// tgSend posts a message, splitting on Telegram's ~4096-char limit (by rune).
func (s *Server) tgSend(api string, chatID int64, text string) {
	runes := []rune(text)
	const max = 3500
	for len(runes) > 0 {
		n := len(runes)
		if n > max {
			n = max
		}
		form := url.Values{}
		form.Set("chat_id", strconv.FormatInt(chatID, 10))
		form.Set("text", string(runes[:n]))
		runes = runes[n:]
		if resp, err := http.PostForm(api+"/sendMessage", form); err == nil {
			resp.Body.Close()
		}
	}
}

func (s *Server) tgAction(api string, chatID int64, action string) {
	form := url.Values{}
	form.Set("chat_id", strconv.FormatInt(chatID, 10))
	form.Set("action", action)
	if resp, err := http.PostForm(api+"/sendChatAction", form); err == nil {
		resp.Body.Close()
	}
}

// POST /api/telegram/send {text} — deliver a message to the caller's linked chat.
func (s *Server) handleTelegramSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	var b struct {
		Text    string `json:"text"`
		Session string `json:"session"` // optional user hint for service calls (cron)
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Text) == "" {
		http.Error(w, "text required", 400)
		return
	}
	m, _ := s.channels["telegram"].(*telegramManager)
	if m == nil {
		http.Error(w, "telegram unavailable", 503)
		return
	}
	var err error
	if u := currentUser(r); u != nil && u.ID > 0 {
		err = m.SendToUser(u.ID, b.Text)
	} else if uid := userIDFromSession(b.Session); uid > 0 {
		err = m.SendToUser(uid, b.Text)
	} else {
		err = m.SendToOwner(b.Text)
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// userIDFromSession extracts the owner id from a namespaced session ("u3-…").
func userIDFromSession(session string) int64 {
	if !strings.HasPrefix(session, "u") {
		return 0
	}
	rest := session[1:]
	i := strings.IndexByte(rest, '-')
	if i <= 0 {
		return 0
	}
	id, err := strconv.ParseInt(rest[:i], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// GET /api/telegram/config -> {configured, linked} for the CALLER. POST {token}
// to connect their own bot (resets the linked chat), or {unlink:true}.
func (s *Server) handleTelegramConfig(w http.ResponseWriter, r *http.Request) {
	ms := s.userStore(r)
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		_, linked := tgAllowedChat(ms)
		writeJSON(w, map[string]interface{}{
			"configured": tgToken(ms) != "",
			"linked":     linked,
		})
	case "POST":
		var b struct {
			Token  string `json:"token"`
			Unlink bool   `json:"unlink"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if b.Unlink {
			ms.SetConfig(r.Context(), tgAllowedChatKey, "")
			writeJSON(w, map[string]interface{}{"ok": true})
			return
		}
		if err := ms.SetSecret(r.Context(), tgTokenSecret, strings.TrimSpace(b.Token)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// New token = new bot → forget the previously linked chat.
		ms.SetConfig(r.Context(), tgAllowedChatKey, "")
		s.startChannels()
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// attachTelegramFiles resolves each file_id to a download URL (getFile →
// file_path), fetches it, ingests it into the workspace, and prepends its
// preamble to the message text.
func (s *Server) attachTelegramFiles(ctx context.Context, token string, fileIDs, names []string, text string) string {
	api := "https://api.telegram.org/bot" + token
	var atts []attachment
	for i, id := range fileIDs {
		// getFile returns a file_path valid for the file/ download endpoint.
		resp, err := http.PostForm(api+"/getFile", url.Values{"file_id": {id}})
		if err != nil {
			log.Printf("[telegram] getFile: %v", err)
			continue
		}
		var gf struct {
			OK     bool `json:"ok"`
			Result struct {
				FilePath string `json:"file_path"`
			} `json:"result"`
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if json.Unmarshal(body, &gf) != nil || !gf.OK || gf.Result.FilePath == "" {
			log.Printf("[telegram] getFile: bad response for %s", id)
			continue
		}
		name := names[i]
		if name == "" {
			name = filepath.Base(gf.Result.FilePath)
		}
		dlURL := "https://api.telegram.org/file/bot" + token + "/" + gf.Result.FilePath
		data, fname, err := downloadAttachment(ctx, dlURL, "", name)
		if err != nil {
			log.Printf("[telegram] download %s: %v", name, err)
			continue
		}
		atts = append(atts, s.ingestAttachment(data, fname))
	}
	return prependAttachments(text, atts)
}
