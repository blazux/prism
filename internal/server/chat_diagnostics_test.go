package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"prism/internal/agent"
	"prism/internal/memory"
)

func TestChatErrorSurvivesReconnectAndStaysOutOfModelHistory(t *testing.T) {
	ms := securityStore(t)
	s := &Server{memStore: ms}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := &Client{user: &memory.User{ID: 7}, sessionID: "u7-fixture", send: make(chan []byte, 10)}
	ms.AppendMessage(ctx, owner.sessionID, "user", "Read a report", nil)
	ms.AppendMessage(ctx, owner.sessionID, "assistant", "Partial answer", nil)
	run, err := s.beginRun(owner, cancel, "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	owner.sendJSON(map[string]any{"type": "error", "content": "The model connection was interrupted."})
	s.finishRun(run, ctx)
	peer := &Client{user: owner.user, sessionID: owner.sessionID, send: make(chan []byte, 10)}
	s.attachRun(peer)
	var history struct {
		Messages []struct{ Role, Content string }
	}
	if err := json.Unmarshal(<-peer.send, &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Messages) != 3 || history.Messages[2].Role != "error" || !strings.Contains(history.Messages[2].Content, "interrupted") {
		t.Fatalf("diagnostic missing after reconnect: %+v", history)
	}
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
		}
		json.NewDecoder(r.Body).Decode(&body)
		for _, message := range body.Messages {
			if message.Role == "error" || strings.Contains(message.Content, "The model connection was interrupted.") {
				t.Error("UI error diagnostic leaked into provider history")
			}
		}
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	resumed := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL})
	resumed.memStore = ms
	if _, err := resumed.runHeadlessChatTap(t.Context(), owner.sessionID, "Continue", "fixture", CallerContext{}, nil, agent.Limits{PromptProfile: "minimal"}); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("resumed conversation not sent to provider")
	}
	// A different account cannot receive this session's diagnostics.
	stranger := &Client{user: &memory.User{ID: 8}, sessionID: "u8-fixture", send: make(chan []byte, 10)}
	s.attachRun(stranger)
	for len(stranger.send) > 0 {
		if strings.Contains(string(<-stranger.send), "The model connection was interrupted.") {
			t.Fatal("diagnostic crossed user sessions")
		}
	}
}
