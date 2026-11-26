package server

// REST endpoints for the Email app so the user can configure their account and
// read/send mail directly, without going through the agent. Shares the exact
// same storage as the agent's `email` tool: non-secret settings in agent_config
// under "email_config", the password in the encrypted secrets store under
// "email_password" — so both the UI and the agent see one account.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	Signature string `json:"signature,omitempty"`
}

func (sc emailStoredConfig) clientConfig() email.Config {
	return email.Config{IMAPHost: sc.IMAPHost, IMAPPort: sc.IMAPPort,
		SMTPHost: sc.SMTPHost, SMTPPort: sc.SMTPPort, User: sc.User, From: sc.From,
		Security: sc.Security, Insecure: sc.Insecure}
}

func (s *Server) loadEmailCfg(r *http.Request) (email.Config, bool) {
	if s.userStore(r) == nil {
		return email.Config{}, false
	}
	raw, ok, _ := s.userStore(r).GetConfig(r.Context(), emailConfigKey)
	if !ok || raw == "" {
		return email.Config{}, false
	}
	var sc emailStoredConfig
	if json.Unmarshal([]byte(raw), &sc) != nil {
		return email.Config{}, false
	}
	pass, _, _ := s.userStore(r).GetSecret(r.Context(), emailPasswordSecret)
	cfg := sc.clientConfig()
	cfg.Folder, cfg.Pass = r.URL.Query().Get("folder"), pass
	return cfg, true
}

// GET /api/email/config (masked), POST to set/merge.
func (s *Server) handleEmailConfig(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	switch r.Method {
	case "GET":
		var sc emailStoredConfig
		if raw, ok, _ := s.userStore(r).GetConfig(r.Context(), emailConfigKey); ok {
			json.Unmarshal([]byte(raw), &sc)
		}
		pass, hasPass, _ := s.userStore(r).GetSecret(r.Context(), emailPasswordSecret)
		account := ""
		if sc.IMAPHost != "" && sc.User != "" {
			account = sc.clientConfig().AccountFingerprint()
		}
		writeJSON(w, map[string]interface{}{
			"configured":   sc.IMAPHost != "" && sc.User != "",
			"account":      account,
			"imap_host":    sc.IMAPHost,
			"imap_port":    sc.IMAPPort,
			"smtp_host":    sc.SMTPHost,
			"smtp_port":    sc.SMTPPort,
			"user":         sc.User,
			"from":         sc.From,
			"security":     sc.Security,
			"insecure":     sc.Insecure,
			"list_limit":   sc.ListLimit,
			"signature":    sc.Signature,
			"has_password": hasPass && pass != "",
		})
	case "POST":
		var b struct {
			emailStoredConfig
			Password string `json:"password"`
		}
		// Merge over existing so partial updates keep prior values.
		if raw, ok, _ := s.userStore(r).GetConfig(r.Context(), emailConfigKey); ok {
			json.Unmarshal([]byte(raw), &b.emailStoredConfig)
		}
		prior := b.emailStoredConfig
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if len(b.Signature) > 16000 {
			writeErr(w, http.StatusBadRequest, "Signature must be at most 16,000 bytes.")
			return
		}
		if err := email.ValidateBridgePreset(s.userStore(r).LocalMailBridgeDisabled, b.IMAPHost, b.SMTPHost); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if prior.IMAPHost != "" && prior.User != "" {
			if err := email.BindLegacyTags(r.Context(), s.userStore(r), prior.clientConfig().AccountFingerprint()); err != nil {
				writeErr(w, http.StatusInternalServerError, "Could not preserve existing email categories.")
				return
			}
		}
		out, _ := json.Marshal(b.emailStoredConfig)
		if err := s.userStore(r).SetConfig(r.Context(), emailConfigKey, string(out)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if b.Password != "" {
			if err := s.userStore(r).SetSecret(r.Context(), emailPasswordSecret, b.Password); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// Category storage belongs to this mailbox identity within the user's scope.
func (s *Server) handleEmailTags(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	cfg, configured := s.loadEmailCfg(r)
	if !configured {
		writeErr(w, http.StatusBadRequest, "email not configured")
		return
	}
	if !emailAccountMatches(w, cfg, r.URL.Query().Get("account")) {
		return
	}
	store, folder, account := s.userStore(r), r.URL.Query().Get("folder"), cfg.AccountFingerprint()
	switch r.Method {
	case "GET":
		tags, err := email.LoadTags(r.Context(), store, account, folder)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, tags)
	case "POST":
		var incoming map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&incoming) != nil || incoming == nil {
			writeErr(w, 400, "bad body")
			return
		}
		if err := email.MergeTags(r.Context(), store, account, folder, incoming); err != nil {
			writeErr(w, 500, err.Error())
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
		UID     uint32 `json:"uid"`
		Seen    bool   `json:"seen"`
		Account string `json:"account"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.UID == 0 {
		http.Error(w, "uid required", 400)
		return
	}
	if !emailAccountMatches(w, cfg, b.Account) {
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
	cfg.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	cfg.UnreadOnly, _ = strconv.ParseBool(r.URL.Query().Get("unread"))
	page, err := cfg.ListPage(limit)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeEmailPage(w, cfg, page)
}

func (s *Server) handleEmailRead(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	if !emailAccountMatches(w, cfg, r.URL.Query().Get("account")) {
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
	writeJSON(w, map[string]interface{}{"message": msg, "account": cfg.AccountFingerprint()})
}

func (s *Server) handleEmailSearch(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cfg.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	cfg.UnreadOnly, _ = strconv.ParseBool(r.URL.Query().Get("unread"))
	page, err := cfg.SearchPage(r.URL.Query().Get("q"), limit)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeEmailPage(w, cfg, page)
}

func writeEmailPage(w http.ResponseWriter, cfg email.Config, page email.Page) {
	writeJSON(w, struct {
		email.Page
		Account string `json:"account"`
	}{Page: page, Account: cfg.AccountFingerprint()})
}

func emailAccountMatches(w http.ResponseWriter, cfg email.Config, expected string) bool {
	if err := cfg.CheckAccount(expected); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return false
	}
	return true
}

func (s *Server) handleEmailStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "email not configured")
		return
	}
	if !emailAccountMatches(w, cfg, r.URL.Query().Get("account")) {
		return
	}
	status, err := cfg.FolderStatus()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, struct {
		email.FolderStatus
		Account string `json:"account"`
	}{FolderStatus: status, Account: cfg.AccountFingerprint()})
}

// POST /api/email/send — multipart/form-data: to, subject, body, uid (for a
// reply), plus optional file[] attachments. Falls back to a JSON body when not
// multipart (no attachments).
func (s *Server) handleEmailSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "Method not allowed.")
		return
	}
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		writeErr(w, 400, "Email is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEmailRequest)
	var to, cc, bcc, subject, body string
	var uid uint32
	var atts []email.OutAttachment
	var draftID, draftRevision int64
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			writeErr(w, 400, "Invalid email or attachments exceed the request limit.")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		to, cc, bcc, subject, body = r.FormValue("to"), r.FormValue("cc"), r.FormValue("bcc"), r.FormValue("subject"), r.FormValue("body")
		if v, err := strconv.ParseUint(r.FormValue("uid"), 10, 32); err == nil {
			uid = uint32(v)
		}
		if r.MultipartForm != nil {
			total := 0
			for _, fh := range r.MultipartForm.File["file"] {
				f, err := fh.Open()
				if err != nil {
					writeErr(w, 400, "Could not read an attachment.")
					return
				}
				data, err := io.ReadAll(io.LimitReader(f, maxEmailAttachments+1))
				f.Close()
				total += len(data)
				if err != nil || total > maxEmailAttachments {
					writeErr(w, 400, "Attachments exceed the 20 MiB limit or could not be read.")
					return
				}
				atts = append(atts, email.OutAttachment{Filename: fh.Filename, ContentType: fh.Header.Get("Content-Type"), Data: data})
			}
		}
	} else {
		var b struct {
			To, CC, BCC, Subject, Body string
			UID                        uint32
			DraftID                    int64 `json:"draft_id"`
			DraftRevision              int64 `json:"draft_revision"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, 400, "Invalid email request.")
			return
		}
		to, cc, bcc, subject, body, uid = b.To, b.CC, b.BCC, b.Subject, b.Body, b.UID
		draftID, draftRevision = b.DraftID, b.DraftRevision
	}
	var sent, uncertain bool
	if draftID > 0 {
		if draftRevision < 1 {
			writeErr(w, 400, "The saved draft revision is required.")
			return
		}
		ms := s.userStore(r)
		if ms == nil {
			writeErr(w, 503, "Draft storage is unavailable.")
			return
		}
		prior, err := ms.GetEmailDraft(r.Context(), draftID)
		if err != nil {
			emailDraftError(w, err)
			return
		}
		if prior.Account != emailAccountFingerprint(cfg) {
			writeErr(w, 409, "The mailbox connection changed. Save this as a new draft before sending.")
			return
		}
		d, err := ms.ClaimEmailDraft(r.Context(), draftID, draftRevision, emailAccountFingerprint(cfg))
		if err != nil {
			emailDraftError(w, err)
			return
		}
		defer func() {
			// SMTP may outlive an HTTP disconnect. Keep its outcome durable without
			// letting a cancelled browser request strand a confirmed failed send.
			// A transport error after SMTP begins can mean delivery succeeded but
			// its acknowledgement was lost. Keep that snapshot locked for review.
			if uncertain && !sent {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = ms.FinishEmailDraftSend(ctx, draftID, draftRevision, sent)
		}()
		to, cc, bcc, subject, body, uid = d.To, d.CC, d.BCC, d.Subject, d.Body, d.UID
		cfg.Folder = d.Folder
		atts = nil
		for _, a := range d.Attachments {
			atts = append(atts, email.OutAttachment{Filename: a.Filename, ContentType: a.ContentType, Data: a.Data})
		}
	}
	if strings.TrimSpace(body) == "" {
		writeErr(w, 400, "Message body is required.")
		return
	}
	if len(body) > maxEmailText || len(subject) > 8192 || len(to)+len(cc)+len(bcc) > 32768 {
		writeErr(w, 400, "Message text is too large.")
		return
	}
	inReplyTo := ""
	if uid > 0 {
		orig, err := cfg.Read(uid)
		if err != nil {
			writeErr(w, 502, "Could not load original: "+err.Error())
			return
		}
		inReplyTo = orig.MessageID
		if to == "" && cc == "" && bcc == "" {
			to = orig.ReplyTo
			if to == "" {
				to = orig.From
			}
		}
		if subject == "" {
			subject = "Re: " + orig.Subject
		}
	}
	if err := cfg.SendTo(to, cc, bcc, subject, body, inReplyTo, atts); err != nil {
		uncertain = email.DeliveryUncertain(err)
		writeErr(w, 502, err.Error())
		return
	}
	sent = true
	writeJSON(w, map[string]any{"ok": true})
}

// GET /api/email/attachment?uid=X&i=N — download the N-th attachment.
func (s *Server) handleEmailAttachment(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadEmailCfg(r)
	if !ok {
		http.Error(w, "email not configured", 400)
		return
	}
	if !emailAccountMatches(w, cfg, r.URL.Query().Get("account")) {
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
