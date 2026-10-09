package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVisionMetadataIsTriState(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		known, vision  bool
	}{
		{"absent", `{}`, false, false},
		{"null", `{"supports_vision":null}`, false, false},
		{"supported", `{"supports_vision":true}`, true, true},
		{"unsupported", `{"supports_vision":false}`, true, false},
		{"invalid", `{"supports_vision":"false"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadata map[string]any
			if err := json.Unmarshal([]byte(tc.metadata), &metadata); err != nil {
				t.Fatal(err)
			}
			info := ParseCapabilities(metadata)
			if (info.Vision != nil) != tc.known || (info.Vision != nil && *info.Vision != tc.vision) {
				t.Fatalf("unexpected vision metadata: %#v", info)
			}
		})
	}
}

func TestOllamaEffectiveContextDoesNotUseTheoreticalMaximum(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" || r.Method != "POST" {
			t.Error("incorrect discovery endpoint")
		}
		fmt.Fprint(w, `{"model_info":{"qwen.context_length":500000},"capabilities":["completion","vision"]}`)
	}))
	defer s.Close()
	info, err := NewClient(s.URL).ModelCapabilities(context.Background(), "fixture")
	if err != nil || info.ContextWindow != NumCtx || info.Vision == nil || !*info.Vision {
		t.Fatalf("%#v %v", info, err)
	}
}
