package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (e *ToolExecutor) dockerRun(ctx context.Context, image, name string, port int, extraPorts []int, command string, env map[string]string, volumes []string, gpu bool, purpose string) (string, error) {
	if image == "" || name == "" || port == 0 {
		return "", fmt.Errorf("image, name and port are required")
	}
	allPorts := append([]int{port}, extraPorts...)
	hostPorts, err := e.docker.RunService(ctx, name, image, allPorts, command, env, volumes, gpu, purpose)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Service %q started.\n\nPort mappings:\n", name)
	for i, hp := range hostPorts {
		fmt.Fprintf(&sb, "  %d → host port %d\n", allPorts[i], hp)
	}
	fmt.Fprintf(&sb, "\nIframe URL (preferred):  http://%s.localhost/", name)
	fmt.Fprintf(&sb, "\nDirect host URL:         http://<hostname>:%d/", hostPorts[0])
	fmt.Fprintf(&sb, "\nInternal URL (scripts):  http://prism-svc-%s:%d/", name, port)
	return sb.String(), nil
}

func (e *ToolExecutor) dockerStop(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if err := e.docker.StopService(ctx, name); err != nil {
		return fmt.Sprintf("ERROR: %v%s", err, e.noSuchContainerHint(ctx, err)), nil
	}
	return fmt.Sprintf("Service %q stopped and removed.", name), nil
}

// noSuchContainerHint appends the actual service names to a "No such container"
// error, so the model corrects a guessed name on the next call instead of
// retrying blind variations.
func (e *ToolExecutor) noSuchContainerHint(ctx context.Context, err error) string {
	if !strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return ""
	}
	services, lerr := e.docker.ListServices(ctx)
	if lerr != nil {
		return ""
	}
	if len(services) == 0 {
		return "\nNo service containers exist."
	}
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return "\nExisting services: " + strings.Join(names, ", ") + " — use one of these names verbatim."
}

func (e *ToolExecutor) dockerPS(ctx context.Context) (string, error) {
	services, err := e.docker.ListServices(ctx)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(services) == 0 {
		return "No service containers running. Use docker_run to start one.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Service containers (%d):\n\n", len(services))
	for _, s := range services {
		portInfo := ""
		if s.Port > 0 {
			portInfo = fmt.Sprintf(" — http://%s.localhost/  (host port %d)", s.Name, s.Port)
		}
		fmt.Fprintf(&sb, "  • %s (%s) — %s%s\n", s.Name, s.Image, s.Status, portInfo)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) dockerList(ctx context.Context) (string, error) {
	containers, err := e.docker.ListAllContainers(ctx)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err), nil
	}
	if len(containers) == 0 {
		return "No containers found.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "All Docker containers (%d):\n\n", len(containers))
	for _, c := range containers {
		portInfo := ""
		if c.Port > 0 {
			portInfo = fmt.Sprintf(" :%d", c.Port)
		}
		fmt.Fprintf(&sb, "  • %-30s  %-35s  %s%s\n", c.Name, c.Image, c.Status, portInfo)
	}
	return sb.String(), nil
}

func (e *ToolExecutor) dockerLogs(ctx context.Context, name string, tail int) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	out, err := e.docker.ServiceLogs(ctx, name, tail)
	if err != nil {
		return fmt.Sprintf("ERROR: %v%s", err, e.noSuchContainerHint(ctx, err)), nil
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Sprintf("No logs for service %q.", name), nil
	}
	return out, nil
}

func (e *ToolExecutor) dockerExecService(ctx context.Context, name, command string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	out, err := e.docker.ExecService(ctx, name, command, 2*time.Minute)
	if err != nil {
		return fmt.Sprintf("ERROR: %v%s", err, e.noSuchContainerHint(ctx, err)), nil
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Sprintf("Command executed in container %q (no output).", name), nil
	}
	return out, nil
}

func (e *ToolExecutor) dockerCompose(ctx context.Context, action, file, project, service, command string, tail int) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file is required")
	}
	hostPath := filepath.Join(e.workspaceDir, filepath.Clean("/"+file))
	rel, err := filepath.Rel(e.workspaceDir, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("file path escapes workspace")
	}

	// Auto-derive project name from the compose file's directory, prefixed with prism-svc-.
	if project == "" {
		dir := filepath.Base(filepath.Dir(rel))
		if dir == "." || dir == "" {
			dir = "app"
		}
		project = "prism-svc-" + dir
	}

	switch action {
	case "up":
		out, err := e.docker.ComposeUp(ctx, hostPath, project)
		if err != nil {
			return fmt.Sprintf("ERROR: %v\n%s", err, out), nil
		}
		if strings.TrimSpace(out) == "" {
			return fmt.Sprintf("All services started (project: %s).", project), nil
		}
		return out, nil
	case "down":
		out, err := e.docker.ComposeDown(ctx, hostPath, project)
		if err != nil {
			return fmt.Sprintf("ERROR: %v\n%s", err, out), nil
		}
		if strings.TrimSpace(out) == "" {
			return "All services stopped and removed.", nil
		}
		return out, nil
	case "ps":
		out, err := e.docker.ComposePS(ctx, hostPath, project)
		if err != nil {
			return fmt.Sprintf("ERROR: %v", err), nil
		}
		if strings.TrimSpace(out) == "" {
			return "No services running.", nil
		}
		return out, nil
	case "logs":
		out, err := e.docker.ComposeLogs(ctx, hostPath, project, service, tail)
		if err != nil {
			return fmt.Sprintf("ERROR: %v", err), nil
		}
		if strings.TrimSpace(out) == "" {
			return "No logs.", nil
		}
		return out, nil
	case "restart":
		out, err := e.docker.ComposeRestart(ctx, hostPath, project, service)
		if err != nil {
			return fmt.Sprintf("ERROR: %v\n%s", err, out), nil
		}
		if strings.TrimSpace(out) == "" {
			return "Services restarted.", nil
		}
		return out, nil
	case "exec":
		if service == "" {
			return "", fmt.Errorf("service is required for exec action")
		}
		if command == "" {
			return "", fmt.Errorf("command is required for exec action")
		}
		out, err := e.docker.ComposeExec(ctx, hostPath, project, service, command)
		if err != nil {
			return fmt.Sprintf("ERROR: %v", err), nil
		}
		if strings.TrimSpace(out) == "" {
			return fmt.Sprintf("Command executed in %s/%s (no output).", project, service), nil
		}
		return out, nil
	default:
		return "", fmt.Errorf("unknown action %q — valid: up, down, ps, logs, restart, exec", action)
	}
}

// ─── Workspace exec ───────────────────────────────────────────────────────────
