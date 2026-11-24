package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"prism/internal/memory"
)

func TestEmailDraftValidation(t *testing.T) {
	d := memory.EmailDraft{ClientID: "test-client-token-1", Body: "Réunion à 9h", Attachments: []memory.EmailDraftAttachment{{Filename: "résumé.txt", ContentType: "text/plain", Data: []byte("déjà prêt")}}}
	if err := validateEmailDraft(d); err != nil {
		t.Fatal(err)
	}
	d.Attachments[0].ContentType = "text/plain\r\nBcc: bad@example.test"
	if validateEmailDraft(d) == nil {
		t.Fatal("MIME header injection accepted")
	}
	d.Attachments = nil
	d.ClientID = "short"
	if validateEmailDraft(d) == nil {
		t.Fatal("invalid creation token accepted")
	}
}

func TestEmailDraftsPersistIsolateAndRejectStaleEdits(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	a := &memory.User{ID: 41}
	b := &memory.User{ID: 42}
	cfg := `{"imap_host":"mail.example.test","smtp_host":"mail.example.test","user":"fixture@example.test","signature":"Vincent"}`
	for _, u := range []*memory.User{a, b} {
		if err := ms.ConfigScope(fmt.Sprintf("u%d", u.ID)).SetConfig(t.Context(), emailConfigKey, cfg); err != nil {
			t.Fatal(err)
		}
	}
	call := func(user *memory.User, method, path string, body any, code int) map[string]json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := withUser(httptest.NewRequest(method, path, bytes.NewReader(raw)), user)
		w := httptest.NewRecorder()
		s.handleEmailDrafts(w, r)
		if w.Code != code {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var result map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	decode := func(result map[string]json.RawMessage) memory.EmailDraft {
		t.Helper()
		var d memory.EmailDraft
		if err := json.Unmarshal(result["draft"], &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	create := memory.EmailDraft{ClientID: "persistent-client-token-1", To: "alice@example.test", CC: "colleague@example.test", BCC: "private@example.test", Subject: "Réunion", Body: "Prévoir une journée à Paris.", Folder: "Projects", UID: 17, Attachments: []memory.EmailDraftAttachment{{Filename: "résumé.txt", ContentType: "text/plain", Data: []byte("été à Noël")}}}
	result := call(a, "POST", "/api/email/drafts", create, 200)
	saved := decode(result)
	if saved.ID < 1 || saved.Revision != 1 || saved.AttachmentCount != 1 {
		t.Fatalf("bad save: %+v", saved)
	}
	if bytes.Contains(result["draft"], []byte(`"data"`)) {
		t.Fatal("save acknowledgement copied attachment bytes")
	}
	get := decode(call(a, "GET", fmt.Sprintf("/api/email/drafts?id=%d", saved.ID), nil, 200))
	if get.Body != create.Body || get.CC != create.CC || get.BCC != create.BCC || get.Folder != "Projects" || get.UID != 17 || len(get.Attachments) != 1 || !bytes.Equal(get.Attachments[0].Data, create.Attachments[0].Data) {
		t.Fatalf("snapshot did not survive reload: %+v", get)
	}
	call(b, "GET", fmt.Sprintf("/api/email/drafts?id=%d", saved.ID), nil, 404)
	call(b, "DELETE", fmt.Sprintf("/api/email/drafts?id=%d&revision=1", saved.ID), nil, 404)
	// Only text changes: omit attachments to keep the existing files.
	update := map[string]any{"id": saved.ID, "revision": saved.Revision, "to": create.To, "cc": create.CC, "bcc": create.BCC, "subject": create.Subject, "body": "Nouvelle réponse", "folder": create.Folder, "uid": create.UID}
	updated := decode(call(a, "POST", "/api/email/drafts", update, 200))
	if updated.Revision != 2 || updated.AttachmentCount != 1 {
		t.Fatal(updated)
	}
	call(a, "POST", "/api/email/drafts", update, 409)
	call(a, "DELETE", fmt.Sprintf("/api/email/drafts?id=%d&revision=1", saved.ID), nil, 409)
	// A lost create response followed by further typing must be marked as a
	// replay; its returned id/revision is not an acknowledgement of the new text.
	create.Body = "Text typed after a lost response"
	replay := call(a, "POST", "/api/email/drafts", create, 200)
	if string(replay["replayed"]) != "true" || decode(replay).Body != "Nouvelle réponse" {
		t.Fatalf("unsafe create retry: %s", replay["draft"])
	}
	list := call(a, "GET", "/api/email/drafts", nil, 200)
	if bytes.Contains(list["drafts"], []byte("Nouvelle réponse")) || bytes.Contains(list["drafts"], []byte(`"data"`)) {
		t.Fatal("list contains heavy draft body or files")
	}
	var rows []memory.EmailDraft
	json.Unmarshal(list["drafts"], &rows)
	if len(rows) != 1 || rows[0].AttachmentCount != 1 {
		t.Fatal(rows)
	}
	var peers []memory.EmailDraft
	json.Unmarshal(call(b, "GET", "/api/email/drafts", nil, 200)["drafts"], &peers)
	if len(peers) != 0 {
		t.Fatal("another user can list drafts")
	}
	// Saved drafts stay tied to the original mailbox after a configuration edit.
	us := ms.ConfigScope("u41")
	if err := us.SetConfig(t.Context(), emailConfigKey, strings.ReplaceAll(cfg, "fixture@example.test", "other@example.test")); err != nil {
		t.Fatal(err)
	}
	update["revision"] = 2
	call(a, "POST", "/api/email/drafts", update, 409)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest("POST", "/api/email/send", strings.NewReader(fmt.Sprintf(`{"draft_id":%d,"draft_revision":2}`, saved.ID))), a)
	s.handleEmailSend(w, r)
	if w.Code != 409 {
		t.Fatalf("old draft sent with changed identity: %d %s", w.Code, w.Body.String())
	}
	call(a, "DELETE", fmt.Sprintf("/api/email/drafts?id=%d&revision=2", saved.ID), nil, 200)
}

// A disposable localhost SMTP peer. Only synthetic fixture messages reach it.
func draftSMTPPeer(t *testing.T, acknowledge bool, reject ...bool) (int, <-chan string, <-chan []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	messages := make(chan string, 1)
	recipients := make(chan []string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(c, "220 localhost fixture\r\n")
		r := bufio.NewReader(c)
		var rcpts []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				io.WriteString(c, "250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(line, "AUTH"):
				io.WriteString(c, "235 authenticated\r\n")
			case strings.HasPrefix(line, "MAIL"):
				io.WriteString(c, "250 sender\r\n")
			case strings.HasPrefix(line, "RCPT"):
				rcpts = append(rcpts, strings.TrimSpace(line))
				io.WriteString(c, "250 recipient\r\n")
			case strings.HasPrefix(line, "DATA"):
				io.WriteString(c, "354 send data\r\n")
				var body strings.Builder
				for {
					part, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
					body.WriteString(part)
				}
				messages <- body.String()
				recipients <- rcpts
				if !acknowledge {
					return
				}
				if len(reject) > 0 && reject[0] {
					io.WriteString(c, "550 message rejected\r\n")
				} else {
					io.WriteString(c, "250 accepted\r\n")
				}
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(c, "221 goodbye\r\n")
				return
			default:
				io.WriteString(c, "250 ok\r\n")
			}
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, messages, recipients
}

func TestEmailDraftKnownSMTPRejectionRemainsEditable(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	port, _, _ := draftSMTPPeer(t, true, true)
	cfg := emailStoredConfig{IMAPHost: "imap.example.test", SMTPHost: "127.0.0.1", SMTPPort: port, User: "fixture@example.test"}
	raw, _ := json.Marshal(cfg)
	if err := ms.SetConfig(t.Context(), emailConfigKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := ms.SetSecret(t.Context(), emailPasswordSecret, "fixture-only"); err != nil {
		t.Fatal(err)
	}
	c, ok := s.loadEmailCfg(httptest.NewRequest("GET", "/", nil))
	if !ok {
		t.Fatal("fixture configmissing")
	}
	d, err := ms.SaveEmailDraft(t.Context(), memory.EmailDraft{ClientID: "rejected-smtp-token-1", Account: emailAccountFingerprint(c), To: "alice@example.test", Body: "Fixture body"}, true)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleEmailSend(w, httptest.NewRequest("POST", "/api/email/send", strings.NewReader(fmt.Sprintf(`{"draft_id":%d,"draft_revision":%d}`, d.ID, d.Revision))))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "rejected") {
		t.Fatal(w.Code, w.Body.String())
	}
	d, err = ms.GetEmailDraft(t.Context(), d.ID)
	if err != nil || d.Status != "draft" {
		t.Fatal("definite SMTP rejection became unknown delivery", d, err)
	}
	d.Body = "Corrected body"
	if _, err := ms.SaveEmailDraft(t.Context(), d, false); err != nil {
		t.Fatal("rejected draft cannot be corrected", err)
	}
}

func TestEmailDraftSendUsesSavedSnapshotAndProtectsUnknownDelivery(t *testing.T) {
	for _, ack := range []bool{true, false} {
		t.Run(fmt.Sprintf("ack_%t", ack), func(t *testing.T) {
			ms := securityStore(t)
			s := &Server{memStore: ms}
			port, messages, recipients := draftSMTPPeer(t, ack)
			cfg := emailStoredConfig{IMAPHost: "imap.example.test", SMTPHost: "127.0.0.1", SMTPPort: port, User: "fixture@example.test"}
			raw, _ := json.Marshal(cfg)
			ms.SetConfig(t.Context(), emailConfigKey, string(raw))
			ms.SetSecret(t.Context(), emailPasswordSecret, "fixture-only")
			snapshot := memory.EmailDraft{ClientID: "smtp-fixture-token-1", To: "alice@example.test", CC: "colleague@example.test", BCC: "blind@example.test", Subject: "Réunion", Body: "Saved message à envoyer", Attachments: []memory.EmailDraftAttachment{{Filename: "résumé.txt", ContentType: "text/plain", Data: []byte("été")}}}
			post, _ := json.Marshal(snapshot)
			w := httptest.NewRecorder()
			s.handleEmailDrafts(w, httptest.NewRequest("POST", "/api/email/drafts", bytes.NewReader(post)))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var saved struct{ Draft memory.EmailDraft }
			json.Unmarshal(w.Body.Bytes(), &saved)
			send := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				s.handleEmailSend(w, httptest.NewRequest("POST", "/api/email/send", strings.NewReader(fmt.Sprintf(`{"draft_id":%d,"draft_revision":%d,"body":"UNSAVED OVERRIDE","to":"wrong@example.test"}`, saved.Draft.ID, saved.Draft.Revision))))
				return w
			}
			w = send()
			expected := 200
			if !ack {
				expected = 502
			}
			if w.Code != expected {
				t.Fatal(w.Code, w.Body)
			}
			message := <-messages
			if !strings.Contains(message, snapshot.Body) || strings.Contains(message, "UNSAVED OVERRIDE") || strings.Contains(message, "wrong@example.test") || strings.Contains(message, "blind@example.test") {
				t.Fatal("sent a different or leaking snapshot", message)
			}
			if len(<-recipients) != 3 {
				t.Fatal("CC/BCC did not reach SMTP envelope")
			}
			if ack {
				if _, err := ms.GetEmailDraft(t.Context(), saved.Draft.ID); err == nil {
					t.Fatal("sent draft not removed")
				}
				if w := send(); w.Code != 404 {
					t.Fatal("completed draft could be sent twice", w.Code, w.Body)
				}
			} else {
				d, err := ms.GetEmailDraft(t.Context(), saved.Draft.ID)
				if err != nil || d.Status != "sending" {
					t.Fatal("ambiguous delivery became retryable", d, err)
				}
				if w := send(); w.Code != 409 {
					t.Fatal("uncertain draft sent twice", w.Code, w.Body)
				}
			}
		})
	}
}

func TestEmailDraftSendClaimIsExclusive(t *testing.T) {
	ms := securityStore(t).ConfigScope("u31")
	d, err := ms.SaveEmailDraft(t.Context(), memory.EmailDraft{ClientID: "claim-client-token-1", Account: "mailbox", Body: "Hello"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ms.ClaimEmailDraft(t.Context(), d.ID, d.Revision, "mailbox")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d concurrent send claims succeeded", success)
	}
	d.Body = "New edit"
	if _, err := ms.SaveEmailDraft(t.Context(), d, false); err != memory.ErrEmailDraftConflict {
		t.Fatalf("in-flight draft edited: %v", err)
	}
	if err := ms.DeleteEmailDraft(t.Context(), d.ID, d.Revision); err != memory.ErrEmailDraftConflict {
		t.Fatal("in-flight draft deleted", err)
	}
	if err := ms.FinishEmailDraftSend(t.Context(), d.ID, d.Revision, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.SaveEmailDraft(t.Context(), d, false); err != nil {
		t.Fatal("known pre-send failure did not unlock draft", err)
	}
}
