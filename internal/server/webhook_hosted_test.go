package server

import (
	"net/http/httptest"
	"prism/internal/agent"
	"prism/internal/memory"
	"strings"
	"testing"
)

func TestHostedWebhookConcurrencyAndPayload(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms, cfg: Config{WebhookConcurrency: 2}}
	err := ms.WebhookUpsert(t.Context(), memory.WebhookRow{ID: "fixture", Scope: "global", Token: "secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s.webhookActive.Store(2)
	w := httptest.NewRecorder()
	s.handleWebhookIncoming(w, httptest.NewRequest("POST", "/api/webhook/fixture?token=secret", strings.NewReader("payload")))
	if w.Code != 429 || s.webhookActive.Load() != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/webhook/fixture?token=secret&event=test", nil)
	message := composeWebhookMessage("", "", r)
	if strings.Contains(message, "secret") || !strings.Contains(message, "event=test") {
		t.Fatal("credentials leaked or payload lost")
	}
}

func TestHeadlessResponseReportsFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		events := make(chan agent.Event, 4)
		events <- agent.Event{Type: "stream", Content: "Planning"}
		events <- agent.Event{Type: "tool_use"}
		events <- agent.Event{Type: "stream", Content: "Result"}
		if failed {
			events <- agent.Event{Type: "error", Content: "provider unavailable"}
		}
		close(events)
		response, err := collectHeadlessResponse(events, nil, "fixture")
		if response != "Result" || (err != nil) != failed {
			t.Fatalf("response=%q err=%v", response, err)
		}
	}
}
