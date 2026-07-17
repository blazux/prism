package server

// REST endpoints for the Email app so the user can configure their account and
// read/send mail directly, without going through the agent. Shares the exact
// same storage as the agent's `email` tool: non-secret settings in agent_config
// under "email_config", the password in the encrypted secrets store under
// "email_password" — so both the UI and the agent see one account.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"prism/internal/email"
)

// Keep these literals in sync with internal/agent/tools_email.go.
const emailConfigKey = "email_config"
const emailPasswordSecret = "email_password"

type emailStoredConfig struct {
	IMAPHost  string `json:"imap_host"`
	IMAPPort  int    `json:"imap_port"`
	SMTPHost  string `json:"smtp_host"`
	SMTPPort  int    `json:"smtp_port"`
	User      string `json:"user"`
	From      string `json:"from"`
	Security  string `json:"security,omitempty"`
	Insecure  bool   `json:"insecure,omitempty"`
	ListLimit int    `json:"list_limit,omitempty"`
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
			"list_limit":   sc.ListLimit,
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

const emailTagsKey = "email_tags"

// GET /api/email/tags -> stored {uid: {category, tags}} map.
// POST a {uid: {category, tags}} map to merge it in (used by AI triage + manual).
func (s *Server) handleEmailTags(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		raw, _, _ := s.memStore.GetConfig(r.Context(), emailTagsKey)
		if raw == "" {
			raw = "{}"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	case "POST":
		var incoming map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&incoming) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		cur := map[string]json.RawMessage{}
		if raw, ok, _ := s.memStore.GetConfig(r.Context(), emailTagsKey); ok && raw != "" {
			json.Unmarshal([]byte(raw), &cur)
		}
		for k, v := range incoming {
			if string(v) == "null" {
				delete(cur, k)
			} else {
				cur[k] = v
			}
		}
		out, _ := json.Marshal(cur)
		if err := s.memStore.SetConfig(r.Context(), emailTagsKey, string(out)); err != nil {
			http.Error(w, err.Error(), 500)
			return
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

// POST /api/email/send — multipart/form-data: to, subject, body, uid (for a
// reply), plus optional file[] attachments. Falls back to a JSON body when not
// multipart (no attachments).
func (s *Server) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	var to, subject, body string
	var uid uint32
	var atts []email.OutAttachment

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(25 << 20); err != nil { // 25 MB
			http.Error(w, "bad form: "+err.Error(), 400)
			return
		}
		to = r.FormValue("to")
		subject = r.FormValue("subject")
		body = r.FormValue("body")
		if v, err := strconv.ParseUint(r.FormValue("uid"), 10, 32); err == nil {
			uid = uint32(v)
		}
		if r.MultipartForm != nil {
			for _, fh := range r.MultipartForm.File["file"] {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, _ := io.ReadAll(f)
				f.Close()
				atts = append(atts, email.OutAttachment{
					Filename:    fh.Filename,
					ContentType: fh.Header.Get("Content-Type"),
					Data:        data,
				})
			}
		}
	} else {
		var b struct {
			To, Subject, Body string
			UID               uint32
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		to, subject, body, uid = b.To, b.Subject, b.Body, b.UID
	}

	if strings.TrimSpace(body) == "" {
		http.Error(w, "body required", 400)
		return
	}
	inReplyTo := ""
	if uid > 0 {
		orig, err := cfg.Read(uid)
		if err != nil {
			http.Error(w, "could not load original: "+err.Error(), 502)
			return
		}
		inReplyTo = orig.MessageID
		if to == "" {
			to = orig.From
		}
		if subject == "" {
			subject = "Re: " + orig.Subject
		}
	}
	if to == "" {
		http.Error(w, "recipient required", 400)
		return
	}
	if err := cfg.Send(to, subject, body, inReplyTo, atts); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// GET /api/email/attachment?uid=X&i=N — download the N-th attachment.
func (s *Server) handleEmailAttachment(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	uid, _ := strconv.ParseUint(r.URL.Query().Get("uid"), 10, 32)
	idx, _ := strconv.Atoi(r.URL.Query().Get("i"))
	if uid == 0 {
		http.Error(w, "uid required", 400)
		return
	}
	fn, ct, data, err := cfg.Attachment(uint32(uid), idx)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeFilename(fn)))
	w.Write(data)
}

func sanitizeFilename(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\"", "'", "/", "-", "\\", "-").Replace(s)
}
