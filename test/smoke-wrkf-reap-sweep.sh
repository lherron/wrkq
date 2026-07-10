#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${WRKQ_SMOKE_BIN_DIR:-$HOME/.local/bin}"
KEEP_TMP="${WRKQ_SMOKE_KEEP_TMP:-0}"

for binary in wrkq wrkqadm wrkqd wrkf; do
  if [[ ! -x "$BIN_DIR/$binary" ]]; then
    echo "Missing installed $binary at $BIN_DIR/$binary; run: just install" >&2
    exit 1
  fi
done
for dependency in curl python3 sqlite3 shasum; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    echo "$dependency is required for this smoke test." >&2
    exit 1
  fi
done

TMP_DIR="$(mktemp -d -t wrkf-reap-sweep-XXXXXX)"
DB_PATH="$TMP_DIR/wrkq.db"
TRANSCRIPT="$TMP_DIR/reap-sweep-transcript.log"
DAEMON_LOG="$TMP_DIR/wrkqd.log"
DAEMON_PID=""

cleanup() {
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" >/dev/null 2>&1 || true
    wait "$DAEMON_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_TMP" == "1" ]]; then
    echo "SMOKE_ARTIFACTS=$TMP_DIR"
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

record() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$TRANSCRIPT"
}

fail() {
  record "FAIL $*"
  exit 1
}

free_port() {
  python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
port = sock.getsockname()[1]
sock.close()
print(port)
PY
}

json_path() {
  local path="$1"
  python3 -c '
import json, sys
data = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    data = data[int(key)] if key.isdigit() else data[key]
print(json.dumps(data) if isinstance(data, (dict, list)) else data)
' "$path"
}

assert_legal_candidate() {
  local json="$1"
  local instance_id="$2"
  local action="$3"
  python3 -c '
import json, sys
data = json.load(sys.stdin)
instance_id, action = sys.argv[1:]
candidates = data.get("candidates", [])
assert len(candidates) == 1, candidates
candidate = candidates[0]
assert candidate.get("instanceId") == instance_id, candidate
assert candidate.get("action") == action, candidate
assert not candidate.get("blocked", False), candidate
assert candidate.get("semanticActionKey"), candidate
' "$instance_id" "$action" <<<"$json"
}

wait_health() {
  local base_url="$1"
  for _ in {1..100}; do
    if curl -fsS "$base_url/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  fail "isolated wrkqd did not become healthy at $base_url; see $DAEMON_LOG"
}

wrkq_cmd() {
  WRKQ_DB="$DB_PATH" WRKQ_PRINCIPAL_REF="agent:smoke-agent" \
    "$BIN_DIR/wrkq" --db "$DB_PATH" --principal-ref "agent:smoke-agent" "$@"
}

wrkf_cmd() {
  WRKQ_DB="$DB_PATH" WRKF_PRINCIPAL_REF="agent:smoke-agent" \
    "$BIN_DIR/wrkf" --db "$DB_PATH" --json --principal-ref "agent:smoke-agent" "$@"
}

settle_claim() {
  local claim_json="$1"
  local facts="$2"
  local summary="$3"
  local run_id owner_token owner_generation
  run_id="$(json_path "binding.run.id" <<<"$claim_json")"
  owner_token="$(json_path "binding.authority.ownerToken" <<<"$claim_json")"
  owner_generation="$(json_path "binding.authority.ownerGeneration" <<<"$claim_json")"
  wrkf_cmd action settle "$run_id" \
    --result completed \
    --owner-token "$owner_token" \
    --owner-generation "$owner_generation" \
    --facts "$facts" \
    --summary "$summary"
}

WRKQ_VERSION_JSON="$("$BIN_DIR/wrkq" version --json)"
WRKF_VERSION_JSON="$("$BIN_DIR/wrkf" version --json)"
WRKQ_COMMIT="$(json_path commit <<<"$WRKQ_VERSION_JSON")"
WRKF_COMMIT="$(json_path commit <<<"$WRKF_VERSION_JSON")"
[[ "$WRKQ_COMMIT" == "$WRKF_COMMIT" ]] || fail "installed wrkq/wrkf commits differ: $WRKQ_COMMIT vs $WRKF_COMMIT"
WRKQD_SHA256="$(shasum -a 256 "$BIN_DIR/wrkqd" | awk '{print $1}')"
record "installed_bundle commit=$WRKQ_COMMIT wrkqd_path=$BIN_DIR/wrkqd wrkqd_sha256=$WRKQD_SHA256"

"$BIN_DIR/wrkqadm" init \
  --db "$DB_PATH" \
  --attach-dir "$TMP_DIR/attachments" \
  --human-slug "smoke-human" \
  --human-name "Smoke Human" \
  --agent-slug "smoke-agent" \
  --agent-name "Smoke Agent" >/dev/null

PORT="$(free_port)"
ADDR="127.0.0.1:$PORT"
BASE_URL="http://$ADDR/v1"
"$BIN_DIR/wrkqd" --addr "$ADDR" --db "$DB_PATH" >"$DAEMON_LOG" 2>&1 &
DAEMON_PID=$!
record "isolated_daemon_started addr=$ADDR port=$PORT pid=$DAEMON_PID shared_port=7171_untouched"
wait_health "$BASE_URL"

# Seed inside inbox first so the smoke exercises the project-root path used by runners.
wrkq_cmd --project inbox mkdir reap-sweep >/dev/null
TASK_JSON="$(wrkq_cmd --project inbox touch reap-sweep/reap-sweep-task --title "Reap Sweep Smoke" --specification "Prove scheduled reap recovery." --json)"
TASK_ID="$(json_path "0.id" <<<"$TASK_JSON")"
record "seeded_task project=inbox task=$TASK_ID"

wrkf_cmd workflow install "$ROOT/internal/workflow/builtins/wrkq-simple-task-v2.workflow.json" >/dev/null
ATTACH_JSON="$(wrkf_cmd task attach "$TASK_ID" --workflow "wrkq-simple-task@2")"
INSTANCE_ID="$(json_path "id" <<<"$ATTACH_JSON")"
record "attached_builtin_v2 task=$TASK_ID instance=$INSTANCE_ID"

INITIAL_CLAIM="$(wrkf_cmd action claim "$TASK_ID" --runner-id "smoke-dead-runner" --agent-ref "agent:smoke-agent" --action triage --lease-ms 300000)"
INITIAL_RUN_ID="$(json_path "binding.run.id" <<<"$INITIAL_CLAIM")"
record "claimed_live_runner run=$INITIAL_RUN_ID instance=$INSTANCE_ID status=active"

# The only direct mutation is on this isolated database; no reap command is invoked.
sqlite3 "$DB_PATH" "UPDATE workflow_runs SET lease_expires_at = '2000-01-01T00:00:00Z' WHERE id = '$INITIAL_RUN_ID';"
record "forced_expired_lease run=$INITIAL_RUN_ID database=$DB_PATH"
MANUAL_REAP_INVOCATIONS="$( { grep -E '(^|[[:space:]])action[[:space:]]+reap([[:space:]]|$)' "${BASH_SOURCE[0]}" || true; } | wc -l | tr -d ' ')"
[[ "$MANUAL_REAP_INVOCATIONS" == "0" ]] || fail "smoke script must not invoke manual reaping"
record "manual_reap_invocations=$MANUAL_REAP_INVOCATIONS"

TERMINAL_STATUS=""
for attempt in $(seq 1 90); do
  RUN_JSON="$(wrkf_cmd action show "$INITIAL_RUN_ID")"
  RUN_STATUS="$(json_path "status" <<<"$RUN_JSON")"
  record "poll attempt=$attempt run=$INITIAL_RUN_ID status=$RUN_STATUS"
  case "$RUN_STATUS" in
    active)
      sleep 1
      ;;
    failed|operator_required)
      TERMINAL_STATUS="$RUN_STATUS"
      break
      ;;
    *)
      fail "unexpected scheduled-sweep terminal status: $RUN_STATUS"
      ;;
  esac
done
[[ -n "$TERMINAL_STATUS" ]] || fail "scheduled sweep did not settle run within 90 seconds"
record "auto_settle run=$INITIAL_RUN_ID status=$TERMINAL_STATUS source=scheduled_sweep"

NEXT_TRIAGE_JSON="$(wrkf_cmd action next --instance-id "$INSTANCE_ID")"
assert_legal_candidate "$NEXT_TRIAGE_JSON" "$INSTANCE_ID" "triage"
TRIAGE_KEY="$(json_path "candidates.0.semanticActionKey" <<<"$NEXT_TRIAGE_JSON")"
record "ordinary_next instance=$INSTANCE_ID action=triage semantic_key=$TRIAGE_KEY"
RESUMED_TRIAGE="$(wrkf_cmd action claim --instance-id "$INSTANCE_ID" --semantic-action-key "$TRIAGE_KEY" --runner-id "smoke-resumed-triage" --agent-ref "agent:smoke-agent" --lease-ms 300000)"
settle_claim "$RESUMED_TRIAGE" '{"result":"ready"}' "triaged after scheduled reap" >/dev/null

NEXT_IMPLEMENT_JSON="$(wrkf_cmd action next --instance-id "$INSTANCE_ID")"
assert_legal_candidate "$NEXT_IMPLEMENT_JSON" "$INSTANCE_ID" "implement"
IMPLEMENT_KEY="$(json_path "candidates.0.semanticActionKey" <<<"$NEXT_IMPLEMENT_JSON")"
record "ordinary_next instance=$INSTANCE_ID action=implement semantic_key=$IMPLEMENT_KEY"
IMPLEMENT_CLAIM="$(wrkf_cmd action claim --instance-id "$INSTANCE_ID" --semantic-action-key "$IMPLEMENT_KEY" --runner-id "smoke-resumed-implement" --agent-ref "agent:smoke-agent" --lease-ms 300000)"
IMPLEMENT_SETTLE="$(settle_claim "$IMPLEMENT_CLAIM" '{"result":"done","commit.sha":"smoke-reap-sweep","change.id":"change-v1:smoke-reap-sweep","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}' "implemented after scheduled reap")"
IMPLEMENT_EVIDENCE_ID="$(json_path "evidence.id" <<<"$IMPLEMENT_SETTLE")"

NEXT_VERIFY_JSON="$(wrkf_cmd action next --instance-id "$INSTANCE_ID")"
assert_legal_candidate "$NEXT_VERIFY_JSON" "$INSTANCE_ID" "verify"
VERIFY_KEY="$(json_path "candidates.0.semanticActionKey" <<<"$NEXT_VERIFY_JSON")"
record "ordinary_next instance=$INSTANCE_ID action=verify semantic_key=$VERIFY_KEY"
VERIFY_CLAIM="$(wrkf_cmd action claim --instance-id "$INSTANCE_ID" --semantic-action-key "$VERIFY_KEY" --runner-id "smoke-resumed-verify" --agent-ref "agent:smoke-agent" --lease-ms 300000)"
settle_claim "$VERIFY_CLAIM" "{\"result\":\"verified\",\"source.evidence_id\":\"$IMPLEMENT_EVIDENCE_ID\",\"source.commit.sha\":\"smoke-reap-sweep\",\"verified.commit.sha\":\"smoke-reap-sweep\",\"verified.change.id\":\"change-v1:smoke-reap-sweep\",\"context.id\":\"context-v1:smoke-reap-sweep\",\"git.clean\":true}" "verified after scheduled reap" >/dev/null

INSPECT_JSON="$(wrkf_cmd task inspect "$TASK_ID")"
INSTANCE_STATUS="$(json_path "status" <<<"$INSPECT_JSON")"
INSTANCE_PHASE="$(json_path "phase" <<<"$INSPECT_JSON")"
[[ "$INSTANCE_STATUS" == "closed" && "$INSTANCE_PHASE" == "done" ]] || fail "resumed instance state is $INSTANCE_STATUS/$INSTANCE_PHASE, want closed/done"
record "resume_to_done instance=$INSTANCE_ID status=$INSTANCE_STATUS phase=$INSTANCE_PHASE"
record "PASS transcript=$TRANSCRIPT daemon_log=$DAEMON_LOG port=$PORT pid=$DAEMON_PID"
