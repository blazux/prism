package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeDocker(t *testing.T, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' BEGIN \"$@\" END >> \"$DOCKER_TEST_LOG\"\n"
	if fail {
		script += "echo fixture-unavailable >&2\nexit 1\n"
	} else {
		script += "case \"$*\" in *'compose -f -'*) cat > \"$DOCKER_TEST_INPUT\";; esac\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("DOCKER_TEST_LOG", log)
	t.Setenv("DOCKER_TEST_INPUT", filepath.Join(dir, "input"))
	return log
}
func TestServiceBackends(t *testing.T) {
	for _, mode := range []string{"host", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeDocker(t, false)
			root := t.TempDir()
			m := NewManager("outer-fixture", root, 20000, 20999, Backend{Mode: mode})
			ctx := context.Background()
			file := filepath.Join(t.TempDir(), "compose.yml")
			data := []byte("services:\n  web:\n    image: fixture\n")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			calls := []func() error{
				func() error {
					_, e := m.RunService(ctx, "demo", "fixture", []int{80}, "", nil, nil, false, "test")
					return e
				},
				func() error { _, e := m.ListServices(ctx); return e },
				func() error { _, e := m.ListAllContainers(ctx); return e },
				func() error { _, e := m.ServiceLogs(ctx, "demo", 10); return e },
				func() error { _, e := m.ServiceHealth(ctx, "demo"); return e },
				func() error { _, e := m.ExecService(ctx, "demo", "echo test", time.Second); return e },
				func() error { return m.StopService(ctx, "demo") },
				func() error { _, e := m.ComposeUp(ctx, file, "prism-svc-fixture", root); return e },
				func() error { _, e := m.ComposeDown(ctx, file, "prism-svc-fixture", root); return e },
				func() error { _, e := m.ComposePS(ctx, file, "prism-svc-fixture", root); return e },
				func() error { _, e := m.ComposeLogs(ctx, file, "prism-svc-fixture", "web", 10, root); return e },
				func() error { _, e := m.ComposeRestart(ctx, file, "prism-svc-fixture", "web", root); return e },
				func() error {
					_, e := m.ComposeExec(ctx, file, "prism-svc-fixture", "web", "echo test", root)
					return e
				},
			}
			for i, call := range calls {
				if err := call(); err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			b, _ := os.ReadFile(log)
			if mode == "workspace" {
				for _, call := range strings.Split(string(b), "BEGIN\n")[1:] {
					if !strings.HasPrefix(call, "exec\n-i\n--user\n1000\nouter-fixture\nenv\n") {
						t.Fatalf("escaped workspace transport: %s", call)
					}
					if !strings.Contains(call, "docker\n--host\nunix:///run/user/1000/docker.sock\n") {
						t.Fatal("internal endpoint not pinned")
					}
				}
				for _, bad := range []string{"--volumes-from", "traefik.enable", file, root} {
					if strings.Contains(string(b), bad) {
						t.Fatalf("host assumption leaked: %s", bad)
					}
				}
				input, _ := os.ReadFile(os.Getenv("DOCKER_TEST_INPUT"))
				if string(input) != string(data) {
					t.Fatal("compose did not receive exact snapshot")
				}
				if !strings.Contains(string(b), "--project-directory\n/workspace\n") {
					t.Fatal("compose directory not translated")
				}
			} else {
				if strings.Contains(string(b), "--host\nunix:///run/user/") {
					t.Fatal("default mode changed")
				}
				if !strings.Contains(string(b), "--volumes-from\nouter-fixture") {
					t.Fatal("host mount behavior changed")
				}
			}
		})
	}
}
func TestWorkspaceBackendNeverFallsBack(t *testing.T) {
	log := fakeDocker(t, true)
	m := NewManager("outer-fixture", t.TempDir(), 0, 0, Backend{Mode: "workspace"})
	if _, err := m.RunService(context.Background(), "demo", "fixture", []int{80}, "", nil, nil, false, ""); err == nil {
		t.Fatal("daemon failure ignored")
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "BEGIN") != 1 {
		t.Fatal("unexpected retry or mutation after daemon failure")
	}
	if !strings.HasPrefix(string(b), "BEGIN\nexec\n") {
		t.Fatal("used host daemon")
	}
}
func TestBackendValidation(t *testing.T) {
	for _, b := range []Backend{{Mode: "workspce"}, {Mode: "workspace", Socket: "tcp://host:2375"}} {
		if b.Validate() == nil {
			t.Fatal("unsafe/unknown configuration accepted")
		}
	}
	m := NewManager("outer", t.TempDir(), 0, 0, Backend{Mode: "workspace", User: "1234", Socket: "unix:///run/user/1234/docker.sock"})
	cmd, err := m.serviceCommand(context.Background(), "ps")
	if err != nil || !strings.Contains(strings.Join(cmd.Args, " "), "--user 1234") {
		t.Fatal("custom rootless user not applied")
	}
	prefix := strings.Join(m.execPrefix(false), " ")
	if !strings.Contains(prefix, "DOCKER_HOST=unix:///run/user/1234/docker.sock") {
		t.Fatal("scripts miss nested Docker")
	}
}
