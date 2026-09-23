package docker

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// WorkspaceExecution is an already-authorized command capability. It cannot
// create or select an outer container. The embedding host owns admission,
// cancellation and the transport to its workspace manager.
type WorkspaceExecution func(context.Context, string, []byte, map[string]string) (string, error)

// WithExecution constructs an execution-only manager. There is deliberately no
// container name, local Docker backend, or fallback when the capability fails.
func WithExecution(execute WorkspaceExecution) *Manager {
	return &Manager{execute: execute, executionOnly: true}
}

// WorkspaceStatus reports the execution resource, independently of whether
// this process is allowed to administer its outer Docker container.
func (m *Manager) WorkspaceStatus(ctx context.Context) string {
	if m.executionOnly {
		if m.execute == nil {
			return "unavailable"
		}
		// Admission and revocation belong to the embedding host. Do not execute
		// a user command (or claim an outer container state) just to draw a badge.
		return "available"
	}
	if !m.IsDockerAvailable() {
		return "unavailable"
	}
	return m.Status(ctx)
}

func (m *Manager) SupportsTerminal() bool { return !m.executionOnly }

func (m *Manager) executeBound(ctx context.Context, command string, input []byte, env map[string]string, timeout time.Duration) (string, error) {
	if m.execute == nil {
		return "", errors.New("workspace execution unavailable")
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return m.execute(ctx, command, input, env)
}

// WithWorkspaceServices enables only the Docker daemon inside the already-bound
// execution capability. It never grants access to the outer container runtime.
func (m *Manager) WithWorkspaceServices(root string) *Manager {
	m.backend = Backend{Mode: "workspace"}
	m.workspaceDir = root
	m.portRangeStart, m.portRangeEnd = 20000, 20999
	return m
}
func (m *Manager) ConfinedExecution() bool { return m.executionOnly }

// WithServiceURL supplies browser URLs from the embedding application's router.
func (m *Manager) WithServiceURL(resolve func(int) string) *Manager { m.serviceURL = resolve; return m }
func (m *Manager) ServiceURL(port int) string {
	if m.serviceURL != nil {
		return m.serviceURL(port)
	}
	return fmt.Sprintf("/proxy/%d/", port)
}
