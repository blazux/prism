package cronclock

import (
	_ "embed"
	"regexp"
)

//go:embed check.py
var Script []byte

const Path = ".prism-cron-clock.py"

var wrapper = regexp.MustCompile(`^\* \* \* \* \* python3 /workspace/\.prism-cron-clock\.py '([^']*)' '([^']*)' && \( (.*) \)$`)

// Wrap is used only after schedule and timezone validation, including worker tzdata.
func Wrap(schedule, command, zone string) string {
	if zone == "" || schedule == "@reboot" {
		return schedule + " " + command
	}
	return "* * * * * python3 /workspace/" + Path + " '" + schedule + "' '" + zone + "' && ( " + command + " )"
}
func Unwrap(line string) (schedule, command, zone string, ok bool) {
	m := wrapper.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[3], m[2], true
}
