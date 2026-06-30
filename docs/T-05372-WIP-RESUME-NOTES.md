# T-05372 — WIP resume notes (clod, paused on Lance's hold 2026-06-30)

**Task is ON HOLD** (T-05381 must land/push first). This is a WIP checkpoint, **not green** —
do not treat as done. `go build ./...` and `go vet ./...` are clean; the workflow / wrkfapi /
wrkfrpc package tests are green. The `internal/workrpc` acceptance suite has ~2 remaining
**test-fixture** reds described below.

## What's done
- Migration `000036_wrkf_principal_identity.sql`: additive principal_ref columns on wrkf tables
  + backfill (agent:<slug>) + loud-fail guard. Old actor columns orphaned for T-04317.
- `internal/workflow`: full actor→principal_ref cutover (types/storage/reads/writes/hashes/
  messages/next-action `--principal-ref`/check-input JSON key). Dual-writes NOT NULL legacy
  actor cols (workflow_runs, workflow_role_bindings). Package tests green.
- `internal/webhooks`: WorkflowPayload.actor → principal_ref (Origin.actor left alone — core).
- `internal/wrkfapi`, `internal/wrkfrpc`: wire fields actor→principal_ref; wrkfrpc ProtocolVersion
  2026-06-01→2026-06-30; legacy-field guard in decodeParams. Tests green.
- `internal/workrpc`: registry wrkf params → principal_ref; ProtocolVersion 2026-06-14→2026-06-30;
  **wrkf-scoped** legacy-actor guard (`legacy_actor_guard.go`, gated on `wrkf.`/`wrkq.workflow.`
  method prefixes via Server.Register) so core wrkq admin surfaces keep accepting `actor`.
- `internal/wrkfcli/root.go`: `--actor` → `--principal-ref` (persistent + run/action/reap/supervisor),
  WRKF_PRINCIPAL_REF env, struct literals → PrincipalRef. `DefaultActor` (core plumbing) kept.
- Protocol version 2026-06-14→2026-06-30 bumped across all Go test files that hardcoded it.

## REMAINING RED (the in-progress sweep that was interrupted)
**Root cause of the 2 failures: over-eager `"actor"`→`"principal_ref"` replacement in test fixtures
hit CORE `wrkq.*` method calls, not just wrkf ones.**

1. `TestWrkfEventQuery_ReplaysTransitionEventsWithFiltersAndCursor`
   (`wrkfapi_acceptance_test.go` ~line 565+): the bulk replace changed core
   `wrkq.task.update` / `wrkq.workflow.attach` params from `"actor": "agent:smokey"` to
   `"principal_ref": ...`. Core task.update does NOT read `principal_ref` (snake) — so it falls
   back to the harness DefaultActor (`claude-code-agent`, a bare slug) which core principal
   validation now rejects. **FIX:** revert those CORE `wrkq.task.update`/`wrkq.workflow.attach`
   param keys back to whatever the core method actually reads (check `wrkqapi.TaskUpdateParams`
   attribution key — likely camelCase `principalRef` or `as`, NOT snake `principal_ref`). Only
   `wrkf.*` and `wrkq.workflow.*` (guarded) calls should use `principal_ref`. NOTE
   `wrkq.workflow.attach` IS workflow-domain/guarded → it should use `principal_ref`, but confirm
   the WorkflowAttachParams wire key.
2. `TestWrkqWorkflowTimeline_TransitionEventPayload` (`wrkqapi_acceptance_test.go` ~1325/1332):
   wrkf.evidence.add / wrkf.transition.apply still send `"actor"` → guard rejects. Change those
   two wrkf-method `"actor"` keys to `"principal_ref"` (leave the core `wrkq.webhook.add` actors).

## Still TODO after those reds (not started)
- `packages/client`: wrkf types/facade/protocol(2026-06-30)/stdio (`--principal-ref` for wrkf)/
  fixtures/contract tests + negative test proving actor-shaped wrkf params rejected by installed
  entrypoint. (curly's preserved attempt + `principal-ref.test.ts` are reference material.)
- `docs/`: wrkf-rpc.md, wrkq-wrkf-rpc.md principal terminology + version.
- Template/PBC fixtures using `"actors"`/`ownerActor`/`distinctActorFromEvidence` (none in builtin;
  check repo-wide).
- `just verify`, `just install`, installed-binary upgrade smoke + e2e.
