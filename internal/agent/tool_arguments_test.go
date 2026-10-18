package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"prism/internal/ollama"
	"strings"
	"testing"
)

type rejectedArgumentsBackend struct {
	capturingBackend
	calls    int
	sawError bool
}

func (b *rejectedArgumentsBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	if b.calls == 1 {
		out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"existing.txt","content":""}`), ArgumentError: "Incomplete JSON: no action performed"}}}, Done: true}
		return
	}
	for _, m := range req.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "Incomplete JSON") {
			b.sawError = true
		}
	}
	out <- ollama.StreamEvent{Content: "The incomplete call was rejected.", Done: true}
}
func TestMalformedProviderCallNeverExecutesOrRequestsApproval(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing.txt")
	os.WriteFile(path, []byte("keep me"), 0600)
	backend := &rejectedArgumentsBackend{}
	a := &Agent{ollama: backend, executor: &ToolExecutor{workspaceDir: root}, sessionID: "fixture", limits: Limits{PromptProfile: "standard", MaxIterations: 3}}
	a.approvalNeededFn = func() bool { t.Error("invalid call reached approval"); return true }
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Write the file", nil, events)
	close(events)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep me" {
		t.Fatal("rejected call modified a file", err)
	}
	if !backend.sawError {
		t.Fatal("model did not receive the rejection")
	}
	for e := range events {
		if e.Type == "tool_result" && !e.IsError {
			t.Fatal("rejected call was reported as success")
		}
	}
	if _, err = json.Marshal(a.history); err != nil {
		t.Fatal("invalid history", err)
	}
}
func TestWriteFileRequiresExplicitPathAndContent(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "existing.txt"), []byte("keep me"), 0600)
	e := &ToolExecutor{workspaceDir: root}
	for _, args := range []string{`{}`, `{"path":"existing.txt"}`, `{"path":"existing.txt","content":null}`, `{"path":"/workspace","content":"x"}`} {
		if _, _, err := e.Execute(t.Context(), "write_file", json.RawMessage(args)); err == nil {
			t.Fatal("accepted missing required input", args)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "existing.txt"))
	if string(data) != "keep me" {
		t.Fatal("missing content erased file")
	}
	if _, _, err := e.Execute(t.Context(), "write_file", json.RawMessage(`{"path":"empty.txt","content":""}`)); err != nil {
		t.Fatal("explicit empty file rejected", err)
	}
}
