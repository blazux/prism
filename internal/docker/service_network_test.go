package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostServiceUsesWorkspaceNetworkForTraefik(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$DOCKER_TEST_LOG\"\ncase \"$1\" in inspect) echo fixture_multi_network;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("DOCKER_TEST_LOG", log)
	m := NewManager("multi-workspace", t.TempDir(), 21000, 21999)
	if _, err := m.RunService(context.Background(), "fixture", "fixture-image", []int{8080}, "", nil, nil, false, "test"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--network\nfixture_multi_network\n", "--label\ntraefik.docker.network=fixture_multi_network\n"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("service/proxy network mismatch; missing %q: %s", want, b)
		}
	}
}
