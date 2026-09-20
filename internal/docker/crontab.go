package docker

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
