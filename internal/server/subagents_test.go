package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"prism/internal/agent"
	"prism/internal/docker"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubagentsParallelBoundedAndParentScoped(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture result\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := agent.NewToolExecutor(nil, s.cfg.WorkspaceDir, s.cfg.PluginDir, "", "")
	parent := &Client{sessionID: "fixture", send: make(chan []byte, 100), ag: agent.New(nil, e, "fixture", nil, "")}
	run, err := s.beginRun(parent, cancel, "Parent fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := s.subagentTool(parent, e)
	ids := []string{}
	for i := 0; i < 3; i++ {
		raw, err := tool(ctx, map[string]any{"action": "spawn", "task": "Return a fixture result"})
		if err != nil {
			t.Fatal(err)
		}
		var row map[string]string
		json.Unmarshal([]byte(raw), &row)
		ids = append(ids, row["id"])
	}
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("children did not run concurrently")
		}
	}
	if _, err := tool(ctx, map[string]any{"action": "spawn", "task": "fourth"}); err == nil {
		t.Fatal("concurrency cap bypassed")
	}
	other := &Client{sessionID: "other", send: make(chan []byte, 10)}
	if _, err := s.subagentTool(other, e)(ctx, map[string]any{"action": "status", "id": ids[0]}); err == nil {
		t.Fatal("child accessible from another task")
	}
	close(release)
	for _, id := range ids {
		raw, err := tool(ctx, map[string]any{"action": "wait", "id": id})
		if err != nil {
			t.Fatal(err)
		}
		var row map[string]string
		json.Unmarshal([]byte(raw), &row)
		if row["status"] != "completed" || row["result"] != "Fixture result" {
			t.Fatal(row)
		}
	}
	s.finishRun(run, ctx)
}
func TestRunFinishCancelsChildren(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{sessionID: "fixture", send: make(chan []byte, 10)}
	r, err := s.beginRun(c, cancel, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	childCtx, stop := context.WithCancel(ctx)
	child := &childTask{id: "fixture", cancel: stop, done: make(chan struct{})}
	r.children = map[string]*childTask{"fixture": child}
	r.childrenDone.Add(1)
	go func() { defer r.childrenDone.Done(); <-childCtx.Done(); close(child.done) }()
	s.finishRun(r, ctx)
	select {
	case <-child.done:
	default:
		t.Fatal("orphan child")
	}
}

func TestSubagentInheritsParentToolDenials(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var calls atomic.Int32
	var denied atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		for _, tool := range req.Tools {
			if tool.Function.Name == "subagent" || tool.Function.Name == "editor" || tool.Function.Name == "request_secret" {
				t.Errorf("child advertised %s", tool.Function.Name)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"fixture","type":"function","function":{"name":"run_command","arguments":"{\"command\":\"must-not-run\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		for _, msg := range req.Messages {
			if msg.Role == "tool" && strings.Contains(msg.Content, "fixture policy denied") {
				denied.Store(true)
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Cannot perform the task: policy denied.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	s := New(Config{WorkspaceDir: t.TempDir(), PluginDir: t.TempDir(), Model: "fixture", LLMBackend: "openai", OpenAIBaseURL: backend.URL, AgentContainer: "fixture"})
	s.docker = docker.WithExecution(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := agent.NewToolExecutor(nil, s.cfg.WorkspaceDir, s.cfg.PluginDir, "", "")
	e.SetToolGuard(func(string, map[string]interface{}) error { return fmt.Errorf("fixture policy denied") })
	parent := &Client{sessionID: "fixture", send: make(chan []byte, 100), ag: agent.New(nil, e, "fixture", nil, "")}
	run, err := s.beginRun(parent, cancel, "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.finishRun(run, ctx)
	tool := s.subagentTool(parent, e)
	raw, err := tool(ctx, map[string]any{"action": "spawn", "task": "Try the fixture command"})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]string
	json.Unmarshal([]byte(raw), &row)
	if _, err = tool(ctx, map[string]any{"action": "wait", "id": row["id"]}); err != nil {
		t.Fatal(err)
	}
	if !denied.Load() {
		t.Fatal("child did not inherit parent tool denial")
	}
}
