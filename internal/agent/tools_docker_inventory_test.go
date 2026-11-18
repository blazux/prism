package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"prism/internal/docker"
)

func TestDockerInventoryReportsItsActualScope(t *testing.T) {
	for _, mode := range []string{"host", "workspace"} {
		for _, action := range []string{"ps", "list"} {
			for _, populated := range []bool{false, true} {
				label := mode + "/" + action + "/empty"
				if populated {
					label = mode + "/" + action + "/populated"
				}
				t.Run(label, func(t *testing.T) {
					dir := t.TempDir()
					log := filepath.Join(dir, "calls")
					script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_INVENTORY_LOG\"\nprintf '%s' \"$DOCKER_INVENTORY_OUTPUT\"\n"
					if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
						t.Fatal(err)
					}
					t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
					t.Setenv("DOCKER_INVENTORY_LOG", log)
					output := ""
					if populated {
						output = "prism-svc-example\texample/image\tExited (0)\t0.0.0.0:23456->80/tcp\n"
					}
					t.Setenv("DOCKER_INVENTORY_OUTPUT", output)
					e := &ToolExecutor{docker: docker.NewManager("fixture-workspace", dir, 0, 0, docker.Backend{Mode: mode})}
					var result string
					var err error
					filter := "prism"
					if action == "ps" {
						filter = "prism-svc-"
						result, err = e.dockerPS(t.Context())
					} else {
						result, err = e.dockerList(t.Context())
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, expected := range []string{mode + " Docker daemon", "filter name=" + filter + ";", "running and stopped"} {
						if !strings.Contains(result, expected) {
							t.Fatalf("inventory omits %q: %s", expected, result)
						}
					}
					if strings.Contains(result, "outer host containers are not listed") != (mode == "workspace") {
						t.Fatalf("wrong daemon visibility: %s", result)
					}
					if strings.Contains(result, "Use docker_run") || strings.Contains(result, "All Docker containers") {
						t.Fatalf("inventory overstates coverage or proposes a duplicate deployment: %s", result)
					}
					if populated {
						if !strings.Contains(result, "example/image") || !strings.Contains(result, "Exited (0)") {
							t.Fatalf("existing container details lost: %s", result)
						}
						if action == "ps" {
							url := "http://example.localhost/"
							if mode == "workspace" {
								url = "/proxy/23456/"
							}
							if !strings.Contains(result, url) {
								t.Fatalf("existing service URL changed: %s", result)
							}
						}
					} else if !strings.Contains(result, "No matching") {
						t.Fatalf("empty result lacks scoped absence: %s", result)
					}
					calls, err := os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(calls), "ps\n-a\n--filter\nname="+filter+"\n") {
						t.Fatalf("inventory query changed: %s", calls)
					}
				})
			}
		}
	}
}
