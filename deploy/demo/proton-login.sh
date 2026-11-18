#!/bin/sh
set -eu
cd "$(dirname "$0")"
compose() {
    docker compose -p prism-demo-mono --env-file .env.mono -f compose.yaml "$@"
}
restart_bridge() {
    status=$?
    trap - EXIT
    compose up -d --no-deps --pull never protonmail-bridge || exit 1
    exit "$status"
}
trap restart_bridge EXIT
compose stop protonmail-bridge
compose run --rm --no-deps -T --entrypoint /bin/bash protonmail-bridge -ec '
    if [ ! -s /root/.password-store/.gpg-id ]; then
        if ! gpg --batch --list-secret-keys pass-key >/dev/null 2>&1; then
            gpg --generate-key --batch /protonmail/gpgparams
        fi
        pass init pass-key
    fi
'
compose run --rm --no-deps --entrypoint protonmail-bridge protonmail-bridge --cli
