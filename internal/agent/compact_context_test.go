package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
func (f *fakeSummarizeBackend) ContextBudgetChars() int                          { return 0 }

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
func (f *historyMutatingBackend) ContextBudgetChars() int                          { return 0 }

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

// summarizeDroppedSpan reports a backend failure as an error (never blocks,
// never propagates a panic) so the caller can leave an honest note.
func TestSummarizeDroppedSpan_FailureIsReported(t *testing.T) {
	a := &Agent{model: "test", ollama: &fakeSummarizeBackend{err: errors.New("backend down")}}
	got, err := a.summarizeDroppedSpan(context.Background(), []ollama.Message{{Role: "user", Content: "something important"}})
	if err == nil {
		t.Fatalf("expected an error, got summary %q", got)
	}
	if !strings.Contains(err.Error(), "backend down") {
		t.Fatalf("error should carry the cause, got %v", err)
	}
	if got != "" {
		t.Fatalf("no summary expected on failure, got %q", got)
	}
}

// When the summary fails, the compaction still happens (the context must
// shrink) but the note left in history and the UI line both say the details
// are LOST — never a bland placeholder the model could mistake for context.
func TestCompactLiveContext_FailedSummaryLeavesHonestNote(t *testing.T) {
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
	a := &Agent{model: "test", ollama: &fakeSummarizeBackend{err: errors.New("boom")}, history: history}
	events := make(chan Event, 10)
	a.compactLiveContextIfNeeded(context.Background(), events)
	close(events)

	if len(a.history) >= len(history) {
		t.Fatalf("history must still shrink on a failed summary: before=%d after=%d", len(history), len(a.history))
	}
	if !strings.Contains(a.history[0].Content, "could NOT be summarized") || !strings.Contains(a.history[0].Content, "boom") {
		t.Fatalf("note must say the summary failed and why, got %q", a.history[0].Content)
	}
	var progress string
	for _, ev := range drainEvents(events) {
		if ev.Type == "progress" {
			progress = ev.Content
		}
	}
	if !strings.Contains(progress, "summary failed") {
		t.Fatalf("UI progress line must not claim success, got %q", progress)
	}
}

// capturingBackend records the request it receives and replies with a canned
// summary — to inspect the excerpt the summarizer is actually sent.
type capturingBackend struct {
	req   ollama.ChatRequest
	reply string
}

func (f *capturingBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	f.req = req
	out <- ollama.StreamEvent{Content: f.reply}
}
func (f *capturingBackend) Ping(ctx context.Context) error                   { return nil }
func (f *capturingBackend) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (f *capturingBackend) ContextBudgetChars() int                          { return 0 }

// Tool outputs dominate a long session's history and are the least useful to
// a summary: they must be cut down hard in the excerpt (this is what turned a
// ~60s cold prefill into a few seconds on Flash-Next), while the assistant's
// tool calls are still named so the summary knows what was done.
func TestCompactionExcerpt_TrimsToolOutputKeepsToolNames(t *testing.T) {
	be := &capturingBackend{reply: "ok"}
	a := &Agent{model: "test", ollama: be}
	dropped := []ollama.Message{
		{Role: "user", Content: "lis le rapport"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "read_file"}}}},
		{Role: "tool", Content: strings.Repeat("x", 50_000)},
		{Role: "assistant", Content: "Le rapport dit " + strings.Repeat("y", 10_000)},
	}
	if _, err := a.summarizeDroppedSpan(context.Background(), dropped); err != nil {
		t.Fatal(err)
	}
	excerpt := be.req.Messages[len(be.req.Messages)-1].Content
	if len(excerpt) > 6_000 {
		t.Fatalf("excerpt not trimmed: %d chars", len(excerpt))
	}
	for _, want := range []string{"[user] lis le rapport", "(called tools: read_file)", "tool output truncated, 50000 chars total", "chars truncated"} {
		if !strings.Contains(excerpt, want) {
			t.Errorf("excerpt missing %q", want)
		}
	}
	if be.req.Options.NumPredict < 800 {
		t.Errorf("summary generation budget too small: %d", be.req.Options.NumPredict)
	}
	if !be.req.NoThinking {
		t.Error("summarizer must run without thinking")
	}
}

// The deadline scales with the excerpt (cold prefill is linear in input) and
// is floored high enough that a small span still survives a busy backend.
func TestSummarizeTimeout_ScalesWithExcerpt(t *testing.T) {
	if got := summarizeTimeout(0); got < 30*time.Second {
		t.Fatalf("floor too low: %s", got)
	}
	if small, big := summarizeTimeout(5_000), summarizeTimeout(60_000); big <= small {
		t.Fatalf("timeout must grow with the excerpt: %s vs %s", small, big)
	}
	if got := summarizeTimeout(10_000_000); got > 180*time.Second {
		t.Fatalf("cap exceeded: %s", got)
	}
}

// appendingBackend simulates the NEXT turn starting while a background
// compaction's summarization call is in flight: it appends to history
// (a plain append, no generation bump — exactly what Chat does).
type appendingBackend struct {
	agent *Agent
}

func (f *appendingBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	f.agent.histMu.Lock()
	f.agent.history = append(f.agent.history,
		ollama.Message{Role: "user", Content: "next turn's question"},
		ollama.Message{Role: "assistant", Content: "next turn's answer"},
	)
	f.agent.histMu.Unlock()
	out <- ollama.StreamEvent{Content: "summary of the old span"}
}
func (f *appendingBackend) Ping(ctx context.Context) error                   { return nil }
func (f *appendingBackend) ListModels(ctx context.Context) ([]string, error) { return nil, nil }
func (f *appendingBackend) ContextBudgetChars() int                          { return 0 }

// A compaction pass must splice against the CURRENT history, so messages the
// next turn appended while the summary was being generated survive.
func TestCompactLiveContext_KeepsMessagesAppendedMidFlight(t *testing.T) {
	origBudget := liveContextCharBudget
	liveContextCharBudget = 1000
	defer func() { liveContextCharBudget = origBudget }()

	var history []ollama.Message
	for i := 0; i < 20; i++ {
		history = append(history,
			ollama.Message{Role: "user", Content: string(make([]byte, 100)), DBID: int64(2*i + 1)},
			ollama.Message{Role: "assistant", Content: string(make([]byte, 100)), DBID: int64(2*i + 2)},
		)
	}
	a := &Agent{model: "test", history: history}
	a.ollama = &appendingBackend{agent: a}

	a.compactLiveContextTo(context.Background(), nil, liveContextCharBudget/2)

	n := len(a.history)
	if n < 3 || a.history[n-2].Content != "next turn's question" || a.history[n-1].Content != "next turn's answer" {
		t.Fatalf("messages appended mid-flight were lost; tail = %+v", a.history[max(0, n-3):])
	}
	if !strings.Contains(a.history[0].Content, "summary of the old span") {
		t.Fatalf("expected the note first, got %q", a.history[0].Content)
	}
	if a.history[1].Role != "user" || a.history[1].DBID == 0 {
		t.Fatalf("kept tail must start on a persisted user message, got %+v", a.history[1])
	}
}

// Proactive compaction fires past the soft threshold only.
func TestNeedsProactiveCompaction_SoftThreshold(t *testing.T) {
	origBudget := liveContextCharBudget
	liveContextCharBudget = 1000
	defer func() { liveContextCharBudget = origBudget }()

	a := &Agent{ollama: &fakeSummarizeBackend{reply: "s"}}
	a.history = []ollama.Message{{Role: "user", Content: string(make([]byte, 500))}}
	if a.needsProactiveCompaction() {
		t.Fatal("50% of budget must not trigger the proactive pass")
	}
	a.history = append(a.history, ollama.Message{Role: "assistant", Content: string(make([]byte, 300))})
	if !a.needsProactiveCompaction() {
		t.Fatal("80% of budget must trigger the proactive pass")
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
