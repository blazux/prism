package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"prism/internal/ollama"
)

// All requests use a fake RoundTripper, including the official-looking URL.
// No test can reach an external API or spend funds.
func capturePolicyRequest(t *testing.T, base string, req ollama.ChatRequest) (map[string]any, *ollama.Usage, error) {
	t.Helper()
	c := NewClient(base, "synthetic-key")
	var body map[string]any
	c.httpClient.Transport = streamFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
			"data: {\"service_tier\":\"default\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2000,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":1024},\"completion_tokens_details\":{\"reasoning_tokens\":10}}}\n\ndata: [DONE]\n\n"))}, nil
	})
	out := make(chan ollama.StreamEvent, 16)
	c.Chat(context.Background(), req, out)
	close(out)
	var usage *ollama.Usage
	for event := range out {
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Err != nil {
			return body, usage, event.Err
		}
	}
	return body, usage, nil
}

func TestOfficialPolicyAppliesToEveryCallerAndKnownModels(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name, model, effort, wantEffort string
		noThinking                      bool
		choice                          *bool
		completion, sampling            bool
	}{
		{name: "ordinary GPT5", model: "gpt-5", effort: "high", wantEffort: "high", completion: true},
		{name: "summary nil choice", model: "gpt-5.2", noThinking: true, wantEffort: "none", completion: true, sampling: true},
		{name: "old GPT5 minimal", model: "gpt-5-2025-08-07", noThinking: true, wantEffort: "minimal", completion: true},
		{name: "old GPT5 none translates", model: "gpt-5-mini", effort: "none", wantEffort: "minimal", completion: true},
		{name: "o3 low", model: "o3", noThinking: true, wantEffort: "low", completion: true},
		{name: "explicit off wins", model: "gpt-5.4", choice: &off, effort: "xhigh", wantEffort: "none", completion: true, sampling: true},
		{name: "no thinking wins", model: "gpt-5.5", choice: &on, noThinking: true, effort: "high", wantEffort: "none", completion: true, sampling: true},
		{name: "GPT4 ordinary", model: "gpt-4.1", sampling: true},
		{name: "GPT4 explicit unsupported ignored", model: "gpt-4o", effort: "high", sampling: true},
		{name: "unknown keeps ordinary contract", model: "unknown-model", sampling: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, k := .9, 20
			body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: tc.model, NoThinking: tc.noThinking, EnableThinking: tc.choice,
				ReasoningEffort: tc.effort, Options: ollama.Options{Temperature: .7, NumPredict: 1234, TopP: &p, TopK: &k}})
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"chat_template_kwargs", "top_k", "prompt_cache_options"} {
				if _, present := body[forbidden]; present {
					t.Fatalf("native request received %s", forbidden)
				}
			}
			if got, _ := body["reasoning_effort"].(string); got != tc.wantEffort {
				t.Fatalf("effort %q, want %q", got, tc.wantEffort)
			}
			field, absent := "max_tokens", "max_completion_tokens"
			if tc.completion {
				field, absent = absent, field
			}
			if body[field] != float64(1234) || body[absent] != nil {
				t.Fatalf("generation budget: %#v", body)
			}
			if tc.sampling {
				if body["temperature"] != .7 || body["top_p"] != .9 {
					t.Fatal("sampling was lost")
				}
			} else if body["temperature"] != nil || body["top_p"] != nil {
				t.Fatal("unsupported sampling leaked")
			}
		})
	}
}

func TestOfficialPolicyDoesNotLeakIntoCompatibleEndpoints(t *testing.T) {
	t.Setenv("PRISM_OPENAI_CACHE_RETENTION", "24h")
	for _, base := range []string{"http://gateway/v1", "https://api.openai.com/gateway/v1", "http://api.openai.com/v1", "https://api.openai.com.evil/v1",
		"https://api.openai.com:444/v1", "https://user@api.openai.com/v1", "https://api.openai.com/v1?route=other",
		"https://api.openai.com/v1?", "https://api.openai.com/v1#other", "https://api.openai.com/v1#", "https://api.openai.com/%761"} {
		t.Run(base, func(t *testing.T) {
			k := 20
			body, usage, err := capturePolicyRequest(t, base, ollama.ChatRequest{Model: "gpt-5.2", NoThinking: true, CacheSystemPrefixBytes: 6, Messages: []ollama.Message{{Role: "system", Content: "shared workspace-main"}}, Options: ollama.Options{NumPredict: 123, TopK: &k}})
			if err != nil {
				t.Fatal(err)
			}
			kw, _ := body["chat_template_kwargs"].(map[string]any)
			if kw["enable_thinking"] != false || body["max_tokens"] != float64(123) || body["top_k"] != float64(20) {
				t.Fatalf("compatible contract changed: %#v", body)
			}
			if body["max_completion_tokens"] != nil || body["prompt_cache_key"] != nil || body["prompt_cache_retention"] != nil {
				t.Fatal("native-only fields leaked")
			}
			if usage == nil || usage.PricingProvider != "" || usage.ServiceTier != "" {
				t.Fatal("compatible response was priced as OpenAI")
			}
		})
	}
}

func TestOfficialUsageKeepsCacheReasoningAndReportedTier(t *testing.T) {
	for _, base := range []string{"https://api.openai.com/v1/", "https://api.openai.com:443/v1", "https://API.OPENAI.COM/v1/"} {
		body, usage, err := capturePolicyRequest(t, base, ollama.ChatRequest{Model: "gpt-5.5", NoThinking: true, Options: ollama.Options{NumPredict: 100}})
		if err != nil {
			t.Fatal(err)
		}
		if usage == nil || usage.PricingProvider != "openai" || usage.ServiceTier != "default" || *usage.InputTokens != 2000 || *usage.CacheReadTokens != 1024 || *usage.OutputTokens != 20 || *usage.ReasoningTokens != 10 {
			t.Fatalf("usage attribution changed for %s: %+v", base, usage)
		}
		if body["max_completion_tokens"] != float64(100) || body["max_tokens"] != nil || body["reasoning_effort"] != "none" || body["chat_template_kwargs"] != nil {
			t.Fatalf("equivalent official endpoint received compatible parameters: %#v", body)
		}
	}
}

func TestGPT6ToolCallingFailsBeforeAnyRequest(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6.2-astra", "gpt-6.2-sol"} {
		body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: model, Tools: []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "list_files"}}}})
		if err == nil || !strings.Contains(err.Error(), "Responses API") || body != nil {
			t.Fatalf("unsupported tools reached Chat Completions for %s: %v", model, err)
		}
	}
}

func TestGPT6RestrictionDoesNotInferCapabilitiesForUnknownModels(t *testing.T) {
	body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: "gpt-6.9-unknown", Tools: []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "list_files"}}}})
	if err != nil || len(body["tools"].([]any)) != 1 || body["reasoning_effort"] != nil {
		t.Fatalf("unknown model acquired guessed capabilities: %v", err)
	}
}

func TestOmittedNativeEffortUsesModelSamplingDefault(t *testing.T) {
	old := reasoningEffort
	reasoningEffort = "" // OPENAI_REASONING_EFFORT=none deliberately omits effort
	t.Cleanup(func() { reasoningEffort = old })
	for _, model := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.4"} {
		p := .9
		body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: model, Options: ollama.Options{Temperature: .7, TopP: &p}})
		if err != nil || body["reasoning_effort"] != nil || body["temperature"] != .7 || body["top_p"] != .9 {
			t.Fatalf("omitted effort lost none-default sampling for %s: %v", model, err)
		}
	}
}

func TestOfficialCacheFieldsReachTheRequestWithoutChangingHistory(t *testing.T) {
	t.Setenv("PRISM_OPENAI_CACHE_RETENTION", "24h")
	body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: "gpt-5.2", CacheSystemPrefixBytes: 6,
		Messages: []ollama.Message{{Role: "system", Content: "shared workspace-main"}, {Role: "user", Content: "My request"}}})
	if err != nil || body["prompt_cache_key"] == nil || body["prompt_cache_retention"] != "24h" {
		t.Fatalf("cache policy was not sent: %v", err)
	}
	messages := body["messages"].([]any)
	if messages[0].(map[string]any)["content"] != "shared workspace-main" || messages[1].(map[string]any)["content"] != "My request" {
		t.Fatal("cache routing rewrote conversation contents")
	}
}

func TestUnsupportedNativeReasoningChoiceFailsBeforeRequest(t *testing.T) {
	body, _, err := capturePolicyRequest(t, "https://api.openai.com/v1", ollama.ChatRequest{Model: "o3", ReasoningEffort: "xhigh"})
	if err == nil || body != nil {
		t.Fatal("unsupported reasoning effort reached provider")
	}
}
