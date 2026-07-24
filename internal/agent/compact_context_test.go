package agent

import (
	"context"
	"strings"
	"testing"

	"prism/internal/ollama"
)

// fakeSummarizeBackend is a minimal ollama.Backend that either returns a
// canned summary or fails, for testing summarizeDroppedSpan in isolation.
type fakeSummarizeBackend struct {
	reply string
	err   error
}

func (f *fakeSummarizeBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	if f.err != nil {
		out <- ollama.StreamEvent{Err: f.err}
		return
	}
	out <- ollama.StreamEvent{Content: f.reply}
}
func (f *fakeSummarizeBackend) Ping(ctx context.Context) error                   { return nil }
func (f *fakeSummarizeBackend) ListModels(ctx context.Context) ([]string, error) { return nil, nil }

// historyMutatingBackend simulates something else (the WS read pump calling
// InjectNote/ResetHistory/SetSession) mutating history WHILE
// summarizeDroppedSpan's LLM call is in flight (histMu deliberately
// unlocked during that call) — the exact race compactLiveContextIfNeeded's
// generation check guards against.
type historyMutatingBackend struct {
	agent *Agent
	reply string
}

func (f *historyMutatingBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	f.agent.InjectNote("concurrent note landed mid-summarization")
	out <- ollama.StreamEvent{Content: f.reply}
}
func (f *historyMutatingBackend) Ping(ctx context.Context) error                   { return nil }
func (f *historyMutatingBackend) ListModels(ctx context.Context) ([]string, error) { return nil, nil }

// Below budget: no-op, no event, history untouched.
func TestCompactLiveContext_BelowBudget_NoOp(t *testing.T) {
	a := &Agent{
		model:  "test",
		ollama: &fakeSummarizeBackend{reply: "summary"},
		history: []ollama.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}
	before := len(a.history)
	events := make(chan Event, 10)
	a.compactLiveContextIfNeeded(context.Background(), events)
	close(events)
	if len(a.history) != before {
		t.Fatalf("history changed on a below-budget no-op: got %d messages, want %d", len(a.history), before)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events on no-op, got %d", len(events))
	}
}

// Over budget, clean turn boundaries: kept tail starts on "user", dropped
// span is replaced by exactly one synthetic message, a progress event fires.
func TestCompactLiveContext_OverBudget_CleanBoundaries(t *testing.T) {
	origBudget := liveContextCharBudget
	liveContextCharBudget = 1000
	defer func() { liveContextCharBudget = origBudget }()

	var history []ollama.Message
	// 20 complete "turns": user + assistant, each large enough to blow well past budget.
	for i := 0; i < 20; i++ {
		history = append(history,
			ollama.Message{Role: "user", Content: string(make([]byte, 100))},
			ollama.Message{Role: "assistant", Content: string(make([]byte, 100))},
		)
	}
	a := &Agent{
		model:   "test",
		ollama:  &fakeSummarizeBackend{reply: "concise summary of older turns"},
		history: history,
	}
	events := make(chan Event, 10)
	a.compactLiveContextIfNeeded(context.Background(), events)
	close(events)

	if len(a.history) == 0 {
		t.Fatal("history unexpectedly emptied")
	}
	if a.history[0].Role != "user" {
		t.Fatalf("kept tail must start on a user message boundary, got role %q", a.history[0].Role)
	}
	if !strings.Contains(a.history[0].Content, "concise summary of older turns") {
		t.Fatalf("expected the synthetic first message to carry the summary, got %q", a.history[0].Content)
	}
	if len(a.history) >= len(history) {
		t.Fatalf("expected history to shrink: before=%d after=%d", len(history), len(a.history))
	}
	var gotProgress bool
	for len(events) > 0 {
		if ev := <-events; ev.Type == "progress" {
			gotProgress = true
		}
	}
	if !gotProgress {
		t.Fatal("expected a progress event announcing compaction")
	}
}

// If the only content driving the total over budget is the single
// most-recent in-progress turn (no earlier "user" boundary to safely cut
// at), compaction must be a no-op — it can never discard in-progress data.
func TestCompactLiveContext_NoSafeCutPoint_NoOp(t *testing.T) {
	origBudget := liveContextCharBudget
	liveContextCharBudget = 100
	defer func() { liveContextCharBudget = origBudget }()

	a := &Agent{
		model:  "test",
		ollama: &fakeSummarizeBackend{reply: "summary"},
		history: []ollama.Message{
			{Role: "user", Content: string(make([]byte, 500))},
			{Role: "assistant", Content: "", ToolCalls: []ollama.ToolCall{{}}},
			{Role: "tool", Content: string(make([]byte, 500))},
		},
	}
	before := len(a.history)
	events := make(chan Event, 10)
	a.compactLiveContextIfNeeded(context.Background(), events)
	close(events)
	if len(a.history) != before {
		t.Fatalf("expected no-op (no safe cut point before the in-progress turn), got %d messages (was %d)", len(a.history), before)
	}
	if len(events) != 0 {
		t.Fatal("expected no progress event when nothing was compacted")
	}
}

// summarizeDroppedSpan falls back to a generic note on backend failure,
// rather than propagating the error or blocking.
func TestSummarizeDroppedSpan_FailureFallback(t *testing.T) {
	a := &Agent{model: "test", ollama: &fakeSummarizeBackend{err: context.DeadlineExceeded}}
	got := a.summarizeDroppedSpan(context.Background(), []ollama.Message{{Role: "user", Content: "something important"}})
	if got == "" {
		t.Fatal("expected a non-empty fallback string")
	}
	if got == "something important" {
		t.Fatal("fallback must not just echo the input back")
	}
}

// If InjectNote lands while summarizeDroppedSpan's LLM call is in flight
// (histMu deliberately unlocked for that ~20s best-effort call), applying
// the stale pre-compaction view afterward must NOT silently discard the
// concurrent note — the generation check should abort the compaction pass
// instead, leaving the injected note in place.
func TestCompactLiveContext_AbortsOnConcurrentMutation(t *testing.T) {
	origBudget := liveContextCharBudget
	liveContextCharBudget = 1000
	defer func() { liveContextCharBudget = origBudget }()

	var history []ollama.Message
	for i := 0; i < 20; i++ {
		history = append(history,
			ollama.Message{Role: "user", Content: string(make([]byte, 100))},
			ollama.Message{Role: "assistant", Content: string(make([]byte, 100))},
		)
	}
	a := &Agent{model: "test", history: history}
	a.ollama = &historyMutatingBackend{agent: a, reply: "summary"}

	events := make(chan Event, 10)
	a.compactLiveContextIfNeeded(context.Background(), events)
	close(events)

	// The concurrent InjectNote's message must survive, verbatim, as the last
	// entry — compaction must not have overwritten it with its stale tail.
	last := a.history[len(a.history)-1]
	if last.Content != "concurrent note landed mid-summarization" {
		t.Fatalf("concurrent InjectNote was lost: last message = %+v", last)
	}
	for _, ev := range drainEvents(events) {
		if ev.Type == "progress" {
			t.Error("compaction should not announce success when it aborted due to a concurrent mutation")
		}
	}
}

func drainEvents(ch chan Event) []Event {
	var out []Event
	for len(ch) > 0 {
		out = append(out, <-ch)
	}
	return out
}
