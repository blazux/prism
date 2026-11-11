package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/docker"
	"prism/internal/memory"
	"prism/workspaceexec"
)

func customExecutionServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	tools := filepath.Join(root, "agent_tools")
	if err := os.MkdirAll(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "fixture.py"), []byte("# TOOL: {\"name\":\"fixture_custom\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return New(Config{WorkspaceDir: root, PluginDir: filepath.Join(root, "plugins")})
}

func TestCustomHTTPPathsShareWorkingDirectoryAndKeepResponseContracts(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%v/failed=%v", legacy, failed), func(t *testing.T) {
				s := customExecutionServer(t)
				s.docker = docker.WithExecution(func(ctx context.Context, cmd string, input []byte, env map[string]string) (string, error) {
					if !strings.HasPrefix(cmd, "cd /workspace && python3 ") || !strings.Contains(cmd, "/workspace/agent_tools/fixture.py") {
						t.Fatalf("wrong command: %s", cmd)
					}
					if workspaceexec.Scope(ctx) != "board" || env["PRISM_SESSION"] != "board" || string(input) != `{"count":2}` {
						t.Fatalf("wrong execution scope: %s %#v %s", workspaceexec.Scope(ctx), env, input)
					}
					if failed {
						return "partial stdout", errors.New("exit status 1")
					}
					return `[{"value":2}]`, nil
				})
				path := "/api/builtin/fixture_custom?session=board"
				handler := s.handleBuiltinTool
				if legacy {
					path = "/api/tool/fixture_custom?session=board"
					handler = s.handleToolCall
				}
				r := httptest.NewRequest("POST", path, strings.NewReader(`{"count":2}`))
				w := httptest.NewRecorder()
				handler(w, r)
				if w.Code != 200 {
					t.Fatalf("status=%d: %s", w.Code, w.Body)
				}
				if !failed && legacy {
					if w.Body.String() != `[{"value":2}]` {
						t.Fatalf("legacy JSON changed: %s", w.Body)
					}
					return
				}
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				key := "result"
				if legacy {
					key = "output"
				}
				if failed {
					if response[key] != "partial stdout" || !strings.Contains(fmt.Sprint(response["error"]), "exit status 1") {
						t.Fatalf("error/stdout lost: %#v", response)
					}
				} else if response[key] != `[{"value":2}]` {
					t.Fatalf("builtin contract changed: %#v", response)
				}
			})
		}
	}
}

func TestCustomHTTPSecretsFollowCallerWithoutAnAIConfiguration(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	group, err := ms.CreateGroup(ctx, "custom-tools")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ms.CreateGroup(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	u, err := ms.CreateUser(ctx, "custom@fixture.test", "unused", "Member", memory.RoleMember, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.AddGroupMember(ctx, group.ID, u.ID, memory.GroupRoleMember); err != nil {
		t.Fatal(err)
	}
	personal := ms.ConfigScope(fmt.Sprintf("u%d", u.ID))
	gs := ms.ConfigScope(fmt.Sprintf("g%d", group.ID))
	for _, item := range []struct {
		store       *memory.Store
		name, value string
	}{
		{gs, "SHARED_KEY", "group-value"}, {gs, "GROUP_ONLY", "own-group"},
		{personal, "SHARED_KEY", "personal-value"},
		{ms.ConfigScope(fmt.Sprintf("g%d", other.ID)), "OTHER_ONLY", "other-group"},
		{ms, "DEPLOY_ONLY", "deployment"},
	} {
		if err := item.store.SetScriptSecret(ctx, item.name, item.value); err != nil {
			t.Fatal(err)
		}
	}
	// A broken model profile must not disable a deterministic custom tool.
	if err := ms.SetSecret(ctx, aiProfileSecret, `{"provider":"invalid"}`); err != nil {
		t.Fatal(err)
	}
	if err := personal.SetSecret(ctx, "oauth_google_token", "reserved"); err != nil {
		t.Fatal(err)
	}
	s := customExecutionServer(t)
	s.cfg.MultiUser = true
	s.memStore = ms
	calls := 0
	s.docker = docker.WithExecution(func(ctx context.Context, _ string, _ []byte, env map[string]string) (string, error) {
		calls++
		if env["SHARED_KEY"] != "personal-value" || env["GROUP_ONLY"] != "own-group" {
			t.Fatalf("wrong usable secrets: %#v", env)
		}
		// Unprefixed script secrets are the existing deployment-shared tier.
		if env["DEPLOY_ONLY"] != "deployment" {
			t.Fatal("legacy shared tier changed")
		}
		for _, name := range []string{"OTHER_ONLY", "OAUTH_GOOGLE_TOKEN", "PRISM_AI_PROFILE"} {
			if _, ok := env[name]; ok {
				t.Fatalf("secret outside caller scope exposed: %s", name)
			}
		}
		if want := fmt.Sprintf("u%d-board", u.ID); workspaceexec.Scope(ctx) != want {
			t.Fatalf("execution scope=%s want %s", workspaceexec.Scope(ctx), want)
		}
		return `{}`, nil
	})
	for _, legacy := range []bool{true, false} {
		path := "/api/builtin/fixture_custom?session=board"
		handler := s.handleBuiltinTool
		if legacy {
			path = "/api/tool/fixture_custom?session=board"
			handler = s.handleToolCall
		}
		r := withUser(httptest.NewRequest("POST", path, strings.NewReader(`{}`)), u)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("deterministic tool needs model: %d %s", w.Code, w.Body)
		}
	}
	if calls != 2 {
		t.Fatalf("executions=%d", calls)
	}
	if err := ms.SetToolPolicy(ctx, "fixture_custom", "disabled"); err != nil {
		t.Fatal(err)
	}
	r := withUser(httptest.NewRequest("POST", "/api/tool/fixture_custom?session=board", strings.NewReader(`{}`)), u)
	w := httptest.NewRecorder()
	s.handleToolCall(w, r)
	if w.Code != 403 || calls != 2 {
		t.Fatalf("disabled custom tool executed: %d calls=%d", w.Code, calls)
	}
}

func TestSharedCustomHTTPExecutionUsesGroupSecretsOnly(t *testing.T) {
	ms := securityStore(t)
	ctx := t.Context()
	group, err := ms.CreateGroup(ctx, "shared-custom")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ms.CreateUser(ctx, "shared-admin@fixture.test", "unused", "Admin", memory.RoleGlobalAdmin, memory.StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.ConfigScope(fmt.Sprintf("u%d", admin.ID)).SetScriptSecret(ctx, "ADMIN_ONLY", "private"); err != nil {
		t.Fatal(err)
	}
	if err := ms.ConfigScope(fmt.Sprintf("g%d", group.ID)).SetScriptSecret(ctx, "GROUP_KEY", "shared"); err != nil {
		t.Fatal(err)
	}
	s := customExecutionServer(t)
	s.cfg.MultiUser = true
	s.memStore = ms
	session := fmt.Sprintf("room-g%d", group.ID)
	calls := 0
	s.docker = docker.WithExecution(func(ctx context.Context, _ string, _ []byte, env map[string]string) (string, error) {
		calls++
		if env["GROUP_KEY"] != "shared" || env["ADMIN_ONLY"] != "" || workspaceexec.Scope(ctx) != session {
			t.Fatalf("shared principal inherited wrong context: %v", env)
		}
		return `{}`, nil
	})
	for _, legacy := range []bool{true, false} {
		path := "/api/builtin/fixture_custom?session=" + session
		handler := s.handleBuiltinTool
		if legacy {
			path = "/api/tool/fixture_custom?session=" + session
			handler = s.handleToolCall
		}
		r := withUser(httptest.NewRequest("POST", path, strings.NewReader(`{}`)), &memory.User{ID: -group.ID, Role: memory.RoleMember})
		r = r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, session))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("shared execution: %d %s", w.Code, w.Body)
		}
	}
	if calls != 2 {
		t.Fatalf("executions=%d", calls)
	}
}
