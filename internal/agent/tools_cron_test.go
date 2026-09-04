package agent

import "testing"

// A disabled ("#DISABLED# ") job must still parse its Schedule/Command
// correctly and report Enabled=false — it used to be caught by the generic
// "unrelated comment, ignore" branch, leaving Schedule/Command empty (the
// bug this test guards against: a paused job showed up in the Tasks list
// with a blank schedule instead of being recognized as paused). Mirrors
// internal/server/cron_test.go's TestParseCronJobs for the sibling parser.
func TestParseCronJobs(t *testing.T) {
	raw := "# agent-job: backup\n@daily /bin/backup\n# agent-job: ping\n#DISABLED# */5 * * * * curl http://x\n"
	jobs := ParseCronJobs(raw)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d: %+v", len(jobs), jobs)
	}
	if jobs[0].Name != "backup" || !jobs[0].Enabled {
		t.Errorf("job0 wrong: %+v", jobs[0])
	}
	if jobs[1].Name != "ping" || jobs[1].Enabled {
		t.Errorf("job1 should be disabled: %+v", jobs[1])
	}
	if jobs[1].Schedule != "*/5 * * * *" || jobs[1].Command != "curl http://x" {
		t.Errorf("disabled job's schedule/command should still parse (un-prefixed): %+v", jobs[1])
	}
}

func TestValidateCronSchedule(t *testing.T) {
	ok := []string{"*/5 * * * *", "0 9 * * 1-5", "30 6 1,15 * *", "0 0 * * MON", "@daily", "@hourly", " 0 12 * * * "}
	for _, s := range ok {
		if err := validateCronSchedule(s); err != nil {
			t.Errorf("%q should be accepted: %v", s, err)
		}
	}
	bad := []string{"every 5 min", "* * * *", "* * * * * *", "0 9 * * 1-5 extra", "", "0 9 * * mon;rm -rf /", "@every5m"}
	for _, s := range bad {
		if err := validateCronSchedule(s); err == nil {
			t.Errorf("%q should be rejected", s)
		}
	}
}
