package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const servicePrefix = "prism-svc-"

// ServiceInfo describes a running or stopped service container.
type ServiceInfo struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
	Port   int    `json:"port"`
}

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

// ExecWithStdin runs a command in the container with stdin attached, bypassing
// shell argument-length limits for large payloads (images, documents, etc.).
func (m *Manager) ExecWithStdin(ctx context.Context, command string, stdin []byte, timeout time.Duration, env map[string]string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args := []string{"exec", "-i"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, m.containerName, "bash", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
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

// RunService starts a named service container on the same Docker network as the
// workspace. The container is named prism-svc-<name> and restarts automatically.
func (m *Manager) RunService(ctx context.Context, name, image string, port int, env map[string]string, volumes []string, gpu bool) error {
	// Detect network from the workspace container so services land on the same network.
	netOut, _ := m.run(ctx, "docker", "inspect",
		"--format", "{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}",
		m.containerName)
	network := strings.Fields(strings.TrimSpace(netOut))
	net := "prism_default"
	if len(network) > 0 {
		net = network[0]
	}

	// Remove existing container with the same name (idempotent).
	_, _ = m.run(ctx, "docker", "rm", "-f", servicePrefix+name)

	args := []string{
		"run", "-d",
		"--name", servicePrefix + name,
		"--network", net,
		"--restart", "unless-stopped",
		// Share the workspace volume so /workspace is available inside the service container.
		"--volumes-from", m.containerName,
	}
	if gpu {
		args = append(args, "--gpus", "all")
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	// volumes are ignored: workspace is already mounted via --volumes-from.
	_ = volumes
	args = append(args, image)

	if _, err := m.run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	return nil
}

// StopService stops and removes a service container.
func (m *Manager) StopService(ctx context.Context, name string) error {
	_, err := m.run(ctx, "docker", "rm", "-f", servicePrefix+name)
	return err
}

// ListServices returns all prism-svc-* containers.
func (m *Manager) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	out, err := m.run(ctx, "docker", "ps", "-a",
		"--filter", "name="+servicePrefix,
		"--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}")
	if err != nil {
		return nil, err
	}
	var services []ServiceInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		svc := ServiceInfo{
			Name:   strings.TrimPrefix(parts[0], servicePrefix),
			Image:  parts[1],
			Status: parts[2],
		}
		if len(parts) == 4 {
			svc.Port = parseFirstPort(parts[3])
		}
		services = append(services, svc)
	}
	return services, nil
}

// ListAllContainers returns all prism-* containers (docker ps -a filtered by name).
func (m *Manager) ListAllContainers(ctx context.Context) ([]ServiceInfo, error) {
	out, err := m.run(ctx, "docker", "ps", "-a",
		"--filter", "name=prism",
		"--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}")
	if err != nil {
		return nil, err
	}
	var containers []ServiceInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		c := ServiceInfo{
			Name:   parts[0],
			Image:  parts[1],
			Status: parts[2],
		}
		if len(parts) == 4 {
			c.Port = parseFirstPort(parts[3])
		}
		containers = append(containers, c)
	}
	return containers, nil
}

// ServiceLogs returns the last n log lines from a service container.
func (m *Manager) ServiceLogs(ctx context.Context, name string, tail int) (string, error) {
	return m.run(ctx, "docker", "logs", "--tail", strconv.Itoa(tail), servicePrefix+name)
}

// parseFirstPort extracts the first host port from a docker ps --format {{.Ports}} string.
// e.g. "0.0.0.0:8188->8188/tcp" → 8188
func parseFirstPort(ports string) int {
	for _, mapping := range strings.Fields(ports) {
		if idx := strings.Index(mapping, "->"); idx >= 0 {
			hostPart := mapping[:idx]
			if colon := strings.LastIndex(hostPart, ":"); colon >= 0 {
				if p, err := strconv.Atoi(hostPart[colon+1:]); err == nil {
					return p
				}
			}
		}
	}
	return 0
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
