package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeCatalogOnlyOptedInForClaude(t *testing.T) {
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "1")
	for _, model := range []string{"openai::gpt-5", "claude-sonnet-5-5", "ollama::qwen"} {
		if catalogFromContext(withClaudeCatalog(t.Context(), model, false)) != nil {
			t.Fatalf("catalog enabled for %s", model)
		}
	}
	if catalogFromContext(withClaudeCatalog(t.Context(), "anthropic::claude-sonnet-5-5", true)) != nil {
		t.Fatal("guarded caller received catalog")
	}
	ctx := withClaudeCatalog(t.Context(), "anthropic::claude-sonnet-5-5", false)
	if catalogFromContext(ctx) == nil {
		t.Fatal("Claude catalog not enabled")
	}
	if catalogFromContext(context.Background()) != nil {
		t.Fatal("catalog escaped its turn")
	}
	t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", "")
	if catalogFromContext(withClaudeCatalog(t.Context(), "anthropic::claude-sonnet-5-5", false)) != nil {
		t.Fatal("catalog enabled without opt-in")
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
	if _, _, _, _, err := a.callOllama(withClaudeCatalog(t.Context(), a.model, false), "", events); err != nil {
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
	claude := &Agent{ollama: backend, executor: &ToolExecutor{}, limits: Limits{PromptProfile: "minimal"}, model: "anthropic::claude-sonnet-5-5"}
	ctx := withClaudeCatalog(t.Context(), claude.model, false)
	if _, _, _, _, err := claude.callOllama(ctx, "", events); err != nil {
		t.Fatal(err)
	}
	if len(backend.req.Tools) != 7 || backend.req.CacheToolPrefixCount != 7 {
		t.Fatalf("Claude initial request: %d tools, prefix=%d", len(backend.req.Tools), backend.req.CacheToolPrefixCount)
	}
	if _, ok := backend.req.Tools[6].Function.Parameters.Properties["names"]; !ok {
		t.Fatal("catalog schema missing")
	}
}
