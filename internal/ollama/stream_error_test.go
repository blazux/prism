package ollama

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatReportsErrorInsideSuccessfulHTTPStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":{"content":"partial"}}`)
		fmt.Fprintln(w, `{"error":"provider unavailable"}`)
		fmt.Fprintln(w, `{"message":{"content":"must not arrive"},"done":true,"done_reason":"stop"}`)
	}))
	defer server.Close()
	events := make(chan StreamEvent, 10)
	NewClient(server.URL).Chat(t.Context(), ChatRequest{Model: "fixture"}, events)
	close(events)
	var content string
	var failed bool
	for event := range events {
		content += event.Content
		if event.Err != nil && strings.Contains(event.Err.Error(), "provider unavailable") {
			failed = true
		}
		if event.Done {
			t.Fatal("stream error reported as a completed answer")
		}
	}
	if content != "partial" || !failed {
		t.Fatalf("content=%q error=%v", content, failed)
	}
}

func TestChatRequiresExplicitDoneMarker(t *testing.T) {
	for _, test := range []struct {
		name, body, reason string
	}{
		{"empty EOF", "", "interrupted"},
		{"text EOF", `{"message":{"content":"partial"}}`, "interrupted"},
		{"tools EOF", `{"message":{"tool_calls":[{"function":{"name":"write_file","arguments":{"path":"fixture"}}}]}}`, "interrupted"},
		{"complete", `{"message":{"content":"complete"},"done":true,"done_reason":"stop"}`, "stop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, test.body)
			}))
			defer server.Close()
			events := make(chan StreamEvent, 10)
			NewClient(server.URL).Chat(t.Context(), ChatRequest{Model: "fixture"}, events)
			close(events)
			var terminal []StreamEvent
			for event := range events {
				if event.Err != nil {
					t.Fatalf("unexpected transport error: %v", event.Err)
				}
				if event.Done {
					terminal = append(terminal, event)
				}
			}
			if len(terminal) != 1 || terminal[0].DoneReason != test.reason {
				t.Fatalf("terminal events=%+v, want %s", terminal, test.reason)
			}
		})
	}
}
