package server

// Telegram bridge: lets the owner chat with their Prism agent from Telegram.
// A background goroutine long-polls getUpdates and routes each message through
// the same headless agent run as /api/chat (session "telegram"). Security: the
// bot is locked to a single chat — the first /start claims it (trust on first
// use); messages from any other chat are refused. The agent has powerful tools,
// so this single-owner lock is essential.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const tgTokenSecret = "telegram_bot_token"
const tgAllowedChatKey = "telegram_allowed_chat"

func (s *Server) telegramToken() string {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		return ""
	}
	tok, _, _ := ms.GetSecret(context.Background(), tgTokenSecret)
	return strings.TrimSpace(tok)
}

func (s *Server) telegramAllowedChat() (int64, bool) {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
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

// startTelegram (re)starts the poller if a bot token is configured.
func (s *Server) startTelegram() {
	s.stopTelegram()
	token := s.telegramToken()
	if token == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.tgCancel = cancel
	s.mu.Unlock()
	go s.telegramLoop(ctx, token)
	log.Printf("[telegram] bridge started")
}

func (s *Server) stopTelegram() {
	s.mu.Lock()
	c := s.tgCancel
	s.tgCancel = nil
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

func (s *Server) telegramLoop(ctx context.Context, token string) {
	api := "https://api.telegram.org/bot" + token
	client := &http.Client{Timeout: 70 * time.Second}
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
			log.Printf("[telegram] getUpdates: %v", err)
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
			log.Printf("[telegram] bad response: %.200s", string(body))
			time.Sleep(5 * time.Second)
			continue
		}
		for _, up := range out.Result {
			offset = up.UpdateID + 1
			if up.Message == nil || strings.TrimSpace(up.Message.Text) == "" {
				continue
			}
			s.handleTelegramMessage(ctx, api, up.Message.Chat.ID, strings.TrimSpace(up.Message.Text))
		}
	}
}

func (s *Server) handleTelegramMessage(ctx context.Context, api string, chatID int64, text string) {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		return
	}

	allowed, hasOwner := s.telegramAllowedChat()
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

	s.tgAction(api, chatID, "typing")
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := s.runHeadlessChat(runCtx, "telegram", text, "")
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

// tgSendToOwner delivers a message to the linked chat. Used by scheduled jobs
// (cron → Telegram) and /api/chat's deliver option.
func (s *Server) tgSendToOwner(text string) error {
	token := s.telegramToken()
	if token == "" {
		return fmt.Errorf("telegram not configured")
	}
	chatID, ok := s.telegramAllowedChat()
	if !ok {
		return fmt.Errorf("no linked telegram chat — send /start to your bot first")
	}
	s.tgSend("https://api.telegram.org/bot"+token, chatID, text)
	return nil
}

// POST /api/telegram/send {text} — deliver an arbitrary message to the linked chat.
func (s *Server) handleTelegramSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Text) == "" {
		http.Error(w, "text required", 400)
		return
	}
	if err := s.tgSendToOwner(b.Text); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// GET /api/telegram/config -> {configured, linked}. POST {token} to set (resets
// the linked chat), or {unlink:true} to forget the linked chat.
func (s *Server) handleTelegramConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	ms := s.memStore
	s.mu.RUnlock()
	if ms == nil {
		http.Error(w, "memory store not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		_, linked := s.telegramAllowedChat()
		writeJSON(w, map[string]interface{}{
			"configured": s.telegramToken() != "",
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
		s.startTelegram()
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
