package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const teamsTestApp = "11111111-1111-1111-1111-111111111111"
const teamsTestTenant = "22222222-2222-2222-2222-222222222222"
const teamsTestUser = "33333333-3333-3333-3333-333333333333"

func teamsTestActivity() teamsActivity {
	return teamsActivity{Type: "message", ID: "msg-1", ChannelID: "msteams", ServiceURL: "https://smba.trafficmanager.net/emea/", Text: "<at>Prism</at> hello", From: teamsAccount{ID: "29:alice", AADID: teamsTestUser, Name: "Alice"}, Recipient: teamsAccount{ID: "28:" + teamsTestApp}, Conversation: teamsConversation{ID: "19:channel;messageid=123", Type: "channel", TenantID: teamsTestTenant}}
}

func teamsTestToken(t *testing.T, key *rsa.PrivateKey, mutate func(map[string]any, map[string]any)) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": "test-key"}
	claims := map[string]any{"iss": "https://api.botframework.com", "aud": teamsTestApp, "exp": time.Now().Add(time.Hour).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(), "serviceurl": "https://smba.trafficmanager.net/emea/"}
	if mutate != nil {
		mutate(header, claims)
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	text := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(text))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + text + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestTeamsConnectorAuthentication(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	auth := teamsAuthenticator{keys: map[string]teamsSigningKey{"test-key": {&key.PublicKey, true}}, fetched: time.Now()}
	ctx := context.Background()
	service := "https://smba.trafficmanager.net/emea/"
	if err := auth.verify(ctx, teamsTestToken(t, key, nil), teamsTestApp, service); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"audience", func(h, c map[string]any) { c["aud"] = "another-bot" }},
		{"issuer", func(h, c map[string]any) { c["iss"] = "https://attacker.invalid" }},
		{"expired", func(h, c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{"future", func(h, c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
		{"missing expiry", func(h, c map[string]any) { delete(c, "exp") }},
		{"algorithm confusion", func(h, c map[string]any) { h["alg"] = "HS256" }},
		{"critical extension", func(h, c map[string]any) { h["crit"] = []string{"b64"} }},
		{"unknown key", func(h, c map[string]any) { h["kid"] = "attacker" }},
		{"callback mismatch", func(h, c map[string]any) { c["serviceurl"] = "https://smba.trafficmanager.net/amer/" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if auth.verify(ctx, teamsTestToken(t, key, tc.mutate), teamsTestApp, service) == nil {
				t.Fatal("accepted invalid Connector token")
			}
		})
	}
	badKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	if auth.verify(ctx, teamsTestToken(t, badKey, nil), teamsTestApp, service) == nil {
		t.Fatal("accepted forged signature")
	}
	auth.keys["test-key"] = teamsSigningKey{&key.PublicKey, false}
	if auth.verify(ctx, teamsTestToken(t, key, nil), teamsTestApp, service) == nil {
		t.Fatal("accepted a key not endorsed for Teams")
	}
	for _, raw := range []string{"", "Basic abc", "Bearer a.b.c", "Bearer a.b.c.d"} {
		if auth.verify(ctx, raw, teamsTestApp, service) == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestTeamsAuthenticationDiscoversMicrosoftSigningKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	previous := teamsHTTP
	defer func() { teamsHTTP = previous }()
	requests := 0
	teamsHTTP = &http.Client{Transport: teamsTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var body string
		switch r.URL.String() {
		case teamsOpenIDURL:
			body = `{"issuer":"https://api.botframework.com","jwks_uri":"https://login.botframework.com/v1/.well-known/keys","id_token_signing_alg_values_supported":["RS256"]}`
		case "https://login.botframework.com/v1/.well-known/keys":
			body = fmt.Sprintf(`{"keys":[{"kty":"RSA","use":"sig","kid":"test-key","n":"%s","e":"AQAB","endorsements":["msteams"]}]}`, base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
		default:
			t.Fatalf("unexpected metadata URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	auth := teamsAuthenticator{}
	for i := 0; i < 2; i++ {
		if err := auth.verify(t.Context(), teamsTestToken(t, key, nil), teamsTestApp, "https://smba.trafficmanager.net/emea/"); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 2 {
		t.Fatalf("metadata not cached: requests=%d", requests)
	}
}

func TestTeamsActivityTenantMentionsAndConversationIsolation(t *testing.T) {
	cfg := teamsConfig{AppID: teamsTestApp, TenantID: teamsTestTenant}
	a := teamsTestActivity()
	if !a.validFor(cfg) {
		t.Fatal("valid message refused")
	}
	if a.addressed() {
		t.Fatal("channel chatter must not start an agent")
	}
	if err := json.Unmarshal([]byte(`[{"type":"mention","mentioned":{"id":"28:`+teamsTestApp+`"},"text":"<at>Prism</at>"}]`), &a.Entities); err != nil {
		t.Fatal(err)
	}
	if !a.addressed() || a.messageText() != "hello" {
		t.Fatal("mention not recognized/removed")
	}
	personal := a
	personal.Conversation.Type = "personal"
	personal.Entities = nil
	if !personal.addressed() {
		t.Fatal("direct message must be addressed")
	}
	for _, mutate := range []func(*teamsActivity){
		func(a *teamsActivity) { a.Conversation.TenantID = teamsTestApp },
		func(a *teamsActivity) { a.ChannelData.Tenant.ID = teamsTestApp },
		func(a *teamsActivity) { a.From.AADID = "" },
		func(a *teamsActivity) { a.Recipient.ID = "28:another-bot" },
		func(a *teamsActivity) { a.From.ID = a.Recipient.ID },
		func(a *teamsActivity) { a.ChannelID = "webchat" },
	} {
		b := a
		mutate(&b)
		if b.validFor(cfg) {
			t.Fatalf("accepted foreign or missing identity: %+v", b)
		}
	}
	b := a
	b.Conversation.ID = "19:channel;messageid=456"
	if teamsSessionID(1, a) == teamsSessionID(1, b) || teamsSessionID(1, a) == teamsSessionID(2, a) {
		t.Fatal("histories collide across threads/groups")
	}
	if groupScopeFromSessionID(teamsSessionID(123, a)) != "g123" {
		t.Fatal("self-calls lose the Teams group scope")
	}
}

func TestTeamsSenderPermissionsFailClosed(t *testing.T) {
	cfg := teamsConfig{RAGRead: "*", Tools: teamsTestUser}
	guard := teamsSenderGuard(cfg, teamsTestUser)
	if guard("exec_command", nil) != nil {
		t.Fatal("allowed sender cannot use tools")
	}
	if teamsSenderGuard(cfg, teamsTestTenant)("exec_command", nil) == nil {
		t.Fatal("other sender may use tools")
	}
	if teamsSenderGuard(teamsConfig{}, teamsTestUser)("exec_command", nil) == nil {
		t.Fatal("blank allowlist grants tools")
	}
	if guard("add_attachment", nil) == nil {
		t.Fatal("unsupported outgoing files must not promise a download")
	}
	for _, s := range []string{"*", teamsTestUser + ", " + teamsTestTenant, ""} {
		if !validTeamsAllowlist(s) {
			t.Errorf("refused %q", s)
		}
	}
	for _, s := range []string{"alice@example.com", "Alice", "*everyone"} {
		if validTeamsAllowlist(s) {
			t.Errorf("accepted unstable identity %q", s)
		}
	}
	for _, p := range []string{"/api/teams/config", "/api/teams/manifest", "/api/teams/conversations", "/api/teams/messages-evil/1"} {
		if authPublicPath(p) {
			t.Errorf("management path %s is public", p)
		}
	}
	if !authPublicPath(teamsIncomingPrefix + "1") {
		t.Fatal("Microsoft cannot reach activity receiver")
	}
}

func TestTeamsCallbackAndAttachmentDestinations(t *testing.T) {
	for _, raw := range []string{"https://smba.trafficmanager.net/emea/", "https://smba.infra.teams.microsoft.com/teams/"} {
		if !validTeamsServiceURL(raw) {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"http://smba.trafficmanager.net/", "https://smba.trafficmanager.net.evil.invalid/", "https://user:pass@smba.trafficmanager.net/", "https://smba.trafficmanager.net:444/", "https://127.0.0.1/", "https://smba.trafficmanager.net/?redirect=x", "https://smba.trafficmanager.net/%2e%2e/foo"} {
		if validTeamsServiceURL(raw) {
			t.Errorf("unsafe callback %s", raw)
		}
	}
	for _, raw := range []string{"https://tenant.sharepoint.com/file", "https://eu-api.asm.skype.com/v1/objects/id/views/imgpsh_fullsize"} {
		if _, _, ok := teamsFileHost(raw); !ok {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "http://tenant.sharepoint.com/file", "https://localhost/file", "https://tenant.sharepoint.com.evil.invalid/file", "https://tenant.sharepoint.com:444/file"} {
		if _, _, ok := teamsFileHost(raw); ok {
			t.Errorf("unsafe file %s", raw)
		}
	}
	if _, auth, _ := teamsFileHost("https://tenant.sharepoint.com/file"); auth {
		t.Fatal("storage must not receive Connector token")
	}
	imgs := teamsInlineImages(`<p>hi<img src="https://eu-api.asm.skype.com/x?a=1&amp;b=2"></p>`)
	if len(imgs) != 1 || !strings.Contains(imgs[0], "a=1&b=2") {
		t.Fatal(imgs)
	}
}

type teamsTestTransport func(*http.Request) (*http.Response, error)

func (f teamsTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTeamsRepliesUseCachedCredentialAndKeepThread(t *testing.T) {
	previous := teamsHTTP
	defer func() { teamsHTTP = previous }()
	var bodies []map[string]any
	teamsHTTP = &http.Client{Transport: teamsTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "smba.trafficmanager.net" || !strings.Contains(r.URL.EscapedPath(), "19:channel%3Bmessageid=123/activities/msg-1") {
			t.Errorf("wrong conversation endpoint %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing bot token")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"id":"reply"}`)), Header: make(http.Header)}, nil
	})}
	cfg := teamsConfig{AppID: teamsTestApp, TenantID: teamsTestTenant}
	m := teamsManager{tokens: map[string]teamsAccessToken{teamsFingerprint(cfg, "secret"): {"fixture", time.Now().Add(time.Hour)}}}
	if err := m.reply(context.Background(), cfg, "secret", teamsTestActivity(), strings.Repeat("é", 6000)); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || len([]rune(bodies[0]["text"].(string))) != 5000 || bodies[1]["replyToId"] != "msg-1" {
		t.Fatal("long response was lost or left its thread")
	}
}

func TestTeamsQueueRetriesAndSaturation(t *testing.T) {
	s := &Server{}
	m := &teamsManager{s: s}
	// A synthetic inbox makes dispatch deterministic without running a model.
	a := teamsTestActivity()
	key := teamsSessionID(1, a)
	w := &teamsWorker{queue: make(chan teamsQueued, 1), groupID: 1, cancel: func() {}}
	m.workers = map[string]*teamsWorker{key: w}
	cfg := teamsConfig{}
	if !m.dispatch(1, cfg, "x", a) || !m.dispatch(1, cfg, "x", a) || len(w.queue) != 1 {
		t.Fatal("retry started a duplicate turn")
	}
	b := a
	b.ID = "msg-2"
	if m.dispatch(1, cfg, "x", b) {
		t.Fatal("full queue acknowledged a lost message")
	}
	<-w.queue
	if !m.dispatch(1, cfg, "x", b) {
		t.Fatal("unaccepted message could not be retried")
	}
	m.cancelGroup(1)
	if len(m.workers) != 0 {
		t.Fatal("disconnected workers retained")
	}
}

func TestTeamsReceiverMethodsAndMonoMode(t *testing.T) {
	s := &Server{}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, teamsIncomingPrefix+"1", bytes.NewBufferString(`{}`))
		w := httptest.NewRecorder()
		s.handleTeamsIncoming(w, r)
		want := http.StatusNotFound
		if method == http.MethodGet {
			want = http.StatusMethodNotAllowed
		}
		if w.Code != want {
			t.Fatal(fmt.Sprintf("%s: %d", method, w.Code))
		}
	}
}
