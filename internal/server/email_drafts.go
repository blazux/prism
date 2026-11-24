package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"prism/internal/email"
	"prism/internal/memory"
)

const maxEmailAttachments = 20 << 20
const maxEmailRequest = 30 << 20
const maxEmailText = 1 << 20

var draftClientID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)

// Changing mailboxes must never silently send an old draft under a new identity.
// Password rotation and transport security changes do not change its identity.
func emailAccountFingerprint(c email.Config) string {
	h := sha256.Sum256([]byte(strings.Join([]string{c.IMAPHost, strconv.Itoa(c.IMAPPort), c.SMTPHost, strconv.Itoa(c.SMTPPort), c.User, c.From}, "\x00")))
	return hex.EncodeToString(h[:])
}

func validateEmailDraft(d memory.EmailDraft) error {
	if d.ID < 0 || (d.ID > 0 && d.Revision < 1) {
		return fmt.Errorf("a valid draft revision is required")
	}
	if d.ID == 0 && !draftClientID.MatchString(d.ClientID) {
		return fmt.Errorf("client_id must be a unique 16–100 character draft token")
	}
	if len(d.Body) > maxEmailText || len(d.Subject) > 8192 || len(d.To)+len(d.CC)+len(d.BCC) > 32768 || len(d.Folder) > 4096 {
		return fmt.Errorf("draft text is too large")
	}
	if len(d.Attachments) > 50 {
		return fmt.Errorf("a draft supports at most 50 attachments")
	}
	total := 0
	for _, a := range d.Attachments {
		total += len(a.Data)
		if len(a.Filename) == 0 || len(a.Filename) > 512 || strings.ContainsAny(a.Filename, "\r\n\x00") {
			return fmt.Errorf("attachment filename is invalid")
		}
		if len(a.ContentType) > 256 {
			return fmt.Errorf("attachment content type is invalid")
		}
		if a.ContentType != "" {
			if _, _, err := mime.ParseMediaType(a.ContentType); err != nil {
				return fmt.Errorf("attachment content type is invalid")
			}
		}
	}
	if total > maxEmailAttachments {
		return fmt.Errorf("attachments exceed the 20 MiB draft limit")
	}
	return nil
}

func emailDraftError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeErr(w, 404, "Draft not found or unavailable.")
	case errors.Is(err, memory.ErrEmailDraftConflict):
		writeErr(w, 409, err.Error())
	default:
		writeErr(w, 500, "Could not save or load this draft. Your changes have not been discarded.")
	}
}

// GET lists lightweight summaries or retrieves a full saved snapshot. POST
// creates/updates one snapshot with optimistic concurrency. Omitting attachments
// on an update keeps the previous files, avoiding an upload for each keystroke.
func (s *Server) handleEmailDrafts(w http.ResponseWriter, r *http.Request) {
	if !s.pimStore(w) {
		return
	}
	ms := s.userStore(r)
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("id") != "" {
			id := idParam(r)
			if id < 1 {
				writeErr(w, 400, "A valid draft id is required.")
				return
			}
			d, err := ms.GetEmailDraft(r.Context(), id)
			if err != nil {
				emailDraftError(w, err)
				return
			}
			cfg, configured := s.loadEmailCfg(r)
			writeJSON(w, map[string]any{"draft": d, "mailboxChanged": !configured || d.Account != emailAccountFingerprint(cfg)})
			return
		}
		drafts, err := ms.ListEmailDrafts(r.Context())
		if err != nil {
			emailDraftError(w, err)
			return
		}
		writeJSON(w, map[string]any{"drafts": drafts})
	case http.MethodPost:
		cfg, ok := s.loadEmailCfg(r)
		if !ok {
			writeErr(w, 400, "Connect your mailbox before saving a draft.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxEmailRequest)
		var fields map[string]json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&fields); err != nil {
			writeErr(w, 400, "Invalid draft or request exceeds the 30 MiB limit.")
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			writeErr(w, 400, "Expected one draft object.")
			return
		}
		raw, _ := json.Marshal(fields)
		var d memory.EmailDraft
		if json.Unmarshal(raw, &d) != nil {
			writeErr(w, 400, "Invalid draft fields.")
			return
		}
		if err := validateEmailDraft(d); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		d.Account = emailAccountFingerprint(cfg)
		if d.ID > 0 {
			prior, err := ms.GetEmailDraft(r.Context(), d.ID)
			if err != nil {
				emailDraftError(w, err)
				return
			}
			if prior.Account != d.Account {
				writeErr(w, 409, "The mailbox connection changed. Save this as a new draft to use the current mailbox.")
				return
			}
		}
		_, replaceAttachments := fields["attachments"]
		saved, err := ms.SaveEmailDraft(r.Context(), d, replaceAttachments)
		if err != nil {
			emailDraftError(w, err)
			return
		}
		writeJSON(w, map[string]any{"draft": saved, "replayed": saved.Replayed})
	case http.MethodDelete:
		id := idParam(r)
		revision, _ := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if id < 1 || revision < 1 {
			writeErr(w, 400, "A draft id and revision are required.")
			return
		}
		if err := ms.DeleteEmailDraft(r.Context(), id, revision); err != nil {
			emailDraftError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeErr(w, 405, "Method not allowed.")
	}
}
