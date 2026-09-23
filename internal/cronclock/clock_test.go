package cronclock

import "testing"

func TestWrapper(t *testing.T) {
	cmd := "echo one; echo two && echo three"
	line := Wrap("0 9 * * *", cmd, "Europe/Paris")
	s, c, z, ok := Unwrap(line)
	if !ok || s != "0 9 * * *" || c != cmd || z != "Europe/Paris" {
		t.Fatal(line, s, c, z, ok)
	}
	for _, s := range []string{"@reboot", "0 9 * * *"} {
		if got := Wrap(s, cmd, ""); got != s+" "+cmd {
			t.Fatal(got)
		}
	}
}
