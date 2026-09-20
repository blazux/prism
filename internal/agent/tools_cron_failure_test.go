package agent

import (
	"context"
	"os"
	"path/filepath"
	"prism/internal/docker"
	"strings"
	"testing"
)

func TestCronAddAndListPropagateReadFailure(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("CRON_TEST_LOG", log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf 'called\\n' >> \"$CRON_TEST_LOG\"\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e := &ToolExecutor{docker: docker.NewManager("test", dir, 0, 0)}
	if _, err := e.cronAdd(context.Background(), "new", "@daily", "echo new", ""); err == nil {
		t.Fatal("read failure accepted")
	}
	if _, err := e.cronList(context.Background()); err == nil {
		t.Fatal("read failure reported as empty list")
	}
	calls, _ := os.ReadFile(log)
	if strings.Count(string(calls), "called") != 2 {
		t.Fatal("unexpected write attempt", string(calls))
	}
}
