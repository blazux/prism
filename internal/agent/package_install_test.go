package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"prism/internal/docker"
	"strings"
	"testing"
)

func TestFailedPipDoesNotPersistOrReportSuccess(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pip3"), []byte("#!/bin/sh\necho installation-failed >&2\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	e := &ToolExecutor{workspaceDir: t.TempDir(), docker: docker.WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		out, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
		return string(out), err
	})}
	out, err := e.pipInstall(context.Background(), "nonexistent")
	if err != nil || !strings.Contains(out, "failed") || strings.Contains(out, "pip installed:") {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(e.workspaceDir, ".pip-packages")); !os.IsNotExist(err) {
		t.Fatal("failed installation persisted")
	}
	out, err = e.aptInstall(context.Background(), "curl")
	if err != nil || !strings.Contains(out, "read-only") {
		t.Fatal(out, err)
	}
}
