package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prism/internal/customtools"
	"prism/internal/docker"
	"prism/workspaceexec"
)

func TestDeletionWaitsForHTTPToolProcessCleanup(t *testing.T) {
	for _, route := range []string{"/api/builtin/exec_command", "/api/tool/wait_tool"} {
		t.Run(route, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "wait_tool.py"), []byte("# TOOL: {\"name\":\"wait_tool\",\"description\":\"Fixture\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			started, cancelled, cleaned := make(chan string, 1), make(chan struct{}), make(chan struct{})
			s := &Server{cfg: Config{WorkspaceDir: root, PluginDir: root, LLMBackend: "openai", Model: "fixture"}, customMgr: customtools.NewManager(root), toolLimiter: newToolLimiter()}
			s.docker = docker.WithExecution(func(ctx context.Context, _ string, _ []byte, _ map[string]string) (string, error) {
				started <- workspaceexec.Scope(ctx)
				<-ctx.Done()
				close(cancelled)
				<-cleaned
				return "", ctx.Err()
			})
			handler := s.handleBuiltinTool
			if strings.HasPrefix(route, "/api/tool/") {
				handler = s.handleToolCall
			}
			request := func() *http.Request {
				return httptest.NewRequest("POST", route+"?session=board", strings.NewReader(`{"command":"sleep 90"}`))
			}
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { handler(response, request()); close(done) }()
			select {
			case scope := <-started:
				if scope != "board" {
					t.Fatal(scope)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP execution not admitted", response.Body.String())
			}
			deleteResult := make(chan error, 1)
			go func() {
				release, err := s.quiesceSession(context.Background(), &Client{sessionID: "board"})
				if err == nil {
					release(true)
				}
				deleteResult <- err
			}()
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("HTTP execution not cancelled")
			}
			select {
			case <-deleteResult:
				t.Fatal("deletion did not wait for process cleanup")
			default:
			}
			close(cleaned)
			if err := <-deleteResult; err != nil {
				t.Fatal(err)
			}
			<-done
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			retry := httptest.NewRecorder()
			handler(retry, request())
			if retry.Code != 409 {
				t.Fatal("deleted dashboard admitted another HTTP tool", retry.Code)
			}
		})
	}
}
