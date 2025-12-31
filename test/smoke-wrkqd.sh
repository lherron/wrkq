#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT/bin"

if [[ ! -x "$BIN_DIR/wrkqadm" || ! -x "$BIN_DIR/wrkqd" ]]; then
  echo "Missing binaries. Run: just build" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required for this smoke test." >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for JSON parsing in this smoke test." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d -t wrkqd-smoke-XXXXXX)"
PID1=""
PID2=""

cleanup() {
  if [[ -n "$PID1" ]]; then
    kill "$PID1" >/dev/null 2>&1 || true
    wait "$PID1" >/dev/null 2>&1 || true
  fi
  if [[ -n "$PID2" ]]; then
    kill "$PID2" >/dev/null 2>&1 || true
    wait "$PID2" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

free_port() {
  python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("", 0))
port = sock.getsockname()[1]
sock.close()
print(port)
PY
}

json_path() {
  local path="$1"
  python3 -c 'import json,sys; path=sys.argv[1].split("."); data=json.load(sys.stdin); 
for key in path:
    data = data[key]
print(json.dumps(data) if isinstance(data,(dict,list)) else data)' "$path"
}

do_request() {
  local method="$1"
  local base_url="$2"
  local path="$3"
  local body="${4:-}"
  local url="${base_url}${path}"
  local tmp
  tmp="$(mktemp)"
  local code
  if [[ "$method" == "GET" ]]; then
    code=$(curl -sS -o "$tmp" -w "%{http_code}" \
      -H "Content-Type: application/json" \
      -H "X-Wrkq-Actor: smoke-agent" \
      "$url")
  else
    if [[ -z "$body" ]]; then
      body="{}"
    fi
    code=$(curl -sS -o "$tmp" -w "%{http_code}" \
      -H "Content-Type: application/json" \
      -H "X-Wrkq-Actor: smoke-agent" \
      -X "$method" \
      -d "$body" \
      "$url")
  fi
  if [[ "$code" != "200" ]]; then
    echo "Request $method $path failed ($code): $(cat "$tmp")" >&2
    rm -f "$tmp"
    exit 1
  fi
  cat "$tmp"
  rm -f "$tmp"
}

wait_health() {
  local base_url="$1"
  for _ in {1..50}; do
    if do_request "GET" "$base_url" "/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  echo "wrkqd did not become healthy at $base_url" >&2
  exit 1
}

init_db() {
  local dir="$1"
  local db_name="$2"
  local db_path="$dir/$db_name"
  local attach_dir="$dir/attachments"
  "$BIN_DIR/wrkqadm" init \
    --db "$db_path" \
    --attach-dir "$attach_dir" \
    --human-slug "smoke-human" \
    --human-name "Smoke Human" \
    --agent-slug "smoke-agent" \
    --agent-name "Smoke Agent" >/dev/null
  echo "$db_path"
}

echo "Temp dir: $TMP_DIR"

DB1_DIR="$TMP_DIR/db1"
mkdir -p "$DB1_DIR"
DB1_PATH="$(init_db "$DB1_DIR" "wrkq1.db")"

PORT1="$(free_port)"
ADDR1="127.0.0.1:$PORT1"
BASE1="http://$ADDR1/v1"
"$BIN_DIR/wrkqd" --addr "$ADDR1" --db "$DB1_PATH" >/dev/null 2>&1 &
PID1=$!

wait_health "$BASE1"

do_request "POST" "$BASE1" "/containers/tree" "{}" >/dev/null
do_request "POST" "$BASE1" "/actors/list" "{}" >/dev/null

TASK_CREATE_JSON=$(do_request "POST" "$BASE1" "/tasks/create" "$(cat <<'JSON'
{
  "path": "inbox/smoke-task",
  "fields": {
    "title": "Smoke Task",
    "description": "Initial smoke test task",
    "priority": 2,
    "labels": ["smoke", "wrkqd"]
  }
}
JSON
)")

TASK_ID="$(echo "$TASK_CREATE_JSON" | json_path "task.id")"
TASK_UUID="$(echo "$TASK_CREATE_JSON" | json_path "task.uuid")"

TASK_LIST_JSON=$(do_request "POST" "$BASE1" "/tasks/list" "$(cat <<'JSON'
{
  "project": "inbox",
  "filter": "all",
  "limit": 10
}
JSON
)")

python3 -c 'import json,sys; task_uuid=sys.argv[1]; data=json.load(sys.stdin); 
assert any(t["uuid"] == task_uuid for t in data.get("tasks", []))' "$TASK_UUID" <<<"$TASK_LIST_JSON"

TASK_GET_JSON=$(do_request "POST" "$BASE1" "/tasks/get" "{\"selector\": \"$TASK_ID\"}")
python3 -c 'import json,sys; uuid=sys.argv[1]; data=json.load(sys.stdin); 
assert data["task"]["uuid"] == uuid' "$TASK_UUID" <<<"$TASK_GET_JSON"

do_request "POST" "$BASE1" "/tasks/update" "$(cat <<JSON
{
  "selector": "$TASK_ID",
  "fields": {
    "title": "Smoke Task Updated",
    "state": "in_progress"
  }
}
JSON
)" >/dev/null

do_request "POST" "$BASE1" "/comments/create" "{\"task\": \"$TASK_ID\", \"body\": \"Smoke test comment\"}" >/dev/null
COMMENTS_JSON=$(do_request "POST" "$BASE1" "/comments/list" "{\"task\": \"$TASK_ID\"}")
python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("comments"), "expected comments"' <<<"$COMMENTS_JSON"

TASK2_JSON=$(do_request "POST" "$BASE1" "/tasks/create" "$(cat <<'JSON'
{
  "path": "inbox/smoke-task-2",
  "fields": {
    "title": "Smoke Task 2",
    "description": "Second task"
  }
}
JSON
)")
TASK2_ID="$(echo "$TASK2_JSON" | json_path "task.id")"

do_request "POST" "$BASE1" "/relations/create" "$(cat <<JSON
{
  "from": "$TASK_ID",
  "kind": "relates_to",
  "to": "$TASK2_ID"
}
JSON
)" >/dev/null

REL_JSON=$(do_request "POST" "$BASE1" "/relations/list" "{\"task\": \"$TASK_ID\"}")
python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("relations"), "expected relations"' <<<"$REL_JSON"

do_request "POST" "$BASE1" "/relations/delete" "$(cat <<JSON
{
  "from": "$TASK_ID",
  "kind": "relates_to",
  "to": "$TASK2_ID"
}
JSON
)" >/dev/null

do_request "POST" "$BASE1" "/tasks/archive" "{\"selector\": \"$TASK_ID\"}" >/dev/null
do_request "POST" "$BASE1" "/tasks/restore" "{\"selector\": \"$TASK_ID\", \"state\": \"open\"}" >/dev/null

BUNDLE_DIR="$TMP_DIR/bundle"
BUNDLE_JSON=$(do_request "POST" "$BASE1" "/bundle/create" "$(cat <<JSON
{
  "out": "$BUNDLE_DIR",
  "actor": "smoke-agent",
  "project": "inbox",
  "include_refs": true,
  "with_events": false
}
JSON
)")

python3 -c 'import json,sys; expected=sys.argv[1]; data=json.load(sys.stdin); assert data["bundle_dir"] == expected' "$BUNDLE_DIR" <<<"$BUNDLE_JSON"

kill "$PID1" >/dev/null 2>&1 || true
wait "$PID1" >/dev/null 2>&1 || true
PID1=""

DB2_DIR="$TMP_DIR/db2"
mkdir -p "$DB2_DIR"
DB2_PATH="$(init_db "$DB2_DIR" "wrkq2.db")"

PORT2="$(free_port)"
ADDR2="127.0.0.1:$PORT2"
BASE2="http://$ADDR2/v1"
"$BIN_DIR/wrkqd" --addr "$ADDR2" --db "$DB2_PATH" >/dev/null 2>&1 &
PID2=$!

wait_health "$BASE2"

APPLY_JSON=$(do_request "POST" "$BASE2" "/bundle/apply" "$(cat <<JSON
{
  "from": "$BUNDLE_DIR",
  "dry_run": false,
  "continue_on_error": false
}
JSON
)")

python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("success") is True, data' <<<"$APPLY_JSON"

TASKS_AFTER_JSON=$(do_request "POST" "$BASE2" "/tasks/list" "$(cat <<'JSON'
{
  "project": "inbox",
  "filter": "all",
  "limit": 20
}
JSON
)")

python3 -c 'import json,sys; data=json.load(sys.stdin); assert data.get("tasks"), "expected tasks after bundle apply"' <<<"$TASKS_AFTER_JSON"

echo "wrkqd smoke test: PASS"
