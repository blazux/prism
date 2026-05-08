package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Manager struct {
	containerName string
	workspaceDir  string
}

func NewManager(containerName, workspaceDir string) *Manager {
	return &Manager{
		containerName: containerName,
		workspaceDir:  workspaceDir,
	}
}

func (m *Manager) EnsureRunning(ctx context.Context) error {
	// Check if container exists
	out, err := m.run(ctx, "docker", "inspect", "--format", "{{.State.Status}}", m.containerName)
	if err != nil {
		// Container doesn't exist, create it
		return m.create(ctx)
	}

	status := strings.TrimSpace(out)
	if status == "running" {
		return nil
	}

	// Container exists but not running, start it
	_, err = m.run(ctx, "docker", "start", m.containerName)
	return err
}

func (m *Manager) create(ctx context.Context) error {
	// Pull image first (non-blocking log)
	fmt.Printf("[docker] pulling ubuntu:22.04...\n")

	pullCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := m.run(pullCtx, "docker", "pull", "ubuntu:22.04"); err != nil {
		return fmt.Errorf("pull ubuntu: %w", err)
	}

	// Create and start the container with workspace volume
	args := []string{
		"run", "-d",
		"--name", m.containerName,
		"-v", m.workspaceDir + ":/workspace",
		"--network", "bridge",
		"ubuntu:22.04",
		"tail", "-f", "/dev/null",
	}
	if _, err := m.run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	// Install base tools
	fmt.Printf("[docker] installing base tools in workspace container...\n")
	setupCmd := `apt-get update -qq && apt-get install -y -qq \
		python3 python3-pip python3-venv \
		nodejs npm \
		curl wget git vim \
		build-essential \
		jq \
		2>&1 | tail -5`
	if _, err := m.Exec(ctx, setupCmd, 5*time.Minute); err != nil {
		fmt.Printf("[docker] warning: base tool install failed: %v\n", err)
	}

	fmt.Printf("[docker] workspace container ready\n")
	return nil
}

func (m *Manager) Exec(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	out, err := m.run(ctx, "docker", "exec", m.containerName, "bash", "-c", command)
	return out, err
}

// ExecWithEnv runs a command in the container with extra environment variables
// passed via docker exec -e flags (values never appear in the bash command string).
func (m *Manager) ExecWithEnv(ctx context.Context, command string, timeout time.Duration, env map[string]string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"exec"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, m.containerName, "bash", "-c", command)
	return m.run(ctx, "docker", args...)
}

func (m *Manager) ExecStream(ctx context.Context, command string) (<-chan string, <-chan error) {
	outCh := make(chan string, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(outCh)
		defer close(errCh)

		cmd := exec.CommandContext(ctx, "docker", "exec", m.containerName, "bash", "-c", command)
		cmd.Stdout = &chanWriter{ch: outCh}
		cmd.Stderr = &chanWriter{ch: outCh}

		if err := cmd.Run(); err != nil {
			errCh <- err
		}
	}()

	return outCh, errCh
}

func (m *Manager) IsDockerAvailable() bool {
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	return cmd.Run() == nil
}

func (m *Manager) Status(ctx context.Context) string {
	out, err := m.run(ctx, "docker", "inspect", "--format", "{{.State.Status}}", m.containerName)
	if err != nil {
		return "not found"
	}
	return strings.TrimSpace(out)
}

func (m *Manager) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type chanWriter struct {
	ch  chan<- string
	buf []byte
}

func (w *chanWriter) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx+1])
		w.buf = w.buf[idx+1:]
		select {
		case w.ch <- line:
		default:
		}
	}
	return len(p), nil
}
