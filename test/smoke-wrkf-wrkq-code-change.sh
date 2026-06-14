#!/usr/bin/env bash
# smoke-wrkf-wrkq-code-change.sh — RED test for T-04379 (wrkq-code-change workflow template)
#
# REDS until wrkf/templates/wrkq-code-change.workflow.json exists.
# When impl creates it, this smoke drives the full locked design (daedalus DM #7497):
#   open/intake → active/red → active/verify → active/review → closed/done
#   roles: implementer / tester / reviewer / system
#   SoD: 6 evidence-actor-pair constraints on sign_off; 2 on implement/full_verify
#   effects: set_task_state completed on sign_off
#
# Assertions (daedalus test matrix):
#   1. workflow validate + install succeed (this REDs now — file absent)
#   2. producibleBy negatives: wrong role cannot add each restricted evidence kind
#   3. SoD negatives: same-actor pairs and cross-role violations each block the right transition
#   4. Happy path: distinct-actor chain drives open/intake → closed/done end-to-end
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
export WRKQ_ACTOR="local-human"
unset ASP_PROJECT

"$BIN/wrkqadm" init --db "$DB" >/dev/null

# T-00001 = happy path   T-00002 = negatives (producibleBy + SoD)
"$BIN/wrkq" --db "$DB" --as local-human touch inbox/wrkq-cc-main -t "wrkq code-change happy path" >/dev/null
"$BIN/wrkq" --db "$DB" --as local-human touch inbox/wrkq-cc-neg  -t "wrkq code-change negatives"  >/dev/null

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
  | jq -e '.id == "wrkq-code-change"' >/dev/null

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
# § assertion 3: SoD negatives — same-actor pairs and cross-role violations
# Drive T-00002 to active/review so all four SoD blocks can be tested.
# ═══════════════════════════════════════════════════════════════════════════════

# author_red on T-00002 (tester)
"$BIN/wrkf" --db "$DB" --actor agent:tester --role tester \
  evidence add T-00002 --kind red_test --ref "git:neg-r0" \
  --facts '{"verdict":"red"}' --summary "neg red test" --json \
  | jq -e '.kind == "red_test"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --actor agent:tester \
  transition T-00002 author_red --expect-revision 0 --idempotency-key neg-author-red --json \
  | jq -e '.state.phase == "red"' >/dev/null

# SoD block 1: red_test + verify produced by same actor → implement must block
"$BIN/wrkf" --db "$DB" --actor agent:tester --role implementer \
  evidence add T-00002 --kind verify --ref "git:neg-v-sod" \
  --facts '{"verdict":"pass"}' --summary "verify by tester (SoD violation)" --json \
  | jq -e '.kind == "verify"' >/dev/null

if "$BIN/wrkf" --db "$DB" --role implementer --actor agent:implementer \
     transition T-00002 implement --idempotency-key neg-impl-sod1 --json >/dev/null 2>&1; then
  echo "FAIL: implement should block — red_test+verify produced by same actor (tester)" >&2; exit 1
fi

# fix: override verify with implementer actor (different from red_test actor)
"$BIN/wrkf" --db "$DB" --actor agent:implementer --role implementer \
  evidence add T-00002 --kind verify --ref "git:neg-v0" \
  --facts '{"verdict":"pass"}' --summary "verify by implementer" --json \
  | jq -e '.kind == "verify"' >/dev/null

"$BIN/wrkf" --db "$DB" --role implementer --actor agent:implementer \
  transition T-00002 implement --expect-revision 1 --idempotency-key neg-impl-ok --json \
  | jq -e '.state.phase == "verify"' >/dev/null

# SoD block 2: verify + verify_full produced by same actor → full_verify must block
"$BIN/wrkf" --db "$DB" --actor agent:implementer --role tester \
  evidence add T-00002 --kind verify_full --ref "git:neg-vf-sod" \
  --facts '{"verdict":"pass"}' --summary "verify_full by implementer (SoD violation)" --json \
  | jq -e '.kind == "verify_full"' >/dev/null

if "$BIN/wrkf" --db "$DB" --role tester --actor agent:tester \
     transition T-00002 full_verify --idempotency-key neg-fv-sod1 --json >/dev/null 2>&1; then
  echo "FAIL: full_verify should block — verify+verify_full produced by same actor (implementer)" >&2; exit 1
fi

# fix: override verify_full with tester actor (different from verify actor)
"$BIN/wrkf" --db "$DB" --actor agent:tester --role tester \
  evidence add T-00002 --kind verify_full --ref "git:neg-vf0" \
  --facts '{"verdict":"pass"}' --summary "verify_full by tester" --json \
  | jq -e '.kind == "verify_full"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --actor agent:tester \
  transition T-00002 full_verify --expect-revision 2 --idempotency-key neg-fv-ok --json \
  | jq -e '.state.phase == "review"' >/dev/null

# verify blocking review_signoff obligation opened by full_verify
OBL_NEG="$("$BIN/wrkf" --db "$DB" obligation list T-00002 --json \
  | jq -r '.obligations[] | select(.kind == "review_signoff" and .status == "open") | .id')"
test -n "$OBL_NEG"

# SoD block 3: installed_binary produced by implementer actor → sign_off must block
# (violates SoD pair: verify.actor == installed_binary.actor)
"$BIN/wrkf" --db "$DB" --actor agent:implementer --role reviewer \
  evidence add T-00002 --kind installed_binary --ref "bin:neg-sod3" \
  --facts '{"verdict":"pass","binary":"wrkf","cmd":"wrkf version"}' \
  --summary "installed_binary by implementer (SoD violation)" --json \
  | jq -e '.kind == "installed_binary"' >/dev/null

"$BIN/wrkf" --db "$DB" --actor agent:reviewer --role reviewer \
  evidence add T-00002 --kind review_signoff --ref "pr:neg-sod3" \
  --facts '{"verdict":"approved"}' --summary "signoff by reviewer" --json \
  | jq -e '.kind == "review_signoff"' >/dev/null

NEG_RS_EV="$("$BIN/wrkf" --db "$DB" evidence list T-00002 --json \
  | jq -r '.evidence | map(select(.kind=="review_signoff")) | last | .id')"
"$BIN/wrkf" --db "$DB" --role reviewer obligation satisfy T-00002 "$OBL_NEG" \
  --evidence "$NEG_RS_EV" --json | jq -e '.status == "satisfied"' >/dev/null

if "$BIN/wrkf" --db "$DB" --role reviewer --actor agent:reviewer \
     transition T-00002 sign_off --idempotency-key neg-so-sod3 --json >/dev/null 2>&1; then
  echo "FAIL: sign_off should block — installed_binary actor == verify actor (implementer)" >&2; exit 1
fi

# SoD block 4: review_signoff produced by tester actor → sign_off must block
# (violates SoD pair: verify_full.actor == review_signoff.actor)
# first fix installed_binary (reviewer actor, distinct from verify/verify_full/red_test)
"$BIN/wrkf" --db "$DB" --actor agent:reviewer --role reviewer \
  evidence add T-00002 --kind installed_binary --ref "bin:neg-sod4-fix" \
  --facts '{"verdict":"pass","binary":"wrkf","cmd":"wrkf version"}' \
  --summary "installed_binary by reviewer (fixed)" --json \
  | jq -e '.kind == "installed_binary"' >/dev/null

# now add review_signoff with tester actor (same as verify_full → SoD violation)
"$BIN/wrkf" --db "$DB" --actor agent:tester --role reviewer \
  evidence add T-00002 --kind review_signoff --ref "pr:neg-sod4" \
  --facts '{"verdict":"approved"}' --summary "signoff by tester (SoD violation)" --json \
  | jq -e '.kind == "review_signoff"' >/dev/null

if "$BIN/wrkf" --db "$DB" --role reviewer --actor agent:reviewer \
     transition T-00002 sign_off --idempotency-key neg-so-sod4 --json >/dev/null 2>&1; then
  echo "FAIL: sign_off should block — review_signoff actor == verify_full actor (tester)" >&2; exit 1
fi

# ═══════════════════════════════════════════════════════════════════════════════
# § assertion 4: happy path — T-00001 full lifecycle with distinct actors
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
"$BIN/wrkf" --db "$DB" --actor agent:tester --role tester \
  evidence add T-00001 --kind red_test --ref "git:sha-red-01" \
  --facts '{"verdict":"red"}' --summary "T-04379 red smoke committed" --json \
  | jq -e '.kind == "red_test" and .facts.verdict == "red"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --actor agent:tester \
  transition T-00001 author_red \
  --expect-revision 0 --idempotency-key author-red \
  --json | jq -e '.state.status == "active" and .state.phase == "red" and .revision == 1' >/dev/null

# ── transition 2: implement (implementer) active/red → active/verify ──────────
# SoD: verify.actor (implementer) ≠ red_test.actor (tester) ✓
"$BIN/wrkf" --db "$DB" --actor agent:implementer --role implementer \
  evidence add T-00001 --kind verify --ref "git:sha-green-01" \
  --facts '{"verdict":"pass"}' --summary "just verify green; builds clean" --json \
  | jq -e '.kind == "verify" and .facts.verdict == "pass"' >/dev/null

"$BIN/wrkf" --db "$DB" --role implementer --actor agent:implementer \
  transition T-00001 implement \
  --expect-revision 1 --idempotency-key implement \
  --json | jq -e '.state.phase == "verify" and .revision == 2' >/dev/null

# ── transition 3: full_verify (tester) active/verify → active/review ──────────
# SoD: verify_full.actor (tester) ≠ verify.actor (implementer) ✓
# opens blocking review_signoff obligation owned by reviewer
"$BIN/wrkf" --db "$DB" --actor agent:tester --role tester \
  evidence add T-00001 --kind verify_full --ref "git:sha-smoke-01" \
  --facts '{"verdict":"pass"}' --summary "just verify-full + smoke green" --json \
  | jq -e '.kind == "verify_full" and .facts.verdict == "pass"' >/dev/null

"$BIN/wrkf" --db "$DB" --role tester --actor agent:tester \
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
# SoD for sign_off — 6 pairs all distinct:
#   verify(implementer) ≠ review_signoff(reviewer) ✓
#   verify(implementer) ≠ installed_binary(reviewer) ✓
#   verify_full(tester)  ≠ review_signoff(reviewer) ✓
#   verify_full(tester)  ≠ installed_binary(reviewer) ✓
#   red_test(tester)     ≠ review_signoff(reviewer) ✓
#   red_test(tester)     ≠ installed_binary(reviewer) ✓
"$BIN/wrkf" --db "$DB" --actor agent:reviewer --role reviewer \
  evidence add T-00001 --kind installed_binary --ref "bin:wrkf@sha-01" \
  --facts '{"verdict":"pass","binary":"wrkf","cmd":"wrkf version"}' \
  --summary "installed wrkf; exercised wrkf workflow install" --json \
  | jq -e '.kind == "installed_binary"' >/dev/null

"$BIN/wrkf" --db "$DB" --actor agent:reviewer --role reviewer \
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
"$BIN/wrkf" --db "$DB" --role reviewer --actor agent:reviewer \
  transition T-00001 sign_off \
  --expect-revision 3 --idempotency-key sign-off \
  --json | jq -e '.state.status == "closed" and .state.phase == "done" and .revision == 4' >/dev/null

# set_task_state effect must be pending
"$BIN/wrkf" --db "$DB" effect list T-00001 --json \
  | jq -e '.effects[] | select(.kind == "set_task_state" and .status == "pending")' >/dev/null

EFF_ID="$("$BIN/wrkf" --db "$DB" effect list T-00001 --json \
  | jq -r '.effects[] | select(.kind == "set_task_state") | .id')"

# claim + ack the effect
CLAIM="$("$BIN/wrkf" --db "$DB" effect claim T-00001 --adapter smoke --limit 1 --lease-ms 30000 --json)"
TOKEN="$(jq -r '.leaseToken' <<<"$CLAIM")"
jq -e '.effects[0].status == "leased"' <<<"$CLAIM" >/dev/null

"$BIN/wrkf" --db "$DB" effect ack "$EFF_ID" --lease-token "$TOKEN" --json \
  | jq -e '.status == "delivered"' >/dev/null

# timeline: exactly 4 transition events
"$BIN/wrkf" --db "$DB" task timeline T-00001 --json \
  | jq -e '[.events[] | select(.kind == "transition")] | length == 4' >/dev/null

# no further actions (workflow closed)
"$BIN/wrkf" --db "$DB" next T-00001 --role reviewer --json \
  | jq -e '.actions | length == 0' >/dev/null

echo "smoke-wrkf-wrkq-code-change: ok"
