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

func TestCommitMessage(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "agent turn"},
		{"   ", "agent turn"},
		{"fix the widget", "turn: fix the widget"},
		{"first line\nsecond line", "turn: first line"},
		{"line one\r\nline two", "turn: line one"},
	}
	for _, c := range cases {
		if got := commitMessage(c.in); got != c.want {
			t.Errorf("commitMessage(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Long input is truncated to a bounded, rune-safe subject (no split runes —
	// the model emits multilingual text, so byte-slicing would corrupt it).
	long := strings.Repeat("é", 200) // 200 runes, 400 bytes
	got := commitMessage(long)
	if !strings.HasPrefix(got, "turn: ") || !strings.HasSuffix(got, "…") {
		t.Errorf("long message not prefixed/ellipsized: %q", got)
	}
	if !utf8ValidTrimmed(got) {
		t.Errorf("truncation split a rune: %q", got)
	}
}

func utf8ValidTrimmed(s string) bool {
	for _, r := range s {
		if r == '�' { // replacement char = an invalid byte sequence
			return false
		}
	}
	return true
}

// CommitWorkspace must be a safe no-op (never panic) when there is no workspace
// container to run git in — versioning is a best-effort safety net, so a missing
// or misconfigured docker manager just disables it silently.
func TestCommitWorkspace_NoDockerIsSafeNoop(t *testing.T) {
	var e *ToolExecutor // nil executor
	e.CommitWorkspace(context.Background(), "anything")

	e2 := &ToolExecutor{} // docker == nil
	e2.CommitWorkspace(context.Background(), "anything")
}

func TestWorkspaceKeyUntrackedButPreserved(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return string(out)
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".secret_key"), []byte("fixture only"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".secret_key")
	cachePath := filepath.Join(commandOutputDirectory, commandOutputName("fixture", "go test", "stdout", ""))
	if err := os.MkdirAll(filepath.Join(dir, commandOutputDirectory), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cachePath), []byte("transient recovery output"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-f", cachePath)
	if err := os.WriteFile(filepath.Join(dir, "watched.txt"), []byte("version one"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "watched.txt")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "-q", "-m", "legacy snapshot with transient files")
	legacy := strings.TrimSpace(run("rev-parse", "HEAD"))
	// Fake only the Docker transport; execute real Git in the temporary workspace.
	script := `#!/usr/bin/python3
import base64,json,os,subprocess,sys
payload=json.loads(sys.stdin.buffer.readline())
command=payload['command'].replace('/workspace',os.environ['REVIEW_WORKSPACE'])
result=subprocess.run(['bash','-c',command],input=base64.b64decode(payload.get('input') or ''))
sys.stderr.write('\x1ePRISM_EXEC_FINISHED:'+sys.argv[-1]+'\x1f');sys.stderr.flush()
sys.exit(result.returncode)
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REVIEW_WORKSPACE", dir)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	e := &ToolExecutor{docker: docker.NewManager("unused", dir, 0, 0)}
	if !e.protectWorkspaceKey(context.Background()) || !e.protectWorkspaceKey(context.Background()) {
		t.Fatal("protection failed")
	}
	// Even an explicit inclusion in a hand-written .gitignore cannot override
	// the mandatory exclusion from Prism's own staging command.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("!/.secret_key\n!/.prism-command-output/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "watched.txt"), []byte("version one"), 0644); err != nil {
		t.Fatal(err)
	}
	e.CommitWorkspace(context.Background(), "fixture")
	first := strings.TrimSpace(run("rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(dir, "watched.txt"), []byte("version two"), 0644); err != nil {
		t.Fatal(err)
	}
	e.CommitWorkspace(context.Background(), "fixture update")
	// A restore also commits. A manually staged recovery file must not enter
	// that snapshot even when the workspace has a custom inclusion rule.
	run("add", "-f", cachePath)
	if _, err := e.workspaceRestore(context.Background(), first, "watched.txt"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "watched.txt")); err != nil || string(data) != "version one" {
		t.Fatal("workspace restore failed")
	}
	if _, err := e.workspaceRestore(context.Background(), legacy, "."); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run("ls-files"), ".secret_key") {
		t.Fatal("key still tracked")
	}
	if strings.Contains(run("ls-files"), commandOutputDirectory) {
		t.Fatal("recovery cache entered workspace history")
	}
	if data, err := os.ReadFile(filepath.Join(dir, cachePath)); err != nil || string(data) != "transient recovery output" {
		t.Fatal("live recovery file changed")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".secret_key"))
	if err != nil || string(data) != "fixture only" {
		t.Fatal("live key changed")
	}
}
