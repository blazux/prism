package server

// REST endpoints for the Email app so the user can configure their account and
// read/send mail directly, without going through the agent. Shares the exact
// same storage as the agent's `email` tool: non-secret settings in agent_config
// under "email_config", the password in the encrypted secrets store under
// "email_password" — so both the UI and the agent see one account.

import (
	"encoding/json"
	"net/http"
	"strconv"

	"prism/internal/email"
)

// Keep these literals in sync with internal/agent/tools_email.go.
const emailConfigKey = "email_config"
const emailPasswordSecret = "email_password"

type emailStoredConfig struct {
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	User     string `json:"user"`
	From     string `json:"from"`
	Security string `json:"security,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

func (s *Server) loadEmailCfg(r *http.Request) (email.Config, bool) {
	if s.memStore == nil {
		return email.Config{}, false
	}
	raw, ok, _ := s.memStore.GetConfig(r.Context(), emailConfigKey)
	if !ok || raw == "" {
		return email.Config{}, false
	}
	var sc emailStoredConfig
	if json.Unmarshal([]byte(raw), &sc) != nil {
		return email.Config{}, false
	}
	pass, _, _ := s.memStore.GetSecret(r.Context(), emailPasswordSecret)
	return email.Config{
		IMAPHost: sc.IMAPHost, IMAPPort: sc.IMAPPort,
		SMTPHost: sc.SMTPHost, SMTPPort: sc.SMTPPort,
		User: sc.User, From: sc.From, Pass: pass,
		Security: sc.Security, Insecure: sc.Insecure,
	}, true
}

// GET /api/email/config (masked), POST to set/merge.
func (s *Server) handleEmailConfig(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		var sc emailStoredConfig
		if raw, ok, _ := s.memStore.GetConfig(r.Context(), emailConfigKey); ok {
			json.Unmarshal([]byte(raw), &sc)
		}
		pass, hasPass, _ := s.memStore.GetSecret(r.Context(), emailPasswordSecret)
		writeJSON(w, map[string]interface{}{
			"configured":   sc.IMAPHost != "" && sc.User != "",
			"imap_host":    sc.IMAPHost,
			"imap_port":    sc.IMAPPort,
			"smtp_host":    sc.SMTPHost,
			"smtp_port":    sc.SMTPPort,
			"user":         sc.User,
			"from":         sc.From,
			"security":     sc.Security,
			"insecure":     sc.Insecure,
			"has_password": hasPass && pass != "",
		})
	case "POST":
		var b struct {
			emailStoredConfig
			Password string `json:"password"`
		}
		// Merge over existing so partial updates keep prior values.
		if raw, ok, _ := s.memStore.GetConfig(r.Context(), emailConfigKey); ok {
			json.Unmarshal([]byte(raw), &b.emailStoredConfig)
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		out, _ := json.Marshal(b.emailStoredConfig)
		if err := s.memStore.SetConfig(r.Context(), emailConfigKey, string(out)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if b.Password != "" {
			if err := s.memStore.SetSecret(r.Context(), emailPasswordSecret, b.Password); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// GET /api/email/unread -> { count }. Soft-fails to 0 so the badge poll never
// errors (e.g. email not configured or briefly unreachable).
func (s *Server) handleEmailUnread(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		writeJSON(w, map[string]interface{}{"count": 0})
		return
	}
	n, err := cfg.UnreadCount()
	if err != nil {
		writeJSON(w, map[string]interface{}{"count": 0})
		return
	}
	writeJSON(w, map[string]interface{}{"count": n})
}

// POST /api/email/markseen  {uid, seen}  — marks a message read/unread.
func (s *Server) handleEmailMarkSeen(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	var b struct {
		UID  uint32 `json:"uid"`
		Seen bool   `json:"seen"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.UID == 0 {
		http.Error(w, "uid required", 400)
		return
	}
	if err := cfg.SetSeen(b.UID, b.Seen); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

func (s *Server) handleEmailList(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := cfg.List(limit)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if msgs == nil {
		msgs = []email.Message{}
	}
	writeJSON(w, map[string]interface{}{"messages": msgs})
}

func (s *Server) handleEmailRead(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	uid, _ := strconv.ParseUint(r.URL.Query().Get("uid"), 10, 32)
	if uid == 0 {
		http.Error(w, "uid required", 400)
		return
	}
	msg, err := cfg.Read(uint32(uid))
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{"message": msg})
}

func (s *Server) handleEmailSearch(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := cfg.Search(r.URL.Query().Get("q"), limit)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if msgs == nil {
		msgs = []email.Message{}
	}
	writeJSON(w, map[string]interface{}{"messages": msgs})
}

// POST /api/email/send  {to, subject, body}  or reply {uid, body[, to]}
func (s *Server) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	var b struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
		UID     uint32 `json:"uid"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.Body == "" {
		http.Error(w, "body required", 400)
		return
	}
	inReplyTo := ""
	if b.UID > 0 {
		orig, err := cfg.Read(b.UID)
		if err != nil {
			http.Error(w, "could not load original: "+err.Error(), 502)
			return
		}
		inReplyTo = orig.MessageID
		if b.To == "" {
			b.To = orig.From
		}
		if b.Subject == "" {
			b.Subject = "Re: " + orig.Subject
		}
	}
	if b.To == "" {
		http.Error(w, "recipient required", 400)
		return
	}
	if err := cfg.Send(b.To, b.Subject, b.Body, inReplyTo); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}
