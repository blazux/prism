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

func discoveryHistory() []ollama.Message {
	return []ollama.Message{
		{Role: "user", Content: "List the notes."},
		{Role: "assistant", Content: "Checking notes.", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "note", Arguments: json.RawMessage(`{"action":"list"}`)}}},
			ProviderBlocks: []json.RawMessage{
				json.RawMessage(`{"type":"text","text":"Checking notes."}`),
				json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_1","name":"tool_search_tool_bm25","input":{"query":"notes"}}`),
				json.RawMessage(`{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_1","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"note"}]},"opaque":"preserve"}`),
				json.RawMessage(`{"type":"tool_use","id":"toolu_note","name":"note","input":{"action":"list"}}`),
			}},
		{Role: "tool", Content: "No notes."},
	}
}

func TestToolSearchReplayFollowsModeAndCurrentPermissions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		native, remove bool
	}{
		{"still enabled and permitted", true, false},
		{"native discovery disabled after reload", false, false},
		{"tool permission removed after reload", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := discoveryHistory()
			tools := cacheFixtureTools()
			if tc.remove {
				filtered := tools[:0]
				for _, tool := range tools {
					if tool.Function.Name != "note" {
						filtered = append(filtered, tool)
					}
				}
				tools = filtered
			}
			got := buildMessagesForToolSearch(history, tc.native, tools)
			body, _ := json.Marshal(got)
			wantNative := tc.native && !tc.remove
			if strings.Contains(string(body), `"tool_reference"`) != wantNative || strings.Contains(string(body), `"server_tool_use"`) != wantNative {
				t.Fatalf("native replay mismatch: %s", body)
			}
			if !strings.Contains(string(body), "Checking notes.") || !strings.Contains(string(body), "No notes.") || !strings.Contains(string(body), `"name":"note"`) {
				t.Fatalf("canonical history was lost: %s", body)
			}
			if wantNative && !strings.Contains(string(body), `"opaque":"preserve"`) {
				t.Fatal("provider-only field lost")
			}
			if !wantNative {
				call := got[1].Content[1]
				if call.Type != "tool_use" || got[2].Content[0].ToolUseID != call.ID {
					t.Fatal("canonical replay no longer pairs the historical tool result")
				}
			}
			if len(history[1].ProviderBlocks) != 4 {
				t.Fatal("conversion mutated original history")
			}
		})
	}
}

func TestChatDoesNotReviveRemovedToolsForNativeReplay(t *testing.T) {
	t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", "1")
	tools := cacheFixtureTools()
	for index, tool := range tools {
		if tool.Function.Name == "note" {
			tools = append(tools[:index:index], tools[index+1:]...)
			break
		}
	}
	var sent apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()
	_, _, _, err := collect(t, NewClient(server.URL, "sk-ant-api03-fixture", ""), ollama.ChatRequest{Model: "claude-sonnet-4-6", Messages: discoveryHistory(), Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range sent.Tools {
		if tool.Name == "note" {
			t.Fatal("historical discovery revived a removed tool schema")
		}
	}
	body, _ := json.Marshal(sent.Messages)
	if strings.Contains(string(body), `"tool_reference"`) || strings.Contains(string(body), `"server_tool_use"`) {
		t.Fatalf("removed-tool references escaped into request: %s", body)
	}
}

func TestInvalidSearchReferencesUseCanonicalReplay(t *testing.T) {
	for _, raw := range []string{
		`{"type":"tool_search_tool_result","content":{"tool_references":[{"type":"tool_reference","tool_name":"missing"}]}}`,
		`{"type":"tool_search_tool_result","content":{"tool_references":[{"type":"tool_reference"}]}}`,
		`{"type":"server_tool_use","id":"server_1","name":"an_undeclared_server_tool"}`,
		`{"type":"tool_use","id":"tool_1","name":"removed"}`,
		`null`,
	} {
		history := discoveryHistory()
		history[1].ProviderBlocks = []json.RawMessage{json.RawMessage(raw)}
		got := buildMessagesForToolSearch(history, true, cacheFixtureTools())
		if len(got[1].Content) != 2 || got[1].Content[1].Type != "tool_use" || got[1].Content[1].Name != "note" {
			t.Fatalf("invalid provider history did not fall back to canonical content: %s", raw)
		}
	}
}

func TestPausedSearchHistoryDoesNotCrossANewUserRequest(t *testing.T) {
	call := func(id string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"server_tool_use","id":%q,"name":"tool_search_tool_bm25","input":{"query":"notes"}}`, id))
	}
	result := func(id string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"tool_search_tool_result","tool_use_id":%q,"content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"note"}]}}`, id))
	}
	assistant := func(text string, blocks ...json.RawMessage) ollama.Message {
		return ollama.Message{Role: "assistant", Content: text, ProviderBlocks: blocks}
	}
	user := ollama.Message{Role: "user", Content: "A new user request."}
	for _, tc := range []struct {
		name    string
		history []ollama.Message
		native  bool
	}{
		{"pending at end is a valid continuation", []ollama.Message{user, assistant("first", call("srvtoolu_a"))}, true},
		{"result in following assistant", []ollama.Message{user, assistant("first", call("srvtoolu_a")), assistant("second", result("srvtoolu_a"))}, true},
		{"resolved search before next request", []ollama.Message{user, assistant("first", call("srvtoolu_a")), assistant("second", result("srvtoolu_a")), user}, true},
		{"abandoned search before next request", []ollama.Message{user, assistant("first", call("srvtoolu_a")), user}, false},
		{"late result after next request", []ollama.Message{user, assistant("first", call("srvtoolu_a")), user, assistant("second", result("srvtoolu_a"))}, false},
		{"shared assistant connects the two searches", []ollama.Message{user, assistant("first", call("srvtoolu_a")), assistant("second", result("srvtoolu_a"), call("srvtoolu_b")), user}, false},
		{"orphan after a valid result cannot strand the first call", []ollama.Message{user, assistant("first", call("srvtoolu_a")), assistant("second", result("srvtoolu_a"), result("srvtoolu_unknown"))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := buildMessagesForToolSearch(tc.history, true, cacheFixtureTools())
			body, _ := json.Marshal(messages)
			if strings.Contains(string(body), `"server_tool_use"`) != tc.native {
				t.Fatalf("pause boundary replay mismatch: %s", body)
			}
			if !tc.native && (!strings.Contains(string(body), "first") || strings.Contains(string(body), `"tool_reference"`)) {
				t.Fatalf("canonical fallback lost text or kept an abandoned reference: %s", body)
			}
		})
	}
	// A completed earlier search remains usable when a later, independent one
	// was abandoned. Only the unfinished chain loses its provider metadata.
	history := []ollama.Message{user, assistant("first", call("srvtoolu_a")), assistant("second", result("srvtoolu_a")), assistant("third", call("srvtoolu_b")), user}
	encoded, _ := json.Marshal(buildMessagesForToolSearch(history, true, cacheFixtureTools()))
	if !strings.Contains(string(encoded), "srvtoolu_a") || strings.Contains(string(encoded), "srvtoolu_b") || !strings.Contains(string(encoded), "third") {
		t.Fatalf("fallback affected an unrelated completed discovery: %s", encoded)
	}
}
