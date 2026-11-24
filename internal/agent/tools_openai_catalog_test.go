package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"prism/internal/ollama"
	"prism/internal/openai"
)

// Use the real provider policy but capture requests without any network I/O.
type openAICatalogUsageBackend struct {
	*openai.Client
	capture usageBackend
}

func (b *openAICatalogUsageBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.capture.Chat(ctx, req, out)
}

func TestOpenAIClientCatalogKeepsAllPermittedSchemasLoadable(t *testing.T) {
	t.Setenv("PRISM_OPENAI_LAZY_TOOLS", "1")
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
	backend := &openAICatalogUsageBackend{Client: openai.NewClient("https://api.openai.com/v1", "synthetic-key")}
	a := &Agent{ollama: backend, executor: &ToolExecutor{hiddenTools: map[string]bool{"email": true}},
		disabledTools: []string{"cron"}, limits: Limits{PromptProfile: "minimal"}, model: "gpt-5.5"}
	ctx := withClaudeCatalog(t.Context(), a.model, backend)
	catalog := catalogFromContext(ctx)
	if catalog == nil {
		t.Fatal("official GPT client did not enable the opted-in catalog")
	}
	full, _ := a.buildToolListWithCachePrefix()
	events := make(chan Event, 16)
	if _, _, _, _, err := a.callOllama(ctx, "", events); err != nil {
		t.Fatal(err)
	}
	req := backend.capture.req
	if len(req.Tools) != 6 || !req.ClientToolCatalog || req.CacheToolPrefixCount != 6 {
		t.Fatalf("initial request=%d tools prefix=%d active=%v", len(req.Tools), req.CacheToolPrefixCount, req.ClientToolCatalog)
	}
	if !strings.Contains(req.Messages[0].Content, "Prism tool loading is active") || strings.Contains(req.Messages[0].Content, "Claude tool loading") {
		t.Fatal("GPT received a provider-specific catalog instruction")
	}
	var names []string
	for _, tool := range full {
		names = append(names, tool.Function.Name)
	}
	args, _ := json.Marshal(map[string]any{"action": "load", "names": append(names, "email", "cron", "missing")})
	result, err := catalog.execute(args, full)
	if err != nil || !strings.Contains(result, "Unavailable: email, cron, missing") {
		t.Fatalf("catalog changed tool permissions: %v %s", err, result)
	}
	if _, _, _, _, err := a.callOllama(ctx, "", events); err != nil {
		t.Fatal(err)
	}
	loaded := make(map[string]ollama.Tool)
	for _, tool := range backend.capture.req.Tools {
		if tool.Function.Name != "tool_catalog" {
			loaded[tool.Function.Name] = tool
		}
	}
	if len(loaded) != len(full) {
		t.Fatalf("loading all permitted tools lost schemas: %d vs %d", len(loaded), len(full))
	}
	for _, tool := range full {
		if !reflect.DeepEqual(loaded[tool.Function.Name], tool) {
			t.Errorf("tool contract changed: %s", tool.Function.Name)
		}
	}
	if catalogFromContext(withClaudeCatalog(t.Context(), a.model, backend)).loaded["note"] {
		t.Fatal("loaded tools escaped their turn")
	}
}

func TestOpenAIClientCatalogDoesNotChangeCompatibleServers(t *testing.T) {
	t.Setenv("PRISM_OPENAI_LAZY_TOOLS", "1")
	for _, endpoint := range []string{"http://127.0.0.1:30000/v1", "https://gateway.example/v1"} {
		backend := &openAICatalogUsageBackend{Client: openai.NewClient(endpoint, "synthetic-key")}
		a := &Agent{ollama: backend, executor: &ToolExecutor{}, limits: Limits{PromptProfile: "minimal"}, model: "gpt-5.5"}
		ctx := withClaudeCatalog(t.Context(), a.model, backend)
		if catalogFromContext(ctx) != nil {
			t.Fatal("a compatible endpoint opted in based on its model name")
		}
		if _, _, _, _, err := a.callOllama(ctx, "", make(chan Event, 16)); err != nil {
			t.Fatal(err)
		}
		if backend.capture.req.ClientToolCatalog || len(backend.capture.req.Tools) != len(a.buildToolList()) {
			t.Fatal("compatible server lost its ordinary catalog")
		}
	}
}
