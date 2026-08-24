package openai

import (
	"encoding/json"
	"testing"

	"prism/internal/ollama"
)

func toolCall(name string) ollama.ToolCall {
	return ollama.ToolCall{Function: ollama.ToolCallFunction{Name: name, Arguments: json.RawMessage(`{}`)}}
}

// asParts asserts a message's content is a []contentPart with exactly one image
// and returns that image's data URL — the shape a vision backend needs.
func firstImageURL(t *testing.T, m chatMsg) string {
	t.Helper()
	parts, ok := m.Content.([]contentPart)
	if !ok {
		t.Fatalf("expected []contentPart content, got %T", m.Content)
	}
	for _, p := range parts {
		if p.Type == "image_url" && p.ImageURL != nil {
			return p.ImageURL.URL
		}
	}
	t.Fatalf("no image_url part in %+v", parts)
	return ""
}

// TestToolImageBecomesUserMessage is the fix for widget auto-preview blindness:
// an image attached to a tool result must be re-emitted as a following user
// message, because the OpenAI wire format drops images on tool-role messages.
func TestToolImageBecomesUserMessage(t *testing.T) {
	in := []ollama.Message{
		{Role: "user", Content: "add a widget"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{toolCall("add_widget")}},
		{Role: "tool", Content: "Auto-preview: screenshot attached, inspect it.", Images: []string{"SCREENSHOTB64"}},
	}
	out := buildMessages(in)

	if len(out) != 4 {
		t.Fatalf("expected 4 messages (user, assistant, tool, user-image), got %d: %+v", len(out), out)
	}
	if out[2].Role != "tool" || out[2].ToolCallID == "" {
		t.Fatalf("out[2] should be the tool result with a tool_call_id, got %+v", out[2])
	}
	if s, ok := out[2].Content.(string); !ok || s == "" {
		t.Fatalf("tool message content should stay a plain string, got %T %v", out[2].Content, out[2].Content)
	}
	if out[3].Role != "user" {
		t.Fatalf("out[3] should be a user message carrying the image, got role %q", out[3].Role)
	}
	if url := firstImageURL(t, out[3]); url != "data:image/jpeg;base64,SCREENSHOTB64" {
		t.Fatalf("image url = %q", url)
	}
}

// TestParallelToolImagesFlushAfterBatch ensures the injected user-image message
// never splits a run of parallel tool results — strict servers reject a user
// message wedged between two tool answers of the same assistant turn.
func TestParallelToolImagesFlushAfterBatch(t *testing.T) {
	in := []ollama.Message{
		{Role: "assistant", ToolCalls: []ollama.ToolCall{toolCall("a"), toolCall("b")}},
		{Role: "tool", Content: "result A", Images: []string{"IMGA"}},
		{Role: "tool", Content: "result B"},
		{Role: "assistant", Content: "both done"},
	}
	out := buildMessages(in)

	// Expect: assistant(tool_calls), tool A, tool B, user(image), assistant("both done").
	roles := make([]string, len(out))
	for i, m := range out {
		roles[i] = m.Role
	}
	want := []string{"assistant", "tool", "tool", "user", "assistant"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v (both tool results must precede the image user message)", roles, want)
		}
	}
	if url := firstImageURL(t, out[3]); url != "data:image/jpeg;base64,IMGA" {
		t.Fatalf("image url = %q", url)
	}
}
