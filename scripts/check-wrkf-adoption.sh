#!/usr/bin/env bash
# check-wrkf-adoption.sh — S14 canonical wrkf adoption probe
#
# The canonical wrkq store is reached through WRKQ_DB and may be remote. Query
# it through the supported wrkf CLI/RPC surface instead of opening a local
# SQLite path. T-06783 is a durable, completed wrkf-task-loop canary whose
# record proves a multi-role workflow advanced through landing.
#
# Usage:
#   scripts/check-wrkf-adoption.sh
#   WRKF_ADOPTION_TASK=T-06783 scripts/check-wrkf-adoption.sh
#   WRKF_BIN=/path/to/wrkf scripts/check-wrkf-adoption.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADOPTION_TASK="${WRKF_ADOPTION_TASK:-T-06783}"

if [ -n "${WRKF_BIN:-}" ]; then
  WRKF="$WRKF_BIN"
elif [ -x "$ROOT/bin/wrkf" ]; then
  WRKF="$ROOT/bin/wrkf"
elif command -v wrkf >/dev/null 2>&1; then
  WRKF="$(command -v wrkf)"
else
  echo "FAIL: wrkf CLI not found (build bin/wrkf or set WRKF_BIN)" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "FAIL: jq is required for the wrkf adoption probe" >&2
  exit 1
fi

INSPECT="$("$WRKF" task inspect "$ADOPTION_TASK" --json)" || {
  echo "FAIL: could not inspect canonical wrkf adoption task $ADOPTION_TASK" >&2
  exit 1
}

RUNS="$("$WRKF" run list "$ADOPTION_TASK" --json)" || {
  echo "FAIL: could not list canonical wrkf runs for $ADOPTION_TASK" >&2
  exit 1
}

if ! jq -e '
  .templateId == "wrkq-simple-task" and
  .templateVersion == "5" and
  .status == "closed" and
  .phase == "done" and
  (.revision >= 5)
' >/dev/null <<<"$INSPECT"; then
  echo "FAIL: $ADOPTION_TASK is not the expected completed wrkq-simple-task@5 canary" >&2
  jq '{id, templateId, templateVersion, status, phase, revision}' <<<"$INSPECT" >&2
  exit 1
fi

RUN_COUNT="$(jq '.runs | length' <<<"$RUNS")"
COMPLETED_COUNT="$(jq '[.runs[] | select(.status == "completed")] | length' <<<"$RUNS")"
ROLES="$(jq -r '[.runs[].role] | unique | join(",")' <<<"$RUNS")"

if ! jq -e '
  (.runs | length) >= 3 and
  ([.runs[] | select(.status == "completed")] | length) >= 3 and
  ([.runs[].role] | index("coordinator")) != null and
  ([.runs[].role] | index("implementer")) != null and
  ([.runs[].role] | index("tester")) != null
' >/dev/null <<<"$RUNS"; then
  echo "FAIL: $ADOPTION_TASK lacks the required completed multi-role wrkf run record" >&2
  echo "  workflow_runs: $RUN_COUNT" >&2
  echo "  completed:     $COMPLETED_COUNT" >&2
  echo "  roles:         $ROLES" >&2
  exit 1
fi

echo "PASS: $ADOPTION_TASK canonical wrkf adoption confirmed through the wrkf API"
echo "  instance:       $(jq -r '.id' <<<"$INSPECT")"
echo "  template:       $(jq -r '.templateId + "@" + .templateVersion' <<<"$INSPECT")"
echo "  status/phase:   $(jq -r '.status + "/" + .phase' <<<"$INSPECT")"
echo "  workflow_runs:  $RUN_COUNT ($COMPLETED_COUNT completed)"
echo "  roles:          $ROLES"
