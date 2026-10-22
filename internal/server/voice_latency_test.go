package server

import (
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

func TestVoiceSocketSkipsDashboardState(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Bonjour.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleWS(w, withUser(r, serviceUser)) }))
	defer host.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"?session=voice-latency&channel=voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := c.WriteJSON(map[string]any{"type": "chat", "content": "Bonjour", "channel": "voice"}); err != nil {
		t.Fatal(err)
	}
	streamed := false
	for {
		var ev struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := c.ReadJSON(&ev); err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case "container_status", "file_tree", "tools_list", "plugin_load", "notifications_history":
			t.Fatalf("voice received dashboard payload %q", ev.Type)
		case "stream":
			streamed = streamed || strings.Contains(ev.Content, "Bonjour")
		case "error":
			t.Fatal(ev.Content)
		case "turn_complete":
			if !streamed {
				t.Fatal("voice response did not stream")
			}
			return
		}
	}
}

func TestVoiceQueryAloneDoesNotSkipBrowserInitialization(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	host := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer host.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"?session=browser&channel=voice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if ev["type"] != "container_status" {
		t.Fatalf("untrusted query changed dashboard initialization: %s", data)
	}
}
