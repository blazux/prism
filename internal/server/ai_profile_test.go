package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"prism/internal/memory"
)

func TestAIProfileURLAndPolicy(t *testing.T) {
	s := &Server{cfg: Config{MultiUser: true}}
	r := withUser(httptest.NewRequest("POST", "/api/ai/config", nil), &memory.User{ID: 1, Role: "member"})
	for _, base := range []string{"http://127.0.0.1:1234/v1", "https://api.openai.com.evil/v1", "https://key@api.openai.com/v1", "https://api.openai.com/v1?key=foo"} {
		p := aiProfile{Provider: "openai", BaseURL: base, Model: "fixture", APIKey: "dummy"}
		if err := s.validateAIProfileFor(r, &p); err == nil {
			t.Errorf("accepted nonadmin URL %s", base)
		}
	}
	p := aiProfile{Provider: "openai", Model: "fixture", APIKey: "dummy"}
	if err := s.validateAIProfileFor(r, &p); err == nil {
		t.Fatal("member could configure AI")
	}
	r = withUser(r, &memory.User{ID: 2, Role: memory.RoleGlobalAdmin})
	if err := s.validateAIProfileFor(r, &p); err != nil {
		t.Fatal(err)
	}
	if !memory.IsIntegrationSecret(aiProfileSecret) || memory.ValidateScriptSecretName(aiProfileSecret) == nil {
		t.Fatal("profile must not be a script secret")
	}
}

func TestAIProfilePersistenceIsolationAndRouting(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_SECRETS_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx := context.Background()
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	schema := fmt.Sprintf("ai_profile_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, parsed.String(), bytes.Repeat([]byte{7}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			fmt.Fprint(w, `{"data":[{"id":"fixture"}]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			fmt.Fprint(w, `{"data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}`)
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "broken" {
			http.Error(w, "rejected-secret-should-not-escape", 401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": key + ":" + req.Model}}}}
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{LLMBackend: "openai", OpenAIBaseURL: upstream.URL, OpenAIAPIKey: "server-fixture", Model: "default"}}
	a := &memory.User{ID: 11, Role: memory.RoleGlobalAdmin}
	b := &memory.User{ID: 22, Role: memory.RoleGlobalAdmin}
	call := func(u *memory.User, method string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := withUser(httptest.NewRequest(method, "/api/ai/config", bytes.NewReader(raw)), u)
		w := httptest.NewRecorder()
		s.handleAIProfile(w, r)
		return w
	}
	save := func(u *memory.User, key, model string) {
		t.Helper()
		w := call(u, "POST", map[string]any{"action": "save", "provider": "openai", "baseURL": upstream.URL, "model": model, "apiKey": key})
		if w.Code != 200 {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
	}
	save(a, "user-a-fixture", "model-a")
	save(b, "user-b-fixture", "model-b")
	for _, u := range []*memory.User{a, b} {
		w := call(u, "GET", nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "fixture") || strings.Contains(w.Body.String(), "apiKey") {
			t.Fatalf("credential in GET: %s", w.Body.String())
		}
	}
	var raw string
	if err := db.QueryRow(ctx, "SELECT value FROM "+quoted+".secrets WHERE name=$1", aiProfileSecret).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "user-a-fixture") || strings.Contains(raw, "model-a") {
		t.Fatal("profile is not encrypted")
	}
	for _, tc := range []struct {
		u    *memory.User
		want string
	}{{a, "user-b-fixture:model-b"}, {b, "user-b-fixture:model-b"}, {nil, "user-b-fixture:model-b"}} {
		w := httptest.NewRecorder()
		r := withUser(httptest.NewRequest("POST", "/api/ai/assist", strings.NewReader(`{"task":"summarize","text":"fixture"}`)), tc.u)
		s.handleAIAssist(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("wrong routing: %d %s, want %s", w.Code, w.Body.String(), tc.want)
		}
	}
	// Blank key keeps the current credential at the same endpoint only.
	save(a, "", "model-new")
	p, err := loadAIProfile(ctx, ms)
	if err != nil || p.APIKey != "user-b-fixture" {
		t.Fatal("key was not retained", err)
	}
	w := call(a, "POST", map[string]any{"action": "test", "provider": "openai", "baseURL": upstream.URL, "model": "model-new", "apiKey": "broken"})
	if w.Code != 502 || strings.Contains(w.Body.String(), "rejected-secret") {
		t.Fatalf("test leaked upstream error: %s", w.Body.String())
	}
	p, _ = loadAIProfile(ctx, ms)
	if p.APIKey != "user-b-fixture" {
		t.Fatal("test changed stored key")
	}
	w = call(a, "POST", map[string]any{"action": "save", "provider": "openai", "baseURL": upstream.URL + "/other", "model": "different"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, _ = loadAIProfile(ctx, ms)
	if p.APIKey != "" {
		t.Fatal("sent old key to a new endpoint")
	}
	// The existing agent settings tool uses identical owner scope and validators.
	out, err := s.aiSettingsTool(b)(ctx, map[string]any{"action": "ai_set", "model": "tool-model"})
	if err != nil {
		t.Fatal(out, err)
	}
	p, _ = loadAIProfile(ctx, ms)
	if p.Model != "tool-model" || p.APIKey != "" {
		t.Fatal("tool changed credential or wrong owner")
	}
	out, err = s.aiSettingsTool(b)(ctx, map[string]any{"action": "ai_get"})
	if err != nil || strings.Contains(out, "user-b-fixture") {
		t.Fatal("tool leaked key", err)
	}
	// Integration profile cannot be obtained through the user's secret API.
	w = httptest.NewRecorder()
	r := withUser(httptest.NewRequest("GET", "/api/user/secrets/"+aiProfileSecret, nil), b)
	s.handleUserSecretByName(w, r)
	if w.Code == 200 {
		t.Fatal("secret value endpoint exposed AI profile")
	}
	w = call(a, "DELETE", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cfg, err := s.aiConfigFor(ctx, a.ID)
	if err != nil || cfg.cfg.OpenAIAPIKey != "server-fixture" {
		t.Fatal("reset did not restore defaults", err)
	}
	cfg, err = s.aiConfigFor(ctx, b.ID)
	if err != nil || cfg.cfg.OpenAIAPIKey != "server-fixture" {
		t.Fatal("reset did not restore global defaults", err)
	}

	t.Run("embedding settings", func(t *testing.T) {
		body := map[string]any{"action": "save", "provider": "other", "baseURL": upstream.URL, "model": "chat", "embedding": map[string]any{"useSameProvider": true, "model": "embed-fixture"}}
		w := call(a, "POST", body)
		if w.Code != 409 {
			t.Fatalf("missing reindex agreement: %d %s", w.Code, w.Body.String())
		}
		body["embedding"].(map[string]any)["reindex"] = true
		w = call(a, "POST", body)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		p, _ := loadAIProfile(ctx, ms)
		if p.Embedding.ReindexFor != embeddingIdentity(p.effectiveEmbedding()) {
			t.Fatal("confirmation not bound to target")
		}
		body["action"] = "embedding_test"
		w = call(a, "POST", body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"dimension":3`) {
			t.Fatal(w.Body.String())
		}
		// A chat-only caller must retain embeddings and their reindex permission.
		save(a, "new-key", "another-chat")
		p, _ = loadAIProfile(ctx, ms)
		if p.effectiveEmbedding().Model != "embed-fixture" || !p.Embedding.Reindex {
			t.Fatal("chat-only save lost pending embedding configuration")
		}
		if err := s.resetAIProfile(ctx, true); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("source tools and form share persistence", func(t *testing.T) {
		ms.ConfigScope("u11").SetSecret(ctx, "second_ai_key", "secondary-fixture")
		out, err := s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_source_set", "source_id": "secondary", "source_name": "Second server", "provider": "other", "base_url": upstream.URL + "/second", "model": "fixture", "key_secret": "second_ai_key"})
		if err != nil {
			t.Fatal(out, err)
		}
		p, _ := loadAIProfile(ctx, ms)
		if len(p.Sources) != 2 || p.DefaultSource != "primary" {
			t.Fatal("adding source replaced default", p)
		}
		out, err = s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_source_test", "source_id": "secondary"})
		if err != nil || strings.Contains(out, "secondary-fixture") {
			t.Fatal("source test failed or exposed key", out, err)
		}
		// Save the sanitized GET response exactly as a browser would.
		view := s.aiPublicView(p, "configured")
		view["action"] = "save"
		w := call(a, "POST", view)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		p, _ = loadAIProfile(ctx, ms)
		if p.Sources[1].APIKey != "secondary-fixture" {
			t.Fatal("blank source key not retained")
		}
		out, err = s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_source_default", "source_id": "secondary"})
		if err != nil {
			t.Fatal(out, err)
		}
		ai, err := s.aiConfigFor(ctx, 0)
		if err != nil || ai.cfg.AIDefaultSource != "secondary" {
			t.Fatal("default source not applied", err)
		}
		// The remaining source survives a legacy single-source update.
		out, err = s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_set", "model": "another-model"})
		if err != nil {
			t.Fatal(out, err)
		}
		p, _ = loadAIProfile(ctx, ms)
		if len(p.Sources) != 2 || p.Sources[1].Model != "another-model" {
			t.Fatal("legacy tool removed sources")
		}
		out, err = s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_source_remove", "source_id": "primary"})
		if err != nil {
			t.Fatal(out, err)
		}
		p, _ = loadAIProfile(ctx, ms)
		if len(p.Sources) != 1 {
			t.Fatal("source not removed")
		}
		if _, err = s.aiSettingsTool(a)(ctx, map[string]any{"action": "ai_source_remove", "source_id": "secondary"}); err == nil {
			t.Fatal("removed default source")
		}
		if err := s.resetAIProfile(ctx, true); err != nil {
			t.Fatal(err)
		}
	})

	s.cfg.MultiUser = true
	member := &memory.User{ID: 33, Role: "member"}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		if w := call(member, method, map[string]any{"action": "save"}); w.Code != 403 {
			t.Fatalf("member access: %s %d", method, w.Code)
		}
	}
	if _, err := s.aiSettingsTool(member)(ctx, map[string]any{"action": "ai_set", "model": "escape"}); err == nil {
		t.Fatal("member tool changed deployment AI")
	}
	if w := call(a, "GET", nil); w.Code != 200 {
		t.Fatal("global admin denied", w.Code)
	}
	s.cfg.MultiUser = false
	t.Run("connected chat switches on next turn and preserves history", func(t *testing.T) {
		// No actual Docker process or existing workspace is used by this test.
		t.Setenv("PATH", t.TempDir())
		cfg := s.cfg
		cfg.WorkspaceDir = t.TempDir()
		cfg.PluginDir = t.TempDir()
		cfg.AgentContainer = "nonexistent-ai-profile-test"
		live := New(cfg)
		live.memStore = ms
		live.mcpMgr.SetStore(ms)
		endpoint := httptest.NewServer(http.HandlerFunc(live.handleWS))
		defer endpoint.Close()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(endpoint.URL, "http")+"?session=ai-profile-ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		readUntil := func(kind string) string {
			t.Helper()
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			var text strings.Builder
			for {
				var ev struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				}
				if err := conn.ReadJSON(&ev); err != nil {
					t.Fatal(err)
				}
				if ev.Type == "error" {
					t.Fatal(ev.Content)
				}
				if ev.Type == "stream" {
					text.WriteString(ev.Content)
				}
				if ev.Type == kind {
					return text.String()
				}
			}
		}
		readUntil("container_status")
		if err := conn.WriteJSON(map[string]any{"type": "chat", "content": "First message"}); err != nil {
			t.Fatal(err)
		}
		if got := readUntil("turn_complete"); !strings.Contains(got, "server-fixture:default") {
			t.Fatal("wrong initial backend", got)
		}
		save(nil, "mono-new-key", "mono-new-model")
		if err := conn.WriteJSON(map[string]any{"type": "chat", "content": "Second message"}); err != nil {
			t.Fatal(err)
		}
		if got := readUntil("turn_complete"); !strings.Contains(got, "mono-new-key:mono-new-model") {
			t.Fatal("connected chat did not switch", got)
		}
		hist, err := ms.LoadHistory(ctx, "ai-profile-ws")
		if err != nil || len(hist) < 4 {
			t.Fatal("conversation history lost", len(hist), err)
		}
		got, err := live.runHeadlessChat(ctx, "ai-profile-headless", "Third message", "", trustedCallerContext())
		if err != nil || !strings.Contains(got, "mono-new-key:mono-new-model") {
			t.Fatal("headless ignored personal profile", got, err)
		}
	})
	// Storage failures must not silently send a user's chat to the server account.
	if _, err := db.Exec(ctx, "UPDATE "+quoted+".secrets SET value='corrupt' WHERE name=$1", aiProfileSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.aiConfigFor(ctx, b.ID); err == nil {
		t.Fatal("corrupt profile silently fell back")
	}
}
