package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCrontabDistinguishesEmptyFromFailure(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		fail               bool
	}{
		{"existing", "printf '@daily echo keep\\n'", "@daily echo keep", false},
		{"empty", "echo \"no crontab for $(id -un)\" >&2; exit 1", "", false},
		{"permission", "echo 'permission denied' >&2; exit 1", "", true},
		{"unavailable", "exit 127", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(dir, "crontab"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("bash", "-c", ReadCrontabCommand).Output()
			if (err != nil) != tc.fail || (!tc.fail && strings.TrimSpace(string(out)) != tc.want) {
				t.Fatalf("%q %v", out, err)
			}
		})
	}
}
