package server

import "testing"

func TestSplitSchedule(t *testing.T) {
	cases := []struct{ in, sch, cmd string }{
		{"*/5 * * * * curl http://x", "*/5 * * * *", "curl http://x"},
		{"@daily backup.sh", "@daily", "backup.sh"},
		{"0 9 * * 1 echo hi there", "0 9 * * 1", "echo hi there"},
	}
	for _, c := range cases {
		s, cmd := splitSchedule(c.in)
		if s != c.sch || cmd != c.cmd {
			t.Errorf("splitSchedule(%q) = (%q,%q), want (%q,%q)", c.in, s, cmd, c.sch, c.cmd)
		}
	}
}

func TestParseCronJobs(t *testing.T) {
	raw := "# agent-job: backup\n@daily /bin/backup\n# agent-job: ping\n#DISABLED# */5 * * * * curl http://x\n"
	jobs := parseCronJobs(raw)
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Name != "backup" || !jobs[0].Enabled || jobs[0].Schedule != "@daily" || jobs[0].Command != "/bin/backup" {
		t.Errorf("job0 wrong: %+v", jobs[0])
	}
	if jobs[1].Name != "ping" || jobs[1].Enabled {
		t.Errorf("job1 should be disabled: %+v", jobs[1])
	}
	if jobs[1].Command != "curl http://x" {
		t.Errorf("disabled job command should be un-prefixed: %q", jobs[1].Command)
	}
}
