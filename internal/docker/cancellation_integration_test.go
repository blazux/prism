package docker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"prism/workspaceexec"
)

// Opt in explicitly: use an existing workspace, unique /tmp files only, never
// change its container configuration, crontab or persisted user resources.
func TestDockerForegroundCancellation(t *testing.T) {
	container := os.Getenv("PRISM_EXEC_TEST_CONTAINER")
	if container == "" {
		t.Skip("explicit Docker workspace fixture required")
	}
	m := NewManager(container, "", 0, 0)
	root := "/tmp/prism-exec-test-" + workspaceexec.ID()
	defer exec.Command("docker", "exec", container, "rm", "-rf", root).Run()
	ctx, cancel := context.WithCancel(workspaceexec.WithScope(context.Background(), "test-board"))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.Exec(ctx, "mkdir -p '"+root+"'; trap '' TERM; (sleep 3; echo late > '"+root+"/late') & echo \"$$ $!\" > '"+root+"/ready'; wait", 0)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if exec.Command("docker", "exec", container, "test", "-f", root+"/ready").Run() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	ready, err := exec.Command("docker", "exec", container, "cat", root+"/ready").Output()
	if err != nil {
		t.Fatal(err)
	}
	groupID := strings.Fields(string(ready))[0]
	pids, err := exec.Command("docker", "exec", container, "python3", "-c", `import glob,json,sys
result=[]
for p in glob.glob('/proc/[0-9]*/stat'):
 try:
  if open(p).read().rsplit(')',1)[1].split()[2]==sys.argv[1]:result.append(p.split('/')[2])
 except FileNotFoundError:pass
print(json.dumps(result))`, groupID).Output()
	if err != nil {
		t.Fatal(err)
	}
	var members []string
	if err := json.Unmarshal(pids, &members); err != nil || len(members) < 2 {
		t.Fatal("subprocess fixture missing", string(pids), err)
	}
	other := make(chan string, 1)
	go func() {
		out, err := m.Exec(context.Background(), "sleep 1.5; echo unaffected", 5*time.Second)
		if err != nil {
			out = err.Error()
		}
		other <- out
	}()
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || errors.Is(err, workspaceexec.ErrUnconfirmed) {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Docker command survived cancellation")
	}
	if time.Since(started) > 2500*time.Millisecond {
		t.Fatal("stop too slow")
	}
	if out := <-other; out != "unaffected\n" {
		t.Fatal("another command affected:", out)
	}
	args := append([]string{"exec", container, "python3", "-c", `import os,sys;sys.exit(any(os.path.exists('/proc/'+p) for p in sys.argv[1:]))`}, members...)
	if err := exec.Command("docker", args...).Run(); err != nil {
		t.Fatal("cancelled command left a live process or zombie", members)
	}
	if err := m.CheckExecutions(context.Background(), "test-board"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond)
	if exec.Command("docker", "exec", container, "test", "-e", root+"/late").Run() == nil {
		t.Fatal("late write after stop")
	}
	out, err := m.ExecWithStdin(context.Background(), "cat; printf '%s' \"$PRISM_TEST_LITERAL\"", []byte("input\x00\n"), 5*time.Second, map[string]string{"PRISM_TEST_LITERAL": "literal '$"})
	if err != nil || out != "input\x00\nliteral '$" {
		t.Fatalf("IO/env: %q %v", out, err)
	}
	stream, errs := m.ExecStream(context.Background(), "printf partial")
	var chunks []string
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	if err := <-errs; err != nil || strings.Join(chunks, "") != "partial" {
		t.Fatal("stream partial output lost", chunks, err)
	}
}
