package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"prism/internal/ollama"
)

type voiceHangupBackend struct {
	oneToolBackend
	text    string
	tools   []string
	request ollama.ChatRequest
}

func (b *voiceHangupBackend) Chat(_ context.Context, req ollama.ChatRequest, out chan<- ollama.StreamEvent) {
	b.request = req
	b.calls++
	if b.calls > 1 {
		out <- ollama.StreamEvent{Content: "Seconde réponse.", Done: true, DoneReason: "stop"}
		return
	}
	var calls []ollama.ToolCall
	for _, name := range b.tools {
		calls = append(calls, ollama.ToolCall{Function: ollama.ToolCallFunction{
			Name: name, Arguments: json.RawMessage(`{"recipient":"Fixture","content":"Message"}`),
		}})
	}
	out <- ollama.StreamEvent{Content: b.text, ToolCalls: calls, Done: true, DoneReason: "tool_calls"}
}

func TestVoiceHangupFinishesTurnAfterRecordingEntireToolBatch(t *testing.T) {
	for _, spoken := range []string{"Parfait, bonne journée !", ""} {
		t.Run(spoken, func(t *testing.T) {
			backend := &voiceHangupBackend{text: spoken, tools: []string{"end_call", "take_message"}}
			executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
			var relayed []string
			executor.SetTelephony(TelephonyTools, func(name string, _ map[string]interface{}) (string, error) {
				relayed = append(relayed, name)
				return "Transmis à la passerelle.", nil
			})
			a := New(backend, executor, "fixture", nil, "")
			a.SetChannel(voiceChannel)
			events := make(chan Event, 32)
			a.Chat(t.Context(), "Merci, au revoir.", nil, events)
			close(events)
			if backend.calls != 1 || !reflect.DeepEqual(relayed, backend.tools) {
				t.Fatalf("extra model response or dropped tool: calls=%d relayed=%v", backend.calls, relayed)
			}
			if backend.request.NoThinking || backend.request.ReasoningEffort != "low" || !backend.request.NoThinkingIfUnsupported {
				t.Fatalf("voice reasoning policy did not reach backend: off=%v effort=%q", backend.request.NoThinking, backend.request.ReasoningEffort)
			}
			var text strings.Builder
			results, ends := 0, 0
			for event := range events {
				switch event.Type {
				case "stream":
					text.WriteString(event.Content)
				case "tool_result":
					results++
				case "stream_end":
					ends++
				case "error":
					t.Fatal(event.Content)
				}
			}
			if text.String() != spoken || results != 2 || ends != 1 {
				t.Fatalf("farewell/results/end changed: %q, %d, %d", text.String(), results, ends)
			}
			if len(a.history) != 4 || a.history[2].Role != "tool" || a.history[3].Role != "tool" {
				t.Fatal("tool results missing from conversation history")
			}
		})
	}
}

func TestFailedHangupAndNonVoiceTurnsStillAllowFollowup(t *testing.T) {
	for _, channel := range []string{voiceChannel, ""} {
		backend := &voiceHangupBackend{text: "Au revoir.", tools: []string{"end_call"}}
		executor := NewToolExecutor(nil, t.TempDir(), t.TempDir(), "", "")
		executor.SetTelephony(TelephonyTools, func(string, map[string]interface{}) (string, error) {
			if channel == voiceChannel {
				return "", errors.New("fixture relay failed")
			}
			return "OK", nil
		})
		a := New(backend, executor, "fixture", nil, "")
		a.SetChannel(channel)
		events := make(chan Event, 32)
		a.Chat(t.Context(), "Au revoir.", nil, events)
		if backend.calls != 2 {
			t.Fatalf("channel %q stopped despite failed relay or non-voice context", channel)
		}
	}
}
