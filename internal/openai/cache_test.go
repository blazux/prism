package openai

import (
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestCacheKeySharesOnlyMarkedPrefixAcrossConversations(t *testing.T) {
	const prefix = "Shared instructions with accents: réseau.\n"
	req := ollama.ChatRequest{Model: "gpt-5.2", CacheSystemPrefixBytes: len(prefix), Messages: []ollama.Message{{Role: "system", Content: prefix + "Workspace finance"}, {Role: "user", Content: "First question"}}}
	c := NewClient("https://api.openai.com/v1", "synthetic-credential")
	key := func() string { p := chatRequest{}; c.configurePromptCache(&p, req); return p.PromptCacheKey }
	first := key()
	if first == "" || len(first) > 64 || strings.Contains(first, "credential") || strings.Contains(first, "réseau") {
		t.Fatal("invalid opaque routing key")
	}
	req.Messages[0].Content = prefix + "Workspace main"
	req.Messages[1].Content = "Another question"
	req.Tools = []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "dynamic-tool"}}}
	if key() != first {
		t.Fatal("dynamic workspace/history/tools fragmented the shared-prefix key")
	}
	req.Messages[0].Content = "Other" + req.Messages[0].Content[5:]
	if key() == first {
		t.Fatal("changed shared instructions retained the old key")
	}
	req.Messages[0].Content = prefix
	c.apiKey = "other-synthetic-credential"
	if key() == first {
		t.Fatal("credentials did not separate cache routing")
	}
}

func TestCacheRoutingCoversSupportedNonReasoningSnapshots(t *testing.T) {
	t.Setenv("PRISM_OPENAI_CACHE_RETENTION", "24h")
	for _, model := range []string{"gpt-4.1-mini", "gpt-4.1-nano-2025-04-14", "gpt-4o-2024-08-06", "gpt-4o-mini-2024-07-18"} {
		p := chatRequest{}
		NewClient("https://api.openai.com/v1", "fixture").configurePromptCache(&p, ollama.ChatRequest{
			Model: model, CacheSystemPrefixBytes: 6, Messages: []ollama.Message{{Role: "system", Content: "shared dynamic"}},
		})
		if p.PromptCacheKey == "" || p.PromptCacheRetention != "" {
			t.Fatalf("%s: routing must work without requesting unsupported extended retention", model)
		}
	}
}

func TestCacheRetentionRequiresOptInAndExactSupportedModel(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-5.4-2026-03-05", "gpt-5.2", "gpt-5.1", "gpt-5", "gpt-4.1-2025-04-14"} {
		for _, setting := range []string{"", "default", "24h", "1h", "in_memory"} {
			t.Run(model+"/"+setting, func(t *testing.T) {
				t.Setenv("PRISM_OPENAI_CACHE_RETENTION", setting)
				p := chatRequest{}
				NewClient("https://api.openai.com/v1", "fixture").configurePromptCache(&p, ollama.ChatRequest{Model: model})
				want := ""
				if setting == "24h" {
					want = "24h"
				}
				if p.PromptCacheRetention != want {
					t.Fatalf("retention %q, want %q", p.PromptCacheRetention, want)
				}
			})
		}
	}
	t.Setenv("PRISM_OPENAI_CACHE_RETENTION", "24h")
	for _, model := range []string{"gpt-5-mini", "gpt-5.4-mini", "gpt-5.4-pro", "gpt-4.1-mini", "gpt-5.2-2099-01-01", "o3", "unknown", "gpt-5.6", "gpt-6.2-sol"} {
		p := chatRequest{}
		NewClient("https://api.openai.com/v1", "fixture").configurePromptCache(&p, ollama.ChatRequest{Model: model})
		if p.PromptCacheRetention != "" {
			t.Fatalf("unsupported retention for %s", model)
		}
	}
}

func TestCacheRoutingOmitsUnknownUnmarkedAndNewAutomaticModels(t *testing.T) {
	t.Setenv("PRISM_OPENAI_CACHE_RETENTION", "24h")
	for _, tc := range []struct {
		model, text string
		prefix      int
	}{
		{"gpt-5.2", "dynamic session only", 0},
		{"gpt-5.2", "small", 100},
		{"gpt-5.2", "é", 1},
		{"gpt-5.6", "shared", 6},
		{"gpt-6.2-sol", "shared", 6},
		{"unknown", "shared", 6},
	} {
		p := chatRequest{}
		NewClient("https://api.openai.com/v1", "fixture").configurePromptCache(&p, ollama.ChatRequest{Model: tc.model, CacheSystemPrefixBytes: tc.prefix, Messages: []ollama.Message{{Role: "system", Content: tc.text}}})
		if p.PromptCacheKey != "" {
			t.Fatalf("unexpected routing key for %s", tc.model)
		}
	}
}
