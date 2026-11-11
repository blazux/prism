package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/ollama"
)

func loopCall(name, args string) ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunction{
		Name: name, Arguments: json.RawMessage(args),
	}}
}

func TestToolLoopDetectsAlternatingRetries(t *testing.T) {
	var detector toolLoopDetector
	for i, name := range []string{"a", "b", "a", "b", "a"} {
		got := detector.observe(loopCall(name, `{"range":3600}`), "unchanged", nil)
		if got != map[bool]toolLoopSignal{false: toolLoopNone, true: toolLoopWarn}[i == 4] {
			t.Fatalf("call %d: loop=%v", i+1, got)
		}
	}
}

func TestToolLoopPreservesProgressAndExpiresOldCalls(t *testing.T) {
	var detector toolLoopDetector
	for i := 0; i < 20; i++ {
		if detector.observe(loopCall("a", `{}`), strings.Repeat("x", i), nil) != toolLoopNone {
			t.Fatal("different results counted as a loop")
		}
	}
	for i := 0; i < 20; i++ {
		if detector.observe(loopCall("a", `{}`), "screenshot", []string{strings.Repeat("y", i)}) != toolLoopNone {
			t.Fatal("different images counted as a loop")
		}
	}
	for i := 0; i < toolLoopWindow; i++ {
		if detector.observe(loopCall("b", `{}`), strings.Repeat("z", i), nil) != toolLoopNone {
			t.Fatal("different results counted as a loop")
		}
	}
	if detector.observe(loopCall("a", `{}`), "unchanged", nil) != toolLoopNone {
		t.Fatal("expired calls contributed to a loop")
	}
	if len(detector.recent) > toolLoopWindow {
		t.Fatal("unbounded tool history")
	}
}

func TestToolLoopCanonicalJSONWithoutLosingIntegerPrecision(t *testing.T) {
	var detector toolLoopDetector
	call := loopCall("a", `{"b":2, "a":9007199254740992}`)
	if detector.observe(call, "same", nil) != toolLoopNone {
		t.Fatal("first occurrence")
	}
	call.Function.Arguments = json.RawMessage(`{"a":9007199254740992,"b":2}`)
	if detector.observe(call, "same", nil) != toolLoopNone {
		t.Fatal("second occurrence")
	}
	other := loopCall("a", `{"a":9007199254740993,"b":2}`)
	if detector.observe(other, "same", nil) != toolLoopNone {
		t.Fatal("distinct integer lost precision")
	}
	if detector.observe(call, "same", nil) != toolLoopWarn {
		t.Fatal("equivalent JSON did not match")
	}
}

type cyclingToolBackend struct {
	fakeSummarizeBackend
	calls    int
	trailing []ollama.ToolCall
}

func (b *cyclingToolBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	name := "missing_a"
	if b.calls%2 == 0 {
		name = "missing_b"
	}
	calls := []ollama.ToolCall{loopCall(name, `{}`)}
	if b.calls == 7 {
		calls = append(calls, b.trailing...)
	}
	out <- ollama.StreamEvent{ToolCalls: calls, Done: true, DoneReason: "tool_calls"}
}

func TestAlternatingLoopStopsActualAgentAndKeepsFinalToolResult(t *testing.T) {
	backend := &cyclingToolBackend{}
	a := &Agent{ollama: backend, executor: &ToolExecutor{}, model: "fixture",
		sessionID: "fixture", limits: Limits{MaxIterations: 20}}
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Investigate the service", nil, events)
	close(events)
	if backend.calls != 7 {
		t.Fatalf("model calls=%d, want 7", backend.calls)
	}
	if len(a.history) < 2 || a.history[len(a.history)-2].Role != "tool" {
		t.Fatal("loop guard discarded the final tool result")
	}
	if !strings.HasPrefix(a.history[len(a.history)-1].Content, agentStopPrefix) {
		t.Fatal("execution stop reason missing from the next turn's context")
	}
	var foundError, ended bool
	for event := range events {
		if event.Type == "error" && strings.Contains(event.Content, "Loop detected") {
			foundError = true
		}
		ended = ended || event.Type == "stream_end"
	}
	if !foundError || !ended {
		t.Fatal("loop did not close the turn with an explicit diagnostic")
	}
}

func TestIterationLimitRecordsActualReasonForFollowUp(t *testing.T) {
	backend := &cyclingToolBackend{}
	a := &Agent{ollama: backend, executor: &ToolExecutor{}, model: "fixture",
		sessionID: "fixture", limitsOverride: Limits{MaxIterations: 1}}
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Investigate the service", nil, events)
	note := a.history[len(a.history)-1]
	if note.Role != "user" || !strings.HasPrefix(note.Content, agentStopPrefix) ||
		!strings.Contains(note.Content, "Iteration limit reached (1 model calls") {
		t.Fatalf("actual stop reason was not retained: %+v", note)
	}
	followup := &stoppingBackend{capturingBackend: capturingBackend{reply: "The previous turn exhausted its agent iterations."}}
	a.ollama = followup
	a.Chat(t.Context(), "Why did it stop?", nil, events)
	found := false
	for _, message := range followup.req.Messages {
		if message.Content == note.Content {
			found = true
		}
	}
	if !found {
		t.Fatal("follow-up request lost the recorded execution status")
	}
}

func TestLoopClosesToolBatchWithoutExecutingRemainingCalls(t *testing.T) {
	workspace := t.TempDir()
	backend := &cyclingToolBackend{trailing: []ollama.ToolCall{
		loopCall("write_file", `{"path":"must-not-exist","content":"unwanted"}`),
	}}
	a := &Agent{ollama: backend, executor: &ToolExecutor{workspaceDir: workspace},
		model: "fixture", sessionID: "fixture"}
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Investigate", nil, events)
	if _, err := os.Stat(filepath.Join(workspace, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("executed a tool after detecting the loop")
	}
	for i, message := range a.history {
		if len(message.ToolCalls) == 0 {
			continue
		}
		for offset := range message.ToolCalls {
			if i+offset+1 >= len(a.history) || a.history[i+offset+1].Role != "tool" {
				t.Fatal("assistant tool batch has missing results")
			}
		}
	}
}

func TestToolLoopWarningMustReachModelBeforeStop(t *testing.T) {
	var d toolLoopDetector
	call := loopCall("poll", `{}`)
	for i := 0; i < 2; i++ {
		if d.observe(call, "unchanged", nil) != toolLoopNone {
			t.Fatal("early warning")
		}
	}
	if d.observe(call, "unchanged", nil) != toolLoopWarn {
		t.Fatal("missing warning")
	}
	if d.observe(call, "unchanged", nil) != toolLoopWarn {
		t.Fatal("same batch consumed warning opportunity")
	}
	if d.warning() == "" {
		t.Fatal("warning missing from next context")
	}
	d.afterBatch()
	if d.observe(call, "unchanged", nil) != toolLoopStop {
		t.Fatal("unbroken cycle after warning did not stop")
	}
}

func TestToolLoopAllowsChecksBetweenUsefulOperations(t *testing.T) {
	var d toolLoopDetector
	call := loopCall("read_file", `{"path":"status"}`)
	for i := 0; i < 3; i++ {
		d.observe(call, "unchanged", nil)
	}
	d.afterBatch()
	// A new operation may be useful even when the checked status stays stable.
	if d.observe(loopCall("write_file", `{"path":"result","content":"work"}`), "written", nil) != toolLoopNone {
		t.Fatal("new work classified as loop")
	}
	if d.observe(call, "unchanged", nil) == toolLoopStop {
		t.Fatal("check after new work caused hard stop")
	}
	// Changed observations, including screenshots, also cancel old warnings.
	if d.observe(call, "new status", nil) != toolLoopNone {
		t.Fatal("changed result classified as loop")
	}
	if d.warning() != "" {
		t.Fatal("warning survived changed observation")
	}
}

type warningAwareToolBackend struct {
	fakeSummarizeBackend
	calls      int
	sawWarning bool
}

func (b *warningAwareToolBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.calls++
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "Some tool calls returned the same result") {
			b.sawWarning = true
		}
	}
	if b.sawWarning {
		out <- ollama.StreamEvent{Content: "The observations are stable; the requested checks are complete.", Done: true, DoneReason: "stop"}
		return
	}
	out <- ollama.StreamEvent{ToolCalls: []ollama.ToolCall{loopCall("missing_a", `{}`)}, Done: true, DoneReason: "tool_calls"}
}
func TestRepeatedChecksCanConcludeAfterWarning(t *testing.T) {
	b := &warningAwareToolBackend{}
	a := &Agent{ollama: b, executor: &ToolExecutor{}, model: "fixture", sessionID: "fixture"}
	events := make(chan Event, 100)
	a.Chat(t.Context(), "Check the service", nil, events)
	close(events)
	if !b.sawWarning || b.calls != 4 {
		t.Fatalf("warning delivered=%v calls=%d", b.sawWarning, b.calls)
	}
	for ev := range events {
		if ev.Type == "error" {
			t.Fatalf("bounded checks could not conclude: %s", ev.Content)
		}
	}
}
