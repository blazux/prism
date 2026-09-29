package agent

import (
	"context"
	"encoding/json"
	"prism/internal/ollama"
	"testing"
)

func TestTaskIDInheritedByChild(t *testing.T) {
	parent := withTaskID(context.Background(), "parent-task")
	child := withTaskID(parent, "child-message")
	if got := taskIDFromContext(child); got != "parent-task" {
		t.Fatalf("child got task ID %q", got)
	}
}

func TestHistoryFootprintSkipsOnlyAssembledSystemPrompt(t *testing.T) {
	content, results, arguments, images := historyFootprint([]ollama.Message{
		{Role: "system", Content: "main prompt"},
		{Role: "user", Content: "user"},
		{Role: "system", Content: "live reminder"},
		{Role: "tool", Content: "tool result"},
	})
	if content != len("userlive remindertool result") || results != len("tool result") || arguments != 0 || images != 0 {
		t.Fatalf("wrong history footprint: %d %d %d %d", content, results, arguments, images)
	}
}

type usageCapture struct {
	session, kind, item string
	records             []*ModelUsage
}

func (c *usageCapture) AddUsage(_ context.Context, _ int64, session, kind, item string, _ int64, meta map[string]interface{}) {
	c.session, c.kind, c.item = session, kind, item
	if record, ok := meta["measurement"].(*ModelUsage); ok {
		c.records = append(c.records, record)
	}
}

func TestSubagentUsageRecordedWithoutHistoryPersistence(t *testing.T) {
	const arguments = `{"path":"x"}`
	a := &Agent{
		ollama: &usageBackend{}, executor: &ToolExecutor{}, model: "fixture", sessionID: "child-session",
		history: []ollama.Message{
			{Role: "user", Content: "request", Images: []string{"abcd"}},
			{Role: "assistant", Content: "code", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "read_file", Arguments: json.RawMessage(arguments)}}}},
			{Role: "tool", Content: "result"},
		},
	}
	capture := &usageCapture{}
	a.SetUsageRecorder(capture, "subagent")
	events := make(chan Event, 20)
	if _, _, _, _, err := a.callOllama(withTaskID(t.Context(), "parent-task"), "", events); err != nil {
		t.Fatal(err)
	}
	if a.memStore != nil || len(capture.records) != 1 || capture.session != "child-session" || capture.kind != "model_request" || capture.item != "fixture" {
		t.Fatalf("subagent history or usage routing is wrong: store=%v capture=%+v", a.memStore, capture)
	}
	record := capture.records[0]
	if record.Scope != "subagent" || record.TaskID != "parent-task" || record.HistoryContentBytes != len("requestcoderesult") ||
		record.HistoryToolResultBytes != len("result") || record.HistoryToolArgumentBytes != len(arguments) || record.HistoryImageBytes != 4 {
		t.Fatalf("wrong content-free request sizes or attribution: %+v", record)
	}
	raw, _ := json.Marshal(record)
	if string(raw) == "" || containsString(string(raw), "request") || containsString(string(raw), "\"result\"") || containsString(string(raw), arguments) {
		t.Fatalf("usage record contains conversation content: %s", raw)
	}
}
