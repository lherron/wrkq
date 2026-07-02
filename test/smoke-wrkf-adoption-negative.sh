#!/usr/bin/env bash
# smoke-wrkf-adoption-negative.sh — proves the S14 adoption smoke fires on bad.
#
# It drives the positive smoke with only the run-start calls disabled and expects
# the scoped workflow_runs assertion to fail. This keeps the negative fixture
# close to the real smoke path instead of maintaining a second hand-copied flow.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

set +e
OUTPUT="$(WRKF_ADOPTION_NEGATIVE_SKIP_RUN_STARTS=1 "$ROOT/test/smoke-wrkf-adoption.sh" 2>&1)"
STATUS=$?
set -e

if [ "$STATUS" -eq 0 ]; then
  echo "FAIL: adoption negative smoke unexpectedly passed" >&2
  echo "$OUTPUT" >&2
  exit 1
fi

if ! grep -q "expected >= 3 workflow_runs" <<<"$OUTPUT"; then
  echo "FAIL: adoption negative smoke failed for the wrong reason" >&2
  echo "$OUTPUT" >&2
  exit 1
fi

echo "smoke-wrkf-adoption-negative: ok"
