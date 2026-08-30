package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ServiceHealthInfo is a snapshot of a service container's state, parsed from
// `docker inspect`.
type ServiceHealthInfo struct {
	Exists       bool
	Running      bool
	Restarting   bool
	ExitCode     int
	RestartCount int
	Status       string // docker's State.Status: running, restarting, exited, created, dead
}

// ServiceHealth inspects a running service container and reports its state.
func (m *Manager) ServiceHealth(ctx context.Context, name string) (ServiceHealthInfo, error) {
	out, err := m.run(ctx, "docker", "inspect", "--format",
		"{{.State.Running}}|{{.State.Restarting}}|{{.State.ExitCode}}|{{.RestartCount}}|{{.State.Status}}",
		servicePrefix+name)
	if err != nil {
		return ServiceHealthInfo{}, err
	}
	return parseServiceHealth(out), nil
}

func parseServiceHealth(out string) ServiceHealthInfo {
	p := strings.Split(strings.TrimSpace(out), "|")
	if len(p) < 5 {
		return ServiceHealthInfo{}
	}
	toi := func(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
	return ServiceHealthInfo{
		Exists:       true,
		Running:      strings.TrimSpace(p[0]) == "true",
		Restarting:   strings.TrimSpace(p[1]) == "true",
		ExitCode:     toi(p[2]),
		RestartCount: toi(p[3]),
		Status:       strings.TrimSpace(p[4]),
	}
}

// startupVerdict returns "" when the container is healthy, or a model-readable
// diagnosis when it exited or is crash-looping. Pure so it can be tested
// without a live daemon.
func startupVerdict(h ServiceHealthInfo, logs string) string {
	if !h.Exists || (h.Running && !h.Restarting) {
		return "" // up and not looping: healthy
	}
	logsBlock := ""
	if strings.TrimSpace(logs) != "" {
		logsBlock = "\nRecent logs:\n" + strings.TrimSpace(logs)
	}
	switch {
	case h.Restarting || h.Status == "restarting" || h.RestartCount > 0:
		return fmt.Sprintf("⚠ The container started but is CRASH-LOOPING (restarted %d time(s)) — it is not a usable service. "+
			"Fix the image or command and run docker_run again, or docker_manage stop to remove it.%s",
			h.RestartCount, logsBlock)
	case !h.Running:
		return fmt.Sprintf("⚠ The container EXITED immediately (exit code %d) — nothing is running. "+
			"If this is a one-shot script rather than a long-lived server, docker_run is the wrong tool (it keeps restarting anything that exits): "+
			"run the script with exec_command instead. Otherwise fix the command, then docker_run again; docker_manage stop removes the stopped container.%s",
			h.ExitCode, logsBlock)
	default:
		return ""
	}
}

// CheckStartup gives a freshly started service a moment to crash, then returns
// a startup verdict for the agent: "" if it is running, or a diagnosis (with
// recent logs) if it exited or is looping. This closes the silent-zombie gap —
// a docker_run whose container dies on start used to still report "started".
func (m *Manager) CheckStartup(ctx context.Context, name string, settle time.Duration) string {
	select {
	case <-ctx.Done():
		return ""
	case <-time.After(settle):
	}
	h, err := m.ServiceHealth(ctx, name)
	if err != nil {
		return "" // inspection failed — don't invent a problem
	}
	if v := startupVerdict(h, ""); v == "" {
		return ""
	}
	logs, _ := m.ServiceLogs(ctx, name, 15)
	return startupVerdict(h, logs)
}
