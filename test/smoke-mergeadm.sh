#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin"

if [[ ! -x "$BIN/wrkqadm" ]]; then
  echo "error: missing wrkqadm in $BIN (run 'just build' first)" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

set +e
output="$("$BIN/wrkqadm" merge \
  --source "$tmpdir/src.db" \
  --dest "$tmpdir/dest.db" \
  --project proj \
  --path-prefix canonical \
  --report "$tmpdir/report.json" 2>&1)"
status=$?
set -e

if [ "$status" -eq 0 ]; then
  echo "FAIL: wrkqadm merge unexpectedly succeeded" >&2
  exit 1
fi

if ! grep -q "legacy actor data movement is no longer supported" <<<"$output"; then
  echo "FAIL: wrkqadm merge failed with the wrong diagnostic" >&2
  echo "$output" >&2
  exit 1
fi

echo "wrkqadm merge unsupported smoke test: PASS"
