#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

DB="$TMPDIR/wrkq.db"
BIN="$TMPDIR/bin"
mkdir -p "$BIN"

go build -tags sqlite_fts5 -o "$BIN/wrkq" "$ROOT/cmd/wrkq"
go build -tags sqlite_fts5 -o "$BIN/wrkqadm" "$ROOT/cmd/wrkqadm"
go build -tags sqlite_fts5 -o "$BIN/wrkf" "$ROOT/cmd/wrkf"

cd "$TMPDIR"
export WRKQ_DB_PATH="$DB"
export WRKQ_ACTOR="local-human"
unset ASP_PROJECT

"$BIN/wrkqadm" init --db "$DB" >/dev/null
"$BIN/wrkq" --db "$DB" --as local-human touch inbox/wrkf-rpc-smoke -t "wrkf rpc smoke" >/dev/null

cat >"$TMPDIR/hook.sh" <<'HOOK'
#!/usr/bin/env bash
set -euo pipefail
jq -e '.transition.id == "finish"' >/dev/null
HOOK
chmod +x "$TMPDIR/hook.sh"

cat >"$TMPDIR/hooks.json" <<HOOKS
{
  "schemaVersion": "wrkf.hook-catalog.v0",
  "hooks": {
    "finish_hook": {
      "kind": "check",
      "argv": ["$TMPDIR/hook.sh"],
      "stdin": "json",
      "stdout": "exit_code",
      "timeoutMs": 300000,
      "cwd": "template_dir",
      "env": { "allow": ["PATH"] },
      "maxStdoutBytes": 65536,
      "maxStderrBytes": 65536
    }
  }
}
HOOKS

cat >"$TMPDIR/workflow.json" <<'FLOW'
{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "smoke_flow",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "plan" },
  "roles": {
    "coordinator": { "description": "Coordinates the smoke workflow" },
    "supervisor": { "description": "Handles recovery" }
  },
  "states": [
    { "status": "active", "phase": "plan" },
    { "status": "active", "phase": "done" },
    { "status": "active", "phase": "error" },
    { "status": "closed", "outcome": "completed" }
  ],
  "evidenceKinds": {
    "implementation": {
      "description": "Implementation proof",
      "facts": {
        "required": ["verdict"],
        "properties": {
          "verdict": {
            "type": "string",
            "enum": ["ready", "needs_patch"]
          }
        }
      }
    }
  },
  "obligationKinds": {
    "cleanup": { "description": "Cleanup duty" }
  },
  "checks": {
    "finish_check": {
      "type": "hook",
      "hookId": "finish_hook",
      "exitMap": {
        "0": { "verdict": "pass", "outcome": "ok" },
        "*": { "verdict": "error", "outcome": "failed" }
      }
    }
  },
  "transitions": [
    {
      "id": "plan_ready",
      "from": { "status": "active", "phase": "plan" },
      "by": ["coordinator"],
      "requires": [{ "evidence": { "kind": "implementation", "facts": { "verdict": "ready" } } }],
      "outcomes": [
        {
          "id": "ready",
          "when": { "always": true },
          "to": { "status": "active", "phase": "done" },
          "obligations": [
            { "kind": "cleanup", "ownerRole": "coordinator", "blocking": true, "reason": "cleanup before close" }
          ],
          "effects": [
            { "kind": "wake_role", "role": "coordinator", "reason": "ready to finish" }
          ]
        }
      ]
    },
    {
      "id": "finish",
      "from": { "status": "active", "phase": "done" },
      "by": ["coordinator"],
      "checks": ["finish_check"],
      "outcomes": [
        {
          "id": "finished",
          "when": { "checkVerdict": { "check": "finish_check", "is": "pass" } },
          "to": { "status": "closed", "outcome": "completed" }
        },
        {
          "id": "failed",
          "when": { "otherwise": true },
          "to": { "status": "active", "phase": "error" }
        }
      ]
    }
  ]
}
FLOW

python3 - "$BIN/wrkf" "$DB" "$TMPDIR/hooks.json" "$TMPDIR/workflow.json" <<'PY'
import json
import subprocess
import sys

bin_wrkf, db_path, hooks_path, workflow_path = sys.argv[1:5]

proc = subprocess.Popen(
    [
        bin_wrkf,
        "--db",
        db_path,
        "--hook-catalog",
        hooks_path,
        "--actor",
        "human:local-human",
        "--role",
        "coordinator",
        "rpc",
        "--stdio",
    ],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
    bufsize=1,
)

seq = 0


def rpc(method, params=None, expect_error=None):
    global seq
    seq += 1
    req_id = f"req_{seq}"
    frame = {"jsonrpc": "2.0", "id": req_id, "method": method}
    if params is not None:
        frame["params"] = params
    proc.stdin.write(json.dumps(frame, separators=(",", ":")) + "\n")
    proc.stdin.flush()
    line = proc.stdout.readline()
    if not line:
        err = proc.stderr.read()
        raise AssertionError(f"no response for {method}; stderr={err!r}")
    resp = json.loads(line)
    assert resp["jsonrpc"] == "2.0", resp
    assert resp["id"] == req_id, resp
    if expect_error:
        data = resp.get("error", {}).get("data", {})
        assert data.get("code") == expect_error, resp
        assert isinstance(data.get("retryable"), bool), resp
        return resp["error"]
    if "error" in resp:
        raise AssertionError(f"{method} failed: {resp}")
    return resp["result"]


init = rpc(
    "wrkf.initialize",
    {"protocolVersion": "2026-06-01", "client": {"name": "smoke", "version": "0"}},
)
assert init["protocolVersion"] == "2026-06-01", init
assert init["database"]["path"] == db_path, init
assert init["capabilities"]["effectClaimLease"] is True, init
assert init["schemaHash"].startswith("sha256:"), init

valid = rpc("wrkf.workflow.validate", {"path": workflow_path})
assert valid["valid"] is True, valid

installed = rpc("wrkf.workflow.install", {"path": workflow_path})
assert installed["id"] == "smoke_flow" and installed["version"] == "1", installed

listed = rpc("wrkf.workflow.list")
assert len(listed["templates"]) == 1, listed

shown = rpc("wrkf.workflow.show", {"ref": "smoke_flow@1"})
assert shown["template"]["id"] == "smoke_flow", shown

diff = rpc("wrkf.workflow.diff", {"oldPath": workflow_path, "newPath": workflow_path})
assert diff["sameHash"] is True, diff

attached = rpc("wrkf.task.attach", {"task": "T-00001", "workflow": "smoke_flow@1"})
assert attached["phase"] == "plan" and attached["revision"] == 0, attached

inspect = rpc("wrkf.task.inspect", {"task": "T-00001"})
assert inspect["templateId"] == "smoke_flow", inspect

timeline = rpc("wrkf.task.timeline", {"task": "T-00001"})
assert len(timeline) == 1, timeline

refreshed = rpc("wrkf.task.refresh", {"task": "T-00001"})
assert refreshed["taskDocHash"].startswith("sha256:"), refreshed

synced = rpc("wrkf.task.syncMeta", {"task": "T-00001"})
assert synced["updated"] == 1, synced

preflight = rpc("wrkf.check.preflight", {"task": "T-00001", "transition": "plan_ready"})
assert any(b["id"] == "plan_ready" for b in preflight["blockedTransitions"]), preflight

next_action = rpc("wrkf.next", {"task": "T-00001"})
assert any(a["kind"] == "collect_evidence" for a in next_action["actions"]), next_action

bad_evidence = rpc(
    "wrkf.evidence.add",
    {"task": "T-00001", "kind": "implementation", "ref": "git:bad", "summary": "missing facts"},
    expect_error="WRKF_VALIDATION",
)
assert bad_evidence["code"] == -32602, bad_evidence

needs_patch = rpc(
    "wrkf.evidence.add",
    {
        "task": "T-00001",
        "kind": "implementation",
        "ref": "git:abc123",
        "summary": "needs patch",
        "facts": {"verdict": "needs_patch"},
    },
)
assert needs_patch["facts"]["verdict"] == "needs_patch", needs_patch

suggest = rpc("wrkf.evidence.suggest", {"task": "T-00001", "transition": "plan_ready"})
assert "implementation" in suggest["missing"], suggest

blocked = rpc("wrkf.next", {"task": "T-00001"})
assert any(
    "needs_patch" in block["message"]
    for transition in blocked["blockedTransitions"]
    for block in transition.get("blocksOn", [])
), blocked

stale_context = rpc("wrkf.task.inspect", {"task": "T-00001"})["contextHash"]

ready = rpc(
    "wrkf.evidence.add",
    {
        "task": "T-00001",
        "kind": "implementation",
        "ref": "git:def456",
        "summary": "ready",
        "facts": {"verdict": "ready"},
    },
)
assert ready["facts"]["verdict"] == "ready", ready

evidence = rpc("wrkf.evidence.list", {"task": "T-00001"})
assert len(evidence) == 2, evidence

ev_show = rpc("wrkf.evidence.show", {"id": evidence[0]["id"]})
assert ev_show["id"] == evidence[0]["id"], ev_show

suggest = rpc("wrkf.evidence.suggest", {"task": "T-00001", "transition": "plan_ready"})
assert suggest["missing"] == [], suggest

rpc(
    "wrkf.transition.apply",
    {
        "task": "T-00001",
        "transition": "plan_ready",
        "role": "coordinator",
        "expectRevision": 0,
        "contextHash": stale_context,
        "idempotencyKey": "stale-plan-ready",
    },
    expect_error="WRKF_CONTEXT_MISMATCH",
)

transition = rpc(
    "wrkf.transition.apply",
    {
        "task": "T-00001",
        "transition": "plan_ready",
        "role": "coordinator",
        "expectRevision": 0,
        "idempotencyKey": "plan-ready",
    },
)
assert transition["state"]["phase"] == "done" and transition["revision"] == 1, transition

obligations = rpc("wrkf.obligation.list", {"task": "T-00001"})
assert obligations[0]["status"] == "open", obligations
obl = obligations[0]["id"]

obl_show = rpc("wrkf.obligation.show", {"id": obl})
assert obl_show["id"] == obl, obl_show

effects = rpc("wrkf.effect.list", {"task": "T-00001", "all": True})
assert effects[0]["status"] == "pending", effects
eff = effects[0]["id"]

eff_show = rpc("wrkf.effect.show", {"id": eff})
assert eff_show["id"] == eff, eff_show

claim = rpc("wrkf.effect.claim", {"task": "T-00001", "adapter": "smoke", "limit": 1, "leaseMs": 30000})
assert claim["effects"][0]["id"] == eff and claim["leaseToken"], claim

acked = rpc("wrkf.effect.ack", {"effectId": eff, "leaseToken": claim["leaseToken"]})
assert acked["status"] == "delivered", acked

satisfied = rpc("wrkf.obligation.satisfy", {"task": "T-00001", "id": obl, "evidenceId": evidence[0]["id"]})
assert satisfied["status"] == "satisfied", satisfied

hooks = rpc("wrkf.hook.list")
assert hooks["hooks"][0]["id"] == "finish_hook", hooks

hook = rpc("wrkf.hook.show", {"id": "finish_hook"})
assert hook["id"] == "finish_hook", hook

hook_run = rpc("wrkf.hook.run", {"task": "T-00001", "transition": "finish", "hookId": "finish_hook"})
assert hook_run["verdict"] == "pass", hook_run

check_run = rpc("wrkf.check.run", {"task": "T-00001", "transition": "finish"})
assert check_run["runs"][0]["verdict"] == "pass", check_run

check_show = rpc("wrkf.check.show", {"id": check_run["runs"][0]["id"]})
assert check_show["id"] == check_run["runs"][0]["id"], check_show

check_list = rpc("wrkf.check.list", {"task": "T-00001", "transition": "finish"})
assert len(check_list) >= 1, check_list

run = rpc(
    "wrkf.run.start",
    {
        "task": "T-00001",
        "role": "coordinator",
        "actor": "human:local-human",
        "idempotencyKey": "run-1",
    },
)
run_id = run["id"]
assert run["status"] == "active", run

bound = rpc(
    "wrkf.run.bindExternal",
    {"runId": run_id, "externalRunRef": "smoke/external/1", "idempotencyKey": "bind-1"},
)
assert bound["externalRunRef"] == "smoke/external/1", bound

run_show = rpc("wrkf.run.show", {"id": run_id})
assert run_show["status"] == "active", run_show

finished = rpc("wrkf.run.finish", {"runId": run_id, "summary": "done"})
assert finished["status"] == "completed", finished

runs = rpc("wrkf.run.list", {"task": "T-00001"})
assert len(runs) == 1, runs

closed = rpc(
    "wrkf.transition.apply",
    {
        "task": "T-00001",
        "transition": "finish",
        "role": "coordinator",
        "expectRevision": 1,
        "runChecks": True,
        "idempotencyKey": "finish",
    },
)
assert closed["state"]["outcome"] == "completed" and closed["revision"] == 2, closed

done = rpc("wrkf.next", {"task": "T-00001"})
assert done["actions"] == [], done

rpc("wrkf.shutdown")
proc.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "wrkf.exit"}) + "\n")
proc.stdin.flush()
proc.stdin.close()
code = proc.wait(timeout=5)
if code != 0:
    raise AssertionError(f"wrkf rpc exited {code}; stderr={proc.stderr.read()!r}")
PY

echo "smoke-wrkf-rpc: ok"
