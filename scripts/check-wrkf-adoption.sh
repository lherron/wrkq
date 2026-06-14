#!/usr/bin/env bash
# check-wrkf-adoption.sh — S14 canonical PROBE (SOFT; NOT a hard just-verify gate)
#
# Inspects the REAL canonical wrkq db and asserts the SCOPED adoption signal for
# task T-04383's wrkq-code-change@1 instance.
#
# Scoped predicate (daedalus ruling DM #7538):
#   workflow_instances row for T-04383 with template wrkq-code-change reaching
#   >= active/review, PLUS:
#     workflow_runs >= 3
#     workflow_role_bindings >= 3 (roles include tester/implementer/reviewer)
#     workflow_obligations >= 1 kind=review_signoff
#
# Behavior matrix (daedalus required test #4):
#   (a) canonical db ABSENT or workflow tables missing → SKIP (exit 0)
#   (b) db PRESENT, no scoped T-04383 adoption rows   → FAIL (exit 1)
#   (c) post-adoption db with scoped rows              → PASS (exit 0)
#
# Usage:
#   scripts/check-wrkf-adoption.sh [--db /path/to/wrkq.db]
#   WRKQ_DB_PATH=/path/to/wrkq.db scripts/check-wrkf-adoption.sh
#
# Default db path resolution order:
#   1. --db flag
#   2. WRKQ_DB_PATH env var
#   3. WRKQ_DB_PATH from <repo-root>/.env.local (if file exists)
#   4. <repo-root>/.wrkq/wrkq.db
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── resolve db path (--db flag > WRKQ_DB_PATH env > .env.local > default) ────
DB_PATH=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) DB_PATH="$2"; shift 2 ;;
    *)    echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$DB_PATH" ] && [ -n "${WRKQ_DB_PATH:-}" ]; then
  DB_PATH="$WRKQ_DB_PATH"
fi

# Source .env.local from repo root to pick up WRKQ_DB_PATH if not already set
if [ -z "$DB_PATH" ] && [ -f "$ROOT/.env.local" ]; then
  ENVLOCAL_DB="$(grep '^WRKQ_DB_PATH=' "$ROOT/.env.local" | cut -d= -f2- | head -1)"
  if [ -n "$ENVLOCAL_DB" ]; then
    DB_PATH="$ENVLOCAL_DB"
  fi
fi

if [ -z "$DB_PATH" ]; then
  DB_PATH="$ROOT/.wrkq/wrkq.db"
fi

# ── (a) db absent or uninitialized → SKIP ────────────────────────────────────
if [ ! -f "$DB_PATH" ] || [ ! -s "$DB_PATH" ]; then
  echo "SKIP: canonical db not found or empty: $DB_PATH"
  exit 0
fi

# Check whether workflow tables exist (uninitialized db has no workflow_runs)
HAS_TABLES="$(sqlite3 "$DB_PATH" \
  "SELECT count(*) FROM sqlite_master
   WHERE type='table' AND name='workflow_runs';" 2>/dev/null || echo "0")"

if [ "$HAS_TABLES" = "0" ]; then
  echo "SKIP: workflow tables not present in $DB_PATH (db uninitialized for wrkf)"
  exit 0
fi

# ── resolve T-04383 → task uuid ───────────────────────────────────────────────
TASK_UUID="$(sqlite3 "$DB_PATH" \
  "SELECT uuid FROM tasks WHERE id='T-04383' LIMIT 1;" 2>/dev/null || true)"

if [ -z "$TASK_UUID" ]; then
  echo "FAIL: T-04383 not found in tasks table of $DB_PATH"
  echo "  → adoption run has not been recorded in the canonical db"
  exit 1
fi

# ── find the wrkq-code-change@1 instance for T-04383 ─────────────────────────
INSTANCE_ID="$(sqlite3 "$DB_PATH" \
  "SELECT id FROM workflow_instances
   WHERE task_uuid='$TASK_UUID'
     AND template_id='wrkq-code-change'
     AND template_version='1'
   LIMIT 1;" 2>/dev/null || true)"

if [ -z "$INSTANCE_ID" ]; then
  echo "FAIL: no wrkq-code-change@1 instance found for T-04383 (uuid=$TASK_UUID)"
  echo "  → wrkf task attach has not been run for this task in the canonical db"
  exit 1
fi

# ── check instance reached >= active/review ───────────────────────────────────
# Phases that satisfy ">= active/review": review, done
PHASE="$(sqlite3 "$DB_PATH" \
  "SELECT phase FROM workflow_instances WHERE id='$INSTANCE_ID';" 2>/dev/null || true)"

if [[ "$PHASE" != "review" && "$PHASE" != "done" ]]; then
  echo "FAIL: instance $INSTANCE_ID phase='$PHASE' has not reached >= active/review"
  echo "  → lifecycle has not progressed through full_verify in the canonical db"
  exit 1
fi

# ── check workflow_runs >= 3 ──────────────────────────────────────────────────
RUN_COUNT="$(sqlite3 "$DB_PATH" \
  "SELECT count(*) FROM workflow_runs WHERE instance_id='$INSTANCE_ID';" 2>/dev/null || echo "0")"

if [ "$RUN_COUNT" -lt 3 ]; then
  echo "FAIL: workflow_runs=$RUN_COUNT for instance $INSTANCE_ID (expected >= 3)"
  echo "  → wrkf run start has not been called for tester/implementer/reviewer"
  exit 1
fi

# ── check workflow_role_bindings >= 3 (tester/implementer/reviewer) ───────────
RB_COUNT="$(sqlite3 "$DB_PATH" \
  "SELECT count(*) FROM workflow_role_bindings
   WHERE instance_id='$INSTANCE_ID'
     AND role IN ('tester','implementer','reviewer');" 2>/dev/null || echo "0")"

if [ "$RB_COUNT" -lt 3 ]; then
  echo "FAIL: role_bindings for tester/implementer/reviewer = $RB_COUNT (expected >= 3)"
  echo "  Bound roles:"
  sqlite3 "$DB_PATH" \
    "SELECT '  ' || role || ' → ' || actor FROM workflow_role_bindings WHERE instance_id='$INSTANCE_ID';" \
    2>/dev/null || true
  exit 1
fi

# ── check workflow_obligations >= 1 kind=review_signoff ──────────────────────
OBL_COUNT="$(sqlite3 "$DB_PATH" \
  "SELECT count(*) FROM workflow_obligations
   WHERE instance_id='$INSTANCE_ID' AND kind='review_signoff';" 2>/dev/null || echo "0")"

if [ "$OBL_COUNT" -lt 1 ]; then
  echo "FAIL: no review_signoff obligation found for instance $INSTANCE_ID"
  echo "  → full_verify transition (which opens the obligation) has not been run"
  exit 1
fi

# ── PASS ──────────────────────────────────────────────────────────────────────
echo "PASS: T-04383 wrkq-code-change@1 adoption confirmed in $DB_PATH"
echo "  instance:       $INSTANCE_ID"
echo "  phase:          $PHASE"
echo "  workflow_runs:  $RUN_COUNT"
echo "  role_bindings:  $RB_COUNT (tester/implementer/reviewer)"
echo "  obligations:    $OBL_COUNT (review_signoff)"
exit 0
