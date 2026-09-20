package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"prism/internal/customtools"
	"prism/internal/docker"
	"strings"
	"testing"
)

func TestAddWidgetPreservesLockedContent(t *testing.T) {
	e, _, dir := newTestExecutorWithPlugins(t)
	for _, id := range []string{"locked", "derived_title"} {
		writeWidget(t, dir, id, "original")
		meta := []byte(`{"title":"Keep","locked":true,"x":0,"w":480}`)
		if err := os.WriteFile(filepath.Join(dir, id+".meta.json"), meta, 0644); err != nil {
			t.Fatal(err)
		}
		supplied := id
		title := "Locked"
		if id == "derived_title" {
			supplied = ""
			title = "Derived title"
		}
		if _, _, err := e.addUIPlugin(context.Background(), supplied, title, "replacement", 1, 200); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatal(err)
		}
		html, _ := os.ReadFile(filepath.Join(dir, id+".html"))
		after, _ := os.ReadFile(filepath.Join(dir, id+".meta.json"))
		if string(html) != "original" || string(after) != string(meta) {
			t.Fatal("locked widget was modified")
		}
	}
}
func TestCustomToolExecutionStatus(t *testing.T) {
	dir := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := &ToolExecutor{workspaceDir: dir, docker: docker.NewManager("unused", dir, 0, 0)}
	file := filepath.Join(bin, "docker")
	if err := os.WriteFile(file, []byte("#!/bin/sh\necho 'synthetic Python failure' >&2\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := e.execCustomTool(context.Background(), &customtools.Tool{Name: "test_tool", Filename: "test.py"}, json.RawMessage(`{}`))
	var exit *exec.ExitError
	if err == nil || !errors.As(err, &exit) || !strings.Contains(err.Error(), "synthetic Python failure") {
		t.Fatalf("execution error lost: %v", err)
	}
	if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf '{\"ok\":true}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := e.execCustomTool(context.Background(), &customtools.Tool{Name: "test_tool", Filename: "test.py"}, json.RawMessage(`{}`))
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("successful result changed: %q %v", out, err)
	}
}
