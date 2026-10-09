package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestContextMetadataIsExplicitAndCredentialScoped(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/model_group/info" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer a" {
			fmt.Fprint(w, `{"data":[{"id":"qwen","context_length":500000,"max_model_len":131072,"supports_vision":true},{"id":"unknown"}]}`)
		} else {
			fmt.Fprint(w, `{"data":[{"id":"qwen","context_length":32768}]}`)
		}
	}))
	defer s.Close()
	c := NewClient(s.URL+"/v1", "a")
	info, err := c.ModelCapabilities(context.Background(), "qwen")
	if err != nil || info.ContextWindow != 131072 || info.Vision == nil || !*info.Vision {
		t.Fatalf("%#v %v", info, err)
	}
	if _, err = c.ModelCapabilities(context.Background(), "qwen"); err != nil || calls.Load() != 1 {
		t.Fatal("metadata not cached")
	}
	info, err = NewClient(s.URL+"/v1", "b").ModelCapabilities(context.Background(), "qwen")
	if err != nil || info.ContextWindow != 32768 {
		t.Fatal("metadata crossed credential boundary")
	}
	info, err = c.ModelCapabilities(context.Background(), "unknown")
	if err != nil || info.ContextWindow != 0 || info.Vision != nil {
		t.Fatal("invented unknown model capabilities")
	}
}

func TestGatewayCapabilitiesFallback(t *testing.T) {
	for _, tc := range []struct {
		name, list, group   string
		status, wantContext int
		wantVision          *bool
	}{
		{"ids only", `{"data":[{"id":"qwen /27b"}]}`, `{"data":[{"model_group":"unrelated","max_input_tokens":999999,"supports_vision":false},{"model_group":"qwen /27b","max_input_tokens":500000,"supports_vision":true}]}`, 200, 500000, boolPtr(true)},
		{"keep explicit list limit", `{"data":[{"id":"qwen /27b","max_model_len":32768}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":500000,"supports_vision":true}]}`, 200, 32768, boolPtr(true)},
		{"group default false means unknown", `{"data":[{"id":"qwen /27b"}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":null,"supports_vision":false}]}`, 200, 0, nil},
		{"null vision remains unknown", `{"data":[{"id":"qwen /27b","supports_vision":null}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":500000,"supports_vision":null}]}`, 200, 500000, nil},
		{"null list vision can be filled", `{"data":[{"id":"qwen /27b","supports_vision":null}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":126976,"supports_vision":true}]}`, 200, 126976, boolPtr(true)},
		{"group false remains unknown with context", `{"data":[{"id":"qwen /27b"}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":500000,"supports_vision":false}]}`, 200, 500000, nil},
		{"explicit list false is retained", `{"data":[{"id":"qwen /27b","supports_vision":false}]}`, `{"data":[{"model_group":"qwen /27b","max_input_tokens":500000,"supports_vision":true}]}`, 200, 500000, boolPtr(false)},
		{"missing exact group", `{"data":[{"id":"qwen /27b"}]}`, `{"data":[{"model_group":"other","max_input_tokens":500000,"supports_vision":true}]}`, 200, 0, nil},
		{"metadata forbidden", `{"data":[{"id":"qwen /27b","context_length":32768}]}`, `{}`, 403, 32768, nil},
		{"not a gateway", `{"data":[{"id":"qwen /27b"}]}`, `{}`, 404, 0, nil},
		{"bad metadata", `{"data":[{"id":"qwen /27b"}]}`, `invalid`, 200, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("incorrect metadata request")
				}
				switch r.URL.Path {
				case "/gateway/v1/models":
					fmt.Fprint(w, tc.list)
				case "/gateway/model_group/info":
					if r.URL.Query().Get("model_group") != "qwen /27b" {
						t.Error("incorrect model query")
					}
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.group)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			c := NewClient(s.URL+"/gateway/v1", "fixture")
			for i := 0; i < 2; i++ {
				info, err := c.ModelCapabilities(context.Background(), "qwen /27b")
				if err != nil || info.ContextWindow != tc.wantContext || (info.Vision == nil) != (tc.wantVision == nil) || (info.Vision != nil && *info.Vision != *tc.wantVision) {
					t.Fatalf("%#v %v", info, err)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("metadata not cached: %d calls", calls.Load())
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
