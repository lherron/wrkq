#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="$(mktemp -d)"
DAEMON_PID=""
cleanup() {
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

DB="$TMPDIR/wrkq.db"
BIN="$TMPDIR/bin"
mkdir -p "$BIN"

go build -tags sqlite_fts5 -o "$BIN/wrkq" "$ROOT/cmd/wrkq"
go build -tags sqlite_fts5 -o "$BIN/wrkqadm" "$ROOT/cmd/wrkqadm"
go build -tags sqlite_fts5 -o "$BIN/wrkf" "$ROOT/cmd/wrkf"
go build -tags sqlite_fts5 -o "$BIN/wrkqd" "$ROOT/cmd/wrkqd"

cd "$TMPDIR"
export WRKQ_DB_PATH="$DB"
export WRKQ_ACTOR="agent:local-human"
export WRKQ_PROJECT_ROOT=""
unset ASP_PROJECT

"$BIN/wrkqadm" init --db "$DB" >/dev/null
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/wrkf-rpc-smoke -t "wrkf rpc smoke" >/dev/null
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/pbc-rpc-walk -t "pbc rpc walk" >/dev/null

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
      "timeoutMs": 20000,
      "cwd": "template_dir",
      "env": { "allow": ["PATH"] },
      "maxStdoutBytes": 65536,
      "maxStderrBytes": 65536
    }
  },
  "effectHandlers": {
    "smoke_deliver": {
      "kind": "effect",
      "argv": ["/usr/bin/true"],
      "stdin": "json",
      "stdout": "text",
      "timeoutMs": 20000,
      "cwd": "template_dir",
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
            { "kind": "wake_role", "role": "coordinator", "reason": "ready to finish" },
            { "kind": "smoke_deliver", "role": "coordinator", "reason": "exercise delivery" }
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

python3 - "$BIN/wrkf" "$DB" "$TMPDIR/hooks.json" "$TMPDIR/workflow.json" "$ROOT/pbc/workflow-template.json" <<'PY'
import json
import subprocess
import sys

bin_wrkf, db_path, hooks_path, workflow_path, pbc_preset = sys.argv[1:6]
with open(workflow_path, encoding="utf-8") as src:
    workflow_body = src.read()
with open(pbc_preset, encoding="utf-8") as src:
    pbc_body = src.read()

proc = subprocess.Popen(
    [
        bin_wrkf,
        "--db",
        db_path,
        "--hook-catalog",
        hooks_path,
        "--principal-ref",
        "agent:local-human",
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
    "rpc.initialize",
    {"protocolVersion": "2026-06-30", "client": {"name": "smoke", "version": "0"}},
)
assert init["protocolVersion"] == "2026-06-30", init
assert init["database"]["path"] == db_path, init
assert init["capabilities"]["effectClaimLease"] is True, init
assert init["protocolSchemaHash"].startswith("sha256:"), init

valid = rpc("wrkf.workflow.validate", {"body": workflow_body, "sourceName": workflow_path})
assert valid["valid"] is True, valid

installed = rpc("wrkf.workflow.install", {"body": workflow_body, "sourceName": workflow_path})
assert installed["id"] == "smoke_flow" and installed["version"] == "1", installed

listed = rpc("wrkf.workflow.list")
assert len(listed["templates"]) == 1, listed

shown = rpc("wrkf.workflow.show", {"ref": "smoke_flow@1"})
assert shown["template"]["id"] == "smoke_flow", shown

diff = rpc("wrkf.workflow.diff", {"oldBody": workflow_body, "newBody": workflow_body, "oldSourceName": workflow_path, "newSourceName": workflow_path})
assert diff["sameHash"] is True, diff

attached_result = rpc("wrkq.workflow.attach", {"task": "T-00001", "workflow": "smoke_flow@1"})
attached = attached_result["instance"]
assert attached["phase"] == "plan" and attached["revision"] == 0, attached

inspect = rpc("wrkf.instance.show", {"task": "T-00001"})
assert inspect["templateId"] == "smoke_flow", inspect

timeline = rpc("wrkq.workflow.timeline", {"task": "T-00001"})
assert len(timeline["events"]) == 1, timeline

refreshed = rpc("wrkq.workflow.refresh", {"task": "T-00001"})["instance"]
assert refreshed["taskDocHash"].startswith("sha256:"), refreshed

preflight = rpc("wrkf.check.preflight", {"task": "T-00001", "transition": "plan_ready"})
assert any(b["id"] == "plan_ready" for b in preflight["blockedTransitions"]), preflight

next_action = rpc("wrkf.instance.next", {"task": "T-00001"})
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

blocked = rpc("wrkf.instance.next", {"task": "T-00001"})
assert any(
    "needs_patch" in block["message"]
    for transition in blocked["blockedTransitions"]
    for block in transition.get("blocksOn", [])
), blocked

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
assert len(effects) == 2 and all(effect["status"] == "pending" for effect in effects), effects
by_kind = {effect["kind"]: effect for effect in effects}
eff = by_kind["wake_role"]["id"]
deliver_eff = by_kind["smoke_deliver"]["id"]

eff_show = rpc("wrkf.effect.show", {"id": eff})
assert eff_show["id"] == eff, eff_show

claim = rpc("wrkf.effect.claim", {"task": "T-00001", "adapter": "smoke", "limit": 1, "leaseMs": 30000})
assert claim["effects"][0]["id"] == eff and claim["leaseToken"], claim

acked = rpc("wrkf.effect.ack", {"effectId": eff, "leaseToken": claim["leaseToken"]})
assert acked["status"] == "delivered", acked

satisfied = rpc(
    "wrkf.obligation.satisfy",
    {
        "task": "T-00001",
        "id": obl,
        "evidenceId": evidence[0]["id"],
        "principal_ref": "agent:local-human",
        "role": "coordinator",
    },
)
assert satisfied["status"] == "satisfied", satisfied

hooks = rpc("wrkf.hook.list")
assert hooks["hooks"][0]["id"] == "finish_hook", hooks

hook = rpc("wrkf.hook.show", {"id": "finish_hook"})
assert hook["id"] == "finish_hook", hook

hook_run = rpc("wrkf.hook.run", {"task": "T-00001", "transition": "finish", "hookId": "finish_hook"})
assert hook_run["verdict"] == "pass", hook_run

run = rpc(
    "wrkf.run.start",
    {
        "task": "T-00001",
        "role": "coordinator",
        "principal_ref": "agent:local-human",
        "deliveryRef": "smoke-delivery",
        "idempotencyKey": "run-1",
    },
)
run_id = run["id"]
assert run["status"] == "active", run

delivered = rpc("wrkf.effect.deliver", {"effectId": deliver_eff, "adapter": "smoke"})
assert delivered["effect"]["status"] == "delivered" and delivered["exitCode"] == 0, delivered

bound = rpc(
    "wrkf.run.bindExternal",
    {"runId": run_id, "externalRunRef": "smoke/external/1", "idempotencyKey": "bind-1"},
)
assert bound["externalRunRef"] == "smoke/external/1", bound

run_show = rpc("wrkf.run.show", {"id": run_id})
assert run_show["status"] == "active", run_show

runs = rpc("wrkf.run.list", {"task": "T-00001"})
assert len(runs) == 1, runs

check_run = rpc(
    "wrkf.check.run",
    {"task": "T-00001", "transition": "finish", "principal_ref": "agent:local-human", "role": "coordinator"},
)
assert check_run["runs"][0]["verdict"] == "pass", check_run

check_show = rpc("wrkf.check.show", {"id": check_run["runs"][0]["id"]})
assert check_show["id"] == check_run["runs"][0]["id"], check_show

check_list = rpc("wrkf.check.list", {"task": "T-00001", "transition": "finish"})
assert len(check_list) >= 1, check_list

rpc(
    "wrkf.transition.apply",
    {
        "task": "T-00001",
        "transition": "finish",
        "role": "coordinator",
        "principal_ref": "agent:local-human",
        "runChecks": True,
    },
    expect_error="WRKF_VALIDATION",
)

closed = rpc(
    "wrkf.transition.apply",
    {
        "task": "T-00001",
        "transition": "finish",
        "role": "coordinator",
        "principal_ref": "agent:local-human",
        "expectRevision": 1,
        "idempotencyKey": "finish",
    },
)
assert closed["state"]["outcome"] == "completed" and closed["revision"] == 2, closed

finished = rpc("wrkf.run.finish", {"runId": run_id, "summary": "done"})
assert finished["status"] == "completed", finished

done = rpc("wrkf.instance.next", {"task": "T-00001"})
assert done["actions"] == [], done

# ── Real-preset regression gate: drive ./pbc (pbc-progressive-refinement) intake → finalized ──
# Uses the actual installed preset, not a synthetic template, to prove the RPC surface
# walks a real multi-state workflow to a terminal state with CAS + evidence at every step.
pbc_inst = rpc("wrkf.workflow.install", {"body": pbc_body, "sourceName": pbc_preset})
assert pbc_inst["id"] == "pbc-progressive-refinement", pbc_inst
pbc_ref = f"{pbc_inst['id']}@{pbc_inst['version']}"

pbc_att = rpc("wrkq.workflow.attach", {"task": "T-00002", "workflow": pbc_ref})["instance"]
assert pbc_att["phase"] == "intake" and pbc_att["revision"] == 0, pbc_att

PBC_ROLE, PBC_ACTOR = "agent", "agent:local-human"


def pbc_step(transition, evidence, want_phase):
    for kind, facts, ref in evidence:
        actor = "agent:pressure-reviewer" if kind == "pressure_pass" else PBC_ACTOR
        rpc(
            "wrkf.evidence.add",
            {"task": "T-00002", "kind": kind, "ref": ref, "summary": f"pbc {kind}", "facts": facts, "principal_ref": actor},
        )
    # Re-read the instance revision before each transition.
    cur = rpc("wrkf.instance.show", {"task": "T-00002"})
    req = {
        "task": "T-00002",
        "transition": transition,
        "role": PBC_ROLE,
        "principal_ref": PBC_ACTOR,
        "expectRevision": cur["revision"],
        "idempotencyKey": f"pbc:{transition}:{cur['revision']}",
    }
    res = rpc("wrkf.transition.apply", req)
    assert res["state"]["phase"] == want_phase, res
    assert res["revision"] == cur["revision"] + 1, res
    return res, req


pbc_step("normalize_feedback", [("intake_metadata", {}, "note:intake")], "behavior_note")
pbc_step(
    "draft_pbc",
    [
        ("behavior_note", {}, "note:bn"),
        ("pre_interview_analysis", {"clarification_needed": False}, "note:pia"),
    ],
    "pbc_draft",
)
pbc_step("run_pressure_pass", [("pbc_draft", {}, "note:draft")], "pressure")
pbc_final, pbc_final_req = pbc_step(
    "finalize_ready_pbc",
    [("pressure_pass", {"verdict": "ready"}, "note:pp"), ("pbc_final", {}, "note:final")],
    "finalized",
)
assert pbc_final["state"]["status"] == "closed", pbc_final

# idempotency replay: re-applying the finalize with the SAME key+params short-circuits
# (idempotency is checked before CAS) and returns the ORIGINAL committed result.
pbc_replay = rpc("wrkf.transition.apply", pbc_final_req)
assert pbc_replay["eventId"] == pbc_final["eventId"], (pbc_replay, pbc_final)

pbc_done = rpc("wrkf.instance.next", {"task": "T-00002"})
assert pbc_done["actions"] == [], pbc_done

rpc("rpc.shutdown")
proc.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "rpc.exit"}) + "\n")
proc.stdin.flush()
proc.stdin.close()
code = proc.wait(timeout=5)
if code != 0:
    raise AssertionError(f"wrkf rpc exited {code}; stderr={proc.stderr.read()!r}")
PY

# Real HTTP transport regression: --run-checks must persist the daemon-side
# hook result first, then apply the transition with that check id.
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/remote-run-checks -t "remote run checks" >/dev/null
cat >"$TMPDIR/remote-workflow.json" <<'REMOTE_FLOW'
{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "remote_hook_flow",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": { "coordinator": { "description": "Coordinates the remote smoke" } },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "completed" }
  ],
  "checks": {
    "finish_check": {
      "type": "hook",
      "hookId": "finish_hook",
      "exitMap": {
        "0": { "verdict": "pass", "outcome": "passed" },
        "*": { "verdict": "error", "outcome": "failed" }
      }
    }
  },
  "transitions": [{
    "id": "finish",
    "from": { "status": "active", "phase": "ready" },
    "by": ["coordinator"],
    "checks": ["finish_check"],
    "outcomes": [{
      "id": "completed",
      "when": { "checkVerdict": { "check": "finish_check", "is": "pass" } },
      "to": { "status": "closed", "outcome": "completed" }
    }]
  }]
}
REMOTE_FLOW
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" --principal-ref agent:local-human workflow install "$TMPDIR/remote-workflow.json" --json >/dev/null
"$BIN/wrkf" --db "$DB" --principal-ref agent:local-human task attach T-00003 --workflow remote_hook_flow@1 --json >/dev/null

PORT="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
WRKF_HOOK_CATALOG="$TMPDIR/hooks.json" "$BIN/wrkqd" --db "$DB" --addr "127.0.0.1:$PORT" --token smoke-token >"$TMPDIR/wrkqd.log" 2>&1 &
DAEMON_PID="$!"
for _ in $(seq 1 50); do
  if curl -fsS -H 'Authorization: Bearer smoke-token' "http://127.0.0.1:$PORT/v1/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.05
done
curl -fsS -H 'Authorization: Bearer smoke-token' "http://127.0.0.1:$PORT/v1/health" >/dev/null
WRKQ_DB="rpc://127.0.0.1:$PORT" WRKQD_TOKEN=smoke-token \
  "$BIN/wrkf" --principal-ref agent:local-human --role coordinator transition T-00003 finish \
    --run-checks --expect-revision 0 --idempotency-key remote-finish --json \
  | jq -e '.state.outcome == "completed" and .revision == 1' >/dev/null
WRKQ_DB="rpc://127.0.0.1:$PORT" WRKQD_TOKEN=smoke-token \
  "$BIN/wrkf" check list T-00003 --transition finish --json \
  | jq -e '.checks | length == 1 and .[0].id != "" and .[0].verdict == "pass"' >/dev/null

echo "smoke-wrkf-rpc: ok (synthetic lifecycle + real ./pbc preset walk + remote --run-checks)"
