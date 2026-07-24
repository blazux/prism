package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The generation cap is the only guard against a model that loops instead of
// emitting a stop token: Ollama's own default is -1 — generate until the context
// is full — so a request that carries no num_predict can stream for as long as
// that takes, with the caller blocked and nothing to log. Assert on the wire, not
// on the struct: the cap is only worth anything if it actually reaches Ollama.
func TestChat_SendsGenerationCap(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want int
	}{
		{"default applies when the caller sets none", Options{}, DefaultNumPredict},
		{"an explicit cap is left alone", Options{NumPredict: 64}, 64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got ChatRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Write([]byte(`{"done":true}` + "\n"))
			}))
			defer srv.Close()

			ch := make(chan StreamEvent, 8)
			go func() {
				NewClient(srv.URL).Chat(context.Background(), ChatRequest{Model: "m", Options: tc.opts}, ch)
				close(ch)
			}()
			for range ch { // drain
			}

			if got.Options.NumPredict != tc.want {
				t.Errorf("num_predict on the wire = %d, want %d", got.Options.NumPredict, tc.want)
			}
		})
	}
}

// A transient failure at the start of a turn (503 / connection blip) must be
// retried before any token is streamed — mirrors
// internal/openai/client_test.go's TestChat_RetriesTransientThenSucceeds; the
// GX10 fleet's flaky link bites this backend exactly as much as the
// OpenAI-compatible one.
func TestChat_RetriesTransientThenSucceeds(t *testing.T) {
	old := retryBaseBackoff
	retryBaseBackoff = time.Millisecond
	defer func() { retryBaseBackoff = old }()

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n < 2 { // fail the first two attempts
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"message":{"content":"ok"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	ch := make(chan StreamEvent, 8)
	go func() {
		NewClient(srv.URL).Chat(context.Background(), ChatRequest{Model: "m"}, ch)
		close(ch)
	}()

	var content string
	var lastErr error
	for ev := range ch {
		content += ev.Content
		if ev.Err != nil {
			lastErr = ev.Err
		}
	}
	if lastErr != nil {
		t.Fatalf("unexpected err: %v", lastErr)
	}
	if content != "ok" {
		t.Errorf("content = %q, want %q", content, "ok")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

// Ping and ListModels previously ignored the HTTP status code entirely — a
// reverse proxy returning a 502/404 HTML error page for /api/tags still read
// as "healthy" (Ping) or decoded into an empty, error-free model list
// (ListModels), masking a genuinely down backend.
func TestPing_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.Ping(context.Background()); err == nil {
		t.Error("expected error for 502 response, got nil")
	}
}

func TestListModels_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("<html>not found</html>"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	models, err := c.ListModels(context.Background())
	if err == nil {
		t.Errorf("expected error for 404 response, got nil (models=%v)", models)
	}
}
