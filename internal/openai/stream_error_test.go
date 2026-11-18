package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type streamFixtureTransport func(*http.Request) (*http.Response, error)

func (f streamFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func chatFixtureStream(t *testing.T, stream string) []ollama.StreamEvent {
	t.Helper()
	client := NewClient("https://fixture.invalid/v1", "")
	client.httpClient = &http.Client{Transport: streamFixtureTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})}
	out := make(chan ollama.StreamEvent, 20)
	client.Chat(context.Background(), ollama.ChatRequest{Model: "fixture"}, out)
	close(out)
	var events []ollama.StreamEvent
	for event := range out {
		events = append(events, event)
	}
	return events
}

func TestStreamErrorRejectsCompletionAndPendingTools(t *testing.T) {
	for name, prefix := range map[string]string{
		"before_content": "",
		"after_content":  "data: {\"choices\":[{\"delta\":{\"content\":\"Partial answer\"}}]}\n\n",
		"partial_tool":   "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n",
		"finished_tool":  "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"write_file\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			events := chatFixtureStream(t, prefix+"data: {\"error\":{\"message\":\"upstream out of quota\",\"code\":429}}\n\ndata: [DONE]\n\n")
			var gotError bool
			for _, event := range events {
				if event.Done || len(event.ToolCalls) != 0 {
					t.Fatalf("failed provider turn delivered a completed action: %+v", event)
				}
				if event.Err != nil && strings.Contains(event.Err.Error(), "upstream out of quota") {
					gotError = true
				}
			}
			if !gotError {
				t.Fatalf("provider error was lost: %+v", events)
			}
		})
	}
}

func TestStreamErrorTakesPrecedenceOverChoices(t *testing.T) {
	events := chatFixtureStream(t, "data: {\"error\":{\"message\":\"failed\"},\"choices\":[{\"delta\":{\"content\":\"invalid output\"},\"finish_reason\":\"stop\"}]}\n\n")
	if len(events) != 1 || events[0].Err == nil || events[0].Content != "" || events[0].Done {
		t.Fatalf("error chunk's choices accepted: %+v", events)
	}
}

func TestStreamUnknownErrorDoesNotEchoRawEnvelope(t *testing.T) {
	events := chatFixtureStream(t, "data: {\"error\":{\"credential\":\"private-secret\"}}\n\n")
	if len(events) != 1 || events[0].Err == nil || strings.Contains(events[0].Err.Error(), "private-secret") {
		t.Fatalf("unexpected generic provider error: %+v", events)
	}
}

func TestStreamNullErrorAndUsageOnlyChunksRemainValid(t *testing.T) {
	events := chatFixtureStream(t, "data: {\"error\":null,\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n")
	if len(events) != 2 || events[0].Content != "ok" || !events[1].Done || events[1].DoneReason != "stop" || events[1].Usage == nil {
		t.Fatalf("valid stream rejected: %+v", events)
	}
	for _, event := range events {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
}
