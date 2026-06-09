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
	containerName  string
	workspaceDir   string
	portRangeStart int
	portRangeEnd   int
}

func NewManager(containerName, workspaceDir string, portRangeStart, portRangeEnd int) *Manager {
	if portRangeStart <= 0 {
		portRangeStart = 20000
	}
	if portRangeEnd <= portRangeStart {
		portRangeEnd = portRangeStart + 999
	}
	return &Manager{
		containerName:  containerName,
		workspaceDir:   workspaceDir,
		portRangeStart: portRangeStart,
		portRangeEnd:   portRangeEnd,
	}
}

// allocateHostPorts returns n unused host ports from the configured range.
func (m *Manager) allocateHostPorts(ctx context.Context, n int) ([]int, error) {
	out, _ := m.run(ctx, "docker", "ps", "-a",
		"--filter", "name="+servicePrefix,
		"--format", "{{.Ports}}")
	used := make(map[int]bool)
	for _, line := range strings.Split(out, "\n") {
		for _, word := range strings.Fields(line) {
			if p := parseFirstPort(word); p > 0 {
				used[p] = true
			}
		}
	}
	var result []int
	for p := m.portRangeStart; p <= m.portRangeEnd && len(result) < n; p++ {
		if !used[p] {
			result = append(result, p)
		}
	}
	if len(result) < n {
		return nil, fmt.Errorf("not enough free ports in range %d-%d (need %d)", m.portRangeStart, m.portRangeEnd, n)
	}
	return result, nil
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
// workspace. The container is named prism-svc-<name>, restarts automatically,
// and is exposed on an auto-allocated host port from the configured range.
// Returns the allocated host port.
func (m *Manager) RunService(ctx context.Context, name, image string, ports []int, command string, env map[string]string, volumes []string, gpu bool) ([]int, error) {
	if len(ports) == 0 {
		return nil, fmt.Errorf("at least one port is required")
	}
	hostPorts, err := m.allocateHostPorts(ctx, len(ports))
	if err != nil {
		return nil, err
	}

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
		"--volumes-from", m.containerName,
		// Traefik labels: route <name>.localhost → container on its primary port.
		"--label", "traefik.enable=true",
		"--label", fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s.localhost`)", name, name),
		"--label", fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", name, ports[0]),
		"--label", fmt.Sprintf("traefik.http.routers.%s.middlewares=strip-frames@docker", name),
	}
	for i, hp := range hostPorts {
		args = append(args, "-p", fmt.Sprintf("%d:%d", hp, ports[i]))
	}
	if gpu {
		args = append(args, "--gpus", "all")
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	_ = volumes
	if command != "" {
		// Clear the image's default entrypoint so our command runs directly
		// as sh -c "...". Without this, images with ENTRYPOINT ["/bin/bash"]
		// would interpret our args as bash arguments instead of a new command.
		args = append(args, "--entrypoint", "")
	}
	args = append(args, image)
	if command != "" {
		args = append(args, "sh", "-c", command)
	}

	if _, err := m.run(ctx, "docker", args...); err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	return hostPorts, nil
}

// StopService stops and removes a service container.
func (m *Manager) StopService(ctx context.Context, name string) error {
	_, err := m.run(ctx, "docker", "rm", "-f", servicePrefix+name)
	return err
}

// ExecService runs a shell command inside a named service container.
func (m *Manager) ExecService(ctx context.Context, name, command string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return m.run(ctx, "docker", "exec", servicePrefix+name, "sh", "-c", command)
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

// ComposeUp runs `docker compose up -d` for the given compose file (absolute host path).
func (m *Manager) ComposeUp(ctx context.Context, file, project string) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "up", "-d", "--remove-orphans")
	return m.run(ctx, "docker", args...)
}

// ComposeDown runs `docker compose down` for the given compose file.
func (m *Manager) ComposeDown(ctx context.Context, file, project string) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "down")
	return m.run(ctx, "docker", args...)
}

// ComposePS lists services for the given compose file.
func (m *Manager) ComposePS(ctx context.Context, file, project string) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "ps")
	return m.run(ctx, "docker", args...)
}

// ComposeLogs returns logs from the given compose project (optionally filtered to one service).
func (m *Manager) ComposeLogs(ctx context.Context, file, project, service string, tail int) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "logs", "--no-color", "--tail", strconv.Itoa(tail))
	if service != "" {
		args = append(args, service)
	}
	return m.run(ctx, "docker", args...)
}

// ComposeExec runs a shell command inside a compose service container (non-interactive).
func (m *Manager) ComposeExec(ctx context.Context, file, project, service, command string) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "exec", "-T", service, "sh", "-c", command)
	return m.run(ctx, "docker", args...)
}

// ComposeRestart restarts services in the given compose project.
func (m *Manager) ComposeRestart(ctx context.Context, file, project, service string) (string, error) {
	args := []string{"compose", "-f", file}
	if project != "" {
		args = append(args, "--project-name", project)
	}
	args = append(args, "restart")
	if service != "" {
		args = append(args, service)
	}
	return m.run(ctx, "docker", args...)
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
