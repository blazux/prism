package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in: run only against an operator-created disposable production-image
// workspace. The wrapper binds every command to that sandbox, not user data.
func TestProductionWorkspaceServices(t *testing.T) {
	wrapper := os.Getenv("PRISM_TEST_EXEC_WRAPPER")
	if wrapper == "" {
		t.Skip("disposable production-image workspace required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	m := WithExecution(func(ctx context.Context, command string, input []byte, env map[string]string) (string, error) {
		cmd := exec.CommandContext(ctx, wrapper, command)
		cmd.Stdin = bytes.NewReader(input)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}).WithWorkspaceServices(root)
	ports, err := m.RunService(ctx, "audit-fixture", "busybox:1.37", []int{8088}, "mkdir -p /www; echo fixture >/www/index.html; httpd -f -p 8088 -h /www", nil, nil, false, "disposable test")
	if err != nil {
		t.Fatal(err)
	}
	defer m.StopService(context.Background(), "audit-fixture")
	var out string
	for i := 0; i < 20; i++ {
		out, err = m.Exec(ctx, fmt.Sprintf("python3 -c 'import urllib.request; print(urllib.request.urlopen(\"http://127.0.0.1:%d\", timeout=2).read().decode())'", ports[0]), 5*time.Second)
		if err == nil && strings.Contains(out, "fixture") {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || !strings.Contains(out, "fixture") {
		t.Fatal(out, err)
	}
	if out, err = m.ExecService(ctx, "audit-fixture", "wget -q -T 10 -O - http://example.com", 15*time.Second); err != nil || !strings.Contains(out, "Example Domain") {
		t.Fatalf("nested egress: %s %v", out, err)
	}
	if _, err = m.ExecService(ctx, "audit-fixture", "echo 'quoted value'", time.Second*5); err != nil {
		t.Fatal(err)
	}
	// Compose stdin must contain the validated host snapshot, never an outer path.
	file := filepath.Join(root, "compose.yml")
	compose := "services:\n  fixture:\n    image: busybox:1.37\n    command: sleep 120\n  web:\n    image: busybox:1.37\n    command: sh -c 'mkdir -p /www; echo compose-network >/www/index.html; httpd -f -p 8080 -h /www'\n"
	if err = os.WriteFile(file, []byte(compose), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err = m.composeRun(ctx, "compose", "-p", "audit-compose", "-f", file, "--project-directory", root, "up", "-d"); err != nil {
		t.Fatal(out, err)
	}
	defer m.composeRun(context.Background(), "compose", "-p", "audit-compose", "-f", file, "--project-directory", root, "down")
	if out, err = m.composeRun(ctx, "compose", "-p", "audit-compose", "-f", file, "--project-directory", root,
		"exec", "-T", "fixture", "wget", "-q", "-T", "10", "-O", "-", "http://web:8080"); err != nil || !strings.Contains(out, "compose-network") {
		t.Fatalf("Compose service DNS/network: %s %v", out, err)
	}
	if _, err = m.serviceRun(ctx, "ps"); err != nil {
		t.Fatal(err)
	}
}
