package docker

import (
	"fmt"
	"strings"
)

// Only cron's explicit "no crontab" response means an empty schedule. Docker,
// permission and command failures must stop a read-modify-write operation.
// The workspace uses Debian cron; LC_ALL makes its diagnostic predictable.
const ReadCrontabCommand = `out=$(LC_ALL=C crontab -l 2>&1); status=$?
if [ "$status" -eq 0 ]; then
  printf '%s\n' "$out"
elif [ "$status" -eq 1 ] && [ "$out" = "no crontab for $(id -un)" ]; then
  exit 0
else
  printf '%s\n' "$out" >&2
  exit "$status"
fi`

// CronReadError provides useful diagnostics without echoing crontab contents,
// which can contain injected credentials. Unexpected runtime errors stay opaque.
func CronReadError(err error, output string) error {
	if err == nil {
		return nil
	}
	detail := ""
	switch {
	case strings.Contains(output, "Permission denied"):
		detail = " (workspace user cannot access crontab)"
	case strings.Contains(output, "crontab: not found") || strings.Contains(output, "crontab: command not found"):
		detail = " (crontab is not installed in the workspace)"
	case strings.Contains(output, "Read-only file system"):
		detail = " (cron storage is read-only)"
	}
	return fmt.Errorf("cannot read crontab%s: %w", detail, err)
}
