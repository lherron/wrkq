# wrkf Scheduler and agent-loop Runner Proposal

Status: proposal

Related reference work:

- `T-04962`: Smithers vs wrkf gap analysis, informational only.
- `T-05067`: orphaned wrkf actions strand tasks when a coordinator dies mid-action.
- `T-05362`: draft this proposal.

## Summary

`wrkf` already owns the durable workflow ledger: instances, runs, actions,
evidence, checks, obligations, transitions, effects, CAS, idempotency, and the
JSON-RPC contract. The missing product layer is executable orchestration around
that ledger.

Build that layer as two cooperating pieces:

1. `wrkq/wrkf` adds scheduler-facing primitives for discovering, claiming, and
   reclaiming runnable workflow work.
2. `agent-loop` adds a generic `wrkf`-driven runner that consumes those claims,
   dispatches action handlers, persists evidence, and re-queries `wrkf` after
   each action.

The runner should execute one action at a time per workflow instance, but it
must be able to pin an instance through mandatory continuations. The immediate
case is implementation work: after a successful `implement` action, the same
instance and exact source implement action/commit must stay pinned until
`verify` terminalizes or becomes `operator_required`. A runner that owns the
same worktree must not move to unrelated work while mandatory verify is
runnable, blocked, or operationally unresolved.

## Review Consensus

Clod and Daedalus reviewed this proposal independently, then Cody mediated a
three-way consensus round where each agent responded to the other's blocking
edits. All three signed this direction:

1. Keep a runner-only first phase only as a non-production vertical spike. It can
   prove handler dispatch and `implement -> verify` continuation with one runner,
   but it is not crash-safe or multi-runner safe.
2. Require durable `wrkf` scheduler authority before production or unattended
   multi-runner use.
3. Treat leases and fences separately. A lease proves liveness; a fence is the
   write authority. Final action settlement and scheduler release must reject a
   stale, expired, or stolen fence.
4. Make `implement -> verify` continuation state/discovery-backed, not dependent
   on runner memory or an optional `continuationFirst` preference.
5. Keep the existing triage scanner temporarily as an attachment/input source
   until normal executable tasks are auto-attached or an intake seeder exists.

Final consensus sentence:

> `wrkf` remains the durable scheduler authority. Production or unattended
> multi-runner scheduling requires durable scheduler claims with lease for
> liveness and fence for write authority; revision/context CAS; canonical action
> identity used as the canonical idempotency key for action-run start/resume;
> fence-gated pre-side-effect execution and final settlement; release/reaper
> outcomes derived from terminal action truth with reconcile-not-rerun behavior;
> ledger-derived `implement -> verify` continuation discovery with exact source
> implement action/commit lineage independent of live claim rows;
> capability-aware claims; caller-owned scoping; explicit worktree fencing or
> runner-owned worktree guarding; and a dedupe/precedence decision with ACP
> verify-launch before both verify launch paths are enabled. Phase 1 is a
> non-production vertical spike, and the durable scheduler-core phase is not
> production-ready until continuation policy and verify-launch authority are
> settled.

This document has been patched to reflect that consensus. It remains proposal
text; durable architecture records should be added or amended in the
implementation PR that introduces production scheduler claims/fences.

## Goals

- Discover runnable work across active attached `wrkf` instances, filtered by
  project, path, template, role, lane, status, and runner capability.
- Claim one runnable action for one workflow instance with lease and fence
  tokens so multiple runners cannot execute or settle the same action.
- Run exactly one action, complete/fail it durably, then re-query `wrkf`.
- Support mandatory same-instance continuation policies such as
  `implement -> verify`.
- Recover from scheduler or coordinator crashes through expiring leases,
  monotonic fencing, and explicit reclaim/fail/operator-required behavior.
- Let existing domain packages such as `@praesidium/agent-loop-triage` and
  `@praesidium/agent-loop-impl` become action handlers under the generic
  runner.
- Keep `wrkf` as the source of truth. `agent-loop` traces are diagnostics and
  replay aids, not authoritative workflow state.

## Non-Goals

- Do not turn `wrkf` into a full agent runtime.
- Do not move HRC session/process ownership into `wrkq/wrkf`.
- Do not allow scheduler code to mutate `tasks.state` directly.
- Do not let `agent-loop` scrape tasks and infer workflow legality outside
  `wrkf`.
- Do not fan out multiple speculative actions for the same workflow instance by
  default.
- Do not make `runWorkflow` the durable scheduler. It remains trace/orchestration
  plumbing.

## Current State

The current public surface already has the key low-level pieces:

- `wrkf.instance.next` computes per-instance next actions, blockers,
  obligations, pending effects, expected state, revision, and context hash.
- `wrkf.action.*` composes run, evidence, and transition primitives for semantic
  actions.
- `wrkf.run.*` records durable runs and supports external binding.
- `wrkf.effect.*` supports claim/lease/ack/fail semantics for effects.
- `agent-loop` has `withAction`, `startAction`, `completeAction`, and
  `failAction` wrappers over `wrkf.action.*`.
- `agent-loop` has packaged action handlers:
  - `@praesidium/agent-loop-triage`: triage worker plus architecture review,
    `triage_result` evidence, and specification CAS write.
  - `@praesidium/agent-loop-impl`: implement/verify cores that accept already
    started action bindings and complete/fail only that bound action.

The missing pieces are global runnable-work discovery, workflow-instance/action
claiming, mandatory continuation ownership, and crash recovery across the
scheduler boundary.

## Core Model

The scheduler operates on this conceptual unit:

```ts
interface SchedulerClaim {
  claimId: string
  instanceId: string
  task: string
  workflow: { id: string; version: string; hash?: string }
  state: { status: string; phase?: string; outcome?: string }
  revision: number
  contextHash: string
  action: {
    id: string
    kind: string
    semanticAction?: string
    role: string
    lane?: string
    expectedState?: { status: string; phase?: string; outcome?: string }
  }
  leaseToken: string
  leaseExpiresAt: string
  fenceToken: string
  workspaceRef?: string
  continuation?: ContinuationPolicy
}

interface ContinuationPolicy {
  afterAction: string
  mustRunNext: string[]
  sameInstance: true
  releaseOnlyOn: ("closed" | "blocked" | "operator_required" | "failed")[]
}
```

`leaseToken` and `fenceToken` have different jobs:

- `leaseToken` proves the runner currently owns a time-bounded lease.
- `fenceToken` is the authority to commit side effects for this claim. Final
  settlement must reject if the fence is no longer current, even if an old
  handler is still running locally.

The production implementation may use one opaque token that carries both roles,
but the contract must preserve both semantics.

The normal loop is:

```ts
while (runnerActive) {
  const claim = await wrkf.scheduler.claimNext({ project, roles, capabilities })
  if (!claim) break

  await runClaimedAction(claim)

  while (true) {
    const continuation = await wrkf.scheduler.claimContinuation({
      priorClaimId: claim.claimId,
      leaseToken: claim.leaseToken,
    })
    if (!continuation) break
    await runClaimedAction(continuation)
  }
}
```

The runner must re-query `wrkf` after every action. It must not snapshot all
`next.actions` for an instance and execute them without returning to the ledger.

## Production Invariants

These invariants are required before the scheduler surface is used for
production or multi-runner execution:

1. **Current fence required for writes.** `wrkf.action.complete`,
   `wrkf.action.fail`, transition commit, and scheduler release must be gated by
   the current claim fence, or must be performed through a scheduler-owned
   settlement method that verifies the current fence before committing
   evidence/transition/release.
2. **Current fence required before external side effects.** Before a runner
   mutates a worktree, launches an external runtime, or performs any other
   non-idempotent handler side effect, it must confirm the current claim fence
   for that action and workspace scope. Final action settlement must re-check
   the fence. If the fence is stale after partial local side effects, the runner
   must stop and mark/report `operator_required`; it must not continue or
   release as completed.
3. **Release and reaper outcomes derive from action truth.**
   `scheduler.release(completed)` is invalid unless the referenced action run is
   terminal and matches the claim's instance, canonical action identity, role,
   lane, revision, and context hash. Scheduler release is never the source of
   semantic workflow completion. The reaper must also reconcile instead of
   re-running: if a claim expires after the action run terminalizes,
   settle/reconcile the claim to the action outcome and do not execute the
   action again; if the action run is non-terminal, it is reclaimable only under
   a new fence that invalidates stale final writes.
4. **Stale claims fail by CAS.** Claiming must validate current revision and
   context hash inside the transaction. Stale candidates must not lease.
5. **Action-run idempotency is canonical.** The canonical action identity must
   be the action-run idempotency key. Claim/start/resume must use the existing
   wrkq/wrkf canonical request hashing and replay-vs-mismatch rules so
   stale-but-retried claims resume an existing action run instead of forking a
   second one. Do not introduce a separate claim-local idempotency discipline.
6. **Continuation is ledger-derived and crash-survivable.** After
   `implement.done`, discovery must rank the same-instance `verify`
   continuation as non-skippable before fresh unrelated work. That discovery
   must be derived from workflow ledger truth, such as instance state and action
   runs, not from the presence of live scheduler claim rows. `claimContinuation`
   is a same-runner fast path, not the only recovery path after a crash.
7. **Exact verify lineage.** Verify claims and bindings must carry the exact
   `sourceImplementActionRunId` and source commit. "Latest implement" fallback
   is forbidden.
8. **No double verify launch.** Until the scheduler supersedes or consumes
   `wrkq.contract.wrkf-verify-launch-producer`, ACP verify-launch effects remain
   a separate opt-in path. The production design must define dedupe/precedence
   before both paths are enabled for the same workflow.
9. **Capability-aware claim selection.** `claimNext` must select only actions
   the runner declares it can execute. A runner should not claim work and then
   discover there is no handler.
10. **Caller-owned scope.** Project/path filters are caller-resolved selectors.
   Scheduler RPC must not read `WRKQ_PROJECT_ROOT`, `ASP_PROJECT`, `--project`,
   or caller environment to infer scope.
11. **Worktree pin is explicit.** `wrkf` can durably pin instance/action/source
    lineage. If production scheduling coordinates worktrees globally, the claim
    must also record a workspace/worktree identity and fence it. Otherwise the
    runner/runtime owns the worktree guard explicitly.

## Proposed wrkf RPC Surface

### `wrkf.scheduler.discover`

Read-only preview of runnable candidates. This is useful for dashboards,
dry-runs, and operator inspection. It must not create a lease.

```ts
interface WrkfSchedulerDiscoverParams {
  project?: string
  path?: string
  recursive?: boolean
  templates?: string[]
  statuses?: string[]
  phases?: string[]
  roles?: string[]
  actions?: string[]
  lanes?: string[]
  includeBlocked?: boolean
  includeClaimed?: boolean
  limit?: number
  cursor?: string
}

interface WrkfSchedulerDiscoverResult {
  items: WrkfSchedulerCandidate[]
  nextCursor?: string
  hasMore: boolean
}
```

Each candidate should include enough data to explain why it is runnable without
requiring clients to run `wrkf.instance.next` again for display:

```ts
interface WrkfSchedulerCandidate {
  task: string
  instanceId: string
  workflow: { id: string; version: string; hash?: string }
  state: { status: string; phase?: string; outcome?: string }
  revision: number
  contextHash: string
  nextAction: WrkfNextAction
  blockedTransitions?: WrkfBlockedTransition[]
  openObligations?: WrkfObligation[]
  pendingEffects?: WrkfEffect[]
  claimStatus?: "unclaimed" | "claimed" | "expired" | "continuation"
}
```

### `wrkf.scheduler.claimNext`

Atomically claim one runnable action. This is the main runner entrypoint.

```ts
interface WrkfSchedulerClaimNextParams extends WrkfSchedulerDiscoverParams {
  runner: string
  capabilities: WrkfRunnerCapability[]
  leaseMs: number
  idempotencyKey?: string
}

interface WrkfSchedulerClaimResult {
  claim?: SchedulerClaim
}
```

Rules:

- Select at most one action per workflow instance.
- Exclude live claims owned by other runners.
- Prefer mandatory continuations before unrelated fresh work. This is not a
  caller preference; it is scheduler policy for crash-survivable continuation.
- Recompute `wrkf.instance.next` inside the claim transaction and validate the
  current revision/context hash. Stale candidates fail.
- Persist scheduler ownership with claim id, instance id, canonical action
  identity, expected revision, expected context hash, runner, lease token, fence
  token, and expiry.
- Match the candidate against `capabilities` before claiming it.
- Use canonical idempotency hashing and replay/mismatch behavior from the
  existing wrkq/wrkf RPC contract.

### `wrkf.scheduler.claimContinuation`

Claim the next mandatory action for the same instance after a successful action.

```ts
interface WrkfSchedulerClaimContinuationParams {
  priorClaimId: string
  leaseToken: string
  runner: string
  leaseMs: number
}
```

Rules:

- The prior claim must be owned by the runner or already terminalized by that
  runner.
- Re-query the same instance.
- If the continuation policy says `implement -> verify`, claim `verify` only
  when the instance is now at the expected post-implement state.
- If no mandatory continuation is runnable and the instance is terminal,
  blocked, or operator-required, return no claim and release the pin.
- If a mandatory continuation is blocked by an infrastructure/orphan condition,
  return a claim/error shape that keeps the instance pinned or marks it
  operator-required. Do not silently fall through to unrelated work.
- A different or restarted runner normally cannot present the prior lease token;
  it recovers mandatory continuations through `claimNext` discovery ordering, not
  this fast path.

### `wrkf.scheduler.heartbeat`

Extend a claim lease during long-running agent work.

```ts
interface WrkfSchedulerHeartbeatParams {
  claimId: string
  leaseToken: string
  leaseMs: number
}
```

Heartbeat must be an atomic CAS on the current lease/fence token and server-time
expiry. A heartbeat racing with a reaper/steal must produce exactly one winner.

### `wrkf.scheduler.release`

Release or terminalize a claim after action completion/failure.

```ts
interface WrkfSchedulerReleaseParams {
  claimId: string
  leaseToken: string
  result: "completed" | "failed" | "blocked" | "operator_required" | "abandoned"
  actionRunId?: string
  summary?: string
}
```

Release is not semantic workflow completion. The action handler still completes
or fails the `wrkf.action.*` run, or settlement goes through a scheduler-owned
method that verifies the current fence and terminalizes the run. In either case,
`release(completed)` must reconcile from the terminal action run. If the action
run is not terminal or does not match the claim, release is invalid.

### `wrkf.scheduler.reap`

Recover expired claims and orphaned active actions.

```ts
interface WrkfSchedulerReapParams {
  runner?: string
  olderThanMs?: number
  dryRun?: boolean
  limit?: number
}
```

This should cover `T-05067`: a coordinator that dies after `wrkf.action.start`
and before complete/fail must not permanently hide the task.

## Proposed Storage

Production scheduling needs durable ownership state, but the storage design is
not settled by this proposal. Before implementation, decide whether scheduler
ownership extends the existing `workflow_effects` lease mechanics or uses a
separate `workflow_scheduler_claims` table.

A separate table is acceptable only if it explicitly reuses the effect-lease
invariants in shape:

- server-time expiry;
- unguessable lease/fence token;
- atomic claim CAS;
- expired-token rejection;
- terminal paths clearing or reconciling lease state;
- reaper behavior that cannot race a live heartbeat into split-brain.

If a separate table is used, the justification should be the net-new scheduler
state: canonical action identity, continuation/pinning lineage, prior-claim
links, optional workspace/worktree fencing, and action-run reconciliation.

Candidate table shape, exact migration number TBD:

```sql
CREATE TABLE workflow_scheduler_claims (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  action_id TEXT NOT NULL,
  action_kind TEXT NOT NULL,
  semantic_action TEXT,
  role TEXT,
  lane TEXT,
  expected_revision INTEGER NOT NULL,
  expected_context_hash TEXT NOT NULL,
  runner TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN (
    'leased',
    'completed',
    'failed',
    'blocked',
    'operator_required',
    'expired',
    'abandoned'
  )),
  lease_token TEXT NOT NULL,
  fence_token TEXT NOT NULL,
  leased_until TEXT NOT NULL,
  action_run_id TEXT REFERENCES workflow_runs(id),
  continuation_group TEXT,
  prior_claim_id TEXT REFERENCES workflow_scheduler_claims(id),
  workspace_ref TEXT,
  idempotency_key TEXT,
  request_hash TEXT,
  terminal_reason TEXT,
  last_error TEXT,
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  terminal_at TEXT
);

CREATE INDEX workflow_scheduler_claims_instance_status_idx
  ON workflow_scheduler_claims(instance_id, status);

CREATE UNIQUE INDEX workflow_scheduler_claims_live_instance_idx
  ON workflow_scheduler_claims(instance_id)
  WHERE status = 'leased';

CREATE UNIQUE INDEX workflow_scheduler_claims_idempotency_idx
  ON workflow_scheduler_claims(instance_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';
```

The unique live-instance index is deliberately conservative: one live scheduler
claim per instance. That matches the default policy of one action per instance
at a time. Parallel-safe action groups can be added later with a narrower key.

## Continuation Policy

Continuation policy can be runner-local only in the non-production spike. Before
production or multi-runner use, mandatory continuation policy must live in
workflow template metadata or a `wrkf`-owned policy catalog.

Candidate policy form:

```json
{
  "workflow": "wrkq-simple-task@1",
  "continuations": [
    {
      "afterAction": "implement",
      "from": { "status": "active", "phase": "ready" },
      "postState": { "status": "active", "phase": "implemented" },
      "mustRunNext": ["verify"],
      "sameInstance": true,
      "releaseOnlyOn": ["closed", "blocked", "operator_required", "failed"]
    }
  ]
}
```

Required invariant:

> After `implement.done`, the same instance and exact source implement
> action/commit are pinned until `verify` terminalizes or becomes
> `operator_required`. Any runner that owns that worktree must not claim
> unrelated work in that worktree while mandatory verify is runnable, blocked,
> or operationally unresolved.

This does not make `implement` and `verify` one ledger action. It makes them one
exclusive-attention group for the scheduler and worktree owner.

Discovery, not only `claimContinuation`, must enforce this. `claimContinuation`
is a same-runner fast path after a successful action. After a crash or restart,
the next runner recovers the continuation through `claimNext` discovery ordering
and exact source lineage.

## agent-loop Runner Concept

Add a generic runner package, for example:

```text
packages/agent-loop-wrkf-runner/
```

Responsibilities:

- Open an `AgentLoopWorkContext`.
- Claim runnable work from `wrkf.scheduler.claimNext`.
- Resolve an action handler by workflow/template/action/role.
- Start or resume the matching `wrkf.action.*` run.
- Bind external runtime refs when the action launches HRC/agent-loop work.
- Pass a structured binding to the action handler.
- Heartbeat while the handler runs.
- Settle the action and release the scheduler claim only while holding the
  current fence, or call a scheduler-owned settlement method that performs that
  check.
- Re-query and run mandatory continuations before returning to discovery.

The handler interface should be small:

```ts
interface WrkfActionHandlerInput {
  claim: SchedulerClaim
  binding: {
    taskId: string
    actionRunId: string
    wrkfRunId: string
    action: string
    role: string
    project: string
    sessionRef?: string
    lane?: string
    sourceImplementActionRunId?: string
  }
  options: Record<string, unknown>
}

interface WrkfActionHandler {
  readonly action: string
  readonly role?: string
  run(input: WrkfActionHandlerInput): Promise<{
    status: "completed" | "blocked" | "failed" | "skipped"
    summary: string
  }>
}
```

Handler registration:

```ts
const handlers = {
  "wrkq-simple-task@1:triage": runTriageTaskRunner,
  "wrkq-simple-task@1:implement": runImplTaskRunner,
  "wrkq-simple-task@1:verify": runImplTaskRunner,
}
```

The current `loops/wrkq-task-triage/wrkq-task-triage.ts` scanner becomes a
compatibility shim or disappears after task creation/attachment policy is
settled. The packaged `@praesidium/agent-loop-triage` handler remains.

The current `@praesidium/agent-loop-impl` implementation and verification cores
already match the target shape: they accept a structured already-open action
binding, validate it against the live `wrkf` action record, and complete/fail
only that bound action.

## Implementation Flow

For a ready task under `wrkq-simple-task@1`:

```text
1. Scheduler discovers active/ready instance.
2. Scheduler claims action=implement role=implementer.
3. Runner starts/binds or resumes wrkf action run.
4. Implement handler runs:
   - red author names bounded bar
   - core executes red proof
   - implementer makes code changes
   - core re-runs frozen bar
   - core checks clean git and commit
   - complete implement with implement_result evidence
5. wrkf transitions to active/implemented.
6. Scheduler re-queries same instance.
7. Continuation policy requires verify before unrelated work.
8. Scheduler claims action=verify role=tester.
9. Verify handler runs:
   - loads exact source implement action
   - runs install
   - runs smoke/e2e
   - checks clean git and same commit
   - complete verify with verify_result evidence
10. wrkf closes task completed, or rewinds/blocks on semantic verification failure.
```

If `verify` fails operationally after implementation, the scheduler must not
silently move to unrelated work. It should either keep the instance pinned,
release as `operator_required`, or let the claim expire into a reaper path.

## Attachment and Intake

A scheduler that only discovers attached `wrkf` instances will not find plain
open wrkq tasks with no workflow attached. Decide one of:

1. Attach `wrkq-simple-task@1` when tasks are created.
2. Add an intake seeder that finds open no-spec tasks and attaches the built-in
   workflow.
3. Keep a compatibility triage scanner for unattached tasks only.

The preferred direction is eventual automatic attachment for normal executable
tasks, with explicit opt-out for reference/informational tasks.

## Failure and Recovery

Failure categories:

- Semantic block: handler completed action with evidence indicating blocked
  product/workflow state. `wrkf` should transition or project state accordingly.
- Operational failure: runner/handler infrastructure failed. The action should
  fail or remain reclaimable without changing product state.
- Scheduler crash: claim lease expires; reaper can reclaim or mark
  operator-required. Any stale handler that later wakes up must be fenced out
  before it can settle the action or release the claim.
- External run orphan: action has an `externalRunRef`, but the external runtime
  is terminal or missing and the action remains active. Reaper resolves it.
- Continuation gap: implement succeeded but verify did not start or did not
  terminalize. This is high-priority reclaim work before unrelated discovery.

Recovery must be idempotent. A repeated claim or action start with the same
canonical idempotency key should replay the original durable object or reject
mismatched parameters. Reaper must distinguish semantic block from operational
failure: a verify smoke failure is workflow/product evidence, while a missing
HRC runtime or lost external ref is operational/operator-required until an
adapter proves otherwise.

## Phased Plan

### Phase 1: Non-production vertical spike

- Implement an `agent-loop` runner that uses existing `wrkf.instance.next`,
  `wrkf.action.*`, and handler packages.
- Use the existing triage scanner or another explicit attachment source so the
  spike has real input.
- Enforce `implement -> verify` continuation in process for one runner.
- Run only one scheduler process.
- No new `wrkq/wrkf` storage.
- Label this mode explicitly as non-production: no crash-safety, no multi-runner
  guarantee, and no durable scheduler authority.

This proves handler dispatch and continuation semantics, but it is not a
multi-runner or crash-safe guarantee.

### Phase 2: Durable scheduler core

- Settle the scheduler ownership storage design: extend effect-lease mechanics
  or add a justified `workflow_scheduler_claims` table.
- Add `wrkf.scheduler.discover`, `claimNext`, `claimContinuation`,
  `heartbeat`, `release`, and `reap`.
- Add fencing semantics before enabling multi-runner use.
- Add deterministic action-run start/resume idempotency for claimed actions.
- Add release/action-run reconciliation.
- Add capability-aware claim selection and caller-owned scoping.
- Add tests for concurrent claimers, stale revision/context, expired leases,
  stolen fences, release/action mismatch, and mandatory continuation recovery.

This phase is not production-ready by itself. Production or unattended
multi-runner use remains blocked until durable continuation policy and ACP
verify-launch dedupe/precedence are resolved.

### Phase 3: continuation hardening

- Encode continuation policy in workflow template metadata or a registered
  scheduler policy catalog.
- Ensure `implement -> verify` is state/discovery-pinned across restarts.
- Resolve scheduler verify authority against the existing ACP verify-launch
  effect producer contract.
- Add dashboard/operator readback for pinned continuations and blocked
  mandatory actions.

### Phase 4: intake/attachment cleanup

- Decide task creation attachment behavior.
- Retire the standalone triage scanner except for explicit compatibility cases.
- Route all normal executable task work through scheduler discovery.

## Acceptance Criteria

- Two concurrent scheduler workers cannot claim the same workflow instance.
- An expired, stolen, or stale fence cannot complete, fail, transition, or
  release an action.
- A stale or stolen fence prevents a runner from entering worktree-mutating,
  external-runtime-launching, or other non-idempotent handler phases.
- If a runner loses the fence after partial local side effects, it stops and
  reports `operator_required`; it does not continue to verify or release as
  completed.
- `scheduler.release(completed)` fails unless the matching action run is
  terminal and belongs to the claim.
- If a claim expires after its action run terminalizes, the reaper reconciles the
  claim from terminal action truth and does not re-run the action.
- A successful `implement` action is followed by same-instance `verify` before
  the runner claims unrelated work.
- If the runner crashes after `implement` and before `verify`, a later scheduler
  discovers the continuation gap before fresh work and carries exact
  `sourceImplementActionRunId` plus commit lineage.
- If a runner dies with an active action, the reaper can fail/reclaim it and the
  task does not stay hidden behind an active run forever.
- ACP verify-launch and scheduler continuation cannot launch duplicate verify
  actions for the same source implement action.
- Runner/worktree guards refuse unrelated work while mandatory verify is
  unresolved for that worktree.
- Existing triage and implementation handlers can run from structured
  scheduler-provided bindings without starting their own scanner loops.
- `wrkf` remains the authority for workflow state, task-state projection,
  evidence, runs, actions, transitions, and effects.
- `agent-loop` traces and filesystem artifacts are linked from runs/evidence but
  are not required to reconstruct workflow truth.

## Open Questions

- Should production scheduler ownership extend existing `workflow_effects`
  lease mechanics or use a separate `workflow_scheduler_claims` table?
- Should production continuation policy live in workflow-template metadata or a
  separate `wrkf`-owned policy catalog?
- Should `wrkf.action.start` be folded into `wrkf.scheduler.claimNext`, or should
  claim and action-start remain separate but idempotent?
- What is the correct operator state for an implementation that succeeded but
  verification cannot launch due to infrastructure failure: `operator_required`,
  `failed`, or a leased continuation waiting for reaper?
- What is the durable workspace/worktree identity, if production scheduling
  coordinates worktree reuse globally?
- Should normal `wrkq touch` automatically attach `wrkq-simple-task@1`, or should
  attachment remain explicit until task kind/purpose is clearer?
