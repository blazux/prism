package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"prism/internal/memory"
	"prism/internal/ollama"
)

func TestSummarizeDroppedSpanRequiresSuccessfulFinish(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []ollama.StreamEvent
		valid  bool
	}{
		{"stop", []ollama.StreamEvent{{Content: "Complete summary.", Done: true, DoneReason: "stop"}}, true},
		{"end turn", []ollama.StreamEvent{{Content: "Complete summary.", Done: true, DoneReason: "end_turn"}}, true},
		{"stop sequence", []ollama.StreamEvent{{Content: "Complete summary.", Done: true, DoneReason: "stop_sequence"}}, true},
		{"interrupted", []ollama.StreamEvent{{Content: "Partial summary", Done: true, DoneReason: "interrupted"}}, false},
		{"length", []ollama.StreamEvent{{Content: "Partial summary", Done: true, DoneReason: "length"}}, false},
		{"max tokens", []ollama.StreamEvent{{Content: "Partial summary", Done: true, DoneReason: "max_tokens"}}, false},
		{"refusal", []ollama.StreamEvent{{Content: "Cannot summarize.", Done: true, DoneReason: "refusal"}}, false},
		{"missing finish", []ollama.StreamEvent{{Content: "Unconfirmed summary."}}, false},
		{"missing reason", []ollama.StreamEvent{{Content: "Unconfirmed summary.", Done: true}}, false},
		{"empty summary", []ollama.StreamEvent{{Done: true, DoneReason: "stop"}}, false},
		{"unexpected tool", []ollama.StreamEvent{{Content: "Incomplete summary.", ToolCalls: []ollama.ToolCall{cancelledWrite()}, Done: true, DoneReason: "stop"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &failingStreamBackend{stream: func(_ context.Context, out chan<- ollama.StreamEvent) {
				for _, event := range tc.events {
					out <- event
				}
			}}
			a := &Agent{model: "fixture", ollama: backend}
			summary, err := a.summarizeDroppedSpan(t.Context(), []ollama.Message{{Role: "user", Content: "Move the file only after checking permissions."}})
			if tc.valid {
				if err != nil || summary != "Complete summary." {
					t.Fatalf("complete summary rejected: %q, %v", summary, err)
				}
			} else if err == nil || summary != "" {
				t.Fatalf("unfinished summary accepted: %q, %v", summary, err)
			}
		})
	}
}

func compactionFinishHistory() []ollama.Message {
	var history []ollama.Message
	for range 10 {
		history = append(history,
			ollama.Message{Role: "user", Content: strings.Repeat("request ", 10)},
			ollama.Message{Role: "assistant", Content: strings.Repeat("response ", 10)},
		)
	}
	return history
}

func TestCompactLiveContextUnfinishedSummaryRetainsHistory(t *testing.T) {
	for _, reason := range []string{"interrupted", "length", "max_tokens"} {
		t.Run(reason, func(t *testing.T) {
			history := compactionFinishHistory()
			a := &Agent{model: "fixture", history: append([]ollama.Message(nil), history...), ollama: &failingStreamBackend{
				stream: func(_ context.Context, out chan<- ollama.StreamEvent) {
					out <- ollama.StreamEvent{Content: "The user wants", Done: true, DoneReason: reason}
				},
			}}
			if err := a.compactLiveContextTo(t.Context(), nil, 300); err == nil {
				t.Fatal("unfinished compaction reported success")
			}
			if !reflect.DeepEqual(a.history, history) || a.historyGen != 0 {
				t.Fatal("unfinished summary changed active history")
			}
		})
	}
}

func TestReloadStoredHistoryInvalidatesInFlightCompaction(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a := &Agent{
		model: "fixture", memStore: &memory.Store{}, history: compactionFinishHistory(), historyLoaded: true,
		ollama: &failingStreamBackend{stream: func(_ context.Context, out chan<- ollama.StreamEvent) {
			close(started)
			<-release
			out <- ollama.StreamEvent{Content: "Complete summary.", Done: true, DoneReason: "stop"}
		}},
	}
	done := make(chan error, 1)
	go func() { done <- a.compactLiveContextTo(t.Context(), nil, 300) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("compaction did not start")
	}
	a.ReloadStoredHistory()
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale compaction reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compaction did not finish after reload")
	}
	if a.historyGen != 1 || a.historyLoaded || len(a.history) != 0 {
		t.Fatalf("reload state was replaced by stale compaction: generation=%d loaded=%v history=%+v", a.historyGen, a.historyLoaded, a.history)
	}
}

func TestRejectedCompactionPreservesStoredTranscript(t *testing.T) {
	store := resourceDatabase(t)
	a := &Agent{model: "fixture", sessionID: "summary-fixture", memStore: store, ollama: &failingStreamBackend{
		stream: func(_ context.Context, out chan<- ollama.StreamEvent) {
			out <- ollama.StreamEvent{Content: "The user wants", Done: true, DoneReason: "interrupted"}
		},
	}}
	for _, message := range compactionFinishHistory() {
		message.DBID = a.saveMessageToDB(t.Context(), message)
		if message.DBID == 0 {
			t.Fatal("could not persist fixture")
		}
		a.history = append(a.history, message)
	}
	before, err := store.LoadActiveHistoryTail(t.Context(), a.sessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.compactLiveContextTo(t.Context(), nil, 300); err == nil {
		t.Fatal("unfinished summary accepted")
	}
	after, err := store.LoadActiveHistoryTail(t.Context(), a.sessionID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || store.GetLiveCompaction(t.Context(), a.sessionID) != nil {
		t.Fatal("rejected compaction altered the archive or persisted summary")
	}
}
