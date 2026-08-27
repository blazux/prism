package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCompose(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidateComposeSafety(t *testing.T) {
	safe := []string{
		`services:
  web:
    image: nginx:alpine
    ports: ["8080:80"]
    volumes:
      - webdata:/usr/share/nginx/html
      - ./conf:/etc/nginx/conf.d
    environment:
      - TZ=UTC
volumes:
  webdata:
`,
		`services:
  api:
    image: myapi
    volumes:
      - type: volume
        source: apidata
        target: /data
`,
	}
	for i, body := range safe {
		if err := validateComposeSafety(writeCompose(t, body)); err != nil {
			t.Errorf("safe compose %d rejected: %v", i, err)
		}
	}

	unsafe := map[string]string{
		"privileged": `services:
  x: { image: a, privileged: true }
`,
		"pid host": `services:
  x: { image: a, pid: host }
`,
		"network host": `services:
  x: { image: a, network_mode: host }
`,
		"cap_add": `services:
  x:
    image: a
    cap_add: [SYS_ADMIN]
`,
		"devices": `services:
  x:
    image: a
    devices: ["/dev/kmsg:/dev/kmsg"]
`,
		"security_opt unconfined": `services:
  x:
    image: a
    security_opt: ["apparmor:unconfined"]
`,
		"bind host root (short)": `services:
  x:
    image: a
    volumes: ["/:/host"]
`,
		"bind docker socket": `services:
  x:
    image: a
    volumes: ["/var/run/docker.sock:/var/run/docker.sock"]
`,
		"bind host etc (long)": `services:
  x:
    image: a
    volumes:
      - type: bind
        source: /etc
        target: /etc
`,
	}
	for name, body := range unsafe {
		err := validateComposeSafety(writeCompose(t, body))
		if err == nil {
			t.Errorf("%s: expected rejection", name)
			continue
		}
		if !strings.Contains(err.Error(), "run docker_compose up again") {
			t.Errorf("%s: rejection lacks recovery hint: %v", name, err)
		}
	}

	// Unreadable / malformed files are left for docker to report.
	if err := validateComposeSafety(filepath.Join(t.TempDir(), "nope.yml")); err != nil {
		t.Errorf("missing file should not error here: %v", err)
	}
	if err := validateComposeSafety(writeCompose(t, "::: not yaml :::")); err != nil {
		t.Errorf("malformed yaml should not error here: %v", err)
	}
}
