#!/bin/bash
set -Eeuo pipefail

# The inherited workspace is also the host of this deployment's service daemon.
# Passing dockerd explicitly retains the official DinD setup without its default
# TCP listeners. There is no socket mounted from the VPS.
unset DOCKER_CONTEXT DOCKER_TLS_VERIFY DOCKER_CERT_PATH
export DOCKER_HOST=unix:///var/run/docker.sock
mkdir -p /workspace /var/lib/docker /run/docker
rm -f /run/dockerd.pid
/usr/local/bin/dockerd-entrypoint.sh dockerd --config-file=/etc/docker/daemon.json &
daemon_pid=$!

cleanup() {
  trap - EXIT TERM INT
  if kill -0 "$daemon_pid" 2>/dev/null; then
    kill -TERM "$daemon_pid" 2>/dev/null || true
    wait "$daemon_pid" || true
  fi
}
trap cleanup EXIT
trap 'exit 0' TERM INT

ready=0
for attempt in $(seq 1 60); do
  if docker info >/dev/null 2>&1; then ready=1; break; fi
  if ! kill -0 "$daemon_pid" 2>/dev/null; then
    echo >&2 'The private workspace Docker daemon exited during startup.'
    exit 1
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  echo >&2 'The private workspace Docker daemon did not become ready within 60 seconds.'
  exit 1
fi

# Restore the same persistent additions as the normal Prism workspace.
if [ -s /workspace/.apt-packages ]; then
  apt-get update -qq
  xargs -r apt-get install -y < /workspace/.apt-packages
fi
if [ -s /workspace/.pip-packages ]; then
  xargs -r pip3 install -q < /workspace/.pip-packages
fi
if [ -f /workspace/.crontab ]; then crontab /workspace/.crontab; fi
cron
echo 'Prism demo workspace ready; Docker listens on its private Unix socket.'
wait "$daemon_pid"
