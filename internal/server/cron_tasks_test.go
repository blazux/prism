package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// cronPendingJobTasks reads the PERSISTED .crontab mirror, not the live
// crontab — see tools_cron.go's cronRemove and this package's mutateJob,
// which both write to that same file. Before their fix, removing the last
// cron job cleared the live crontab (`crontab -r`) but left this file with
// its old content, so entries kept showing up here for jobs that no longer
// existed anywhere — "ghost" Tasks. These tests pin the read side: an empty
// (or missing) persisted file must produce zero Tasks entries.
func TestCronPendingJobTasks_EmptyFileProducesNoGhosts(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: dir}}
	req, _ := http.NewRequest("GET", "/", nil)

	// A real job present → shows up.
	crontab := "# agent-job: refresh-alertes-ocom\n# agent-owner: default\n*/2 * * * * curl ...\n"
	if err := os.WriteFile(filepath.Join(dir, ".crontab"), []byte(crontab), 0600); err != nil {
		t.Fatal(err)
	}
	items := s.cronPendingJobTasks(req)
	if len(items) != 1 || items[0].ID != "cron:refresh-alertes-ocom" {
		t.Fatalf("expected 1 task for the live job, got %+v", items)
	}

	// The fixed removal path: persisted file cleared to empty, exactly what
	// cronRemove/mutateJob now do when the last job is removed.
	if err := os.WriteFile(filepath.Join(dir, ".crontab"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	items = s.cronPendingJobTasks(req)
	if len(items) != 0 {
		t.Errorf("expected no tasks after the persisted crontab was cleared, got %+v", items)
	}
}

func TestCronPendingJobTasks_MissingFileIsFine(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{WorkspaceDir: dir}}
	req, _ := http.NewRequest("GET", "/", nil)

	items := s.cronPendingJobTasks(req)
	if items != nil {
		t.Errorf("expected nil for a workspace with no .crontab yet, got %+v", items)
	}
}
