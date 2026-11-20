#!/bin/sh

# Restore packages and scheduled jobs kept on the persistent workspace volume.
# Keep startup available when a repository or saved package is unavailable.
python=${1:-python3}
workspace_dir=${WORKSPACE_DIR:-/workspace}

if [ -s "$workspace_dir/.apt-packages" ]; then
    if ! (apt-get update -qq && xargs -r apt-get install -y < "$workspace_dir/.apt-packages"); then
        echo "[workspace] Could not restore all saved APT packages; see the errors above." >&2
    fi
fi

if [ -s "$workspace_dir/.pip-packages" ]; then
    if ! xargs -r "$python" -m pip install -q < "$workspace_dir/.pip-packages"; then
        echo "[workspace] Could not restore all saved Python packages; see the errors above." >&2
    fi
fi

if [ -f "$workspace_dir/.crontab" ]; then
    if ! crontab "$workspace_dir/.crontab"; then
        echo "[workspace] Could not restore the saved crontab; see the errors above." >&2
    fi
fi

cron || exit $?
exec tail -f /dev/null
