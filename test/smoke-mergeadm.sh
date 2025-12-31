#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin"

if [[ ! -x "$BIN/wrkq" || ! -x "$BIN/wrkqadm" ]]; then
  echo "error: missing binaries in $BIN (run 'just build' first)" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

src_db="$tmpdir/src.db"
dest_db="$tmpdir/dest.db"
src_attach="$tmpdir/src-attach"
dest_attach="$tmpdir/dest-attach"

(
  cd "$tmpdir"
  "$BIN/wrkqadm" init --db "$src_db" --attach-dir "$src_attach" --human-slug smoke-human --agent-slug smoke-agent >/dev/null
  "$BIN/wrkqadm" init --db "$dest_db" --attach-dir "$dest_attach" --human-slug smoke-human --agent-slug smoke-agent >/dev/null
)

export WRKQ_DB_PATH="$src_db"
export WRKQ_ATTACH_DIR="$src_attach"
export WRKQ_ACTOR="smoke-human"

"$BIN/wrkq" mkdir proj >/dev/null
"$BIN/wrkq" mkdir proj/child >/dev/null
"$BIN/wrkq" touch proj/child/task-one -t "Task One" >/dev/null

note="$tmpdir/note.txt"
echo "hello" > "$note"
"$BIN/wrkq" attach put proj/child/task-one "$note" >/dev/null

report="$tmpdir/report.json"
"$BIN/wrkqadm" merge \
  --source "$src_db" \
  --dest "$dest_db" \
  --project proj \
  --path-prefix canonical \
  --source-attach-dir "$src_attach" \
  --dest-attach-dir "$dest_attach" \
  --as smoke-human \
  --report "$report" >/dev/null

python3 - <<PY
import json, os, sqlite3, sys

db_path = "$dest_db"
attach_dir = "$dest_attach"

conn = sqlite3.connect(db_path)
row = conn.execute("SELECT path FROM v_task_paths WHERE path = 'canonical/child/task-one'").fetchone()
if not row:
    sys.exit("expected task path not found in destination DB")

row = conn.execute("SELECT relative_path FROM attachments LIMIT 1").fetchone()
if not row:
    sys.exit("expected attachment metadata in destination DB")

rel = row[0]
abs_path = os.path.join(attach_dir, rel)
if not os.path.exists(abs_path):
    sys.exit(f"expected attachment file at {abs_path}")
PY

echo "wrkqadm merge smoke test: PASS"
