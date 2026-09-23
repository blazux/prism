package hosting

import (
	"io/fs"
	agenttools "prism/agent_tools"
	help "prism/docs/help"
	"testing"
)

func TestBundlesRetainInternalPaths(t *testing.T) {
	app, err := New(Config{})
	if err != nil || app == nil {
		t.Fatal(err)
	}
	// The help indexer and tool seeder retain their existing prefixed paths.
	for _, tc := range []struct {
		root fs.FS
		name string
	}{
		{prefixedFS{"docs/help", help.Files()}, "docs/help/overview.md"},
		{prefixedFS{"agent_tools", agenttools.Files()}, "agent_tools/.apt-packages"},
	} {
		b, err := fs.ReadFile(tc.root, tc.name)
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := tc.root.Open("../outside"); err == nil {
			t.Fatal("invalid path accepted")
		}
	}
}
func TestInvalidBackendRejectedBeforeStart(t *testing.T) {
	if _, err := New(Config{DockerBackend: DockerBackend{Mode: "typo"}}); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
