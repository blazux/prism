package ollama

import (
	"encoding/json"
	"strings"
	"testing"
)

// NoThinking must reach Ollama as "think": false; otherwise the field is absent
// so models without a thinking mode never see it.
func TestThinkSerialization(t *testing.T) {
	b, _ := json.Marshal(ChatRequest{Model: "m"})
	if strings.Contains(string(b), "think") {
		t.Errorf("default request should not carry think: %s", b)
	}
	off := false
	b, _ = json.Marshal(ChatRequest{Model: "m", Think: &off})
	if !strings.Contains(string(b), `"think":false`) {
		t.Errorf("want think:false, got %s", b)
	}
}
