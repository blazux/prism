package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"prism/internal/docker"
	"strings"
	"testing"
)

func TestCronReadFailureDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	mirror := filepath.Join(dir, ".crontab")
	t.Setenv("CRON_TEST_LOG", log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf 'called\\n' >> \"$CRON_TEST_LOG\"\necho 'simulated read failure' >&2\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("# agent-job: keep\n@daily echo keep\n")
	if err := os.WriteFile(mirror, original, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{WorkspaceDir: dir}, docker: docker.NewManager("test", dir, 0, 0)}
	for _, run := range []func() error{
		func() error { return s.mutateJob("keep", func(s string) string { return "" }) },
		func() error { return s.editJobBlock("keep", "@hourly", "echo changed", "") },
		func() error { return s.upsertCronJob("new", "@daily", "echo new", "", "") },
	} {
		if err := run(); err == nil {
			t.Fatal("read failure accepted")
		}
	}
	r := httptest.NewRequest("GET", "/api/cron", nil)
	w := httptest.NewRecorder()
	s.handleCron(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(mirror)
	if string(data) != string(original) {
		t.Fatal("persistent schedule changed")
	}
	calls, _ := os.ReadFile(log)
	if strings.Count(string(calls), "called") != 4 {
		t.Fatal("unexpected write attempt", string(calls))
	}
}

func TestCronInstallCommitsOnlyOnSuccess(t *testing.T) {
	dir := t.TempDir()
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	executable := filepath.Join(bin, "docker")
	mirror := filepath.Join(dir, ".crontab")
	original := "@daily echo keep\n"
	replacement := "@hourly echo new\n"
	if err := os.WriteFile(mirror, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: Config{WorkspaceDir: dir}, docker: docker.NewManager("unused", dir, 0, 0)}
	for _, success := range []bool{false, true} {
		script := "#!/bin/sh\nexit 42\n"
		if success {
			script = "#!/bin/sh\nexit 0\n"
		}
		if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		err := s.applyCrontab(replacement)
		if (err == nil) != success {
			t.Fatalf("unexpected result: %v", err)
		}
		want := original
		if success {
			want = replacement
		}
		data, err := os.ReadFile(mirror)
		if err != nil || string(data) != want {
			t.Fatalf("restart copy changed incorrectly: %q %v", data, err)
		}
		staged, _ := filepath.Glob(filepath.Join(dir, ".crontab-stage-*"))
		if len(staged) != 0 {
			t.Fatal("staging files leaked", staged)
		}
	}
}
