#!/usr/bin/env bash
# List the launchd/*.plist files `just launchd-install`/`launchd-uninstall` act on.
#
#   scripts/launchd-jobs.sh [job...]
#
# No arguments: every repo plist. A job is a label (com.praesidium.llama-server)
# or its short name (llama-server); an unknown job refuses so a typo never
# silently acts on nothing.
set -euo pipefail
shopt -s nullglob
all=(launchd/*.plist)
if [ "${#all[@]}" -eq 0 ]; then
  echo "launchd-jobs: no launchd/*.plist in $(pwd)" >&2
  exit 1
fi
if [ "$#" -eq 0 ]; then
  printf '%s\n' "${all[@]}"
  exit 0
fi
for job in "$@"; do
  src="launchd/${job%.plist}.plist"
  [ -f "$src" ] || src="launchd/com.praesidium.${job%.plist}.plist"
  if [ ! -f "$src" ]; then
    echo "launchd-jobs: no launchd job $job; known: $(basename -a "${all[@]}" | sed 's/\.plist$//' | tr '\n' ' ')" >&2
    exit 2
  fi
  echo "$src"
done
