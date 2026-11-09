package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"prism/internal/agent"
	"prism/internal/memory"
)

const teamsIncomingPrefix = "/api/teams/messages/"
const teamsSecretKey = "teams_client_secret"

var teamsGUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type teamsConfig struct {
	AppID             string `json:"appId"`
	TenantID          string `json:"tenantId"`
	RAGRead           string `json:"ragRead"`
	RAGWrite          string `json:"ragWrite"`
	Tools             string `json:"tools"`
	OwnerConversation string `json:"ownerConversation"`
}

func teamsKey(base string, groupID int64) string { return fmt.Sprintf("%s:g%d", base, groupID) }

func (s *Server) teamsConfig(ctx context.Context, groupID int64) (teamsConfig, string, error) {
	var cfg teamsConfig
	ms := s.store()
	if ms == nil {
		return cfg, "", fmt.Errorf("database unavailable")
	}
	// Deleted groups must stop working even if best-effort residue cleanup fails.
	groups, err := ms.ListGroups(ctx)
	if err != nil {
		return cfg, "", err
	}
	found := false
	for _, g := range groups {
		if g.ID == groupID {
			found = true
			break
		}
	}
	if !found {
		return cfg, "", fmt.Errorf("group not found")
	}
	raw, _, err := ms.GetConfig(ctx, teamsKey("teams_config", groupID))
	if err != nil {
		return cfg, "", err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, "", err
		}
	}
	secret, _, err := ms.GetSecret(ctx, teamsKey(teamsSecretKey, groupID))
	return cfg, secret, err
}

func (c teamsConfig) configured(secret string) bool {
	return teamsGUID.MatchString(c.AppID) && teamsGUID.MatchString(c.TenantID) && secret != ""
}
func teamsFingerprint(cfg teamsConfig, secret string) string {
	b, _ := json.Marshal(cfg)
	digest := sha256.Sum256(append(b, []byte(secret)...))
	return hex.EncodeToString(digest[:])
}

type teamsAccount struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	AADID string `json:"aadObjectId,omitempty"`
}
type teamsConversation struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"conversationType,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}
type teamsActivity struct {
	Type         string            `json:"type"`
	ID           string            `json:"id"`
	ChannelID    string            `json:"channelId"`
	ServiceURL   string            `json:"serviceUrl"`
	Text         string            `json:"text"`
	ReplyToID    string            `json:"replyToId,omitempty"`
	From         teamsAccount      `json:"from"`
	Recipient    teamsAccount      `json:"recipient"`
	Conversation teamsConversation `json:"conversation"`
	ChannelData  struct {
		Tenant struct {
			ID string `json:"id"`
		} `json:"tenant"`
	} `json:"channelData"`
	Entities []struct {
		Type      string       `json:"type"`
		Mentioned teamsAccount `json:"mentioned"`
		Text      string       `json:"text"`
	} `json:"entities"`
	Attachments []struct {
		Type    string          `json:"contentType"`
		URL     string          `json:"contentUrl"`
		Name    string          `json:"name"`
		Content json.RawMessage `json:"content"`
	} `json:"attachments,omitempty"`
}

func (a teamsActivity) validFor(cfg teamsConfig) bool {
	tenant := a.ChannelData.Tenant.ID
	if tenant == "" {
		tenant = a.Conversation.TenantID
	}
	return strings.EqualFold(tenant, cfg.TenantID) && (a.Conversation.TenantID == "" || strings.EqualFold(a.Conversation.TenantID, cfg.TenantID)) &&
		(a.ChannelData.Tenant.ID == "" || strings.EqualFold(a.ChannelData.Tenant.ID, cfg.TenantID)) &&
		a.ChannelID == "msteams" && a.ID != "" && len(a.ID) <= 1024 && a.Conversation.ID != "" && len(a.Conversation.ID) <= 2048 &&
		teamsGUID.MatchString(a.From.AADID) && a.From.ID != "" && a.From.ID != a.Recipient.ID && a.Recipient.ID == "28:"+cfg.AppID &&
		(a.Conversation.Type == "personal" || a.Conversation.Type == "groupChat" || a.Conversation.Type == "channel")
}

func (a teamsActivity) addressed() bool {
	if a.Conversation.Type == "personal" {
		return true
	}
	for _, e := range a.Entities {
		if e.Type == "mention" && e.Mentioned.ID == a.Recipient.ID {
			return true
		}
	}
	return false
}

func (a teamsActivity) messageText() string {
	text := a.Text
	for _, e := range a.Entities {
		if e.Type == "mention" && e.Mentioned.ID == a.Recipient.ID && e.Text != "" {
			text = strings.ReplaceAll(text, e.Text, "")
		}
	}
	// Only strip the bot's mention, preserving mentions of human participants.
	return strings.TrimSpace(html.UnescapeString(text))
}

func teamsSessionID(groupID int64, a teamsActivity) string {
	// Teams channel conversation IDs include the thread ID. Never truncate opaque
	// IDs with sanitizeSessionID: shared prefixes would collapse unrelated chats.
	digest := sha256.Sum256([]byte(a.Conversation.ID))
	return fmt.Sprintf("teams-g%d-%x", groupID, digest[:16])
}

func teamsSenderGuard(cfg teamsConfig, aadID string) agent.ToolGuard {
	read, write, tools := parseAllowlist(cfg.RAGRead), parseAllowlist(cfg.RAGWrite), parseAllowlist(cfg.Tools)
	id := strings.ToLower(aadID)
	return func(name string, args map[string]interface{}) error {
		if name == "add_attachment" {
			return fmt.Errorf("Teams file uploads are not configured; include the result in your message instead of attaching a file")
		}
		set := tools
		switch agent.ToolPermission(name, args) {
		case agent.PermRAGRead:
			set = read
		case agent.PermRAGWrite:
			set = write
		}
		if set["*"] || set[id] {
			return nil
		}
		return fmt.Errorf("permission denied: Teams sender %s is not allowed to %s", id, agent.ToolPermission(name, args).Action())
	}
}

type teamsQueued struct {
	activity    teamsActivity
	fingerprint string
}
type teamsWorker struct {
	queue   chan teamsQueued
	cancel  context.CancelFunc
	groupID int64
}
type teamsManager struct {
	s        *Server
	auth     teamsAuthenticator
	configMu sync.Mutex
	mu       sync.Mutex
	workers  map[string]*teamsWorker
	seen     map[string]time.Time
	tokens   map[string]teamsAccessToken
}
type teamsAccessToken struct {
	value string
	until time.Time
}

func (m *teamsManager) Name() string { return "teams" }
func (m *teamsManager) Configured() bool {
	ms := m.s.store()
	if ms == nil {
		return false
	}
	groups, err := ms.ListGroups(m.s.runtimeContext())
	if err != nil {
		return false
	}
	for _, g := range groups {
		cfg, secret, err := m.s.teamsConfig(m.s.runtimeContext(), g.ID)
		if err == nil && cfg.configured(secret) {
			return true
		}
	}
	return false
}
func (m *teamsManager) Run(ctx context.Context) { <-ctx.Done() } // Events arrive at the authenticated HTTP endpoint.

func (s *Server) teamsManager() *teamsManager { m, _ := s.channels["teams"].(*teamsManager); return m }

func (m *teamsManager) cancelGroup(groupID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, w := range m.workers {
		if w.groupID == groupID {
			w.cancel()
			delete(m.workers, key)
		}
	}
	// Config changes invalidate cached credentials. Receipts still prevent retries.
	m.tokens = nil
}

func (m *teamsManager) dispatch(groupID int64, cfg teamsConfig, secret string, a teamsActivity) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	for key, at := range m.seen {
		if now.Sub(at) > 24*time.Hour {
			delete(m.seen, key)
		}
	}
	receipt := fmt.Sprintf("%d/%s/%s", groupID, a.Conversation.ID, a.ID)
	if _, duplicate := m.seen[receipt]; duplicate {
		return true
	}
	key := teamsSessionID(groupID, a)
	if m.workers == nil {
		m.workers = map[string]*teamsWorker{}
	}
	w := m.workers[key]
	if w == nil {
		if len(m.workers) >= 64 {
			return false
		}
		ctx, cancel := context.WithCancel(m.s.runtimeContext())
		w = &teamsWorker{queue: make(chan teamsQueued, 16), cancel: cancel, groupID: groupID}
		m.workers[key] = w
		if !m.s.background(func() { m.work(ctx, key, w) }) {
			cancel()
			delete(m.workers, key)
			return false
		}
	}
	if len(m.seen) >= 8192 {
		return false
	}
	select {
	case w.queue <- teamsQueued{a, teamsFingerprint(cfg, secret)}:
		m.seen[receipt] = now
		return true
	default:
		return false
	}
}

func (m *teamsManager) work(ctx context.Context, key string, w *teamsWorker) {
	defer func() {
		w.cancel()
		m.mu.Lock()
		if m.workers[key] == w {
			delete(m.workers, key)
		}
		m.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case queued := <-w.queue:
			if ctx.Err() != nil {
				return
			}
			m.handle(ctx, w.groupID, queued)
		case <-time.After(time.Minute):
			// Remove only while holding the same lock used by dispatch, so an
			// arrival cannot be acknowledged into a queue being abandoned.
			m.mu.Lock()
			if len(w.queue) == 0 {
				if m.workers[key] == w {
					delete(m.workers, key)
				}
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
		}
	}
}

func (s *Server) handleTeamsIncoming(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if !s.cfg.MultiUser {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, teamsIncomingPrefix)
	groupID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || groupID <= 0 || id != strconv.FormatInt(groupID, 10) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	m := s.teamsManager()
	if m == nil {
		writeErr(w, http.StatusServiceUnavailable, "Teams unavailable")
		return
	}
	cfg, secret, err := s.teamsConfig(r.Context(), groupID)
	if err != nil || !cfg.configured(secret) {
		writeErr(w, http.StatusNotFound, "bot not configured")
		return
	}
	var a teamsActivity
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&a); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid activity")
		return
	}
	if err := m.auth.verify(r.Context(), r.Header.Get("Authorization"), cfg.AppID, a.ServiceURL); err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid Microsoft authentication")
		return
	}
	// Lifecycle/non-message activities don't necessarily have a human sender.
	// They have no side effects; only authenticated messages enter an agent run.
	if a.Type != "message" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !a.validFor(cfg) {
		writeErr(w, http.StatusForbidden, "activity is not allowed for this group")
		return
	}
	if !a.addressed() {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !m.dispatch(groupID, cfg, secret, a) {
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusTooManyRequests, "Teams inbox full; retry later")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (m *teamsManager) handle(ctx context.Context, groupID int64, q teamsQueued) {
	ctx, cancel := context.WithTimeout(ctx, channelTurnTimeout)
	defer cancel()
	cfg, secret, err := m.s.teamsConfig(ctx, groupID)
	if err != nil || !cfg.configured(secret) || teamsFingerprint(cfg, secret) != q.fingerprint {
		return
	}
	a := q.activity
	m.rememberConversation(ctx, groupID, cfg, a)
	text := a.messageText()
	text, images := m.attachments(ctx, cfg, secret, a, text)
	if strings.TrimSpace(text) == "" {
		return
	}
	ms := m.s.store()
	if ms == nil {
		return
	}
	room, err := ms.GetRoomConfig(ctx, groupID)
	if err != nil {
		log.Printf("[teams-g%d] room config: %v", groupID, err)
		return
	}
	session := teamsSessionID(groupID, a)
	// Always set, including an empty prompt, to clear a previous group prompt.
	if err := ms.SetConfig(ctx, memory.KeyPersonality+"_"+session, room.AgentPrompt); err != nil {
		log.Printf("[teams-g%d] prompt: %v", groupID, err)
		return
	}
	cc := m.s.callerContextForGroup(ctx, groupID).withSenderGate(teamsSenderGuard(cfg, a.From.AADID))
	// Keep denied tools out of the catalogue as well as refusing their execution.
	for _, tool := range agent.ToolDefinitions {
		if cc.Guard(tool.Function.Name, nil) != nil {
			cc.HiddenTools[tool.Function.Name] = true
		}
	}
	text = fmt.Sprintf("[%s; Microsoft Entra user ID: %s]: %s", a.From.Name, a.From.AADID, text)
	ms.AddUsage(ctx, 0, session, "channel_msg", "teams", 1, nil)
	resp, err := m.s.runHeadlessChatImagesTap(ctx, session, text, room.AgentModel, images, cc, nil, roomLimits(room))
	if ctx.Err() == context.Canceled {
		return
	}
	if err != nil {
		log.Printf("[teams-g%d] chat: %v", groupID, err)
		resp = channelFailureMessage(err)
	}
	if strings.TrimSpace(resp) == "" {
		resp = "(no response)"
	}
	// A completed/expired model call must still be able to send its error reply.
	postCtx, stop := context.WithTimeout(m.s.runtimeContext(), 30*time.Second)
	defer stop()
	current, currentSecret, err := m.s.teamsConfig(postCtx, groupID)
	if err != nil || teamsFingerprint(current, currentSecret) != q.fingerprint {
		return
	}
	if err := m.reply(postCtx, cfg, secret, a, resp); err != nil {
		log.Printf("[teams-g%d] send: %v", groupID, err)
	}
}

func (m *teamsManager) accessToken(ctx context.Context, cfg teamsConfig, secret string) (string, error) {
	key := teamsFingerprint(cfg, secret)
	m.mu.Lock()
	cached := m.tokens[key]
	m.mu.Unlock()
	if cached.value != "" && time.Now().Before(cached.until) {
		return cached.value, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {cfg.AppID}, "client_secret": {secret}, "scope": {"https://api.botframework.com/.default"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/"+cfg.TenantID+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := teamsHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Microsoft token request returned HTTP %d", resp.StatusCode)
	}
	var data struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return "", err
	}
	if data.Token == "" || data.Expires <= 60 || data.Expires > 86400 {
		return "", fmt.Errorf("invalid Microsoft access token response")
	}
	m.mu.Lock()
	if m.tokens == nil {
		m.tokens = map[string]teamsAccessToken{}
	}
	m.tokens[key] = teamsAccessToken{data.Token, time.Now().Add(time.Duration(data.Expires-60) * time.Second)}
	m.mu.Unlock()
	return data.Token, nil
}

func (m *teamsManager) reply(ctx context.Context, cfg teamsConfig, secret string, a teamsActivity, text string) error {
	if !validTeamsServiceURL(a.ServiceURL) {
		return fmt.Errorf("invalid Teams callback URL")
	}
	token, err := m.accessToken(ctx, cfg, secret)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(a.ServiceURL, "/") + "/v3/conversations/" + url.PathEscape(a.Conversation.ID) + "/activities/" + url.PathEscape(a.ID)
	runes := []rune(text)
	for len(runes) > 0 {
		n := len(runes)
		if n > 5000 {
			n = 5000
		}
		payload := map[string]any{"type": "message", "text": string(runes[:n]), "textFormat": "markdown", "from": a.Recipient, "conversation": a.Conversation, "replyToId": a.ID}
		if a.From.ID != "" {
			payload["recipient"] = a.From
		}
		body, _ := json.Marshal(payload)
		runes = runes[n:]
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := teamsHTTP.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("Teams send returned HTTP %d", resp.StatusCode)
		}
	}
	return nil
}
