package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"prism/internal/ollama"
)

type traceLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *traceLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *traceLogBuffer) traces() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var traces []string
	for _, line := range strings.Split(b.Buffer.String(), "\n") {
		if strings.Contains(line, "[model-request]") {
			traces = append(traces, line)
		}
	}
	return strings.Join(traces, "\n")
}
func captureModelTraces(t *testing.T) *traceLogBuffer {
	t.Helper()
	var buffer traceLogBuffer
	previous := log.Writer()
	log.SetOutput(&buffer)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buffer
}

func TestSourceTraceIdentifiesResolvedSourceAndKeepsSecretsOut(t *testing.T) {
	logs := captureModelTraces(t)
	fixture := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", label)
		}))
	}
	a, b := fixture("first"), fixture("second")
	defer a.Close()
	defer b.Close()
	// A legacy URL may contain credentials even though the settings form now
	// rejects them. Its diagnostic must still disclose only the clean hop.
	secretURL := strings.Replace(b.URL, "http://", "http://private-user:private-password@", 1) + "/v1?token=private-query#private-fragment"
	router := &sourcesBackend{primary: "first", sources: []aiSource{
		{ID: "first", Provider: "other", BaseURL: a.URL + "/v1", APIKey: "private-first-key", Model: "same-model"},
		{ID: "second", Provider: "other", BaseURL: secretURL, APIKey: "private-second-key", Model: "same-model"},
	}}
	for _, test := range []struct{ model, want string }{{"same-model", "first"}, {"second::same-model", "second"}} {
		events := make(chan ollama.StreamEvent, 10)
		router.Chat(t.Context(), ollama.ChatRequest{TaskID: "task-fixture", Model: test.model, Messages: []ollama.Message{{Role: "user", Content: "private-prompt"}}}, events)
		close(events)
		var content string
		for event := range events {
			if event.Err != nil {
				t.Fatal(event.Err)
			}
			content += event.Content
		}
		if content != test.want {
			t.Fatalf("routed %q to %q, want %q", test.model, content, test.want)
		}
	}
	traces := logs.traces()
	for _, required := range []string{`source_id="first"`, `source_id="second"`, `task_id="task-fixture"`, `model="same-model"`, `provider="other"`, `http_status=200 outcome="complete"`, `destination="` + b.URL + `/v1"`} {
		if !strings.Contains(traces, required) {
			t.Fatalf("missing %s in %s", required, traces)
		}
	}
	for _, secret := range []string{"private-user", "private-password", "private-query", "private-fragment", "private-first-key", "private-second-key", "private-prompt"} {
		if strings.Contains(traces, secret) {
			t.Fatalf("trace disclosed %s", secret)
		}
	}
	ids := regexp.MustCompile(`phase=start request_id="([^"]+)"`).FindAllStringSubmatch(traces, -1)
	if len(ids) != 2 || ids[0][1] == ids[1][1] {
		t.Fatalf("request IDs missing or reused: %s", traces)
	}
	for _, id := range ids {
		if !strings.Contains(traces, `phase=end request_id="`+id[1]+`"`) {
			t.Fatalf("missing terminal diagnostic for %s", id[1])
		}
	}
}

func TestSourceTraceRecordsHTTPAndStreamOutcomesForEachProvider(t *testing.T) {
	for _, provider := range []string{"other", "ollama", "anthropic"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%v", provider, fail), func(t *testing.T) {
				logs := captureModelTraces(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if fail {
						http.Error(w, "private-provider-error", http.StatusBadRequest)
						return
					}
					switch provider {
					case "ollama":
						fmt.Fprintln(w, `{"message":{"content":"ok"},"done":true,"done_reason":"stop"}`)
					case "anthropic":
						fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n")
					default:
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n")
					}
				}))
				defer server.Close()
				router := &sourcesBackend{primary: "fixture", sources: []aiSource{{ID: "fixture", Provider: provider, BaseURL: server.URL, APIKey: "private-key", Model: "fixture-model"}}}
				events := make(chan ollama.StreamEvent, 10)
				router.Chat(t.Context(), ollama.ChatRequest{Model: "fixture-model", Messages: []ollama.Message{{Role: "user", Content: "private-prompt"}}}, events)
				close(events)
				var failed bool
				for event := range events {
					failed = failed || event.Err != nil
				}
				want := `http_status=200 outcome="complete"`
				if fail {
					want = `http_status=400 outcome="error"`
				}
				traces := logs.traces()
				if failed != fail || !strings.Contains(traces, want) || strings.Contains(traces, "private-") {
					t.Fatalf("failed=%v, trace=%s", failed, traces)
				}
			})
		}
	}
}

func TestSourceTraceRouteFailureHasUnknownHTTPStatus(t *testing.T) {
	logs := captureModelTraces(t)
	router := &sourcesBackend{primary: "fixture"}
	events := make(chan ollama.StreamEvent, 1)
	router.Chat(t.Context(), ollama.ChatRequest{TaskID: "route-task", Model: "missing::model"}, events)
	if event := <-events; event.Err == nil {
		t.Fatal("missing source accepted")
	}
	traces := logs.traces()
	if !strings.Contains(traces, `http_status=0 outcome="route_error"`) || !strings.Contains(traces, `model="missing::model"`) {
		t.Fatal(traces)
	}
}

func TestSourceTraceCancellationDrainsProviderWithBlockedConsumer(t *testing.T) {
	logs := captureModelTraces(t)
	requestEnded := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(requestEnded)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	router := &sourcesBackend{primary: "fixture", sources: []aiSource{{ID: "fixture", Provider: "other", BaseURL: server.URL, Model: "fixture-model"}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out, finished := make(chan ollama.StreamEvent), make(chan struct{})
	go func() {
		router.Chat(ctx, ollama.ChatRequest{Model: "fixture-model", Messages: []ollama.Message{{Role: "user", Content: "fixture"}}}, out)
		close(finished)
	}()
	select {
	case event := <-out:
		if event.Content != "partial" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not start")
	}
	cancel()
	// No further reads from out: the relay must still release its producer.
	for _, done := range []<-chan struct{}{finished, requestEnded} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled source request leaked a producer")
		}
	}
	if traces := logs.traces(); !strings.Contains(traces, `http_status=200 outcome="cancelled"`) {
		t.Fatal(traces)
	}
}
