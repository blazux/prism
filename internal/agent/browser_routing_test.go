package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"prism/internal/docker"
)

func TestBrowserServiceProxyBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		manager *docker.Manager
		want    string
	}{
		{"no workspace", nil, ""},
		{"host default", docker.NewManager("fixture", "", 0, 0), "traefik"},
		{"host explicit", docker.NewManager("fixture", "", 0, 0, docker.Backend{Mode: "host"}), "traefik"},
		{"workspace daemon", docker.NewManager("fixture", "", 0, 0, docker.Backend{Mode: "workspace"}), ""},
		{"hosted capability", docker.WithExecution(func(context.Context, string, []byte, map[string]string) (string, error) { return "", nil }), ""},
		{"hosted inner Docker", docker.WithExecution(nil).WithWorkspaceServices("/workspace"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&ToolExecutor{docker: tc.manager}).browserServiceProxy(); got != tc.want {
				t.Fatalf("proxy = %q, want %q", got, tc.want)
			}
		})
	}
}

// Exercise the shared Python launcher without installing Playwright locally.
// This checks both the local rule and the complete absence of a Cloud override.
func TestBrowserLauncherAndServiceDiagnostics(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	script := browserLaunchScript + `
import json
class Chromium:
    def launch(self, **kwargs): return kwargs
class Playwright:
    chromium = Chromium()
print(json.dumps({
    'host': launch_browser(Playwright(), 'traefik'),
    'cloud': launch_browser(Playwright(), ''),
    'service_error': local_service_hint('http://drawio.localhost/', 'net::ERR_NAME_NOT_RESOLVED'),
    'public_error': local_service_hint('https://missing.example/', 'net::ERR_NAME_NOT_RESOLVED'),
    'loopback_error': local_service_hint('http://localhost:3000/', 'net::ERR_CONNECTION_REFUSED')
}))
`
	out, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("launcher: %v: %s", err, out)
	}
	var result struct {
		Host struct {
			Args []string `json:"args"`
		} `json:"host"`
		Cloud struct {
			Args []string `json:"args"`
		} `json:"cloud"`
		ServiceError  string `json:"service_error"`
		PublicError   string `json:"public_error"`
		LoopbackError string `json:"loopback_error"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Host.Args, " "), "--host-resolver-rules=MAP *.localhost traefik") {
		t.Fatal(result.Host.Args)
	}
	for _, arg := range result.Cloud.Args {
		if strings.Contains(arg, "host-resolver") {
			t.Fatal("local route leaked into Cloud", arg)
		}
	}
	if !strings.Contains(result.ServiceError, "Traefik") || result.PublicError != "" || result.LoopbackError != "" {
		t.Fatal("service diagnostic escaped its scope", string(out))
	}
}
