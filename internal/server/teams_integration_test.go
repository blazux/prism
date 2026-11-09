package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"prism/internal/docker"
	"prism/internal/memory"
)

// Run against a disposable database, never the deployment's database:
// PRISM_TEAMS_TEST_DATABASE_URL=postgres://... go test ./internal/server -run TestTeamsDatabase
func TestTeamsDatabase(t *testing.T) {
	dsn := os.Getenv("PRISM_TEAMS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set PRISM_TEAMS_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	store, err := memory.NewStore(ctx, dsn, make([]byte, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	group, err := store.CreateGroup(ctx, "Teams fixture "+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.DeleteGroup(ctx, group.ID); store.PurgeGroupResidue(ctx, group.ID) }()
	other, err := store.CreateGroup(ctx, "Other Teams fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.DeleteGroup(ctx, other.ID); store.PurgeGroupResidue(ctx, other.ID) }()
	s := &Server{cfg: Config{MultiUser: true, AuthToken: "prism-fixture-only"}, memStore: store}
	s.initChannels()
	m := s.teamsManager()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m.auth = teamsAuthenticator{keys: map[string]teamsSigningKey{"test-key": {&key.PublicKey, true}}, fetched: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/teams/config", s.handleTeamsConfig)
	mux.HandleFunc("/api/teams/manifest", s.handleTeamsManifest)
	mux.HandleFunc(teamsIncomingPrefix, s.handleTeamsIncoming)
	handler := s.withAuth(mux)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	configPath := "/api/teams/config?group=" + strconv.FormatInt(group.ID, 10)
	if w := call("GET", configPath, "", ""); w.Code != 401 {
		t.Fatalf("public config: %d", w.Code)
	}
	saved := `{"appId":"` + teamsTestApp + `","tenantId":"` + teamsTestTenant + `","clientSecret":"fixture-client-secret","ragRead":"*","tools":"` + teamsTestUser + `"}`
	if w := call("POST", configPath, "Bearer prism-fixture-only", saved); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if w := call("GET", configPath, "Bearer prism-fixture-only", ""); w.Code != 200 || strings.Contains(w.Body.String(), "fixture-client-secret") {
		t.Fatalf("secret disclosure/config failed: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/api/teams/config?group="+strconv.FormatInt(other.ID, 10), "Bearer prism-fixture-only", saved); w.Code != 409 {
		t.Fatalf("same bot assigned twice: %d %s", w.Code, w.Body)
	}
	// Ordinary members cannot manage the bot, even if signed in.
	memberRequest := withUser(httptest.NewRequest("GET", configPath, nil), &memory.User{ID: 999999, Role: memory.RoleMember})
	w := httptest.NewRecorder()
	s.handleTeamsConfig(w, memberRequest)
	if w.Code != 403 {
		t.Fatalf("member config: %d", w.Code)
	}
	// The app ZIP is complete and uses the bot's ID; icons have Teams dimensions.
	w = call("GET", "/api/teams/manifest?group="+strconv.FormatInt(group.ID, 10), "Bearer prism-fixture-only", "")
	if w.Code != 200 {
		t.Fatalf("package: %d %s", w.Code, w.Body)
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 3 {
		t.Fatal("incomplete Teams app package")
	}
	for _, file := range z.File {
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(f)
		f.Close()
		if file.Name == "manifest.json" {
			var manifest map[string]any
			if json.Unmarshal(data, &manifest) != nil || manifest["id"] != teamsTestApp {
				t.Fatal("wrong manifest")
			}
		} else {
			icon, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			want := 32
			if file.Name == "color.png" {
				want = 192
			}
			if icon.Width != want || icon.Height != want {
				t.Fatal("wrong icon size")
			}
		}
	}
	a := teamsTestActivity()
	a.Conversation.Type = "personal"
	inbox := &teamsWorker{queue: make(chan teamsQueued, 2), cancel: func() {}, groupID: group.ID}
	m.workers = map[string]*teamsWorker{teamsSessionID(group.ID, a): inbox}
	messagePath := teamsIncomingPrefix + strconv.FormatInt(group.ID, 10)
	body, _ := json.Marshal(a)
	if w := call("POST", messagePath, "", string(body)); w.Code != 401 {
		t.Fatalf("unsigned message: %d", w.Code)
	}
	token := teamsTestToken(t, key, nil)
	for i := 0; i < 2; i++ {
		if w := call("POST", messagePath, token, string(body)); w.Code != 202 {
			t.Fatalf("signed message: %d %s", w.Code, w.Body)
		}
	}
	if len(inbox.queue) != 1 {
		t.Fatal("Microsoft retry duplicated the turn")
	}
	foreign := a
	foreign.Conversation.TenantID = teamsTestApp
	body, _ = json.Marshal(foreign)
	if w := call("POST", messagePath, token, string(body)); w.Code != 403 {
		t.Fatalf("foreign tenant: %d", w.Code)
	}
	// Private chats cannot become an announcement destination.
	cfg, _, err := s.teamsConfig(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.rememberConversation(ctx, group.ID, cfg, a)
	refs, err := m.conversations(ctx, group.ID)
	if err != nil || len(refs) != 0 {
		t.Fatal("private conversation exposed as group announcement")
	}
	collective := a
	collective.Conversation.Type = "channel"
	m.rememberConversation(ctx, group.ID, cfg, collective)
	refs, err = m.conversations(ctx, group.ID)
	if err != nil || len(refs) != 1 {
		t.Fatal("group conversation not discovered")
	}
	if err := s.deliverTeamsSession(ctx, "u1-private", "private result"); err == nil {
		t.Fatal("personal result sent to a group")
	}
	if w := call("POST", configPath, "Bearer prism-fixture-only", `{"disconnect":true}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if len(m.workers) != 0 {
		t.Fatal("disconnect did not cancel group queues")
	}
	body, _ = json.Marshal(a)
	if w := call("POST", messagePath, token, string(body)); w.Code != 404 {
		t.Fatalf("disconnected bot still accepts messages: %d", w.Code)
	}
	if secret, _, _ := store.GetSecret(ctx, teamsKey(teamsSecretKey, group.ID)); secret != "" {
		t.Fatal("disconnect kept client secret")
	}
}

func TestTeamsAgentUsesSharedConfigurationAndVision(t *testing.T) {
	for _, vision := range []bool{true, false} {
		t.Run(fmt.Sprintf("vision=%t", vision), func(t *testing.T) {
			store := securityStore(t)
			ctx := t.Context()
			group, err := store.CreateGroup(ctx, "Teams model fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetRoomConfig(ctx, memory.RoomConfig{GroupID: group.ID, AgentModel: "group-model", AgentPrompt: "Teams group fixture identity.", AgentPromptProfile: "minimal"}); err != nil {
				t.Fatal(err)
			}
			cfg := teamsConfig{AppID: teamsTestApp, TenantID: teamsTestTenant, RAGRead: "*"}
			data, _ := json.Marshal(cfg)
			if err := store.SetConfig(ctx, teamsKey("teams_config", group.ID), string(data)); err != nil {
				t.Fatal(err)
			}
			if err := store.SetSecret(ctx, teamsKey(teamsSecretKey, group.ID), "secret"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Model    string
					Messages []struct {
						Role    string
						Content json.RawMessage
					}
					Tools []struct{ Function struct{ Name string } }
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				calls++
				if req.Model != "group-model" {
					t.Errorf("used %q instead of shared agent's model", req.Model)
				}
				payload := string(mustTeamsJSON(t, req.Messages))
				for _, part := range []string{"Teams group fixture identity.", teamsTestUser, "Describe this", "hello from the document", "/workspace/uploads/"} {
					if !strings.Contains(payload, part) {
						t.Errorf("missing context %q", part)
					}
				}
				if strings.Contains(payload, chatTestPNG) != vision {
					t.Errorf("vision=%t: pixels routed incorrectly", vision)
				}
				if !vision && !strings.Contains(payload, "cannot inspect images") {
					t.Error("text-only model missing limitation notice")
				}
				for _, tool := range req.Tools {
					if tool.Function.Name == "exec_command" || tool.Function.Name == "add_attachment" {
						t.Errorf("denied tool advertised: %s", tool.Function.Name)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A useful response.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer model.Close()
			pngData, _ := base64.StdEncoding.DecodeString(chatTestPNG)
			files := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".png") {
					if r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error("inline image missing token")
					}
					w.Write(pngData)
				} else {
					if r.Header.Get("Authorization") != "" {
						t.Error("bot token leaked to document storage")
					}
					fmt.Fprint(w, "hello from the document")
				}
			}))
			defer files.Close()
			fileURL, _ := url.Parse(files.URL)
			previousFiles := teamsFileTransport
			teamsFileTransport = files.Client().Transport.(*http.Transport).Clone()
			teamsFileTransport.TLSClientConfig.ServerName = fileURL.Hostname()
			teamsFileTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, fileURL.Host)
			}
			defer func() { teamsFileTransport.CloseIdleConnections(); teamsFileTransport = previousFiles }()
			s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "wrong-default-model", LLMBackend: "openai", OpenAIBaseURL: model.URL, MultiUser: true, ChatVision: vision})
			s.memStore = store
			s.docker = docker.WithExecution(nil)
			m := s.teamsManager()
			m.tokens = map[string]teamsAccessToken{teamsFingerprint(cfg, "secret"): {"fixture", time.Now().Add(time.Hour)}}
			previous := teamsHTTP
			defer func() { teamsHTTP = previous }()
			sent := 0
			teamsHTTP = &http.Client{Transport: teamsTestTransport(func(r *http.Request) (*http.Response, error) {
				var reply map[string]any
				json.NewDecoder(r.Body).Decode(&reply)
				if reply["text"] != "A useful response." {
					t.Errorf("reply=%v", reply)
				}
				sent++
				return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})}
			a := teamsTestActivity()
			a.Conversation.Type = "personal"
			a.Text = "Describe this"
			if err := json.Unmarshal([]byte(`[{"contentType":"image/png","contentUrl":"https://eu-api.asm.skype.com/image.png","name":"image.png"},{"contentType":"application/vnd.microsoft.teams.file.download.info","name":"document.txt","content":{"downloadUrl":"https://fixture.sharepoint.com/document.txt"}}]`), &a.Attachments); err != nil {
				t.Fatal(err)
			}
			m.handle(ctx, group.ID, teamsQueued{a, teamsFingerprint(cfg, "secret")})
			if calls != 1 || sent != 1 {
				t.Fatalf("model calls=%d sent replies=%d", calls, sent)
			}
		})
	}
}

func mustTeamsJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
