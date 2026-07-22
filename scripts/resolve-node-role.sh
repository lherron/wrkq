#!/usr/bin/env bash
set -euo pipefail

role=""

if [[ "${WRKQ_NODE_ROLE+x}" == "x" ]]; then
  role="$WRKQ_NODE_ROLE"
else
  role_file="${WRKQ_NODE_ROLE_FILE:-$HOME/.config/wrkq/node-role}"
  if [[ -f "$role_file" ]]; then
    role="$(<"$role_file")"
  fi
fi

case "$role" in
  producer|consumer)
    printf '%s\n' "$role"
    ;;
  *)
    printf '%s\n' consumer
    ;;
esac
