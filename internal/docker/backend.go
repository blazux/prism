package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Backend selects where user-created services run. Workspace lifecycle and
// shell execution still use the operator's outer Docker connection.
type Backend struct {
	Mode   string
	Socket string
	User   string
}

func (b Backend) Validate() error {
	if b.Mode != "" && b.Mode != "host" && b.Mode != "workspace" {
		return fmt.Errorf("DOCKER_MODE must be host or workspace")
	}
	if b.Mode == "workspace" && b.Socket != "" && (!strings.HasPrefix(b.Socket, "unix:///") || strings.ContainsAny(b.Socket, "\r\n")) {
		return fmt.Errorf("WORKSPACE_DOCKER_SOCKET must be an absolute unix socket URL inside the workspace")
	}
	if strings.ContainsAny(b.User, "\r\n") {
		return fmt.Errorf("invalid WORKSPACE_DOCKER_USER")
	}
	return nil
}
func (m *Manager) WorkspaceDocker() bool { return m.backend.Mode == "workspace" }
func (m *Manager) serviceCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if err := m.backend.Validate(); err != nil {
		return nil, err
	}
	if !m.WorkspaceDocker() {
		return exec.CommandContext(ctx, "docker", args...), nil
	}
	socket, user := m.backend.Socket, m.backend.User
	if socket == "" {
		socket = "unix:///run/user/1000/docker.sock"
	}
	if user == "" {
		user = "1000"
	}
	// No shell, no inherited inner Docker context or TCP endpoint, no host fallback.
	outer := []string{"exec", "-i", "--user", user, m.containerName,
		"env", "-u", "DOCKER_CONTEXT", "-u", "DOCKER_HOST", "-u", "DOCKER_TLS_VERIFY", "-u", "DOCKER_CERT_PATH",
		"docker", "--host", socket}
	return exec.CommandContext(ctx, "docker", append(outer, args...)...), nil
}
func (m *Manager) serviceRun(ctx context.Context, args ...string) (string, error) {
	return m.serviceInput(ctx, nil, args...)
}
func (m *Manager) serviceInput(ctx context.Context, input []byte, args ...string) (string, error) {
	cmd, err := m.serviceCommand(ctx, args...)
	if err != nil {
		return "", err
	}
	cmd.Stdin = bytes.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("Docker service backend (%s): %w: %s", m.modeName(), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}
func (m *Manager) modeName() string {
	if m.WorkspaceDocker() {
		return "workspace"
	}
	return "host"
}
func (m *Manager) composeRun(ctx context.Context, args ...string) (string, error) {
	if !m.WorkspaceDocker() {
		return m.serviceRun(ctx, args...)
	}
	args = append([]string(nil), args...)
	var data []byte
	for i := 1; i < len(args)-1; i++ {
		switch args[i] {
		case "-f":
			var err error
			data, err = os.ReadFile(args[i+1])
			if err != nil {
				return "", err
			}
			args[i+1] = "-" // Send the validated private snapshot, never reopen in workspace.
			i++
		case "--project-directory":
			rel, err := filepath.Rel(m.workspaceDir, args[i+1])
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				return "", fmt.Errorf("compose directory outside workspace")
			}
			args[i+1] = filepath.Join("/workspace", rel)
			i++
		}
	}
	if data == nil {
		return "", fmt.Errorf("compose snapshot required")
	}
	return m.serviceInput(ctx, data, args...)
}

// Shell commands continue running in the workspace, with its internal daemon
// selected for docker CLI calls made by scripts.
func (m *Manager) execPrefix(interactive bool) []string {
	args := []string{"exec"}
	if interactive {
		args = append(args, "-i")
	}
	if m.WorkspaceDocker() {
		socket := m.backend.Socket
		if socket == "" {
			socket = "unix:///run/user/1000/docker.sock"
		}
		args = append(args, "-e", "DOCKER_HOST="+socket, "-e", "DOCKER_CONTEXT=", "-e", "DOCKER_TLS_VERIFY=")
	}
	return args
}
