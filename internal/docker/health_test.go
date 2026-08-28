package docker

import (
	"strings"
	"testing"
)

func TestParseServiceHealth(t *testing.T) {
	h := parseServiceHealth("true|false|0|0|running\n")
	if !h.Exists || !h.Running || h.Restarting || h.ExitCode != 0 || h.RestartCount != 0 || h.Status != "running" {
		t.Errorf("running parse wrong: %+v", h)
	}
	// The ipam zombie: a one-shot script run as a service, crash-looping.
	h = parseServiceHealth("false|true|1|805|restarting")
	if h.Running || !h.Restarting || h.ExitCode != 1 || h.RestartCount != 805 || h.Status != "restarting" {
		t.Errorf("crash-loop parse wrong: %+v", h)
	}
	if bad := parseServiceHealth("garbage"); bad.Exists {
		t.Errorf("malformed inspect should yield !Exists, got %+v", bad)
	}
}

func TestStartupVerdict(t *testing.T) {
	// Healthy: running, not restarting → no verdict.
	if v := startupVerdict(ServiceHealthInfo{Exists: true, Running: true, Status: "running"}, ""); v != "" {
		t.Errorf("healthy service should have no verdict, got %q", v)
	}
	// Not-yet-inspected / gone → no invented problem.
	if v := startupVerdict(ServiceHealthInfo{}, ""); v != "" {
		t.Errorf("nonexistent should have no verdict, got %q", v)
	}
	// Crash-loop (the ipam case) → verdict names the loop and points to the fix.
	v := startupVerdict(ServiceHealthInfo{Exists: true, Restarting: true, RestartCount: 805, Status: "restarting"}, "FileNotFoundError")
	if !strings.Contains(v, "CRASH-LOOPING") || !strings.Contains(v, "805") || !strings.Contains(v, "FileNotFoundError") {
		t.Errorf("crash-loop verdict missing detail: %q", v)
	}
	// Exited one-shot → verdict steers to exec_command.
	v = startupVerdict(ServiceHealthInfo{Exists: true, Running: false, ExitCode: 0, Status: "exited"}, "done")
	if !strings.Contains(v, "EXITED") || !strings.Contains(v, "exec_command") {
		t.Errorf("exited verdict should steer to exec_command: %q", v)
	}
}
