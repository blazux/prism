package agent

import (
	"context"
	"prism/internal/ollama"
	"testing"
	"time"
)

type reviewDelayedBackend struct{ started, release chan struct{} }

func (b *reviewDelayedBackend) Chat(ctx context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	close(b.started)
	<-b.release
	out <- ollama.StreamEvent{Content: "dummy old answer."}
}
func (b *reviewDelayedBackend) Ping(context.Context) error                   { return nil }
func (b *reviewDelayedBackend) ListModels(context.Context) ([]string, error) { return nil, nil }
func (b *reviewDelayedBackend) ContextBudgetChars() int                      { return 0 }
func TestCancelledReplyIsNotPersisted(t *testing.T) {
	backend := &reviewDelayedBackend{make(chan struct{}), make(chan struct{})}
	a := New(backend, NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", ""), "fixture", nil, "")
	events := make(chan Event, 100)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { defer close(done); a.Chat(ctx, "dummy question", nil, events) }()
	select {
	case <-backend.started:
	case <-time.After(3 * time.Second):
		t.Fatal("backend not reached")
	}
	cancel()
	close(backend.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not end")
	}
	a.histMu.Lock()
	defer a.histMu.Unlock()
	if len(a.history) != 1 || a.history[0].Role != "user" {
		t.Fatal("behavior changed: old response no longer repopulates reset history")
	}

}
