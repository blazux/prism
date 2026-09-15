package agent

import (
	"strings"
	"testing"
)

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

// rewriteCronBlock is the pure part of cronEditBlock. Two properties guarded
// here: (1) an edit that changes nothing yields content identical to the
// normalized input, which is what lets cronEditBlock skip the crontab rewrite
// on "pause an already-paused job"; (2) a real edit keeps the block's owner and
// description lines and the rest of the crontab untouched.
func TestRewriteCronBlock(t *testing.T) {
	e := &ToolExecutor{}
	raw := "MAILTO=x\n# agent-job: backup\n# agent-owner: u1\n# agent-desc: nightly\n@daily /bin/backup\n# agent-job: ping\n#DISABLED# */5 * * * * curl http://x\n\n"
	same := func(desc, line string, enabled bool) (string, string, bool) { return desc, line, enabled }

	// (1) no-op on a paused job → same content, so no write.
	job, msg, content, err := e.rewriteCronBlock(raw, "ping", same)
	if err != nil || msg != "" || job == nil || job.Name != "ping" || job.Enabled {
		t.Fatalf("unexpected: job=%+v msg=%q err=%v", job, msg, err)
	}
	if content != normalizeCrontab(raw) {
		t.Errorf("no-op edit must reproduce the crontab verbatim\n got: %q\nwant: %q", content, normalizeCrontab(raw))
	}

	// (2) pause backup → only its command line gains the prefix.
	_, msg, content, err = e.rewriteCronBlock(raw, "backup", func(desc, line string, _ bool) (string, string, bool) { return desc, line, false })
	if err != nil || msg != "" {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
	want := "MAILTO=x\n# agent-job: backup\n# agent-owner: u1\n# agent-desc: nightly\n#DISABLED# @daily /bin/backup\n# agent-job: ping\n#DISABLED# */5 * * * * curl http://x\n"
	if content != want {
		t.Errorf("pause rewrite wrong\n got: %q\nwant: %q", content, want)
	}
	if content == normalizeCrontab(raw) {
		t.Errorf("a real edit must differ from the input, otherwise the write would be skipped")
	}

	// Refusals: unknown name lists the existing jobs; foreign owner in multi-user mode.
	if _, msg, _, _ := e.rewriteCronBlock(raw, "nope", same); !strings.Contains(msg, "backup, ping") {
		t.Errorf("unknown job should list existing names, got %q", msg)
	}
	mu := &ToolExecutor{multiUser: true, sessionID: "u2-ws"}
	if _, msg, _, _ := mu.rewriteCronBlock(raw, "backup", same); !strings.Contains(msg, "another user") {
		t.Errorf("foreign job should be refused, got %q", msg)
	}
}
