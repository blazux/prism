package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"prism/internal/ollama"
)

func TestToolSearchSupportedModels(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-5", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5",
		"claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5", "claude-sonnet-5-5",
		"claude-haiku-4-5", "claude-haiku-5-5",
		"claude-fable-5", "claude-fable-5-1", "claude-fable-5-2",
		"claude-mythos-5", "claude-mythos-5-1", "claude-mythos-5-2",
		"claude-opus-4-5-20251101", "claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001",
	} {
		if !supportsToolSearch(model) {
			t.Errorf("documented tool-search model excluded: %s", model)
		}
	}
	for _, model := range []string{"", "claude-opus-4-1", "claude-opus-4-1-20250805", "claude-sonnet-4", "claude-3-7-sonnet-latest", "claude-haiku-5", "claude-sonnet-6", "claude-sonnet-4-6-preview", "claude-sonnet-4-6-20261399", "claude-sonnet-4-6-20260101-extra", "source::claude-sonnet-4-6", "gpt-5"} {
		if supportsToolSearch(model) {
			t.Errorf("unknown or unsupported model admitted: %s", model)
		}
	}
}

func TestNativeToolSearchUsesTheActualRequestCatalog(t *testing.T) {
	for _, tc := range []struct {
		name, model, nativeFlag, lazyFlag string
		clientCatalog                     bool
		toolCount                         int
		search                            bool
	}{
		{"default off", "claude-sonnet-4-6", "", "", false, 9, false},
		{"documented model", "claude-opus-4-6", "1", "", false, 9, true},
		{"unknown model", "claude-sonnet-6", "1", "", false, 9, false},
		{"unsupported old model", "claude-opus-4-1", "1", "", false, 9, false},
		{"small catalogue", "claude-sonnet-4-6", "1", "", false, 7, false},
		{"client selected", "claude-sonnet-4-6", "1", "", true, 9, false},
		{"global lazy flag is not an active catalog", "claude-sonnet-4-6", "1", "1", false, 9, true},
		{"active client wins", "claude-sonnet-4-6", "1", "1", true, 9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", tc.nativeFlag)
			t.Setenv("PRISM_CLAUDE_LAZY_TOOLS", tc.lazyFlag)
			t.Setenv("PRISM_ANTHROPIC_CACHE_TTL", "")
			var wire apiRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				if tc.search {
					fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
				} else {
					fmt.Fprint(w, sse(`{"type":"message_stop"}`))
				}
			}))
			defer server.Close()
			client := NewClient(server.URL, "sk-ant-api03-fixture", "")
			_, _, _, err := collect(t, client, ollama.ChatRequest{Model: tc.model, ClientToolCatalog: tc.clientCatalog,
				Messages: []ollama.Message{{Role: "user", Content: "Fixture"}}, Tools: cacheFixtureTools()[:tc.toolCount]})
			if err != nil {
				t.Fatal(err)
			}
			found := len(wire.Tools) > 0 && wire.Tools[0].Type == "tool_search_tool_bm25_20251119"
			if found != tc.search || wire.Stream == tc.search {
				t.Fatalf("search=%v stream=%v, expected search=%v", found, wire.Stream, tc.search)
			}
			if client.UseClientToolCatalog(t.Context(), tc.model) != (tc.lazyFlag == "1") {
				t.Fatal("native client capability must follow its own opt-in flag")
			}
		})
	}
}
