package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"prism/internal/memory"
)

// Webhooks let anything that can make an HTTP call drive the agent: CI, Grafana,
// a form, a smart plug. The payload is wrapped in a stored prompt and handed to a
// normal agent turn, so everything the agent can do — tools, RAG, widgets,
// delivering to Telegram — is available to the sender without them knowing any
// of it exists.
//
// The inbound endpoint is deliberately outside the dashboard's authentication:
// the caller is a machine that has no Prism login. It authenticates with the
// per-webhook token instead (see webhookIncomingPrefix in auth).

const (
	webhookAPIPrefix      = "/api/webhooks/" // dashboard CRUD (authenticated)
	webhookIncomingPrefix = "/api/webhook/"  // inbound calls (token-authenticated)

	// maxWebhookBody bounds what a sender can push into the prompt. Generous for
	// a JSON event, small enough that a runaway producer cannot fill the context
	// window (or memory) with one request.
	maxWebhookBody = 256 << 10

	// contentPlaceholder is substituted with the payload. A prompt without it
	// still works — the payload is appended — so a webhook configured in a hurry
	// does something sensible instead of silently dropping its input.
	contentPlaceholder = "{{content}}"

	// syncWebhookTimeout bounds a respond=true call. Most senders give up long
	// before this; it exists so the connection cannot be held open indefinitely.
	syncWebhookTimeout = 2 * time.Minute

	// asyncWebhookTimeout bounds the detached run. Matches the channel handlers.
	asyncWebhookTimeout = 10 * time.Minute
)

// webhookScope is the config scope that owns a webhook: the deployment in
// single-user mode, the calling user in multi-user mode. It decides which list
// the dashboard shows, not what the agent may do — see runWebhook.
func (s *Server) webhookScope(r *http.Request) string {
	if u := currentUser(r); u != nil && u.ID > 0 {
		return fmt.Sprintf("u%d", u.ID)
	}
	return "global"
}

func newWebhookID() string  { return randomHex(8) }
func newWebhookTok() string { return randomHex(24) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not recoverable into a weaker id: a guessable
		// webhook URL is an open door to the agent.
		panic("webhook: crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ---- dashboard CRUD ------------------------------------------------------

type webhookDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Token      string `json:"token"`
	Prompt     string `json:"prompt"`
	SessionID  string `json:"session_id"`
	Model      string `json:"model"`
	Deliver    string `json:"deliver"`
	Respond    bool   `json:"respond"`
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`
	LastCallAt string `json:"last_call_at"`
	LastStatus string `json:"last_status"`
	CallCount  int64  `json:"call_count"`
}

func toWebhookDTO(w memory.WebhookRow) webhookDTO {
	d := webhookDTO{
		ID: w.ID, Name: w.Name, Token: w.Token, Prompt: w.Prompt,
		SessionID: w.SessionID, Model: w.Model, Deliver: w.Deliver,
		Respond: w.Respond, Enabled: w.Enabled,
		LastStatus: w.LastStatus, CallCount: w.CallCount,
		URL: webhookIncomingPrefix + w.ID + "?token=" + w.Token,
	}
	if w.LastCallAt != nil {
		d.LastCallAt = w.LastCallAt.Format(time.RFC3339)
	}
	return d
}

// handleWebhooks lists (GET) and creates or updates (POST) webhooks.
func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	scope := s.webhookScope(r)

	switch r.Method {
	case http.MethodGet:
		rows, err := ms.WebhookList(r.Context(), scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]webhookDTO, 0, len(rows))
		for _, row := range rows {
			out = append(out, toWebhookDTO(row))
		}
		writeJSON(w, out)

	case http.MethodPost:
		var body webhookDTO
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		row := memory.WebhookRow{
			ID: strings.TrimSpace(body.ID), Scope: scope,
			Name:      strings.TrimSpace(body.Name),
			Prompt:    body.Prompt,
			SessionID: strings.TrimSpace(body.SessionID),
			Model:     strings.TrimSpace(body.Model),
			Deliver:   strings.TrimSpace(body.Deliver),
			Respond:   body.Respond,
			Enabled:   body.Enabled,
			Token:     strings.TrimSpace(body.Token),
		}
		if row.ID == "" {
			row.ID = newWebhookID()
		} else {
			// Editing: refuse to touch another scope's webhook even though ids are
			// globally unique.
			if existing, ok, _ := ms.WebhookByID(r.Context(), row.ID); ok && existing.Scope != scope {
				writeErr(w, http.StatusForbidden, "not your webhook")
				return
			}
		}
		// The session is stored namespaced, exactly as /ws and /api/chat would
		// resolve it for this caller — a member cannot point a webhook at another
		// user's "u<id>-…" board. Empty stays empty (dedicated per-webhook session).
		if row.SessionID != "" {
			sid, ok := s.sessionFor(r, row.SessionID)
			if !ok {
				writeErr(w, http.StatusForbidden, "session belongs to another user")
				return
			}
			row.SessionID = sid
		}
		if row.Token == "" {
			row.Token = newWebhookTok()
		}
		if row.Name == "" {
			row.Name = "webhook " + row.ID
		}
		if err := ms.WebhookUpsert(r.Context(), row); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		saved, _, _ := ms.WebhookByID(r.Context(), row.ID)
		writeJSON(w, toWebhookDTO(saved))

	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

// handleWebhookByID deletes one webhook.
func (s *Server) handleWebhookByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, webhookAPIPrefix), "/")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	if r.Method != http.MethodDelete {
		writeErr(w, http.StatusMethodNotAllowed, "DELETE")
		return
	}
	if err := ms.WebhookDelete(r.Context(), s.webhookScope(r), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"deleted": true})
}

// ---- inbound ------------------------------------------------------------

// handleWebhookIncoming is the endpoint external systems call. It runs outside
// the dashboard's auth, so the per-webhook token is the whole gate.
func (s *Server) handleWebhookIncoming(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "POST, PUT or GET")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	id := strings.Trim(strings.TrimPrefix(r.URL.Path, webhookIncomingPrefix), "/")
	hook, ok, _ := ms.WebhookByID(r.Context(), id)
	// Same answer for "no such webhook" and "wrong token": a caller probing ids
	// learns nothing from the difference.
	if !ok || !webhookTokenOK(r, hook.Token) {
		writeErr(w, http.StatusUnauthorized, "unknown webhook or bad token")
		return
	}
	if !hook.Enabled {
		writeErr(w, http.StatusForbidden, "webhook disabled")
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		ms.WebhookRecordCall(r.Context(), hook.ID, "payload too large")
		writeErr(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	message := composeWebhookMessage(hook.Prompt, string(raw), r)
	if strings.TrimSpace(message) == "" {
		ms.WebhookRecordCall(r.Context(), hook.ID, "empty payload and prompt")
		writeErr(w, http.StatusBadRequest, "nothing to send: empty payload and empty prompt")
		return
	}

	if hook.Respond {
		ctx, cancel := context.WithTimeout(r.Context(), syncWebhookTimeout)
		defer cancel()
		resp, err := s.runWebhook(ctx, hook, message)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]string{"response": resp, "session": webhookSession(hook)})
		return
	}

	// Detached: an agent turn routinely outlives the sender's timeout, and most
	// senders discard the body anyway. Deliberately NOT r.Context() — that dies
	// when this response is written, which would cancel the run instantly.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), asyncWebhookTimeout)
		defer cancel()
		if _, err := s.runWebhook(ctx, hook, message); err != nil {
			log.Printf("[webhook %s] %v", hook.ID, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": "accepted", "session": webhookSession(hook)})
}

// webhookTokenOK accepts the token from a header or the query string — senders
// differ in what they can set, and a URL-only token is often all a device offers.
func webhookTokenOK(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got := r.Header.Get("X-Prism-Token")
	if got == "" {
		got = bearerToken(r)
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// webhookCallerContext resolves the owner of a webhook ("u<id>" scope) to a
// real user record and builds their standard caller context. Falls back to the
// unrestricted context only when there is no user to resolve.
func (s *Server) webhookCallerContext(ctx context.Context, hook memory.WebhookRow, session string) CallerContext {
	ms := s.store()
	if ms == nil || !strings.HasPrefix(hook.Scope, "u") {
		return trustedCallerContext()
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(hook.Scope, "u"), 10, 64)
	if err != nil || id <= 0 {
		return trustedCallerContext()
	}
	u, err := ms.GetUserByID(ctx, id)
	if err != nil || u == nil {
		// Owner gone: a webhook must not outlive its user's permissions.
		return CallerContext{Guard: func(string, map[string]interface{}) error {
			return fmt.Errorf("webhook owner no longer exists")
		}, MultiUser: s.cfg.MultiUser}
	}
	return s.callerContextForUser(ctx, u, session)
}

// webhookSession is the chat session the turn runs in: the configured one, or a
// dedicated per-webhook session so an automated feed never lands in a human's
// conversation.
func webhookSession(h memory.WebhookRow) string {
	if s := strings.TrimSpace(h.SessionID); s != "" {
		return s
	}
	return "webhook-" + h.ID
}

// composeWebhookMessage substitutes the payload into the prompt. JSON is
// pretty-printed first: an agent reads an indented object far more reliably than
// one long line, and most senders post minified JSON.
func composeWebhookMessage(prompt, body string, r *http.Request) string {
	content := strings.TrimSpace(body)
	if content == "" {
		// A GET trigger carries its payload in the query string, if anywhere.
		if q := r.URL.RawQuery; q != "" {
			content = strings.TrimSpace(strings.ReplaceAll(q, "&", "\n"))
		}
	}
	if pretty := prettyJSON(content); pretty != "" {
		content = pretty
	}
	prompt = strings.TrimSpace(prompt)
	switch {
	case prompt == "":
		return content
	case strings.Contains(prompt, contentPlaceholder):
		return strings.ReplaceAll(prompt, contentPlaceholder, content)
	default:
		return prompt + "\n\n" + content
	}
}

func prettyJSON(s string) string {
	var v interface{}
	if json.Unmarshal([]byte(s), &v) != nil {
		return ""
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// runWebhook executes the agent turn and optionally pushes the answer to a
// channel. The turn runs with the caller context of the webhook's OWNER — the
// user whose scope holds it — so their tool policy, RAG/MCP scope and hidden
// tools apply exactly as if they had typed the message themselves. A webhook
// in the "global" scope (single-user, or created by the service identity) is
// unrestricted, as before.
func (s *Server) runWebhook(ctx context.Context, hook memory.WebhookRow, message string) (string, error) {
	session := webhookSession(hook)
	resp, err := s.runHeadlessChat(ctx, session, message, hook.Model, s.webhookCallerContext(ctx, hook, session))
	if ms := s.store(); ms != nil {
		status := "ok"
		if err != nil {
			status = "error: " + err.Error()
		}
		ms.WebhookRecordCall(context.Background(), hook.ID, status)
		// Surface the fire in the activity feed so a human can see what their
		// agent did on an inbound trigger, not just the webhook's own LastCallAt.
		ms.AddUsage(context.Background(), 0, session, "webhook", hook.Name, 1,
			map[string]interface{}{"id": hook.ID, "status": status})
	}
	if err != nil {
		return "", fmt.Errorf("webhook %s: %w", hook.ID, err)
	}
	if hook.Deliver != "" && strings.TrimSpace(resp) != "" {
		if derr := s.deliverToChannelSession(hook.Deliver, session, resp); derr != nil {
			log.Printf("[webhook %s] deliver to %s: %v", hook.ID, hook.Deliver, derr)
		}
	}
	return resp, nil
}
