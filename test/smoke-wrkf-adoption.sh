#!/usr/bin/env bash
# smoke-wrkf-adoption.sh — S14 GUARD: wrkf RUN machinery exercised in a temp db
#
# Verifies that `wrkf run start` creates workflow_runs + workflow_role_bindings
# rows, and that driving the lifecycle through full_verify opens a review_signoff
# obligation. Scoped to the T-00001 test instance.
#
# SCOPED predicate asserted:
#   workflow_runs >= 3 (tester / implementer / reviewer, distinct principals)
#   workflow_role_bindings >= 3
#   workflow_obligations >= 1 kind=review_signoff
#   instance phase == review (reached active/review)
#
# FIRE-ON-BAD: removing the `wrkf run start` calls leaves workflow_runs=0 and
# the assertion fails with "expected >= 3 workflow_runs, got 0". The
# companion negative smoke sets WRKF_ADOPTION_NEGATIVE_SKIP_RUN_STARTS=1 to
# exercise that failure mode without editing this file.
#
# Pattern: mirrors test/smoke-wrkf-wrkq-code-change.sh (S10) happy-path section.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

DB="$TMPDIR/wrkq.db"
BIN="$TMPDIR/bin"
mkdir -p "$BIN"

go build -tags sqlite_fts5 -o "$BIN/wrkq"    "$ROOT/cmd/wrkq"
go build -tags sqlite_fts5 -o "$BIN/wrkqadm" "$ROOT/cmd/wrkqadm"
go build -tags sqlite_fts5 -o "$BIN/wrkf"    "$ROOT/cmd/wrkf"

cd "$TMPDIR"
export WRKQ_DB_PATH="$DB"
export WRKF_PRINCIPAL_REF="agent:local-human"
export WRKQ_PROJECT_ROOT=""
unset ASP_PROJECT

"$BIN/wrkqadm" init --db "$DB" >/dev/null

# Create the test task
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/s14-adopt \
  -t "S14 adoption smoke task" >/dev/null

TEMPLATE="$ROOT/wrkf/templates/wrkq-code-change.workflow.json"

# ── install + attach ──────────────────────────────────────────────────────────
"$BIN/wrkf" --db "$DB" workflow install "$TEMPLATE" --json \
  | jq -e '.id == "wrkq-code-change" and .version == "1"' >/dev/null

"$BIN/wrkf" --db "$DB" task attach T-00001 --workflow wrkq-code-change@1 --json \
  | jq -e '.phase == "intake"' >/dev/null

# ── bind all three roles via `run start` (DISTINCT principals) ───────────────
# This is the surface under test: each call creates a workflow_runs row
# AND a workflow_role_bindings row for the given role/principal pair.
if [ "${WRKF_ADOPTION_NEGATIVE_SKIP_RUN_STARTS:-}" != "1" ]; then
  "$BIN/wrkf" --db "$DB" run start T-00001 --role tester      --principal-ref agent:tester      --json \
    | jq -e '.role == "tester" and .status == "active"' >/dev/null

  "$BIN/wrkf" --db "$DB" run start T-00001 --role implementer --principal-ref agent:implementer --json \
    | jq -e '.role == "implementer" and .status == "active"' >/dev/null

  "$BIN/wrkf" --db "$DB" run start T-00001 --role reviewer    --principal-ref agent:reviewer    --json \
    | jq -e '.role == "reviewer" and .status == "active"' >/dev/null
fi

# ── ASSERT SCOPED: workflow_runs >= 3 ────────────────────────────────────────
RUN_COUNT="$("$BIN/wrkf" --db "$DB" run list T-00001 --json | jq '.runs | length')"
if [ "$RUN_COUNT" -lt 3 ]; then
  echo "FAIL: expected >= 3 workflow_runs, got $RUN_COUNT (run-start calls missing?)" >&2
  exit 1
fi

# ── drive lifecycle: author_red → implement → full_verify ────────────────────

# transition 1: author_red (tester) — open/intake → active/red
"$BIN/wrkf" --db "$DB" --principal-ref agent:tester --role tester \
  evidence add T-00001 --kind red_test --ref "git:sha-red-s14" \
  --facts '{"verdict":"red"}' --summary "S14 red smoke" --json \
  | jq -e '.kind == "red_test"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --principal-ref agent:tester \
  transition T-00001 author_red \
  --expect-revision 0 --idempotency-key s14-author-red --json \
  | jq -e '.state.phase == "red" and .revision == 1' >/dev/null

# transition 2: implement (implementer) — active/red → active/verify
# SoD: verify principal (agent:implementer) ≠ red_test principal (agent:tester) ✓
"$BIN/wrkf" --db "$DB" --principal-ref agent:implementer --role implementer \
  evidence add T-00001 --kind verify --ref "git:sha-green-s14" \
  --facts '{"verdict":"pass"}' --summary "S14 verify green" --json \
  | jq -e '.kind == "verify"' >/dev/null

"$BIN/wrkf" --db "$DB" --role implementer --principal-ref agent:implementer \
  transition T-00001 implement \
  --expect-revision 1 --idempotency-key s14-implement --json \
  | jq -e '.state.phase == "verify" and .revision == 2' >/dev/null

# transition 3: full_verify (tester) — active/verify → active/review
# SoD: verify_full principal (agent:tester) ≠ verify principal (agent:implementer) ✓
# Effect: opens a blocking review_signoff obligation owned by reviewer
"$BIN/wrkf" --db "$DB" --principal-ref agent:tester --role tester \
  evidence add T-00001 --kind verify_full --ref "git:sha-smoke-s14" \
  --facts '{"verdict":"pass"}' --summary "S14 full verify green" --json \
  | jq -e '.kind == "verify_full"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --principal-ref agent:tester \
  transition T-00001 full_verify \
  --expect-revision 2 --idempotency-key s14-full-verify --json \
  | jq -e '.state.phase == "review" and .revision == 3' >/dev/null

# ── ASSERT SCOPED: open review_signoff obligation exists ─────────────────────
OBL_ID="$("$BIN/wrkf" --db "$DB" obligation list T-00001 --json \
  | jq -r '.obligations[] | select(.kind == "review_signoff" and .status == "open") | .id')"
if [ -z "$OBL_ID" ]; then
  echo "FAIL: expected open review_signoff obligation after full_verify transition" >&2
  exit 1
fi

# ── ASSERT SCOPED: instance phase == review ───────────────────────────────────
PHASE="$("$BIN/wrkf" --db "$DB" task inspect T-00001 --json | jq -r '.phase')"
if [ "$PHASE" != "review" ]; then
  echo "FAIL: expected phase=review (>= active/review), got '$PHASE'" >&2
  exit 1
fi

# ── ASSERT SCOPED: sqlite-level binding counts ────────────────────────────────
INSTANCE_ID="$("$BIN/wrkf" --db "$DB" task inspect T-00001 --json | jq -r '.id')"

RB_COUNT="$(sqlite3 "$DB" \
  "SELECT count(*) FROM workflow_role_bindings WHERE instance_id='$INSTANCE_ID';")"
if [ "$RB_COUNT" -lt 3 ]; then
  echo "FAIL: expected >= 3 workflow_role_bindings, got $RB_COUNT" >&2
  exit 1
fi

# All three required roles must be bound
ROLES_OK="$(sqlite3 "$DB" \
  "SELECT count(*) FROM workflow_role_bindings
   WHERE instance_id='$INSTANCE_ID'
     AND role IN ('tester','implementer','reviewer');")"
if [ "$ROLES_OK" -lt 3 ]; then
  echo "FAIL: missing required role bindings (tester/implementer/reviewer); found $ROLES_OK/3" >&2
  exit 1
fi

OBL_COUNT="$(sqlite3 "$DB" \
  "SELECT count(*) FROM workflow_obligations
   WHERE instance_id='$INSTANCE_ID' AND kind='review_signoff';")"
if [ "$OBL_COUNT" -lt 1 ]; then
  echo "FAIL: expected >= 1 review_signoff obligation in db, got $OBL_COUNT" >&2
  exit 1
fi

echo "smoke-wrkf-adoption: ok"
