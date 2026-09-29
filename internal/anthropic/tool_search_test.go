package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestToolSearchRoundTrip(t *testing.T) {
	t.Setenv("PRISM_ANTHROPIC_TOOL_SEARCH", "1")
	var sent []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = append(sent, body)
		w.Header().Set("Content-Type", "application/json")
		if len(sent) == 1 {
			fmt.Fprint(w, `{"content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"tool_search_tool_bm25","input":{"query":"widget"}},{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_1","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"widget"}]}},{"type":"tool_use","id":"toolu_real","name":"widget","input":{"action":"list"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":20}}`)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":120,"output_tokens":3}}`)
	}))
	defer srv.Close()

	tools := make([]ollama.Tool, 0, 8)
	for _, name := range []string{"exec_command", "read_file", "write_file", "widget", "cron", "web_search", "list_files", "delete_file"} {
		tools = append(tools, ollama.Tool{Function: ollama.ToolFunction{Name: name, Description: "Test " + name}})
	}
	c := NewClient(srv.URL, "sk-ant-api03-test", "claude-sonnet-5-5")
	req := ollama.ChatRequest{
		Model:    "claude-sonnet-5-5",
		Messages: []ollama.Message{{Role: "user", Content: "list my widgets"}},
		Tools:    tools,
	}
	ch := make(chan ollama.StreamEvent, 2)
	c.Chat(context.Background(), req, ch)
	first := <-ch
	if first.Err != nil || !first.Done || len(first.ToolCalls) != 1 || first.ToolCalls[0].Function.Name != "widget" || len(first.ProviderBlocks) != 3 {
		t.Fatalf("tool search response lost blocks or call: %+v", first)
	}
	req.Messages = append(req.Messages,
		ollama.Message{Role: "assistant", ToolCalls: first.ToolCalls, ProviderBlocks: first.ProviderBlocks},
		ollama.Message{Role: "tool", Content: "[]"})
	c.Chat(context.Background(), req, ch)
	second := <-ch
	if second.Err != nil || second.Content != "done" || len(sent) != 2 {
		t.Fatalf("resume failed: %+v; requests=%d", second, len(sent))
	}

	var firstWire struct {
		Stream bool                         `json:"stream"`
		Tools  []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(sent[0], &firstWire); err != nil {
		t.Fatal(err)
	}
	if firstWire.Stream || len(firstWire.Tools) != 9 {
		t.Fatalf("tool search must use complete non-streaming catalog: %+v", firstWire)
	}
	if _, ok := firstWire.Tools[0]["input_schema"]; ok {
		t.Fatal("server tool has client schema")
	}
	if string(firstWire.Tools[0]["type"]) != `"tool_search_tool_bm25_20251119"` {
		t.Fatal("missing search tool")
	}
	for _, tool := range firstWire.Tools {
		if string(tool["name"]) == `"web_search"` {
			if string(tool["defer_loading"]) != "true" {
				t.Fatal("web_search was not deferred")
			}
			if _, ok := tool["cache_control"]; ok {
				t.Fatal("deferred tool carried cache marker")
			}
		}
	}

	var resume struct {
		Messages []struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(sent[1], &resume); err != nil {
		t.Fatal(err)
	}
	if len(resume.Messages) != 3 || len(resume.Messages[1].Content) != 3 {
		t.Fatalf("search blocks not replayed: %s", sent[1])
	}
	if !strings.Contains(string(resume.Messages[1].Content[1]), `"tool_reference"`) {
		t.Fatal("tool reference missing")
	}
	if !strings.Contains(string(resume.Messages[2].Content[0]), `"tool_use_id":"toolu_real"`) {
		t.Fatal("tool result paired with wrong id")
	}
	encoded, _ := json.Marshal(req.Messages[1])
	if strings.Contains(string(encoded), "srvtoolu_1") {
		t.Fatal("provider blocks must not serialize into the pivot message")
	}
}

func TestToolSearchRefusalIsAnError(t *testing.T) {
	ch := make(chan ollama.StreamEvent, 1)
	(&Client{}).readMessage(strings.NewReader(`{"content":[],"stop_reason":"refusal","stop_details":{"category":"cyber","explanation":"test refusal"},"usage":{"input_tokens":12,"output_tokens":0}}`), ch)
	ev := <-ch
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "cyber") || ev.DoneReason != "refusal" {
		t.Fatalf("refusal must not look like a silent answer: %+v", ev)
	}
}
