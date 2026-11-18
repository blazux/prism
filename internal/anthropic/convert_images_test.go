package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"prism/internal/ollama"
)

func TestBuildMessagesDetectsImageMediaTypes(t *testing.T) {
	for _, fixture := range []struct {
		name string
		data string
		want string
	}{
		{"png", "\x89PNG\r\n\x1a\n", "image/png"},
		{"jpeg", "\xff\xd8\xff", "image/jpeg"},
		{"gif", "GIF89a", "image/gif"},
		{"webp", "RIFF\x00\x00\x00\x00WEBPVP8 ", "image/webp"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(fixture.data))
			got := buildMessages([]ollama.Message{{Role: "user", Content: "inspect", Images: []string{encoded}}})
			source := got[0].Content[1].Source
			if source.MediaType != fixture.want || source.Data != encoded {
				t.Fatalf("image source = %+v, want %s with original bytes", source, fixture.want)
			}
		})
	}
}

func TestBuildMessagesKeepsToolImagesInsideContiguousResultBatch(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
	got := buildMessages([]ollama.Message{
		{Role: "user", Content: "inspect both"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{
			{Function: ollama.ToolCallFunction{Name: "screenshot", Arguments: json.RawMessage(`{}`)}},
			{Function: ollama.ToolCallFunction{Name: "status", Arguments: json.RawMessage(`{}`)}},
		}},
		{Role: "tool", Content: "Preview", Images: []string{png}},
		{Role: "tool", Content: "Ready"},
	})
	if len(got) != 3 || got[2].Role != "user" || len(got[2].Content) != 2 {
		t.Fatalf("result batch split: %+v", got)
	}
	for i, result := range got[2].Content {
		if result.Type != "tool_result" || result.ToolUseID != got[1].Content[i].ID {
			t.Fatalf("result %d has lost its paired call: %+v", i, result)
		}
	}
	blocks, ok := got[2].Content[0].Content.([]apiBlock)
	if !ok || len(blocks) != 2 || blocks[0].Text != "Preview" || blocks[1].Source == nil || blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != png {
		t.Fatalf("tool image content = %+v", got[2].Content[0].Content)
	}
	wire, err := json.Marshal(got)
	if err != nil || !json.Valid(wire) {
		t.Fatalf("invalid multimodal request: %s, %v", wire, err)
	}
	if got[2].Content[1].Content != "Ready" {
		t.Fatalf("text-only tool output changed: %+v", got[2].Content[1])
	}
}
