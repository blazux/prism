package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestCacheWriteUsageTiers(t *testing.T) {
	for _, tc := range []struct {
		name, ttl, usage string
		five, hour       *int64
		write, total     *int64
		unknown          bool
	}{
		{"mixed lifetimes", "1h", `{"input_tokens":10,"cache_read_input_tokens":70,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}`, int64ptr(5), int64ptr(15), int64ptr(20), int64ptr(100), false},
		{"legacy five-minute response", "", `{"input_tokens":10,"cache_read_input_tokens":70,"cache_creation_input_tokens":20}`, nil, nil, int64ptr(20), int64ptr(100), false},
		{"one hour does not imply all writes are one hour", "1h", `{"input_tokens":10,"cache_read_input_tokens":70,"cache_creation_input_tokens":20}`, nil, nil, int64ptr(20), int64ptr(100), true},
		{"zero writes need no price tier", "1h", `{"input_tokens":10,"cache_creation_input_tokens":0}`, nil, nil, int64ptr(0), int64ptr(10), false},
		{"inconsistent totals", "5m", `{"input_tokens":10,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":10}}`, int64ptr(5), int64ptr(10), int64ptr(20), int64ptr(30), true},
		{"partial breakdown", "5m", `{"input_tokens":10,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":20}}`, int64ptr(20), nil, int64ptr(20), int64ptr(30), true},
		{"derive total from both reported tiers", "1h", `{"input_tokens":10,"cache_read_input_tokens":70,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}`, int64ptr(5), int64ptr(15), int64ptr(20), int64ptr(100), false},
		{"partial tiers cannot establish total input", "1h", `{"input_tokens":10,"cache_creation":{"ephemeral_1h_input_tokens":15}}`, nil, int64ptr(15), nil, nil, true},
		{"negative provider counts", "5m", `{"input_tokens":10,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":-5,"ephemeral_1h_input_tokens":25}}`, int64ptr(-5), int64ptr(25), int64ptr(20), int64ptr(30), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw streamUsage
			if err := json.Unmarshal([]byte(tc.usage), &raw); err != nil {
				t.Fatal(err)
			}
			got := raw.usage(tc.ttl)
			assertUsageCount(t, "5m", got.CacheWrite5mTokens, tc.five)
			assertUsageCount(t, "1h", got.CacheWrite1hTokens, tc.hour)
			assertUsageCount(t, "write", got.CacheWriteTokens, tc.write)
			assertUsageCount(t, "total", got.InputTokens, tc.total)
			if got.CacheWritePricingUnknown != tc.unknown {
				t.Fatalf("pricing unknown=%v, want %v", got.CacheWritePricingUnknown, tc.unknown)
			}
		})
	}
}

func TestStreamMergesCacheTierSnapshots(t *testing.T) {
	for _, stop := range []bool{false, true} {
		raw := sse(
			`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":70,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5}}}}`,
			`{"type":"message_delta","usage":{"output_tokens":7,"cache_creation":{"ephemeral_1h_input_tokens":15}}}`,
			`{"type":"message_delta","usage":{"output_tokens":8},"delta":{"stop_reason":"end_turn"}}`,
		)
		if stop {
			raw += sse(`{"type":"message_stop"}`)
		}
		channel := make(chan ollama.StreamEvent, 5)
		(&Client{}).readStream(t.Context(), strings.NewReader(raw), "fixture", 100, channel, "1h")
		close(channel)
		var usage *ollama.Usage
		for event := range channel {
			if event.Usage != nil {
				usage = event.Usage
			}
		}
		if usage == nil || usage.CacheWritePricingUnknown {
			t.Fatalf("cache tiers lost on terminal event: %+v", usage)
		}
		assertUsageCount(t, "5m", usage.CacheWrite5mTokens, int64ptr(5))
		assertUsageCount(t, "1h", usage.CacheWrite1hTokens, int64ptr(15))
		assertUsageCount(t, "total", usage.InputTokens, int64ptr(100))
		assertUsageCount(t, "output", usage.OutputTokens, int64ptr(8))
	}
}

func TestCacheTTLReachesNonStreamingUsageAccounting(t *testing.T) {
	t.Setenv("PRISM_ANTHROPIC_CACHE_TTL", "1h")
	t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", "1")
	for _, body := range []string{
		`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":20}}`,
		`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, body)
		}))
		client := NewClient(server.URL, "sk-ant-api03-fixture", "")
		channel := make(chan ollama.StreamEvent, 5)
		client.Chat(t.Context(), ollama.ChatRequest{Model: "claude-opus-4-6", Messages: []ollama.Message{{Role: "user", Content: "Fixture"}}, Tools: cacheFixtureTools()}, channel)
		server.Close()
		event := <-channel
		if event.Err != nil || event.Usage == nil {
			t.Fatalf("non-streaming usage missing: %+v", event)
		}
		wantUnknown := !strings.Contains(body, "ephemeral_5m_input_tokens")
		if event.Usage.CacheWritePricingUnknown != wantUnknown {
			t.Fatalf("TTL1h missing-tier handling incorrect: %+v", event.Usage)
		}
	}
}

func int64ptr(value int64) *int64 { return &value }

func assertUsageCount(t *testing.T, label string, got, want *int64) {
	t.Helper()
	if got == nil && want == nil {
		return
	}
	if got == nil || want == nil || *got != *want {
		t.Fatalf("%s counter got %v want %v", label, got, want)
	}
}
