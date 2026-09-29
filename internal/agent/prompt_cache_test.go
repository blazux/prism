package agent

import (
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestSystemPromptCachePrefixIgnoresLiveContext(t *testing.T) {
	service := "service A"
	a := &Agent{
		executor:      &ToolExecutor{},
		sessionID:     "test-session",
		limits:        Limits{PromptProfile: "standard"},
		servicesCtxFn: func() string { return service },
	}
	first, firstPrefix := a.buildSystemPromptWithCachePrefix(t.Context(), "")
	service = "service B"
	second, secondPrefix := a.buildSystemPromptWithCachePrefix(t.Context(), "")
	if firstPrefix == 0 || firstPrefix != secondPrefix || first[:firstPrefix] != second[:secondPrefix] {
		t.Fatal("live context changed the cacheable system prefix")
	}
	if first == second || !strings.Contains(second[secondPrefix:], service) {
		t.Fatal("live context was lost from the uncached suffix")
	}
}

func TestToolCachePrefixAfterVisibleNativeTools(t *testing.T) {
	dynamic := ollama.Tool{Function: ollama.ToolFunction{Name: "cost_test_dynamic"}}
	a := &Agent{executor: &ToolExecutor{telephonyTools: []ollama.Tool{dynamic}, hiddenTools: map[string]bool{"read_file": true}}, disabledTools: []string{"cron"}}
	tools, prefix := a.buildToolListWithCachePrefix()
	if prefix <= 0 || prefix != len(tools)-1 || tools[prefix].Function.Name != dynamic.Function.Name {
		t.Fatalf("wrong native cache boundary: prefix=%d tools=%d", prefix, len(tools))
	}
	for _, tool := range tools[:prefix] {
		if tool.Function.Name == "read_file" || tool.Function.Name == "cron" {
			t.Fatalf("hidden or disabled tool included: %s", tool.Function.Name)
		}
	}
}

func TestToolListDeduplicatesNamesBeforeAnthropicRequest(t *testing.T) {
	first := ollama.Tool{Function: ollama.ToolFunction{Name: "weather_probe", Description: "first"}}
	second := ollama.Tool{Function: ollama.ToolFunction{Name: "weather_probe", Description: "second"}}
	nativeCollision := ollama.Tool{Function: ollama.ToolFunction{Name: "read_file", Description: "dynamic collision"}}
	a := &Agent{executor: &ToolExecutor{telephonyTools: []ollama.Tool{first, second, nativeCollision}}}
	tools, prefix := a.buildToolListWithCachePrefix()
	if prefix != len(tools)-1 {
		t.Fatalf("native cache prefix=%d, tools=%d; want one dynamic tool", prefix, len(tools))
	}
	counts := make(map[string]int)
	for _, tool := range tools {
		counts[tool.Function.Name]++
	}
	if counts["weather_probe"] != 1 || counts["read_file"] != 1 || tools[len(tools)-1].Function.Description != "first" {
		t.Fatalf("duplicate tools exposed or wrong precedence: %#v", tools)
	}
}
