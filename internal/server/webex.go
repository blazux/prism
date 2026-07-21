package server

// Webex bridge via an outbound WebSocket (WDM "Mercury" device registration) —
// no public URL / webhook needed, mirroring the Telegram and Slack bridges. The
// bot lives in group spaces and only answers when @mentioned; in 1:1 spaces it
// answers everything.
//
// Unlike Telegram/Slack (single trusted owner), Webex is multi-user: each space
// member can talk to the bot. So instead of an owner lock, every tool the agent
// tries to call is authorized against three per-sender allowlists (by email):
//   - webex_perm_rag_read   → may query the knowledge base
//   - webex_perm_rag_write  → may add to / delete from the knowledge base
//   - webex_perm_tools      → may trigger any other tool call
// "*" in a list means "everyone in the space". Enforcement happens in the tool
// executor via agent.ToolGuard (see guardFor); a blocked call is reported to the
// model, which relays the refusal to the user.
//
// Message flow: the Mercury socket only signals that an activity happened (the
// payload body is encrypted). We take the activity UUID, turn it into a public
// Webex message id, and fetch the plaintext over the REST API with the bot
// token — the same trick the Python webex_bot library uses.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"prism/internal/agent"
	"prism/internal/memory"
)

const (
	webexTokenSecret    = "webex_bot_token"
	webexPermRAGReadKey = "webex_perm_rag_read"
	webexPermRAGWrite   = "webex_perm_rag_write"
	webexPermToolsKey   = "webex_perm_tools"
	webexOwnerRoomKey   = "webex_owner_room" // room used for cron/API deliveries (SendToOwner)

	webexAPI    = "https://webexapis.com/v1"
	webexWDM    = "https://wdm-a.wbx2.com/wdm/api/v1/devices"
	webexMsgMax = 7000 // Webex hard limit is ~7439 chars per message
)

// webexKey namespaces a Webex config/secret key to a specific group, so each
// group runs its own bot + permission lists (e.g. "webex_bot_token:g3").
func webexKey(base string, groupID int64) string {
	return fmt.Sprintf("%s:g%d", base, groupID)
}

// A webexChannel is one bot connection bound to a single group. Its token,
// permission lists and shared-agent config are all read per-group; the manager
// (webexManager) spawns one per group that has a token configured.
type webexChannel struct {
	s       *Server
	groupID int64
	botID   string // set at the start of each connection; used to ignore own posts + detect mentions
	botName string
	wsMu    sync.Mutex // serializes JSON writes (pings, acks) on the Mercury socket
}

func (c *webexChannel) Name() string { return "webex" }

func (c *webexChannel) store() *memory.Store {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	return c.s.memStore
}

func (c *webexChannel) token() string {
	ms := c.store()
	if ms == nil {
		return ""
	}
	v, _, _ := ms.GetSecret(context.Background(), webexKey(webexTokenSecret, c.groupID))
	return strings.TrimSpace(v)
}

func (c *webexChannel) conf(key string) string {
	ms := c.store()
	if ms == nil {
		return ""
	}
	v, _, _ := ms.GetConfig(context.Background(), webexKey(key, c.groupID))
	return v
}

func (c *webexChannel) Configured() bool { return c.token() != "" }

// ─── manager: one bot connection per group with a token ─────────────────────────

// webexManager is the registered Channel. It fans out to one webexChannel per
// group that has a bot token configured, so every group runs its own Webex bot
// wired to its own shared agent. Reconfigured on save via startChannels().
type webexManager struct{ s *Server }

func (m *webexManager) Name() string { return "webex" }

// groupsWithToken lists the ids of groups that have a Webex bot token set.
func (m *webexManager) groupsWithToken() []int64 {
	ms := m.s.store()
	if ms == nil {
		return nil
	}
	groups, err := ms.ListGroups(context.Background())
	if err != nil {
		return nil
	}
	var out []int64
	for _, g := range groups {
		v, _, _ := ms.GetSecret(context.Background(), webexKey(webexTokenSecret, g.ID))
		if strings.TrimSpace(v) != "" {
			out = append(out, g.ID)
		}
	}
	return out
}

func (m *webexManager) Configured() bool { return len(m.groupsWithToken()) > 0 }

func (m *webexManager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, gid := range m.groupsWithToken() {
		wg.Add(1)
		ch := &webexChannel{s: m.s, groupID: gid}
		go func(c *webexChannel) {
			defer wg.Done()
			log.Printf("[webex-g%d] bot started", c.groupID)
			c.Run(ctx)
		}(ch)
	}
	wg.Wait()
}

// SendToOwner backs `deliver:"webex"` (cron jobs, POST /api/chat). Webex is
// per-group, so the destination is the announcement room of the single group
// that has one configured. With several such groups the target is ambiguous and
// we refuse rather than broadcast into the wrong space — name the group instead.
func (m *webexManager) SendToOwner(text string) error {
	var targets []*webexChannel
	for _, gid := range m.groupsWithToken() {
		c := &webexChannel{s: m.s, groupID: gid}
		if strings.TrimSpace(c.conf(webexOwnerRoomKey)) != "" {
			targets = append(targets, c)
		}
	}
	switch len(targets) {
	case 0:
		return fmt.Errorf("no webex announcement room configured (set it in the Webex config)")
	case 1:
		return targets[0].SendToOwner(text)
	default:
		ids := make([]string, 0, len(targets))
		for _, c := range targets {
			ids = append(ids, fmt.Sprintf("g%d", c.groupID))
		}
		return fmt.Errorf("several groups have an announcement room (%s) — ambiguous target", strings.Join(ids, ", "))
	}
}

// ─── receive loop ─────────────────────────────────────────────────────────────

func (c *webexChannel) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.runOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[webex] connection error: %v — reconnecting in 5s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (c *webexChannel) runOnce(ctx context.Context) error {
	token := c.token()
	if token == "" {
		return fmt.Errorf("not configured")
	}

	// Bot identity — to ignore its own posts and detect @mentions.
	me, err := c.getPerson(ctx, token, "me")
	if err != nil {
		return fmt.Errorf("GET /people/me: %w", err)
	}
	c.botID = me.ID
	c.botName = me.DisplayName

	// Register a Mercury device to obtain a WebSocket URL (no inbound webhook).
	wsURL, err := c.registerDevice(ctx, token)
	if err != nil {
		return fmt.Errorf("device registration: %w", err)
	}

	// Mercury only delivers events when the socket is opened with the right
	// query parameters — most importantly outboundWireFormat=text and
	// bufferStates=true. Without them the socket connects and authorizes fine
	// but receives ZERO frames (observed empirically; params match the working
	// Go reference implementation and the Webex JS SDK).
	if u, perr := url.Parse(wsURL); perr == nil {
		q := u.Query()
		q.Set("outboundWireFormat", "text")
		q.Set("bufferStates", "true")
		q.Set("aliasHttpStatus", "true")
		q.Set("clientTimestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
		u.RawQuery = q.Encode()
		wsURL = u.String()
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, hdr)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)

	// Authorize the socket.
	authFrame := map[string]interface{}{
		"id":   uuid4(),
		"type": "authorization",
		"data": map[string]string{"token": "Bearer " + token},
	}
	if err := conn.WriteJSON(authFrame); err != nil {
		return fmt.Errorf("ws auth: %w", err)
	}
	log.Printf("[webex-g%d] connected as %q", c.groupID, c.botName)

	// Close the socket when the context is cancelled so ReadMessage returns.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Keep the Mercury socket alive with application-level JSON pings — Mercury
	// speaks {"type":"ping"} text messages (answered by {"type":"pong"}), not
	// WebSocket control frames (matches the reference implementations).
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ping, _ := json.Marshal(map[string]string{"id": uuid4(), "type": "ping"})
				c.wsMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, ping)
				c.wsMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		c.handleFrame(ctx, conn, token, data)
	}
}

// handleFrame parses one Mercury event; if it's a new message activity, fetches
// the plaintext, acks it (so Mercury doesn't redeliver) and dispatches it.
func (c *webexChannel) handleFrame(ctx context.Context, conn *websocket.Conn, token string, data []byte) {
	var f struct {
		ID   string `json:"id"` // Mercury envelope id — what acks reference
		Data struct {
			EventType string `json:"eventType"`
			Activity  struct {
				ID     string `json:"id"`
				Verb   string `json:"verb"`
				Target struct {
					ID  string `json:"id"`
					URL string `json:"url"` // conversation service URL on the org's home cluster
				} `json:"target"`
			} `json:"activity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	if f.Data.EventType != "conversation.activity" {
		return
	}
	// Ack every activity envelope right away (by its Mercury envelope id) so the
	// buffer marks it delivered and won't replay it — matches the reference impl.
	if f.ID != "" {
		ack, _ := json.Marshal(map[string]string{"type": "ack", "messageId": f.ID})
		c.wsMu.Lock()
		_ = conn.WriteMessage(websocket.TextMessage, ack)
		c.wsMu.Unlock()
	}
	if f.Data.Activity.Verb != "post" && f.Data.Activity.Verb != "share" {
		return
	}
	if f.Data.Activity.ID == "" {
		return
	}

	// Resolve the activity's PUBLIC message id via the conversation service on
	// the org's home cluster (the activity carries its URL). Building the hydra
	// id locally as ciscospark://us/MESSAGE/<uuid> 404s for orgs on non-default
	// clusters (e.g. urn:TEAM:us-west-2_r) — webex_bot resolves it the same way
	// "to geo-locate the correct DC".
	publicID := c.resolvePublicMessageID(ctx, token, f.Data.Activity.Target.URL, f.Data.Activity.Target.ID, f.Data.Activity.ID)
	if publicID == "" {
		publicID = webexPublicID("MESSAGE", f.Data.Activity.ID) // best-effort fallback
	}
	msg, err := c.getMessage(ctx, token, publicID)
	if err != nil {
		log.Printf("[webex] fetch message: %v", err)
		return
	}
	c.handleMessage(ctx, msg)
}

// resolvePublicMessageID asks the conversation service (home-cluster URL taken
// from the activity itself) for the message's public/base64 id.
func (c *webexChannel) resolvePublicMessageID(ctx context.Context, token, targetURL, targetID, activityID string) string {
	if targetURL == "" || targetID == "" || activityID == "" {
		return ""
	}
	msgURL := strings.Replace(targetURL, "conversations/"+targetID, "messages/"+activityID, 1)
	if msgURL == targetURL {
		return ""
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", msgURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		log.Printf("[webex-g%d] conversation message lookup %s: status %d", c.groupID, msgURL, resp.StatusCode)
		return ""
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return out.ID
}

func (c *webexChannel) handleMessage(ctx context.Context, m webexMessage) {
	// Never react to our own posts (would loop forever).
	if m.PersonID == c.botID {
		return
	}
	// Group spaces: only respond when explicitly @mentioned.
	if m.RoomType == "group" && !contains(m.MentionedPeople, c.botID) {
		return
	}
	text := stripLeadingMention(m.Text, c.botName)

	// Pull in any attachments — a file-only message has empty text, so this must
	// run before the empty guard below.
	if len(m.Files) > 0 {
		text = c.s.attachWebexFiles(ctx, c.token(), m.Files, text)
	}
	if strings.TrimSpace(text) == "" {
		return
	}

	ms := c.store()
	if ms == nil {
		return
	}
	// The Webex bot IS this group's shared agent: same name/prompt/model as the
	// in-app room agent, and the same group RAG scope.
	cfg, _ := ms.GetRoomConfig(ctx, c.groupID)

	// Isolated history per space, namespaced to the group.
	sessionID := fmt.Sprintf("webex-g%d-%s", c.groupID, sanitizeSessionID(m.RoomID))

	// Apply the group admin's configured shared-agent system prompt for this space.
	if cfg.AgentPrompt != "" {
		ms.SetConfig(ctx, memory.KeyPersonality+"_"+sessionID, cfg.AgentPrompt)
	}

	// Two gates in series: the group's tool policy AND the per-sender Webex
	// allowlist. A tool call must pass both.
	cc := c.s.callerContextForGroup(ctx, c.groupID).withSenderGate(c.guardFor(m.PersonEmail))

	ms.AddUsage(ctx, 0, sessionID, "channel_msg", "webex", 1, nil)
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := c.s.runHeadlessChat(runCtx, sessionID, text, cfg.AgentModel, cc)
	if err != nil {
		log.Printf("[webex-g%d] chat: %v", c.groupID, err)
		c.postMarkdown(ctx, m.RoomID, "⚠️ Sorry, something went wrong.")
		return
	}
	if strings.TrimSpace(resp) == "" {
		resp = "(no response)"
	}
	c.postMarkdown(ctx, m.RoomID, resp)
}

// ─── per-sender authorization ──────────────────────────────────────────────────

// guardFor builds the tool authorization gate for a given Webex sender based on
// the three permission allowlists.
func (c *webexChannel) guardFor(email string) agent.ToolGuard {
	read := parseAllowlist(c.conf(webexPermRAGReadKey))
	write := parseAllowlist(c.conf(webexPermRAGWrite))
	tools := parseAllowlist(c.conf(webexPermToolsKey))
	email = strings.ToLower(strings.TrimSpace(email))

	return func(name string, args map[string]interface{}) error {
		var set map[string]bool
		switch agent.ToolPermission(name, args) {
		case agent.PermRAGRead:
			set = read
		case agent.PermRAGWrite:
			set = write
		default:
			set = tools
		}
		if set["*"] || set[email] {
			return nil
		}
		return fmt.Errorf("permission denied: %s is not allowed to %s", email, agent.ToolPermission(name, args).Action())
	}
}


// parseAllowlist splits a comma/newline/space separated list of emails into a
// lookup set (lowercased). "*" means everyone.
func parseAllowlist(s string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == ';'
	}) {
		f = strings.ToLower(strings.TrimSpace(f))
		if f != "" {
			set[f] = true
		}
	}
	return set
}

// ─── deliveries (Channel interface) ─────────────────────────────────────────────

// SendToOwner posts to the configured owner room (cron → Webex, /api/chat
// deliver). Webex has no single owner, so an explicit room id is required.
func (c *webexChannel) SendToOwner(text string) error {
	if c.token() == "" {
		return fmt.Errorf("webex not configured")
	}
	room := strings.TrimSpace(c.conf(webexOwnerRoomKey))
	if room == "" {
		return fmt.Errorf("no webex owner room set — configure webex_owner_room")
	}
	return c.postMarkdown(context.Background(), room, text)
}

// ─── Webex REST helpers ─────────────────────────────────────────────────────────

type webexPerson struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type webexMessage struct {
	ID              string   `json:"id"`
	RoomID          string   `json:"roomId"`
	RoomType        string   `json:"roomType"` // "group" | "direct"
	Text            string   `json:"text"`
	PersonID        string   `json:"personId"`
	PersonEmail     string   `json:"personEmail"`
	MentionedPeople []string `json:"mentionedPeople"`
	Files           []string `json:"files"` // content URLs; fetched with the bot token
}

func (c *webexChannel) getPerson(ctx context.Context, token, id string) (webexPerson, error) {
	var p webexPerson
	err := c.apiGet(ctx, token, "/people/"+id, &p)
	return p, err
}

func (c *webexChannel) getMessage(ctx context.Context, token, id string) (webexMessage, error) {
	var m webexMessage
	err := c.apiGet(ctx, token, "/messages/"+id, &m)
	return m, err
}

// webexRoom is one entry of GET /v1/rooms — the rooms the authenticated identity
// belongs to. With a bot token that is exactly the rooms the bot was added to.
type webexRoom struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"` // "group" | "direct"
}

// listGroupRooms returns the bot's GROUP rooms, newest activity first. Direct
// (1:1) conversations are filtered out: an announcement room is always a space.
func (c *webexChannel) listGroupRooms(ctx context.Context) ([]webexRoom, error) {
	token := c.token()
	if token == "" {
		return nil, fmt.Errorf("webex not configured")
	}
	var page struct {
		Items []webexRoom `json:"items"`
	}
	// max=1000 avoids paging: a bot in more than a thousand spaces is not our case.
	if err := c.apiGet(ctx, token, "/rooms?type=group&max=1000", &page); err != nil {
		return nil, err
	}
	return filterGroupRooms(page.Items), nil
}

// filterGroupRooms keeps only Webex *spaces*. The API is asked for type=group,
// but never trust it blindly: a direct (1:1) room slipping through would let a
// scheduled job broadcast into somebody's private conversation with the bot.
func filterGroupRooms(items []webexRoom) []webexRoom {
	rooms := make([]webexRoom, 0, len(items))
	for _, r := range items {
		if r.Type == "group" {
			rooms = append(rooms, r)
		}
	}
	return rooms
}

func (c *webexChannel) apiGet(ctx context.Context, token, path string, out interface{}) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", webexAPI+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webex API %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// postMarkdown sends a markdown message, splitting on Webex's length limit.
func (c *webexChannel) postMarkdown(ctx context.Context, roomID, text string) error {
	token := c.token()
	if token == "" {
		return fmt.Errorf("webex not configured")
	}
	runes := []rune(text)
	for len(runes) > 0 {
		n := len(runes)
		if n > webexMsgMax {
			n = webexMsgMax
		}
		chunk := string(runes[:n])
		runes = runes[n:]
		body, _ := json.Marshal(map[string]string{"roomId": roomID, "markdown": chunk})
		req, _ := http.NewRequestWithContext(ctx, "POST", webexAPI+"/messages", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("webex post: status %d", resp.StatusCode)
		}
	}
	return nil
}

// wdmBaseURL discovers the bot's home WDM cluster from the u2c service catalog.
// This is CRITICAL: WDM URLs are per-region (wdm-a, wdm-r, …) and a registration
// on the wrong cluster succeeds with a valid-looking device whose Mercury socket
// authorizes, pongs — and never receives a single conversation event, because
// fanout happens on the bot's home cluster. webex_bot does the same discovery.
func (c *webexChannel) wdmBaseURL(ctx context.Context, token string) string {
	fallback := webexWDM
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://u2c.wbx2.com/u2c/api/v1/catalog?format=hostmap", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fallback
	}
	var cat struct {
		ServiceLinks map[string]string `json:"serviceLinks"`
	}
	if json.NewDecoder(resp.Body).Decode(&cat) != nil {
		return fallback
	}
	wdm := strings.TrimRight(cat.ServiceLinks["wdm"], "/")
	if wdm == "" {
		return fallback
	}
	// serviceLinks.wdm is the API root (…/wdm/api/v1); devices live under /devices.
	if !strings.HasSuffix(wdm, "/devices") {
		wdm += "/devices"
	}
	log.Printf("[webex-g%d] WDM cluster: %s", c.groupID, wdm)
	return wdm
}

// deleteStaleDevices removes previously-registered Mercury devices for this bot
// on its home cluster (wdm = the discovered devices URL). Webex accumulates one
// per restart; keeping them around both wastes device slots and risks events
// routing to a dead registration.
func (c *webexChannel) deleteStaleDevices(ctx context.Context, wdm, token string) {
	req, _ := http.NewRequestWithContext(ctx, "GET", wdm, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return
	}
	var list struct {
		Devices []struct {
			URL string `json:"url"`
		} `json:"devices"`
	}
	if json.NewDecoder(resp.Body).Decode(&list) != nil {
		return
	}
	for _, d := range list.Devices {
		if d.URL == "" {
			continue
		}
		dreq, _ := http.NewRequestWithContext(ctx, "DELETE", d.URL, nil)
		dreq.Header.Set("Authorization", "Bearer "+token)
		if dresp, e := http.DefaultClient.Do(dreq); e == nil {
			dresp.Body.Close()
		}
	}
	if len(list.Devices) > 0 {
		log.Printf("[webex-g%d] cleaned %d stale Mercury device(s)", c.groupID, len(list.Devices))
	}
}

// registerDevice creates a Mercury device on the bot's HOME cluster (u2c
// discovery) and returns its WebSocket URL.
func (c *webexChannel) registerDevice(ctx context.Context, token string) (string, error) {
	wdm := c.wdmBaseURL(ctx, token)
	c.deleteStaleDevices(ctx, wdm, token)
	payload, _ := json.Marshal(map[string]string{
		"deviceName":     "spectrum-go",
		"deviceType":     "DESKTOP",
		"localizedModel": "go",
		"model":          "go",
		"name":           "spectrum",
		"systemName":     "spectrum",
		"systemVersion":  "0.1",
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", wdm, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("WDM status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var dev struct {
		WebSocketURL string `json:"webSocketUrl"`
	}
	if err := json.Unmarshal(body, &dev); err != nil {
		return "", err
	}
	if dev.WebSocketURL == "" {
		return "", fmt.Errorf("no webSocketUrl in WDM response")
	}
	return dev.WebSocketURL, nil
}

// ─── small helpers ──────────────────────────────────────────────────────────────

// webexPublicID converts an internal Webex UUID into its public "hydra" id
// (base64 of ciscospark://us/<TYPE>/<uuid>), the form the REST API accepts.
func webexPublicID(kind, uuid string) string {
	return base64.StdEncoding.EncodeToString([]byte("ciscospark://us/" + kind + "/" + uuid))
}

func uuid4() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// stripLeadingMention removes the bot's display name if the message text begins
// with it (Webex leaves the mention as plain text in the `text` field).
func stripLeadingMention(text, botName string) string {
	t := strings.TrimSpace(text)
	if botName != "" && strings.HasPrefix(t, botName) {
		t = strings.TrimSpace(t[len(botName):])
	}
	return t
}

// ─── config endpoint ────────────────────────────────────────────────────────────

// GET  /api/webex/config?group=<id> → this group's Webex config (token presence
//   - per-sender allowlists). Open to group members.
//
// POST /api/webex/config?group=<id> → set the token / allowlists, or
//
//	{disconnect:true} to clear. Group admins (or global admins) only.
func (s *Server) handleWebexConfig(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	ms := s.store()
	if ms == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	wc := &webexChannel{s: s, groupID: groupID}
	switch r.Method {
	case http.MethodGet:
		if !u.IsGlobalAdmin() && !s.userInGroup(r.Context(), u.ID, groupID) {
			writeErr(w, http.StatusForbidden, "not a member")
			return
		}
		writeJSON(w, map[string]interface{}{
			"configured": wc.Configured(),
			"ragRead":    wc.conf(webexPermRAGReadKey),
			"ragWrite":   wc.conf(webexPermRAGWrite),
			"tools":      wc.conf(webexPermToolsKey),
			// The announcement room: where cron jobs and `deliver:"webex"` post.
			// It used to be missing here while the page happily sent it back on
			// save — so it could never be stored, and SendToOwner always failed.
			"ownerRoom": wc.conf(webexOwnerRoomKey),
		})
	case http.MethodPost:
		if !s.isGroupAdminOf(r.Context(), u, groupID) {
			writeErr(w, http.StatusForbidden, "group admin only")
			return
		}
		var b struct {
			Token      *string `json:"token"`
			RagRead    *string `json:"ragRead"`
			RagWrite   *string `json:"ragWrite"`
			Tools      *string `json:"tools"`
			OwnerRoom  *string `json:"ownerRoom"`
			Disconnect bool    `json:"disconnect"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, http.StatusBadRequest, "bad body")
			return
		}
		if b.Disconnect {
			ms.SetSecret(r.Context(), webexKey(webexTokenSecret, groupID), "")
			s.startChannels()
			writeJSON(w, map[string]interface{}{"ok": true, "configured": false})
			return
		}
		if b.Token != nil {
			ms.SetSecret(r.Context(), webexKey(webexTokenSecret, groupID), strings.TrimSpace(*b.Token))
		}
		if b.RagRead != nil {
			ms.SetConfig(r.Context(), webexKey(webexPermRAGReadKey, groupID), strings.TrimSpace(*b.RagRead))
		}
		if b.RagWrite != nil {
			ms.SetConfig(r.Context(), webexKey(webexPermRAGWrite, groupID), strings.TrimSpace(*b.RagWrite))
		}
		if b.Tools != nil {
			ms.SetConfig(r.Context(), webexKey(webexPermToolsKey, groupID), strings.TrimSpace(*b.Tools))
		}
		if b.OwnerRoom != nil {
			ms.SetConfig(r.Context(), webexKey(webexOwnerRoomKey, groupID), strings.TrimSpace(*b.OwnerRoom))
		}
		// Reconnect the group's bot so a new/removed token takes effect.
		s.startChannels()
		writeJSON(w, map[string]interface{}{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleWebexRooms lists the group rooms the bot belongs to, so the admin picks
// the announcement room from a menu instead of pasting an opaque roomId.
// Group admin only: the list reveals the spaces the bot sits in.
func (s *Server) handleWebexRooms(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	groupID, err := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	if u == nil || err != nil || groupID <= 0 {
		writeErr(w, http.StatusBadRequest, "group required")
		return
	}
	if !s.isGroupAdminOf(r.Context(), u, groupID) {
		writeErr(w, http.StatusForbidden, "group admin only")
		return
	}
	wc := &webexChannel{s: s, groupID: groupID}
	rooms, err := wc.listGroupRooms(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{"rooms": rooms})
}

// handleWebexSend posts a raw line to the group's announcement room — the Webex
// twin of /api/telegram/send. It exists because `deliver:"webex"` publishes the
// *model's answer* to a prompt, not a chosen text: asked to "say hello to the
// room", the agent ended up posting "C'est fait, le message a été diffusé".
// A scheduled job that wants to say something exact needs this instead.
func (s *Server) handleWebexSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Text) == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	m, _ := s.channels["webex"].(*webexManager)
	if m == nil {
		http.Error(w, "webex unavailable", http.StatusServiceUnavailable)
		return
	}
	// Same routing as deliver:"webex": the group that has an announcement room.
	if err := m.SendToOwner(b.Text); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// handleWebexPage serves a minimal self-contained config UI at /webex (global
// admin only).
func (s *Server) handleWebexPage(w http.ResponseWriter, r *http.Request) {
	if u := currentUser(r); u != nil && !u.IsGlobalAdmin() {
		http.Redirect(w, r, "/home", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(webexConfigPage))
}

const webexConfigPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Spectrum · Webex</title>
<style>
:root{color-scheme:light dark}
body{font:15px/1.5 system-ui,sans-serif;max-width:640px;margin:2rem auto;padding:0 1rem}
h1{font-size:1.3rem} h2{font-size:1rem;margin:1.6rem 0 .3rem}
label{display:block;font-weight:600;margin-top:.9rem}
.hint{font-weight:400;color:#888;font-size:.85em}
input,textarea{width:100%;box-sizing:border-box;padding:.5rem;margin-top:.25rem;
 border:1px solid #8884;border-radius:6px;background:transparent;color:inherit;font:inherit}
textarea{min-height:3.5rem;resize:vertical}
button{margin-top:1.2rem;padding:.55rem 1.1rem;border:0;border-radius:6px;
 background:#3b82f6;color:#fff;font:inherit;font-weight:600;cursor:pointer}
button.ghost{background:#8883;color:inherit}
#status{margin-left:.8rem;font-weight:600}
.badge{display:inline-block;padding:.1rem .5rem;border-radius:99px;font-size:.8em;font-weight:600}
.on{background:#16a34a33;color:#16a34a} .off{background:#dc262633;color:#dc2626}
</style></head><body>
<h1>Webex bot <span id="state" class="badge off">…</span></h1>
<p class="hint">The bot answers in group spaces when @mentioned, and in 1:1 spaces always.
Permission lists are comma/space/newline-separated emails; <code>*</code> means everyone in the space.</p>

<label>Bot access token <span class="hint">(from developer.webex.com — leave blank to keep current)</span>
<input id="token" type="password" placeholder="••••••••"></label>

<h2>Permissions</h2>
<label>Can query the knowledge base (RAG read)<textarea id="ragRead" placeholder="alice@corp.com, bob@corp.com  — or  *"></textarea></label>
<label>Can modify the knowledge base (RAG write)<textarea id="ragWrite"></textarea></label>
<label>Can trigger tool calls (web, MCP, …)<textarea id="tools"></textarea></label>

<h2>Deliveries</h2>
<label>Owner room ID <span class="hint">(for cron / API pushes; optional)</span>
<input id="ownerRoom" placeholder="Y2lzY29z…"></label>

<div>
<button onclick="save()">Save</button>
<button class="ghost" onclick="disconnect()">Disconnect</button>
<span id="status"></span>
</div>

<script>
const $=id=>document.getElementById(id);
async function load(){
 const r=await fetch('/api/webex/config'); const c=await r.json();
 $('ragRead').value=c.ragRead||''; $('ragWrite').value=c.ragWrite||'';
 $('tools').value=c.tools||''; $('ownerRoom').value=c.ownerRoom||'';
 const s=$('state'); s.textContent=c.configured?'connected':'no token';
 s.className='badge '+(c.configured?'on':'off');
}
async function save(){
 const body={ragRead:$('ragRead').value,ragWrite:$('ragWrite').value,
  tools:$('tools').value,ownerRoom:$('ownerRoom').value};
 if($('token').value.trim())body.token=$('token').value.trim();
 const r=await fetch('/api/webex/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
 $('status').textContent=r.ok?'saved ✓':'error'; $('token').value=''; setTimeout(()=>$('status').textContent='',2500); load();
}
async function disconnect(){
 if(!confirm('Clear the Webex token?'))return;
 await fetch('/api/webex/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({disconnect:true})});
 load();
}
load();
</script></body></html>`

// attachWebexFiles downloads each attachment of an incoming message, ingests it
// into the workspace, and prepends its preamble to the message text. Webex hands
// us opaque content URLs; the real filename comes from Content-Disposition.
func (s *Server) attachWebexFiles(ctx context.Context, token string, urls []string, text string) string {
	var atts []attachment
	for _, u := range urls {
		data, name, err := downloadAttachment(ctx, u, token, "")
		if err != nil {
			log.Printf("[webex] attachment %s: %v", u, err)
			continue
		}
		atts = append(atts, s.ingestAttachment(data, name))
	}
	return prependAttachments(text, atts)
}
