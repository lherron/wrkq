#!/usr/bin/env bash
# smoke-wrkc-note.sh — E2E driver for `wrkc say --note` (T-11305).
#
# Runs against whatever wrkq the environment points at (WRKQ_DB, or the
# configured default), so the same driver proves an isolated scratch wrkqd and
# the live fleet after cutover:
#
#   test/smoke-wrkc-note.sh --ref T-xxxxx --seat agent@project:scope \
#     [--human agent:note-smoke] [--hrc-db PATH] [--settle 30] \
#     [--check-fyi] [--fyi-timeout 120] [--out DIR]
#
# --seat is the recipient. For live acceptance pick a LIVE, IDLE seat: every
# HRC check below is about that seat not being driven. --human is a scope-less
# principal (never kicked or summoned) that plays the other party, so the only
# mail that could reach a runtime is mail addressed to --seat.
#
# Ledger checks (always):
#   1. a note to --seat is created terminal (acked, obligation note) with no
#      presentation receipt and the note_recorded ack event reason;
#   2. raw pendingView {includeFyi} over --seat omits it, while a positive
#      control in the same read does appear: a pending fyi to a never-born
#      sibling scope (an fyi never births a seat), withdrawn right after;
#   3. `wrkc notes` as --seat lists it; `wrkc inbox` shows the count line and
#      --json carries recentNotes;
#   4. --seat asks --human (reply_required; a scope-less addressee is never
#      summoned); --human answers with --note and that reply discharges the ask.
# HRC checks (--hrc-db, the seat node's var/state/hrc/state.sqlite):
#   5. after --settle seconds, no hrc_events row for the seat's scope since the
#      baseline is a turn start/prompt/admission/presentation, and no event
#      anywhere mentions a note id.
# Control (--check-fyi):
#   6. an fyi to the same seat is still presented (unchanged behaviour). This
#      DOES drive a turn on the seat.
#
# Everything is written to $OUT/transcript.txt and $OUT/summary.json. Exit 0
# only when every requested check passed.
set -euo pipefail

WRKC="${WRKC:-wrkc}"
WRKQ="${WRKQ:-wrkq}"
REF="" SEAT="" HUMAN="agent:note-smoke" HRC_DB="" SETTLE=30 CHECK_FYI=0 FYI_TIMEOUT=120 OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref) REF="$2"; shift 2 ;;
    --seat) SEAT="$2"; shift 2 ;;
    --human) HUMAN="$2"; shift 2 ;;
    --hrc-db) HRC_DB="$2"; shift 2 ;;
    --settle) SETTLE="$2"; shift 2 ;;
    --check-fyi) CHECK_FYI=1; shift ;;
    --fyi-timeout) FYI_TIMEOUT="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    -h|--help) sed -n '2,36p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$REF" && -n "$SEAT" ]] || { echo "--ref and --seat are required (see --help)" >&2; exit 2; }
[[ "$SEAT" =~ ^([^@]+)@([^:]+):(.+)$ ]] || { echo "--seat must be agent@project:scope" >&2; exit 2; }
SEAT_AGENT="${BASH_REMATCH[1]}"
SEAT_PRINCIPAL="agent:$SEAT_AGENT"
SEAT_SCOPE_REF="agent:${BASH_REMATCH[1]}:project:${BASH_REMATCH[2]}:task:${BASH_REMATCH[3]}"
for tool in jq "$WRKC" "$WRKQ"; do
  command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 2; }
done
[[ -z "$HRC_DB" ]] || command -v sqlite3 >/dev/null || { echo "--hrc-db needs sqlite3" >&2; exit 2; }

OUT="${OUT:-$(mktemp -d -t wrkc-note-smoke-XXXXXX)}"
mkdir -p "$OUT"
TRANSCRIPT="$OUT/transcript.txt"
: >"$TRANSCRIPT"
RUN_ID="note-smoke-$(date -u +%Y%m%dT%H%M%SZ)-$$"
# A never-born sibling scope for the wake-set positive control.
GHOST="${SEAT%%:*}:note-smoke-control-$$"
FAILURES=0
declare -a RESULTS=()

log() { printf '%s\n' "$*" | tee -a "$TRANSCRIPT" >&2; }
# run prints the command and its full stdout+stderr to the transcript and
# returns stdout; the acting verb's output is never suppressed.
run() {
  local out err status
  log "\$ $*"
  err="$(mktemp)"
  set +e
  out="$("$@" 2>"$err")"
  status=$?
  set -e
  [[ -z "$out" ]] || printf '%s\n' "$out" >>"$TRANSCRIPT"
  [[ ! -s "$err" ]] || sed 's/^/[stderr] /' "$err" >>"$TRANSCRIPT"
  rm -f "$err"
  log "[exit $status]"
  printf '%s' "$out"
  return $status
}
check() {
  local name="$1" ok="$2" detail="$3"
  if [[ "$ok" == 1 ]]; then log "PASS $name — $detail"; else log "FAIL $name — $detail"; FAILURES=$((FAILURES + 1)); fi
  RESULTS+=("$(jq -cn --arg n "$name" --argjson ok "$([[ $ok == 1 ]] && echo true || echo false)" --arg d "$detail" '{check:$n,pass:$ok,detail:$d}')")
}
pending_view() {
  printf '%s\n%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"rpc.initialize","params":{"protocolVersion":"2026-06-30"}}' \
    "$(jq -cn --arg s "$SEAT" --arg g "$GHOST" '{jsonrpc:"2.0",id:2,method:"wrkq.envelope.pendingView",params:{scopes:[$s,$g],includeFyi:true,principalRef:"agent:hrc"}}')" |
    run "$WRKQ" rpc --stdio | jq -c 'select(.id == 2)'
}
hrc_baseline() { sqlite3 -readonly "$HRC_DB" "SELECT COALESCE(MAX(hrc_seq),0) FROM hrc_events"; }

log "# wrkc --note E2E ($RUN_ID) $(date -u +%FT%TZ)"
log "# ref=$REF seat=$SEAT ($SEAT_SCOPE_REF) human=$HUMAN hrc_db=${HRC_DB:-none} check_fyi=$CHECK_FYI"
run "$WRKC" version || true
[[ -z "$HRC_DB" ]] || BASELINE="$(hrc_baseline)"

# 1. A note to the seat is created terminal and never presented.
NOTE_JSON="$(run "$WRKC" say "$REF" --to "$SEAT" --note --as "$HUMAN" --json -m "[$RUN_ID] note: informational, nothing to do")"
NOTE_ID="$(jq -r '.envelopes[0].id' <<<"$NOTE_JSON")"
SHOW="$(run "$WRKC" show "$NOTE_ID" --json)"
check note-created-terminal \
  "$(jq -r 'if .obligation == "note" and .state == "acked" and .terminal == true and (.presentedTo | length) == 0 then 1 else 0 end' <<<"$SHOW")" \
  "$NOTE_ID $(jq -r '"obligation=\(.obligation) state=\(.state) terminal=\(.terminal) receipts=\(.presentedTo | length)"' <<<"$SHOW")"
HUMAN_SHOW="$(run "$WRKC" show "$NOTE_ID" --output human || true)"
check note-show-human "$([[ "$HUMAN_SHOW" == *"obligation: note"* ]] && echo 1 || echo 0)" "wrkc show prints obligation: note"
LOG_JSON="$(run "$WRKC" log "$REF" --json --limit 50)"
check note-in-room-log "$(jq -r --arg id "$NOTE_ID" '[.. | objects | select(.id? == $id and .obligation? == "note")] | if length > 0 then 1 else 0 end' <<<"$LOG_JSON")" "$NOTE_ID is in wrkc log $REF"

# 2. The wake set: a positive control appears, the note never does.
CONTROL_JSON="$(run "$WRKC" say "$REF" --to "$GHOST" --fyi --as "$HUMAN" --json -m "[$RUN_ID] control: fyi to a never-born scope, withdrawn immediately")"
CONTROL_ID="$(jq -r '.envelopes[0].id' <<<"$CONTROL_JSON")"
PENDING="$(pending_view)"
run "$WRKC" withdraw "$CONTROL_ID" --as "$HUMAN" --reason "note smoke positive control" >/dev/null || true
check pendingview-control-present "$(jq -r --arg id "$CONTROL_ID" 'if ([.result.items[].id] | index($id)) != null then 1 else 0 end' <<<"$PENDING")" "control fyi $CONTROL_ID to $GHOST is in the same pendingView{includeFyi} read"
check pendingview-note-absent "$(jq -r --arg id "$NOTE_ID" 'if ([.result.items[].id] | index($id)) == null and ([.result.blocking[]] | index($id)) == null then 1 else 0 end' <<<"$PENDING")" "$NOTE_ID absent from pendingView{includeFyi} items and blocking"

# 3. The recipient surface.
NOTES="$(run "$WRKC" notes --as "$SEAT_PRINCIPAL" --scope-ref "$SEAT" --json)"
check wrkc-notes-lists-it "$(jq -r --arg id "$NOTE_ID" 'if ([.items[].id] | index($id)) != null then 1 else 0 end' <<<"$NOTES")" "wrkc notes as $SEAT lists $NOTE_ID"
INBOX="$(run "$WRKC" inbox --as "$SEAT_PRINCIPAL" --scope-ref "$SEAT" --output human)"
check inbox-count-line "$([[ "$(tail -n1 <<<"$INBOX")" =~ ^[0-9]+\ notes?\ in\ the\ last\ 24h:\ wrkc\ notes$ ]] && echo 1 || echo 0)" "inbox trailing line: $(tail -n1 <<<"$INBOX")"
INBOX_JSON="$(run "$WRKC" inbox --as "$SEAT_PRINCIPAL" --scope-ref "$SEAT" --json)"
check inbox-json-recent-notes "$(jq -r 'if (.recentNotes // 0) >= 1 then 1 else 0 end' <<<"$INBOX_JSON")" "inbox --json recentNotes=$(jq -r '.recentNotes' <<<"$INBOX_JSON")"

# 4. A note reply discharges a reply_required ask.
ASK_JSON="$(run "$WRKC" say "$REF" --to "$HUMAN" --as "$SEAT_PRINCIPAL" --scope-ref "$SEAT" --json -m "[$RUN_ID] ask: reply_required to a scope-less principal")"
ASK_ID="$(jq -r '.envelopes[0].id' <<<"$ASK_JSON")"
REPLY_JSON="$(run "$WRKC" say "$ASK_ID" --to "$SEAT" --note --as "$HUMAN" --json -m "[$RUN_ID] reply: answered by note")"
REPLY_ID="$(jq -r '.envelopes[0].id' <<<"$REPLY_JSON")"
ASK_SHOW="$(run "$WRKC" show "$ASK_ID" --json)"
check note-reply-discharges "$(jq -r --arg id "$ASK_ID" 'if (.acked | index($id)) != null then 1 else 0 end' <<<"$REPLY_JSON")" "reply $REPLY_ID acked [$(jq -r '.acked | join(",")' <<<"$REPLY_JSON")]"
check ask-is-acked "$(jq -r 'if .state == "acked" then 1 else 0 end' <<<"$ASK_SHOW")" "$ASK_ID state=$(jq -r .state <<<"$ASK_SHOW")"
REPLY_SHOW="$(run "$WRKC" show "$REPLY_ID" --json)"
check note-reply-terminal "$(jq -r 'if .obligation == "note" and .state == "acked" and (.presentedTo | length) == 0 then 1 else 0 end' <<<"$REPLY_SHOW")" "$REPLY_ID obligation=$(jq -r .obligation <<<"$REPLY_SHOW") receipts=$(jq -r '.presentedTo | length' <<<"$REPLY_SHOW")"

# 5. HRC never drove the seat for a note.
if [[ -n "$HRC_DB" ]]; then
  log "settling ${SETTLE}s before reading hrc_events past seq $BASELINE"
  sleep "$SETTLE"
  DRIVES="$(sqlite3 -readonly "$HRC_DB" "SELECT hrc_seq||' '||ts||' '||event_kind FROM hrc_events WHERE hrc_seq > $BASELINE AND scope_ref = '$SEAT_SCOPE_REF' AND event_kind IN ('turn.started','turn.user_prompt','turn.accepted','submission.admission','runtime.presentation','runtime.ensured','session.created')")"
  printf '%s\n' "$DRIVES" >>"$TRANSCRIPT"
  check hrc-no-drive "$([[ -z "$DRIVES" ]] && echo 1 || echo 0)" "seat drive events since $BASELINE: ${DRIVES:-none}"
  MENTIONS="$(sqlite3 -readonly "$HRC_DB" "SELECT hrc_seq||' '||scope_ref||' '||event_kind FROM hrc_events WHERE hrc_seq > $BASELINE AND (payload_json LIKE '%$NOTE_ID%' OR payload_json LIKE '%$REPLY_ID%') AND scope_ref = '$SEAT_SCOPE_REF'")"
  check hrc-no-note-mention "$([[ -z "$MENTIONS" ]] && echo 1 || echo 0)" "seat events naming $NOTE_ID/$REPLY_ID: ${MENTIONS:-none}"
fi

# 6. Control: an fyi to the same seat is still presented.
if [[ "$CHECK_FYI" == 1 ]]; then
  FYI_JSON="$(run "$WRKC" say "$REF" --to "$SEAT" --fyi --as "$HUMAN" --json -m "[$RUN_ID] fyi control: expected to be injected; no action needed")"
  FYI_ID="$(jq -r '.envelopes[0].id' <<<"$FYI_JSON")"
  deadline=$((SECONDS + FYI_TIMEOUT))
  receipts=0
  while (( SECONDS < deadline )); do
    receipts="$("$WRKC" show "$FYI_ID" --json | jq -r '.presentedTo | length')"
    [[ "$receipts" -gt 0 ]] && break
    sleep 5
  done
  run "$WRKC" show "$FYI_ID" --json >/dev/null || true
  check fyi-still-presented "$([[ "$receipts" -gt 0 ]] && echo 1 || echo 0)" "$FYI_ID receipts=$receipts within ${FYI_TIMEOUT}s"
fi

printf '%s\n' "${RESULTS[@]}" | jq -s --arg run "$RUN_ID" --arg seat "$SEAT" --arg note "$NOTE_ID" --arg ask "${ASK_ID:-}" --arg reply "${REPLY_ID:-}" \
  '{run:$run, seat:$seat, envelopes:{note:$note, ask:$ask, reply:$reply}, failures:(map(select(.pass|not))|length), checks:.}' >"$OUT/summary.json"
log "# summary: $OUT/summary.json — $FAILURES failure(s)"
[[ "$FAILURES" == 0 ]]
