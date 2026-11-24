package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type catalogUsageBackend struct {
	*usageBackend
	enabled bool
}

func (b *catalogUsageBackend) UseClientToolCatalog(context.Context, string) bool { return b.enabled }

func TestClaudeCatalogUsesResolvedProviderNotSelector(t *testing.T) {
	backend := &catalogUsageBackend{usageBackend: &usageBackend{}, enabled: true}
	for _, model := range []string{"claude-opus-4-6", "primary::claude-sonnet-5-5", "custom_source::claude-sonnet-5", "anthropic::claude-sonnet-5-5"} {
		ctx := withClaudeCatalog(t.Context(), model, backend)
		if catalogFromContext(ctx) == nil {
			t.Fatalf("catalog not enabled for resolved Claude source %s", model)
		}
	}
	for _, backend := range []ollama.Backend{&usageBackend{}, &catalogUsageBackend{usageBackend: &usageBackend{}}} {
		if catalogFromContext(withClaudeCatalog(t.Context(), "anthropic::claude-sonnet-5-5", backend)) != nil {
			t.Fatal("model label enabled a catalog without provider opt-in")
		}
	}
	if catalogFromContext(context.Background()) != nil {
		t.Fatal("catalog escaped its turn")
	}
}

func TestClaudeCatalogLoadsOnlyAvailableTools(t *testing.T) {
	a := &Agent{executor: &ToolExecutor{hiddenTools: map[string]bool{"email": true}}, disabledTools: []string{"cron"}, limits: Limits{PromptProfile: "minimal"}}
	full, _ := a.buildToolListWithCachePrefix()
	catalog := &claudeCatalog{loaded: make(map[string]bool)}
	visible, prefix := catalog.selectTools(full)
	if prefix != len(visible) || len(visible) != 6 || visible[5].Function.Name != "tool_catalog" {
		t.Fatalf("initial catalog: %d tools, prefix=%d", len(visible), prefix)
	}
	index, err := catalog.execute(json.RawMessage(`{"action":"list"}`), full)
	if err != nil || !strings.Contains(index, "note:") || strings.Contains(index, "email:") || strings.Contains(index, "cron:") {
		t.Fatalf("index leaked or lost tools: %v %s", err, index)
	}
	result, err := catalog.execute(json.RawMessage(`{"action":"load","names":["note","cron","email","missing"]}`), full)
	if err != nil || !strings.Contains(result, "Loaded: note") || !strings.Contains(result, "Unavailable: cron, email, missing") {
		t.Fatalf("wrong load result: %v %s", err, result)
	}
	visible, prefix = catalog.selectTools(full)
	if prefix != 6 || len(visible) != 7 || visible[6].Function.Name != "note" {
		t.Fatalf("wrong loaded tools: prefix=%d %+v", prefix, visible)
	}
	if _, err := catalog.execute(json.RawMessage(`{"action":"load"}`), full); err == nil {
		t.Fatal("empty load accepted")
	}
	if _, err := catalog.execute(json.RawMessage(`{"action":"unknown"}`), full); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestClaudeCatalogDoesNotChangeOtherRequests(t *testing.T) {
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "1")
	backend := &usageBackend{}
	a := &Agent{ollama: backend, executor: &ToolExecutor{}, limits: Limits{PromptProfile: "minimal"}, model: "openai::gpt-5"}
	events := make(chan Event, 10)
	if _, _, _, _, err := a.callOllama(withClaudeCatalog(t.Context(), a.model, a.ollama), "", events); err != nil {
		t.Fatal(err)
	}
	if len(backend.req.Tools) != len(a.buildToolList()) {
		t.Fatal("non-Claude request lost tools")
	}
	for _, tool := range backend.req.Tools {
		if tool.Function.Name == "tool_catalog" {
			t.Fatal("Claude-only tool leaked to another provider")
		}
	}
	claude := &Agent{ollama: &catalogUsageBackend{usageBackend: backend, enabled: true}, executor: &ToolExecutor{}, limits: Limits{PromptProfile: "minimal"}, model: "anthropic::claude-sonnet-5-5"}
	ctx := withClaudeCatalog(t.Context(), claude.model, claude.ollama)
	if _, _, _, _, err := claude.callOllama(ctx, "", events); err != nil {
		t.Fatal(err)
	}
	if len(backend.req.Tools) != 7 || backend.req.CacheToolPrefixCount != 7 || !backend.req.ClientToolCatalog {
		t.Fatalf("Claude initial request: %d tools, prefix=%d", len(backend.req.Tools), backend.req.CacheToolPrefixCount)
	}
	if _, ok := backend.req.Tools[6].Function.Parameters.Properties["names"]; !ok {
		t.Fatal("catalog schema missing")
	}
}

func TestClaudeCatalogPreservesExecutionGuard(t *testing.T) {
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "1")
	denied := false
	executor := &ToolExecutor{toolGuard: func(name string, _ map[string]interface{}) error {
		if name == "note" {
			denied = true
			return context.Canceled
		}
		return nil
	}}
	a := &Agent{executor: executor, ollama: &catalogUsageBackend{usageBackend: &usageBackend{}, enabled: true}, model: "anthropic::claude-sonnet-5-5"}
	ctx := withClaudeCatalog(t.Context(), a.model, a.ollama)
	catalog := catalogFromContext(ctx)
	if catalog == nil {
		t.Fatal("Claude catalog disabled for guarded Cloud caller")
	}
	full, _ := a.buildToolListWithCachePrefix()
	result, err := catalog.execute(json.RawMessage(`{"action":"load","names":["note"]}`), full)
	if err != nil || !strings.Contains(result, "Unavailable: note") {
		t.Fatalf("denied tool was exposed by the catalogue: %v %s", err, result)
	}
	if _, _, err := executor.Execute(ctx, "note", json.RawMessage(`{"action":"list"}`)); err != context.Canceled || !denied {
		t.Fatalf("loaded tool bypassed its execution guard: %v", err)
	}
}
