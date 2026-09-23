package server

import (
	"prism/internal/cronclock"
	"testing"
)

func TestZonedCronParsing(t *testing.T) {
	for _, prefix := range []string{"", "#DISABLED# "} {
		line := cronclock.Wrap("0 9 * * *", "echo hello; echo world", "America/Martinique")
		jobs := parseCronJobs("# agent-job: morning\n" + prefix + line + "\n")
		if len(jobs) != 1 {
			t.Fatal(jobs)
		}
		j := jobs[0]
		if j.Timezone != "America/Martinique" || j.Schedule != "0 9 * * *" || j.Command != "echo hello; echo world" || j.Enabled != (prefix == "") {
			t.Fatalf("%+v", j)
		}
	}
}
