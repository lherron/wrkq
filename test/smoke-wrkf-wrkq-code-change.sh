#!/usr/bin/env bash
# smoke-wrkf-wrkq-code-change.sh — RED test for T-04379 (wrkq-code-change workflow template)
#
# REDS until wrkf/templates/wrkq-code-change.workflow.json exists.
# When impl creates it, this smoke drives the full locked design (daedalus DM #7497):
#   open/intake → active/red → active/verify → active/review → closed/done
#   roles: implementer / tester / reviewer / system
#   role gates: producibleBy restricts evidence kinds to tester / implementer / reviewer
#   effects: set_task_state completed on sign_off
#
# Assertions (daedalus test matrix):
#   1. workflow validate + install succeed (this REDs now — file absent)
#   2. producibleBy negatives: wrong role cannot add each restricted evidence kind
#   3. Happy path: distinct-principal chain drives open/intake → closed/done end-to-end
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

# T-00001 = happy path   T-00002 = negatives (producibleBy + SoD)
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/wrkq-cc-main -t "wrkq code-change happy path" >/dev/null
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/wrkq-cc-neg  -t "wrkq code-change negatives"  >/dev/null

TEMPLATE="$ROOT/wrkf/templates/wrkq-code-change.workflow.json"

# ── assertion 1: validate (REDS here — template file is absent) ──────────────
"$BIN/wrkf" --db "$DB" workflow validate "$TEMPLATE" --json \
  | jq -e '.valid == true' >/dev/null

# ── install + introspect ─────────────────────────────────────────────────────
"$BIN/wrkf" --db "$DB" workflow install "$TEMPLATE" --json \
  | jq -e '.id == "wrkq-code-change" and .version == "1"' >/dev/null

"$BIN/wrkf" --db "$DB" workflow list --json \
  | jq -e '[.templates[] | select(.id == "wrkq-code-change")] | length == 1' >/dev/null

"$BIN/wrkf" --db "$DB" workflow show wrkq-code-change@1 --json \
  | jq -e '.template.id == "wrkq-code-change"' >/dev/null

# ═══════════════════════════════════════════════════════════════════════════════
# § assertion 2: producibleBy negatives — wrong role must fail for each kind
# ═══════════════════════════════════════════════════════════════════════════════
"$BIN/wrkf" --db "$DB" task attach T-00002 --workflow wrkq-code-change@1 --json \
  | jq -e '.phase == "intake"' >/dev/null

# red_test (producibleBy: [tester]) — implementer role must fail
if "$BIN/wrkf" --db "$DB" --role implementer \
     evidence add T-00002 --kind red_test --ref "git:x" --summary "wrong role" \
     --facts '{"verdict":"red"}' --json >/dev/null 2>&1; then
  echo "FAIL: red_test addable with role=implementer (producibleBy=[tester])" >&2; exit 1
fi

# verify (producibleBy: [implementer]) — tester role must fail
if "$BIN/wrkf" --db "$DB" --role tester \
     evidence add T-00002 --kind verify --ref "git:x" --summary "wrong role" \
     --facts '{"verdict":"pass"}' --json >/dev/null 2>&1; then
  echo "FAIL: verify addable with role=tester (producibleBy=[implementer])" >&2; exit 1
fi

# verify_full (producibleBy: [tester]) — implementer role must fail
if "$BIN/wrkf" --db "$DB" --role implementer \
     evidence add T-00002 --kind verify_full --ref "git:x" --summary "wrong role" \
     --facts '{"verdict":"pass"}' --json >/dev/null 2>&1; then
  echo "FAIL: verify_full addable with role=implementer (producibleBy=[tester])" >&2; exit 1
fi

# installed_binary (producibleBy: [reviewer]) — tester role must fail
if "$BIN/wrkf" --db "$DB" --role tester \
     evidence add T-00002 --kind installed_binary --ref "bin:x" --summary "wrong role" \
     --facts '{"verdict":"pass","binary":"wrkf","cmd":"wrkf version"}' \
     --json >/dev/null 2>&1; then
  echo "FAIL: installed_binary addable with role=tester (producibleBy=[reviewer])" >&2; exit 1
fi

# review_signoff (producibleBy: [reviewer]) — implementer role must fail
if "$BIN/wrkf" --db "$DB" --role implementer \
     evidence add T-00002 --kind review_signoff --ref "pr:x" --summary "wrong role" \
     --facts '{"verdict":"approved"}' --json >/dev/null 2>&1; then
  echo "FAIL: review_signoff addable with role=implementer (producibleBy=[reviewer])" >&2; exit 1
fi

# ═══════════════════════════════════════════════════════════════════════════════
# § assertion 3: happy path — T-00001 full lifecycle with distinct principals
#   open/intake → active/red → active/verify → active/review → closed/done
# ═══════════════════════════════════════════════════════════════════════════════
"$BIN/wrkf" --db "$DB" task attach T-00001 --workflow wrkq-code-change@1 --json \
  | jq -e '.phase == "intake" and .revision == 0' >/dev/null

"$BIN/wrkf" --db "$DB" task inspect T-00001 --json \
  | jq -e '.templateId == "wrkq-code-change"' >/dev/null

"$BIN/wrkf" --db "$DB" task timeline T-00001 --json \
  | jq -e '.events | length == 1' >/dev/null

# tester: next should prompt collect_evidence (red_test)
"$BIN/wrkf" --db "$DB" next T-00001 --role tester --json \
  | jq -e '.actions[] | select(.kind == "collect_evidence")' >/dev/null

# ── transition 1: author_red (tester) open/intake → active/red ───────────────
"$BIN/wrkf" --db "$DB" --principal-ref agent:tester --role tester \
  evidence add T-00001 --kind red_test --ref "git:sha-red-01" \
  --facts '{"verdict":"red"}' --summary "T-04379 red smoke committed" --json \
  | jq -e '.kind == "red_test" and .facts.verdict == "red"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --principal-ref agent:tester \
  transition T-00001 author_red \
  --expect-revision 0 --idempotency-key author-red \
  --json | jq -e '.state.status == "active" and .state.phase == "red" and .revision == 1' >/dev/null

# ── transition 2: implement (implementer) active/red → active/verify ──────────
# distinct principal from red_test
"$BIN/wrkf" --db "$DB" --principal-ref agent:implementer --role implementer \
  evidence add T-00001 --kind verify --ref "git:sha-green-01" \
  --facts '{"verdict":"pass"}' --summary "just verify green; builds clean" --json \
  | jq -e '.kind == "verify" and .facts.verdict == "pass"' >/dev/null

"$BIN/wrkf" --db "$DB" --role implementer --principal-ref agent:implementer \
  transition T-00001 implement \
  --expect-revision 1 --idempotency-key implement \
  --json | jq -e '.state.phase == "verify" and .revision == 2' >/dev/null

# ── transition 3: full_verify (tester) active/verify → active/review ──────────
# distinct principal from verify
# opens blocking review_signoff obligation owned by reviewer
"$BIN/wrkf" --db "$DB" --principal-ref agent:tester --role tester \
  evidence add T-00001 --kind verify_full --ref "git:sha-smoke-01" \
  --facts '{"verdict":"pass"}' --summary "just verify-full + smoke green" --json \
  | jq -e '.kind == "verify_full" and .facts.verdict == "pass"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --principal-ref agent:tester \
  transition T-00001 full_verify \
  --expect-revision 2 --idempotency-key full-verify \
  --json | jq -e '.state.phase == "review" and .revision == 3' >/dev/null

# blocking review_signoff obligation must be open, owned by reviewer
OBL_ID="$("$BIN/wrkf" --db "$DB" obligation list T-00001 --json \
  | jq -r '.obligations[] | select(.kind == "review_signoff" and .status == "open") | .id')"
test -n "$OBL_ID"

# reviewer's next: sign_off blocked by open obligation
"$BIN/wrkf" --db "$DB" next T-00001 --role reviewer --json \
  | jq -e '.blockedTransitions[] | select(.id == "sign_off") | .blocksOn[] | select(.kind == "obligation")' >/dev/null

# reviewer supplies installed_binary + review_signoff
# Reviewer supplies both final evidence kinds required by sign_off.
"$BIN/wrkf" --db "$DB" --principal-ref agent:reviewer --role reviewer \
  evidence add T-00001 --kind installed_binary --ref "bin:wrkf@sha-01" \
  --facts '{"verdict":"pass","binary":"wrkf","cmd":"wrkf version"}' \
  --summary "installed wrkf; exercised wrkf workflow install" --json \
  | jq -e '.kind == "installed_binary"' >/dev/null

"$BIN/wrkf" --db "$DB" --principal-ref agent:reviewer --role reviewer \
  evidence add T-00001 --kind review_signoff --ref "pr:0001" \
  --facts '{"verdict":"approved"}' --summary "code review approved; LGTM" --json \
  | jq -e '.kind == "review_signoff"' >/dev/null

# satisfy the blocking review_signoff obligation
SIGNOFF_EV="$("$BIN/wrkf" --db "$DB" evidence list T-00001 --json \
  | jq -r '.evidence | map(select(.kind=="review_signoff")) | last | .id')"
"$BIN/wrkf" --db "$DB" --role reviewer obligation satisfy T-00001 "$OBL_ID" \
  --evidence "$SIGNOFF_EV" --json | jq -e '.status == "satisfied"' >/dev/null

# ── transition 4: sign_off (reviewer) active/review → closed/done ─────────────
# effect: set_task_state completed
"$BIN/wrkf" --db "$DB" --role reviewer --principal-ref agent:reviewer \
  transition T-00001 sign_off \
  --expect-revision 3 --idempotency-key sign-off \
  --json | jq -e '.state.status == "closed" and .state.phase == "done" and .revision == 4 and (.effects[] | select(.kind == "set_task_state" and .status == "delivered" and (.receipt.kind == "set_task_state.receipt")))' >/dev/null

# set_task_state is an engine-owned builtin effect and must be auto-delivered.
"$BIN/wrkf" --db "$DB" effect list T-00001 --json \
  | jq -e '.effects[] | select(.kind == "set_task_state" and .status == "delivered" and (.receipt.kind == "set_task_state.receipt"))' >/dev/null

"$BIN/wrkq" --db "$DB" cat T-00001 --json | jq -e '.[0].state == "completed"' >/dev/null

# timeline: exactly 4 transition events
"$BIN/wrkf" --db "$DB" task timeline T-00001 --json \
  | jq -e '[.events[] | select(.type == "workflow.transitioned")] | length == 4' >/dev/null

# no further actions (workflow closed)
"$BIN/wrkf" --db "$DB" next T-00001 --role reviewer --json \
  | jq -e '.actions | length == 0' >/dev/null

echo "smoke-wrkf-wrkq-code-change: ok"
