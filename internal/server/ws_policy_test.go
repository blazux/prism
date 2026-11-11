package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"prism/internal/docker"
)

func TestBrowserRefreshesToolPolicyWithoutReconnect(t *testing.T) {
	ms := securityStore(t)
	catalogues := make(chan bool, 3)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Tools []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		visible := false
		subagentVisible := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "subagent" {
				subagentVisible = true
			}
			if tool.Function.Name == "exec_command" {
				visible = true
			}
		}
		if !subagentVisible {
			t.Error("refreshing policies removed the live subagent capability")
		}
		catalogues <- visible
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: provider.URL})
	s.memStore = ms
	s.docker = docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) { return "", nil })
	server := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?session=policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer func() {
		s.life.mu.Lock()
		if s.life.cancel != nil {
			s.life.cancel()
		}
		s.life.mu.Unlock()
	}()
	for _, access := range []string{"open", "disabled", "open"} {
		if err := ms.SetToolPolicy(t.Context(), "exec_command", access); err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(map[string]string{"type": "chat", "content": "Hello"}); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		for {
			var event struct{ Type, Status string }
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "run_status" && event.Status == "completed" {
				break
			}
		}
		select {
		case visible := <-catalogues:
			if visible != (access == "open") {
				t.Fatalf("policy=%s, tool visible=%v", access, visible)
			}
		default:
			t.Fatal("provider was not called")
		}
	}
}
