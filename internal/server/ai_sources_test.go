package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestAISourcesMigrationAndCredentials(t *testing.T) {
	s := &Server{cfg: Config{LLMBackend: "openai", Model: "default", OpenAIBaseURL: "https://local.example/v1", OpenAIAPIKey: "local-secret", OllamaURL: "http://ollama:11434", AnthropicToken: "anthropic-secret", AnthropicModel: "claude-fixture"}}
	old := s.environmentAIProfile()
	view := s.aiPublicView(old, "server")
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("credential leak")
	}
	migrated := s.withAISources(old)
	if len(migrated.Sources) != 3 {
		t.Fatal("missing environment sources", len(migrated.Sources))
	}
	// Legacy updates preserve secondary sources.
	p := aiProfile{Provider: "other", BaseURL: old.BaseURL, APIKey: old.APIKey, Model: "updated"}
	if err := s.prepareSources(&p, old); err != nil {
		t.Fatal(err)
	}
	if len(p.Sources) != 3 || p.Model != "updated" {
		t.Fatal("legacy save lost sources")
	}
	// Browser blank key means retain only for the same source ID and endpoint.
	next := p
	next.Sources = append([]aiSource(nil), p.Sources...)
	for i := range next.Sources {
		next.Sources[i].APIKey = ""
	}
	if err := s.prepareSources(&next, &p); err != nil {
		t.Fatal(err)
	}
	if next.APIKey != "local-secret" {
		t.Fatal("default credential lost")
	}
	next.Sources[0].BaseURL = "https://other.example/v1"
	next.Sources[0].APIKey = ""
	if err := s.prepareSources(&next, &p); err != nil {
		t.Fatal(err)
	}
	if next.APIKey != "" {
		t.Fatal("credential copied to another host")
	}
	next.Sources[0] = p.Sources[0]
	next.Sources[0].ClearKey = true
	if err := s.prepareSources(&next, &p); err != nil {
		t.Fatal(err)
	}
	next.Embedding = &embeddingProfile{SourceID: next.DefaultSource, Model: "embed"}
	if err := s.prepareEmbedding(&next, &p); err != nil {
		t.Fatal(err)
	}
	if next.effectiveEmbedding().APIKey != "" {
		t.Fatal("embedding restored a removed source key")
	}
	next.Embedding.SourceID = "removed"
	if err := s.prepareEmbedding(&next, &p); err == nil {
		t.Fatal("removed embedding source accepted")
	}
}
func TestAISourcesExactRoutingAndPartialOutage(t *testing.T) {
	fixture := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/models") {
				fmt.Fprint(w, `{"data":[{"id":"same-model"}]}`)
				return
			}
			var body struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Model != "same-model" {
				t.Errorf("source prefix sent upstream: %s", body.Model)
			}
			if r.Header.Get("Authorization") != "Bearer "+label {
				t.Error("wrong source credential")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", label)
		}))
	}
	a, b := fixture("a"), fixture("b")
	defer a.Close()
	defer b.Close()
	router := &sourcesBackend{primary: "a", sources: []aiSource{{ID: "a", Provider: "other", BaseURL: a.URL, APIKey: "a", Model: "same-model"}, {ID: "b", Provider: "other", BaseURL: b.URL, APIKey: "b", Model: "same-model"}}}
	models, err := router.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "same-model,b::same-model" {
		t.Fatal(models)
	}
	for _, tc := range []struct{ model, want string }{{"same-model", "a"}, {"b::same-model", "b"}} {
		events := make(chan ollama.StreamEvent, 20)
		router.Chat(context.Background(), ollama.ChatRequest{Model: tc.model, Messages: []ollama.Message{{Role: "user", Content: "test"}}}, events)
		close(events)
		text := ""
		for ev := range events {
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
			text += ev.Content
		}
		if text != tc.want {
			t.Fatal("wrong source", text)
		}
	}
	a.Close()
	models, err = router.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0] != "b::same-model" {
		t.Fatal("secondary hidden by primary outage", models, err)
	}
	if _, _, err := router.resolve(context.Background(), "deleted::same-model"); err == nil {
		t.Fatal("unknown source fell back")
	}
}
