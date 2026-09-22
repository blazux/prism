package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Compose runs with the server's daemon authority. Accept a deliberately bounded
// document rather than silently ignoring fields which can import host resources.
func validateComposeDocument(data []byte, base string) error {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("invalid compose YAML")
	}
	allowedTop := map[string]bool{"version": true, "name": true, "services": true, "volumes": true, "networks": true}
	for key := range doc {
		if !allowedTop[key] {
			return fmt.Errorf("unsupported compose field %q; use explicit services and ordinary named volumes", key)
		}
	}
	services, ok := doc["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return fmt.Errorf("compose services are required")
	}
	allowedService := strings.Fields("image command entrypoint ports expose environment labels restart networks depends_on hostname user working_dir healthcheck volumes init read_only tmpfs stop_grace_period stop_signal mem_limit mem_reservation cpus pids_limit ulimits logging profiles privileged pid ipc network_mode userns_mode cap_add devices security_opt")
	known := map[string]bool{}
	for _, k := range allowedService {
		known[k] = true
	}
	for name, raw := range services {
		svc, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid service %q", name)
		}
		for key := range svc {
			if !known[key] {
				return fmt.Errorf("service %q uses unsupported field %q; use a published image and ordinary service settings", name, key)
			}
		}
		// These values must not acquire different meanings through interpolation.
		for _, key := range []string{"privileged", "pid", "ipc", "network_mode", "userns_mode", "cap_add", "devices", "security_opt", "volumes", "env_file"} {
			if strings.Contains(fmt.Sprint(svc[key]), "$") {
				return fmt.Errorf("service %q: use literal %s values so Prism can verify them", name, key)
			}
		}
		if mode, _ := svc["network_mode"].(string); mode != "" && mode != "none" && mode != "bridge" {
			return fmt.Errorf("service %q: network_mode must be bridge or none", name)
		}
		for _, key := range []string{"pid", "ipc", "userns_mode"} {
			if mode, _ := svc[key].(string); mode != "" && mode != "private" {
				return fmt.Errorf("service %q: %s namespace sharing is not allowed", name, key)
			}
		}
		if opts, ok := svc["security_opt"].([]any); ok {
			for _, v := range opts {
				if fmt.Sprint(v) != "no-new-privileges:true" && fmt.Sprint(v) != "no-new-privileges" {
					return fmt.Errorf("service %q: security_opt may only enable no-new-privileges", name)
				}
			}
		}
		if err := composePaths(svc["env_file"], base); err != nil {
			return err
		}
		if volumes, ok := svc["volumes"].([]any); ok {
			for _, v := range volumes {
				source := ""
				switch value := v.(type) {
				case string:
					parts := strings.Split(value, ":")
					if len(parts) > 1 {
						source = parts[0]
					}
				case map[string]any:
					typ, _ := value["type"].(string)
					if typ != "volume" && typ != "bind" && typ != "tmpfs" {
						return fmt.Errorf("unsupported mount type")
					}
					source, _ = value["source"].(string)
					for k := range value {
						if k != "type" && k != "source" && k != "target" && k != "read_only" {
							return fmt.Errorf("unsupported mount option %q", k)
						}
					}
				default:
					return fmt.Errorf("invalid volume")
				}
				if strings.Contains(source, "/") || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "~") {
					if err := composePath(source, base); err != nil {
						return err
					}
				}
			}
		}
	}
	if volumes, ok := doc["volumes"].(map[string]any); ok {
		for name, raw := range volumes {
			if raw == nil {
				continue
			}
			opts, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid named volume")
			}
			for key, v := range opts {
				if key != "driver" || v != "local" {
					return fmt.Errorf("volume %q: external volumes, custom names and driver options are not allowed", name)
				}
			}
		}
	}
	if networks, ok := doc["networks"].(map[string]any); ok {
		for name, raw := range networks {
			if name == "host" || name == "none" || strings.Contains(name, "$") {
				return fmt.Errorf("unsafe network name")
			}
			if raw == nil {
				continue
			}
			opts, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid network")
			}
			for key, value := range opts {
				switch key {
				case "external", "internal", "attachable":
					if _, ok := value.(bool); !ok {
						return fmt.Errorf("invalid network flag")
					}
				case "name":
					v, ok := value.(string)
					if !ok || v == "host" || v == "none" || strings.Contains(v, "$") {
						return fmt.Errorf("unsafe network name")
					}
				case "driver":
					if value != "bridge" {
						return fmt.Errorf("network %q must use bridge", name)
					}
				default:
					return fmt.Errorf("unsupported network option %q", key)
				}
			}
		}
	}
	return nil
}
func composePath(path, base string) error {
	if path == "" || isHostPath(path) {
		return fmt.Errorf("path %q escapes the compose directory", path)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	// Open verifies symlinks. Missing paths are allowed only with a verified parent.
	for {
		f, err := root.Open(path)
		if err == nil {
			f.Close()
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("path %q must stay inside the compose directory", path)
		}
		next := filepath.Dir(path)
		if next == path {
			return err
		}
		path = next
	}
}
func composePaths(raw any, base string) error {
	if raw == nil {
		return nil
	}
	if path, ok := raw.(string); ok {
		return composePath(path, base)
	}
	if paths, ok := raw.([]any); ok {
		for _, v := range paths {
			path, ok := v.(string)
			if !ok {
				return fmt.Errorf("env_file must contain local paths")
			}
			if err := composePath(path, base); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("invalid env_file")
}
