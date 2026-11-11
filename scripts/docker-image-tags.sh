#!/usr/bin/env bash
# Print the Docker tags for a Git ref. Prereleases never promote latest.
set -euo pipefail

if [[ $# != 2 || -z "$1" ]]; then
  printf '%s\n' 'Usage: docker-image-tags.sh IMAGE GIT_REF' >&2
  exit 2
fi
image=$1
ref=$2
invalid() { printf '%s\n' 'Invalid release ref (expected vMAJOR.MINOR.PATCH[-PRERELEASE]).' >&2; exit 2; }

case "$ref" in
  refs/heads/?*) printf '%s:unstable\n' "$image" ;;
  refs/tags/*)
    tag=${ref#refs/tags/}
    # Docker tags cannot contain SemVer +build metadata and are at most 128 chars.
    version='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
    [[ "$tag" =~ $version && ${#tag} -le 128 ]] || invalid
    if [[ "$tag" == *-* ]]; then
      IFS=. read -ra identifiers <<< "${tag#*-}"
      for identifier in "${identifiers[@]}"; do
        [[ ! "$identifier" =~ ^0[0-9]+$ ]] || invalid
      done
      printf '%s:%s\n' "$image" "$tag"
    else
      printf '%s:%s,%s:latest\n' "$image" "$tag" "$image"
    fi
    ;;
  *) invalid ;;
esac
