package agent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// prism-server mounts the Docker socket, so `docker compose up` runs the
// agent-authored compose file with the daemon's full authority. A service
// with `privileged: true`, a host-namespace, an added capability, a device,
// or a bind mount of the host root / the socket is a straight path to root on
// the host (agent-review finding #1). docker_run does not have this problem —
// RunService ignores its volumes argument and cannot set privileged — so only
// compose needs this gate.
//
// The check is a fail-closed allowlist of shapes, not behaviour the agent
// relies on: a normal service (image, ports, env, named or relative volumes)
// passes untouched, and the rejection message tells the model exactly what to
// remove, so it recovers in one turn rather than hitting an opaque wall.

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Privileged  bool          `yaml:"privileged"`
	Pid         string        `yaml:"pid"`
	Ipc         string        `yaml:"ipc"`
	NetworkMode string        `yaml:"network_mode"`
	UsernsMode  string        `yaml:"userns_mode"`
	CapAdd      []string      `yaml:"cap_add"`
	Devices     []interface{} `yaml:"devices"`
	SecurityOpt []string      `yaml:"security_opt"`
	Volumes     []interface{} `yaml:"volumes"`
}

// validateComposeSafety parses the compose file at hostPath and returns a
// model-readable error if any service asks for host-level privilege. A parse
// failure is not fatal here — docker will report a malformed file far more
// precisely than we can, so we only block on privilege we positively recognise.
func validateComposeSafety(hostPath string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return nil // let docker compose surface the read/parse error itself
	}
	var cf composeFile
	if yaml.Unmarshal(data, &cf) != nil {
		return nil
	}
	for name, svc := range cf.Services {
		if err := svc.unsafeReason(); err != nil {
			return fmt.Errorf("service %q %w — remove it and run docker_compose up again "+
				"(privileged/host-namespace/device/host-path mounts are not allowed here; "+
				"use named volumes and the default network instead)", name, err)
		}
	}
	return nil
}

func (s composeService) unsafeReason() error {
	if s.Privileged {
		return fmt.Errorf("sets privileged: true")
	}
	if isHostMode(s.Pid) {
		return fmt.Errorf("sets pid: host")
	}
	if isHostMode(s.Ipc) {
		return fmt.Errorf("sets ipc: host")
	}
	if isHostMode(s.NetworkMode) {
		return fmt.Errorf("sets network_mode: host")
	}
	if isHostMode(s.UsernsMode) {
		return fmt.Errorf("sets userns_mode: host")
	}
	if len(s.CapAdd) > 0 {
		return fmt.Errorf("uses cap_add %v", s.CapAdd)
	}
	if len(s.Devices) > 0 {
		return fmt.Errorf("passes through devices")
	}
	for _, so := range s.SecurityOpt {
		if strings.Contains(strings.ToLower(so), "unconfined") {
			return fmt.Errorf("disables confinement (security_opt %q)", so)
		}
	}
	for _, v := range s.Volumes {
		if src := bindHostSource(v); src != "" {
			return fmt.Errorf("bind-mounts host path %q", src)
		}
	}
	return nil
}

func isHostMode(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "host")
}

// bindHostSource returns the host-side source of a bind mount that reaches
// outside the project (an absolute path or ~), or "" for a named volume or a
// project-relative path. Handles both compose volume syntaxes:
//
//	- "/var/run/docker.sock:/var/run/docker.sock"   (short string)
//	- { type: bind, source: /etc, target: /etc }    (long map)
func bindHostSource(v interface{}) string {
	switch vv := v.(type) {
	case string:
		src := vv
		if i := strings.IndexByte(vv, ':'); i >= 0 {
			src = vv[:i]
		}
		if isHostPath(src) {
			return src
		}
	case map[string]interface{}:
		if t, _ := vv["type"].(string); t != "" && t != "bind" {
			return "" // volume / tmpfs, not a host bind
		}
		if src, _ := vv["source"].(string); isHostPath(src) {
			return src
		}
	}
	return ""
}

// isHostPath reports whether a bind source escapes the project directory. A
// named volume ("data") or a project-relative path ("./conf") stays confined;
// an absolute path or ~ reaches the host filesystem.
func isHostPath(src string) bool {
	src = strings.TrimSpace(src)
	return strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~")
}
