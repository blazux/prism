package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/docker"
)

func TestRegisterValidatesWholeHeaderBeforeReplacingScript(t *testing.T) {
	e, root := newTestExecutorWithTools(t)
	original := "# TOOL: {\"name\":\"example\",\"description\":\"original\"}\nprint('original')\n"
	if _, err := e.registerTool(original); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"# TOOL: {\"name\":\"example\",\"parameters\":\"object\"}\nprint('replacement')\n",
		"# TOOL: {\"name\":\"example\",\"parameters\":{\"type\":\"array\"}}\n",
		"# TOOL: {\"name\":\"example\",\"description\":[]}\n",
		strings.Repeat("# line\n", 30) + original,
	} {
		if _, err := e.registerTool(invalid); err == nil {
			t.Fatal("invalid replacement succeeded")
		}
		got, err := os.ReadFile(filepath.Join(root, "agent_tools", "example.py"))
		if err != nil || string(got) != original {
			t.Fatalf("previous script changed: %s %v", got, err)
		}
		if loaded := e.customMgr.Get("example"); loaded == nil || loaded.Description != "original" {
			t.Fatalf("previous registration changed: %#v", loaded)
		}
	}
	if got := e.customMgr.Get("example"); got == nil {
		t.Fatal("successful registration is not discoverable")
	}
}

func TestCustomExecutionPreservesOutputAndPermissionChecks(t *testing.T) {
	e, _ := newTestExecutorWithTools(t)
	if _, err := e.registerTool("# TOOL: {\"name\":\"example\"}\n"); err != nil {
		t.Fatal(err)
	}
	e.SetRawResults(true)
	e.SetSessionID("board")
	calls := 0
	e.docker = docker.WithExecution(func(_ context.Context, cmd string, input []byte, env map[string]string) (string, error) {
		calls++
		if !strings.HasPrefix(cmd, "cd /workspace && python3 ") || env["PRISM_SESSION"] != "board" || string(input) != `{}` {
			t.Fatalf("wrong context: %s %v %s", cmd, env, input)
		}
		return "partial output", errors.New("exit status 1")
	})
	got, err := e.ExecuteCustomTool(context.Background(), "example", json.RawMessage(`{}`))
	if got != "partial output" || err == nil {
		t.Fatalf("failure lost stdout: %q %v", got, err)
	}
	e.SetToolGuard(func(string, map[string]interface{}) error { return errors.New("denied") })
	if _, err := e.ExecuteCustomTool(context.Background(), "example", json.RawMessage(`{}`)); err == nil || calls != 1 {
		t.Fatalf("guard bypassed: %v calls=%d", err, calls)
	}
}
