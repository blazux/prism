package agent

import (
	"strings"
	"testing"

	"prism/internal/ollama"
)

func TestStripHistoryImagesLeavesTextMarker(t *testing.T) {
	a := &Agent{history: []ollama.Message{
		{Role: "user", Content: "What is this?", Images: []string{"one", "two"}},
		{Role: "tool", Content: "screenshot", Images: []string{"three"}},
		{Role: "assistant", Content: "done"},
	}}

	a.stripHistoryImages()

	for i, msg := range a.history {
		if len(msg.Images) != 0 {
			t.Fatalf("history[%d] still contains images", i)
		}
	}
	if !strings.Contains(a.history[0].Content, "cannot inspect images") ||
		!strings.Contains(a.history[1].Content, "cannot inspect images") {
		t.Fatalf("image omission marker missing: %#v", a.history)
	}
}

func TestIsVisionUnsupportedError(t *testing.T) {
	yes := []string{
		"At most 0 image(s) may be provided in one prompt",
		"model does not support image input",
		"image input is not supported by this endpoint",
	}
	for _, msg := range yes {
		if !isVisionUnsupportedError(testError(msg)) {
			t.Errorf("expected vision incompatibility: %q", msg)
		}
	}
	if isVisionUnsupportedError(testError("invalid API key")) {
		t.Error("ordinary provider errors must not trigger an image retry")
	}
}

type testError string

func (e testError) Error() string { return string(e) }
