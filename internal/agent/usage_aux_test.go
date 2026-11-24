package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"prism/internal/ollama"
)

type auxiliaryUsageCapture struct {
	mu      sync.Mutex
	records []*ModelUsage
	users   []int64
	session string
	valid   bool
}

func (c *auxiliaryUsageCapture) AddUsage(ctx context.Context, user int64, session, kind, item string, quantity int64, meta map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline, bounded := ctx.Deadline()
	record, ok := meta["measurement"].(*ModelUsage)
	c.valid = ctx.Err() == nil && bounded && time.Until(deadline) <= 2*time.Second &&
		kind == "model_request" && quantity == 1 && ok && item == record.Model
	c.session = session
	c.users = append(c.users, user)
	c.records = append(c.records, record)
}

func TestAuxiliaryUsageMeasuresWithoutContent(t *testing.T) {
	input, output, cache, write1h := int64(120), int64(9), int64(80), int64(20)
	usage := &ollama.Usage{InputTokens: &input, OutputTokens: &output, CacheReadTokens: &cache, CacheWrite1hTokens: &write1h}
	req := ollama.ChatRequest{TaskID: "parent", Model: "fixture", Messages: []ollama.Message{
		{Role: "system", Content: "private system"},
		{Role: "user", Content: "private question", Images: []string{"private image"}},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "test", Arguments: json.RawMessage(`{"secret":"private argument"}`)}}}},
		{Role: "tool", Content: "private result"},
	}}
	req.Tools = []ollama.Tool{{Type: "function", Function: ollama.ToolFunction{Name: "private_tool", Description: "private tool instructions", Parameters: ollama.ToolParameters{Type: "object"}}}}
	record := MeasureModelUsage(req, "", "research", "standard", time.Now().Add(-time.Second), usage, true)
	if record.TaskID != "parent" || record.Scope != "research" || record.Model != "fixture" || record.Profile != "standard" ||
		record.DurationMS < 1000 || record.SystemBytes != len("private system") || record.ToolBytes == 0 || record.MessageCount != 4 ||
		record.HistoryContentBytes != len("private questionprivate result") || record.HistoryToolResultBytes != len("private result") ||
		record.HistoryImageBytes != len("private image") || record.HistoryToolArgumentBytes == 0 || record.Usage != usage || !record.Complete ||
		record.Usage.CacheWrite1hTokens == nil || *record.Usage.CacheWrite1hTokens != 20 {
		t.Fatalf("incomplete measurement: %+v", record)
	}
	raw, err := json.Marshal(record)
	if err != nil || strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
		t.Fatalf("usage contains request content: %s (%v)", raw, err)
	}
}

func TestAuxiliaryUsageRetainsTaskOrCreatesUniqueStandaloneIdentity(t *testing.T) {
	start := time.Now()
	ctx := withTaskID(t.Context(), "originating-task")
	if got := AuxiliaryTaskID(ctx, "compaction", start); got != "originating-task" {
		t.Fatalf("lost parent task: %s", got)
	}
	one := MeasureModelUsage(ollama.ChatRequest{}, "", "compaction", "", start, nil, false)
	two := MeasureModelUsage(ollama.ChatRequest{}, "", "compaction", "", start, nil, false)
	if one.TaskID == two.TaskID || !strings.HasPrefix(one.TaskID, "compaction-") || one.Usage != nil || two.Usage != nil || one.Complete {
		t.Fatalf("invalid standalone identity or invented usage: %+v %+v", one, two)
	}
}

func TestAuxiliaryUsageRecordsAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	capture := &auxiliaryUsageCapture{}
	a := &Agent{model: "fixture", sessionID: "u7-chat", usageRecorder: capture}
	req := ollama.ChatRequest{Model: "fixture", TaskID: "before-cancel"}
	a.recordAuxiliaryModelUsage(ctx, req, "compaction", time.Now(), nil, false)
	if !capture.valid || len(capture.records) != 1 || capture.session != "u7-chat" || capture.records[0].TaskID != "before-cancel" ||
		capture.records[0].Usage != nil || capture.records[0].Complete {
		t.Fatalf("cancelled request lost or fabricated counters: %+v", capture)
	}
}

func TestResearchUsageInheritsEphemeralParentAndCountsEachRequestOnce(t *testing.T) {
	input, output := int64(123), int64(12)
	usage := &ollama.Usage{InputTokens: &input, OutputTokens: &output}
	backend := &contractStreamBackend{events: []ollama.StreamEvent{
		{Content: "Research result", Usage: usage},
		{Done: true, DoneReason: "end_turn", Usage: usage},
	}}
	capture := &auxiliaryUsageCapture{}
	ctx := withAuxiliaryUsage(withTaskID(t.Context(), "parent-task"), capture, "u7-ephemeral", "minimal")
	e := &ToolExecutor{backend: backend, model: "fixture", sessionID: "wrong-fallback"}
	for range 2 {
		if _, err := e.chatOnce(ctx, "private system", "private research", 0.3); err != nil {
			t.Fatal(err)
		}
		if backend.req.TaskID != "parent-task" || backend.req.CacheSystemPrefixBytes != len("private system") {
			t.Fatalf("request lost attribution or stable prefix: %+v", backend.req)
		}
	}
	if !capture.valid || len(capture.records) != 2 || capture.session != "u7-ephemeral" {
		t.Fatalf("double counting or lost parent recorder: %+v", capture)
	}
	for _, record := range capture.records {
		if record.TaskID != "parent-task" || record.Scope != "research" || record.Profile != "minimal" || !record.Complete || record.Usage != usage {
			t.Fatalf("wrong research measurement: %+v", record)
		}
	}
}

func TestResearchFailureRecordsPartialProviderUsage(t *testing.T) {
	input := int64(120)
	usage := &ollama.Usage{InputTokens: &input}
	backend := &contractStreamBackend{events: []ollama.StreamEvent{
		{Content: "Partial result", Usage: usage},
		{Err: errors.New("provider stopped")},
	}}
	capture := &auxiliaryUsageCapture{}
	ctx := withAuxiliaryUsage(t.Context(), capture, "u7-chat", "standard")
	e := &ToolExecutor{backend: backend, model: "fixture"}
	if _, err := e.chatOnce(ctx, "", "private question", 0.2); err == nil {
		t.Fatal("failed research response was accepted")
	}
	if len(capture.records) != 1 || capture.records[0].Complete || capture.records[0].Usage != usage || capture.records[0].Usage.OutputTokens != nil {
		t.Fatalf("partial provider counters lost or completed: %+v", capture)
	}
}

func TestResearchCancellationRecordsPartialProviderUsage(t *testing.T) {
	input := int64(120)
	usage := &ollama.Usage{InputTokens: &input}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	backend := &contractStreamBackend{events: []ollama.StreamEvent{
		{Usage: usage, Done: true, DoneReason: "interrupted"},
	}}
	capture := &auxiliaryUsageCapture{}
	ctx = withAuxiliaryUsage(ctx, capture, "u7-chat", "standard")
	e := &ToolExecutor{backend: backend, model: "fixture"}
	if _, err := e.chatOnce(ctx, "", "private question", 0.2); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !capture.valid || len(capture.records) != 1 || capture.records[0].Complete || capture.records[0].Usage != usage {
		t.Fatalf("cancelled request lost provider counters: %+v", capture)
	}
}
