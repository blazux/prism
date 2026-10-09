package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"prism/internal/memory"
	"prism/internal/ollama"
	"strings"
	"testing"
	"time"
)

func TestModelOverridesAreSourceAndModelSpecific(t *testing.T) {
	vision := false
	b := &sourcesBackend{primary: "big", sources: []aiSource{
		{ID: "big", Provider: "other", BaseURL: "http://unused.invalid/v1", Model: "same", ModelSettings: map[string]ollama.ModelCapabilities{"same": {ContextWindow: 500000, Vision: &vision}}},
		{ID: "small", Provider: "ollama", BaseURL: "http://unused.invalid", Model: "same", ModelSettings: map[string]ollama.ModelCapabilities{"same": {ContextWindow: 8192, Vision: &vision}}},
	}}
	for id, want := range map[string]int{"same": 500000, "small::same": 8192} {
		info, err := b.ModelCapabilities(context.Background(), id)
		if err != nil || info.ContextWindow != want || info.Vision == nil || *info.Vision {
			t.Fatalf("%s: %#v %v", id, info, err)
		}
	}
	if b.ContextBudgetChars() != 0 {
		t.Fatal("unused small source imposed a global budget")
	}
	if _, err := b.ModelCapabilities(context.Background(), "deleted::same"); err == nil {
		t.Fatal("unknown source fell back")
	}
	for _, n := range []int{-1, 1, 4095, 2000001} {
		if validateModelSettings(map[string]ollama.ModelCapabilities{"same": {ContextWindow: n}}) == nil {
			t.Fatalf("accepted %d", n)
		}
	}
}

func TestModelSettingsPersistWithoutChangingDefaultOrEmbeddings(t *testing.T) {
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
	schema := fmt.Sprintf("model_settings_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = db.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	parsed, _ := url.Parse(dsn)
	q := parsed.Query()
	q.Set("search_path", schema+",public")
	parsed.RawQuery = q.Encode()
	ms, err := memory.NewStore(ctx, parsed.String(), bytes.Repeat([]byte{9}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/model_group/info") {
			http.NotFound(w, r)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("settings generated an upstream completion: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"default","context_length":500000,"supports_vision":true},{"id":"other","context_length":32768}]}`)
	}))
	defer upstream.Close()
	s := &Server{memStore: ms, cfg: Config{MultiUser: true, LLMBackend: "openai", OpenAIBaseURL: upstream.URL, OpenAIAPIKey: "fixture-secret", Model: "default", ChatVision: true}}
	admin := &memory.User{ID: 1, Role: memory.RoleGlobalAdmin}
	call := func(u *memory.User, action string, settings map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"action": action, "sourceID": "primary", "model": "other", "settings": settings})
		r := withUser(httptest.NewRequest("POST", "/api/ai/config", bytes.NewReader(raw)), u)
		w := httptest.NewRecorder()
		s.handleAIProfile(w, r)
		return w
	}
	if w := call(&memory.User{ID: 2, Role: "member"}, "model_settings", nil); w.Code == 200 {
		t.Fatal("member could change deployment settings")
	}
	if w := call(admin, "model_settings", map[string]any{"contextWindow": 500000, "vision": false}); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	p, err := loadAIProfile(ctx, ms)
	if err != nil {
		t.Fatal(err)
	}
	if p.Model != "default" || p.DefaultSource != "primary" || p.Sources[0].APIKey != "fixture-secret" || p.Sources[0].ModelSettings["other"].ContextWindow != 500000 {
		t.Fatal("settings changed source/default or did not persist")
	}
	if w := call(admin, "model_info", nil); w.Code != 200 || strings.Contains(w.Body.String(), "fixture-secret") || !strings.Contains(w.Body.String(), "32768") {
		t.Fatalf("metadata: %s", w.Body.String())
	}
	if _, err := s.aiSettingsTool(&memory.User{ID: 2, Role: "member"})(ctx, map[string]any{"action": "ai_model_set", "source_id": "primary", "model": "other", "context_window": 32768}); err == nil {
		t.Fatal("member tool bypassed admin boundary")
	}
	result, err := s.aiSettingsTool(admin)(ctx, map[string]any{"action": "ai_model_set", "source_id": "primary", "model": "other", "context_window": 32768})
	if err != nil || !strings.Contains(result, "saved") {
		t.Fatalf("tool save: %s %v", result, err)
	}
	result, err = s.aiSettingsTool(admin)(ctx, map[string]any{"action": "ai_model_get", "source_id": "primary", "model": "other"})
	if err != nil || !strings.Contains(result, "32768") || strings.Contains(result, "fixture-secret") {
		t.Fatalf("tool get: %s %v", result, err)
	}
	p, _ = loadAIProfile(ctx, ms)
	if p.Model != "default" || p.Sources[0].ModelSettings["other"].Vision == nil || *p.Sources[0].ModelSettings["other"].Vision {
		t.Fatal("tool erased omitted vision or changed default")
	}
	// Old clients omit the new field; their unrelated source edits retain it.
	legacy := *p
	legacy.Sources = append([]aiSource(nil), p.Sources...)
	legacy.Sources[0].ModelSettings = nil
	if err := s.prepareSources(&legacy, p); err != nil || legacy.Sources[0].ModelSettings["other"].ContextWindow != 32768 {
		t.Fatal("legacy edit erased overrides")
	}
	if w := call(admin, "model_settings", map[string]any{"contextWindow": 0}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, _ = loadAIProfile(ctx, ms)
	if len(p.Sources[0].ModelSettings) != 0 || p.Model != "default" {
		t.Fatal("reset failed")
	}
	// Compaction boundaries do not remove the immutable visible transcript.
	var boundary int64
	for i := 0; i < 60; i++ {
		id, err := ms.AppendMessage(ctx, "fixture", "user", fmt.Sprint(i), nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 40 {
			boundary = id
		}
	}
	if err := ms.SetLiveCompaction(ctx, "fixture", memory.LiveCompaction{Summary: "summary", BeforeID: boundary}); err != nil {
		t.Fatal(err)
	}
	all, err := ms.LoadHistoryTail(ctx, "fixture", 0)
	if err != nil || len(all) != 60 {
		t.Fatal("transcript lost")
	}
	active, err := ms.LoadActiveHistoryTail(ctx, "fixture", 100)
	if err != nil || len(active) != 20 || active[0].ID != boundary {
		t.Fatal("compacted history replay is incorrect")
	}
}
