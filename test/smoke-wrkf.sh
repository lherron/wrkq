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
export WRKQ_ACTOR="agent:local-human"
unset ASP_PROJECT

"$BIN/wrkqadm" init --db "$DB" >/dev/null
"$BIN/wrkq" --db "$DB" --as agent:local-human touch inbox/wrkf-smoke -t "wrkf smoke" >/dev/null

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

"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" workflow validate "$TMPDIR/workflow.json" --json | jq -e '.valid == true' >/dev/null
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" workflow install "$TMPDIR/workflow.json" --json | jq -e '.id == "smoke_flow" and .version == "1"' >/dev/null
"$BIN/wrkf" --db "$DB" workflow list --json | jq -e '.templates | length == 1' >/dev/null
"$BIN/wrkf" --db "$DB" workflow show smoke_flow@1 --json | jq -e '.template.id == "smoke_flow"' >/dev/null

"$BIN/wrkf" --db "$DB" --principal-ref agent:local-human task attach T-00001 --workflow smoke_flow@1 --json | jq -e '.phase == "plan" and .revision == 0' >/dev/null
"$BIN/wrkf" --db "$DB" task inspect T-00001 --json | jq -e '.templateId == "smoke_flow"' >/dev/null
"$BIN/wrkf" --db "$DB" task timeline T-00001 --json | jq -e '.events | length == 1' >/dev/null
"$BIN/wrkf" --db "$DB" task refresh T-00001 --json | jq -e '.instance.taskDocHash | startswith("sha256:")' >/dev/null
"$BIN/wrkf" --db "$DB" task sync-meta T-00001 --json | jq -e '.synced == 1' >/dev/null

"$BIN/wrkf" --db "$DB" check T-00001 plan_ready --role coordinator --json | jq -e '.blockedTransitions[] | select(.id == "plan_ready")' >/dev/null
"$BIN/wrkf" --db "$DB" next T-00001 --role coordinator --json | jq -e '.actions[] | select(.kind == "collect_evidence")' >/dev/null

if "$BIN/wrkf" --db "$DB" --json evidence exec T-00001 --kind implementation --summary "missing facts" -- printf smoke >/dev/null 2>&1; then
  echo "expected evidence exec without required facts to fail" >&2
  exit 1
fi

"$BIN/wrkf" --db "$DB" evidence add T-00001 --kind implementation --ref "git:abc123" --summary "needs patch" --facts '{"verdict":"needs_patch"}' --json | jq -e '.facts.verdict == "needs_patch"' >/dev/null
"$BIN/wrkf" --db "$DB" evidence suggest T-00001 --transition plan_ready --json | jq -e '.missing[0].satisfied == false and .missing[0].latest.facts.verdict == "needs_patch"' >/dev/null
"$BIN/wrkf" --db "$DB" next T-00001 --role coordinator --json | jq -e '.blockedTransitions[] | select(.id == "plan_ready") | .blocksOn[] | select(.message | contains("needs_patch"))' >/dev/null
"$BIN/wrkf" --db "$DB" --json evidence exec T-00001 --kind implementation --summary "exec proof" --facts '{"verdict":"ready"}' -- printf smoke | jq -e '.kind == "implementation" and .facts.verdict == "ready"' >/dev/null
"$BIN/wrkf" --db "$DB" evidence list T-00001 --json | jq -e '.evidence | length == 2' >/dev/null
"$BIN/wrkf" --db "$DB" evidence suggest T-00001 --transition plan_ready --json | jq -e '.missing | length == 0' >/dev/null

"$BIN/wrkf" --db "$DB" transition T-00001 plan_ready --role coordinator --expect-revision 0 --idempotency-key plan-ready --json | jq -e '.state.phase == "done" and .revision == 1' >/dev/null
"$BIN/wrkf" --db "$DB" obligation list T-00001 --json | jq -e '.obligations[0].status == "open"' >/dev/null
"$BIN/wrkf" --db "$DB" effect list T-00001 --json | jq -e '.effects[0].status == "pending"' >/dev/null
EFF="$("$BIN/wrkf" --db "$DB" effect list T-00001 --json | jq -r '.effects[0].id')"
CLAIM="$("$BIN/wrkf" --db "$DB" effect claim T-00001 --adapter smoke --limit 1 --lease-ms 30000 --json)"
TOKEN="$(jq -r '.leaseToken' <<<"$CLAIM")"
jq -e --arg eff "$EFF" '.effects[0].id == $eff and .effects[0].status == "leased" and .leaseToken != ""' <<<"$CLAIM" >/dev/null
"$BIN/wrkf" --db "$DB" effect ack "$EFF" --lease-token "$TOKEN" --json | jq -e '.status == "delivered" and (.leasedBy == null)' >/dev/null
OBL="$("$BIN/wrkf" --db "$DB" obligation list T-00001 --json | jq -r '.obligations[0].id')"
EV="$("$BIN/wrkf" --db "$DB" evidence list T-00001 --json | jq -r '.evidence[0].id')"
"$BIN/wrkf" --db "$DB" obligation satisfy T-00001 "$OBL" --role coordinator --evidence "$EV" --json | jq -e '.status == "satisfied"' >/dev/null

"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" hook list --json | jq -e '.hooks[0].id == "finish_hook"' >/dev/null
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" hook show finish_hook --json | jq -e '.id == "finish_hook"' >/dev/null
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" hook run T-00001 finish --hook finish_hook --json | jq -e '.verdict == "pass"' >/dev/null

RUN="$("$BIN/wrkf" --db "$DB" run start T-00001 --role coordinator --principal-ref agent:local-human --json | jq -r '.id')"
"$BIN/wrkf" --db "$DB" run show "$RUN" --json | jq -e '.status == "active"' >/dev/null
"$BIN/wrkf" --db "$DB" run list T-00001 --json | jq -e '.runs | length == 1' >/dev/null
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" check run T-00001 finish --role coordinator --principal-ref agent:local-human --json | jq -e '.checks[0].verdict == "pass"' >/dev/null
"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" check show chk_000001 --json | jq -e '.id == "chk_000001"' >/dev/null

"$BIN/wrkf" --db "$DB" --hook-catalog "$TMPDIR/hooks.json" transition T-00001 finish --role coordinator --principal-ref agent:local-human --expect-revision 1 --idempotency-key finish --json | jq -e '.state.outcome == "completed" and .revision == 2' >/dev/null
"$BIN/wrkf" --db "$DB" run finish "$RUN" --summary "done" --json | jq -e '.status == "completed"' >/dev/null
"$BIN/wrkf" --db "$DB" next T-00001 --role coordinator --json | jq -e '.actions | length == 0' >/dev/null

"$BIN/wrkf" --db "$DB" supervisor start T-00001 --principal-ref agent:local-human --json | jq -e '.role == "supervisor"' >/dev/null
"$BIN/wrkf" --db "$DB" supervisor call T-00001 --reason smoke --json | jq -e '.kind == "supervisor_call"' >/dev/null
"$BIN/wrkf" --db "$DB" supervisor action T-00001 escalate --reason smoke --json | jq -e '.kind == "supervisor_escalation"' >/dev/null
"$BIN/wrkf" --db "$DB" supervisor action T-00001 create-obligation cleanup --reason follow-up --json | jq -e '.kind == "cleanup" and .status == "open"' >/dev/null

echo "smoke-wrkf: ok"
