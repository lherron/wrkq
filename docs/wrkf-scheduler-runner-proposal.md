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
> implement action/commit binding independent of live claim rows;
> template-declared action-handler contracts; ASP-published handler manifests;
> ASPC-compiled agent work manifests; capability-aware claims that match handler
> contracts and agent assignability transactionally before leasing; caller-owned
> scoping; explicit
> worktree fencing or runner-owned worktree guarding; and a dedupe/precedence
> decision with ACP verify-launch before both verify launch paths are enabled.
> Phase 1 is a non-production vertical spike, and the durable scheduler-core
> phase is not production-ready until handler contracts, continuation policy, and
> verify-launch authority are settled.

This document has been patched to reflect that consensus. It remains proposal
text; durable architecture records should be added or amended in the
implementation PR that introduces production scheduler claims/fences.

## Goals

- Discover runnable work across active attached `wrkf` instances, filtered by
  project, path, template, role, lane, status, runner capability, and
  HRC-returned ASPC agent assignability.
- Claim one runnable action for one workflow instance with lease and fence
  tokens so multiple runners cannot execute or settle the same action.
- Run exactly one action, complete/fail it durably, then re-query `wrkf`.
- Support mandatory same-instance continuation policies such as
  `implement -> verify`.
- Support discovered cross-repo worksets without requiring the user to manually
  decompose a simple task up front.
- Recover from scheduler or coordinator crashes through expiring leases,
  monotonic fencing, and explicit reclaim/fail/operator-required behavior.
- Let existing domain packages such as `@praesidium/agent-loop-triage` and
  `@praesidium/agent-loop-impl` become action handlers under the generic
  runner.
- Make executable action semantics durable, discoverable, and capability
  matchable through template-declared handler contracts, ASP-published handler
  manifests, and ASPC-compiled agent work manifests.
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
claiming, durable action-handler contracts, mandatory continuation ownership,
and crash recovery across the scheduler boundary.

## Action Handler Contract Plane

Production scheduling needs a durable action-handler contract plane. A
code-local map from workflow/action strings to TypeScript functions is only an
adapter cache; it is not enough for scheduler admission or capability matching.

Design note: Lance said no to the hash/provenance layer for this proposal. Do
not add manifest hashes, prompt-bundle hashes, schema hashes, capability hashes,
source provenance, or delivery-ref provenance to scheduler admission. Use named
contracts, versions, roles, work classes, side-effect classes, workspace modes,
and normal `wrkf` action/run/evidence records.

Handler contract and assignment information appears in seven places with separate
authority:

1. **Workflow template action slots.** A template declares the durable contract
   requirements for each executable action. The declaration names the semantic
   action, transition, role, required handler contract, output evidence kinds,
   side-effect classes, and continuation policy.
2. **ASP agent/space handler source.** ASP owns the authored handler
   definition: agent-local or space-provided prompt bundles, instruction files,
   tool/runtime requirements, output schemas, and capability declarations. This
   is where agent-specific prompts belong. `wrkf` must not become the authoring
   surface for agent prompts.
3. **ASPC-compiled agent work manifest.** ASPC compiles each agent profile,
   agent-local skills, composed spaces, handler sources, and explicit
   work declarations into one durable agent manifest. The manifest lists all
   work the agent may be assigned: handler contracts, roles, work classes, risk
   tiers, side-effect classes, project/workspace scopes, concurrency limits, and
   hard exclusions. Assignment must not be inferred from persona text, a bare
   role name, or prior successful runs.
4. **ASP-published handler manifest.** ASP publishes a handler manifest for each
   production handler contract. The manifest defines contract id/version,
   handler id/version, input/output schema refs, required runner capabilities,
   declared side-effect classes, and implementation references.
5. **HRC agent-manifest query surface.** HRC stores or can resolve the latest
   ASPC-produced agent manifests for runnable agents and exposes an endpoint to
   list agents that can perform a proposed task/action. This is the API
   `agent-loop` calls; `agent-loop` should not parse ASP files itself.
6. **`wrkf` manifest/assignability snapshot.** `wrkf` imports or snapshots the
   handler contract, handler version, and HRC-returned assignability decision
   needed for scheduling. It does not author prompts, decide agent skills, store
   prompt provenance, or execute handlers. This is not the existing low-level
   hook/effect catalog unless that catalog is explicitly extended and renamed;
   hook/effect handlers are too low-level to carry agent action semantics by
   themselves.
7. **Runner executable registry.** The generic runner maps an exact
   handler contract plus handler id/version to executable code or harness launch
   mechanics. It is an adapter, not the semantic source of truth. If the local
   registry cannot satisfy the selected handler contract, it refuses before any
   side effect.

Example template action slot:

```toml
[actions.implement]
transition = "implement_complete"
role = "implementer"
handlerContract = "praesidium.wrkq-simple-task.implement@1"
outputEvidence = ["implement_result"]
requiredFacts = ["commit.sha", "bar.schemaVersion"]
sideEffects = ["worktree.write", "git.commit"]
continuation = ["verify"]
```

Example handler manifest shape:

```ts
interface AspHandlerManifest {
  handlerContract: "praesidium.wrkq-simple-task.implement@1"
  handlerId: "@praesidium/agent-loop-impl/runImplTaskRunner"
  handlerVersion: string
  inputSchemaRef?: string
  outputSchemaRef?: string
  outputEvidenceKinds: string[]
  requiredCapabilities: WrkfRunnerCapability[]
  sideEffectClasses: string[]
  implementationRef: {
    provider: "asp"
    agentOrSpaceRef: string
    package: string
    export: string
  }
}
```

Example ASPC agent manifest:

```ts
interface AspcAgentManifest {
  agentRef: "agent:cody"
  version: string
  workCapabilities: AspcAgentWorkCapability[]
}

interface AspcAgentWorkCapability {
  roles: string[]
  handlerContracts: string[]
  workClasses: string[]
  projectScopes: string[]
  sideEffectClasses: string[]
  workspaceModes: string[]
  riskClasses?: string[]
  maxConcurrent?: number
  exclusions?: string[]
}
```

Prompt text lives with the ASP agent/space handler source, not inline in the
workflow template. The scheduler records the selected handler contract,
handler id/version, and assignee. Prompt text is not copied into `wrkf`, and
prompt or schema changes should be managed by normal ASP/agent versioning rather
than scheduler-owned change provenance.

## Core Model

The scheduler operates on this conceptual unit:

```ts
interface SchedulerClaim {
  claimId: string
  instanceId: string
  task: string
  workflow: { id: string; version: string }
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
    handlerContract: string
    handlerId: string
    handlerVersion?: string
    outputEvidenceKinds: string[]
    sideEffectClasses: string[]
  }
  assignee: {
    agentRef: string
    role: string
    workClasses?: string[]
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

interface DynamicLaneRef {
  worksetId: string
  laneId: string
  workspaceRef?: string
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
   be the action-run idempotency key. Claim/start/resume must use existing
   wrkq/wrkf idempotency semantics so stale-but-retried claims resume an
   existing action run instead of forking a second one. Do not introduce a
   separate claim-local idempotency discipline.
6. **Continuation is ledger-derived and crash-survivable.** After
   `implement.done`, discovery must rank the same-instance `verify`
   continuation as non-skippable before fresh unrelated work. That discovery
   must be derived from workflow ledger truth, such as instance state and action
   runs, not from the presence of live scheduler claim rows. `claimContinuation`
   is a same-runner fast path, not the only recovery path after a crash.
7. **Exact verify source binding.** Verify claims and bindings must carry the
   exact `sourceImplementActionRunId` and source commit. "Latest implement"
   fallback is forbidden.
8. **No double verify launch.** Until the scheduler supersedes or consumes
   `wrkq.contract.wrkf-verify-launch-producer`, ACP verify-launch effects remain
   a separate opt-in path. The production design must define dedupe/precedence
   before both paths are enabled for the same workflow.
9. **Capability-aware claim selection.** `claimNext` must select only actions
   the runner declares it can execute and an ASP-declared agent can be assigned.
   A runner should not claim work and then discover there is no handler or no
   eligible assignee.
10. **Template-declared handler contract required.** No production scheduler
    claim or side effect may execute for an action lacking a template-declared
    handler contract and a matching durable handler manifest.
11. **Handler and assignee matching are transactional.**
    `actions.<name>.handlerContract`, the handler id/version, the
    runner-advertised capability, and the HRC-returned ASPC agent capability
    must match inside the claim transaction before leasing. Unsupported work is
    visible as unsupported, not claimed and failed locally.
12. **No scheduler-owned prompt provenance.** Every action run and terminal
    action evidence row should include the handler contract, handler id/version,
    and assignee. It should not include scheduler-owned prompt-bundle, schema,
    manifest, source-provenance, capability, or delivery reference fields.
13. **Caller-owned scope.** Project/path filters are caller-resolved selectors.
   Scheduler RPC must not read `WRKQ_PROJECT_ROOT`, `ASP_PROJECT`, `--project`,
   or caller environment to infer scope.
14. **Worktree pin is explicit.** `wrkf` can durably pin instance/action/source
    binding. If production scheduling coordinates worktrees globally, the claim
    must also record a workspace/worktree identity and fence it. Otherwise the
    runner/runtime owns the worktree guard explicitly.

## Proposed wrkf RPC Surface

### ASP handler manifest publication

ASP must provide a publication surface for action-handler manifests before
production scheduler admission. `claimNext` must not depend on manifests that
exist only in runner process memory.

```ts
interface AspHandlerManifestPublishParams {
  manifest: AspHandlerManifest
  idempotencyKey?: string
}

interface AspHandlerManifestPublishResult {
  handlerContract: string
  handlerId: string
  handlerVersion?: string
}
```

Publishing should be idempotent by handler contract/id/version. This proposal
does not require a no-write canonicalization operation.

### ASPC agent manifest compilation

ASPC must compile agent work manifests from source configuration and composed
spaces. This is the authoritative declaration of what each agent can be assigned.
The proposed protocol method is `aspc.compileAgentManifest`.

```ts
interface AspcCompileAgentManifestParams {
  agentRef?: string
  project?: string
  includeInactive?: boolean
}

interface AspcCompileAgentManifestResult {
  manifests: AspcAgentManifest[]
}
```

ASPC manifest compilation should be deterministic enough for repeatable
operator readback, but scheduler admission uses the declared capabilities
directly rather than a separate identity artifact.

### HRC agent manifest endpoints

HRC must expose the compiled agent manifests to automation clients. The proposed
machine endpoints are `hrc.agentManifest.list` and `hrc.agentManifest.match`.
`agent-loop` uses these endpoints to discover assignable agents and build the
capability tuples it passes to `wrkf.scheduler.claimNext`.

```ts
interface HrcAgentManifestListParams {
  project?: string
  agents?: string[]
  includeInactive?: boolean
}

interface HrcAgentManifestListResult {
  manifests: AspcAgentManifest[]
}

interface HrcAgentManifestMatchParams {
  project?: string
  task?: string
  requirement: {
    handlerContract: string
    role: string
    workClasses?: string[]
    sideEffectClasses: string[]
    workspaceMode?: string
    riskClass?: string
  }
  limit?: number
}

interface HrcAgentManifestMatchResult {
  agents: Array<{
    agentRef: string
    roles: string[]
    handlerContracts: string[]
    workClasses: string[]
    sideEffectClasses: string[]
    workspaceModes: string[]
  }>
}
```

The HRC endpoint may be implemented as HTTP, JSON-RPC, or the existing HRC
client surface, but it must be a stable machine contract. It should return only
agents whose ASPC manifest explicitly permits the requested work. It may also
apply HRC runtime availability and capacity filters, but those filters must be
reported distinctly from static assignability misses.

`wrkf.scheduler.claimNext` must not call HRC from inside its database
transaction. The runner calls HRC first, then passes the selected
handler/assignee capability tuple to `claimNext`. `wrkf` transactionally matches
that tuple against the current workflow action, imported handler manifest, and
current revision/context CAS before leasing. HRC freshness or runtime-capacity
races are operational failure/retry conditions, not semantic workflow outcomes.

### `wrkf.handlerManifest.import`

Import or refresh a scheduler-visible snapshot of an ASP-published manifest by
`handlerContract`, `handlerId`, and optional `handlerVersion`. This is an
admission record, not the prompt authoring surface. `wrkf` stores enough of the
manifest to perform discovery and transactional capability matching.

### `wrkf.handlerManifest.show`

Read an imported manifest snapshot by `handlerContract`, `handlerId`, and
optional `handlerVersion`. Scheduler discovery uses this to explain runnable
work and unsupported work.

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
  agents?: string[]
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
  workflow: { id: string; version: string }
  state: { status: string; phase?: string; outcome?: string }
  revision: number
  contextHash: string
  nextAction: WrkfNextAction
  handler: WrkfHandlerRequirement
  assignment: WrkfAssignmentRequirement
  blockedTransitions?: WrkfBlockedTransition[]
  openObligations?: WrkfObligation[]
  pendingEffects?: WrkfEffect[]
  claimStatus?: "unclaimed" | "claimed" | "expired" | "continuation"
}

interface WrkfHandlerRequirement {
  handlerContract: string
  handlerId: string
  handlerVersion?: string
  outputEvidenceKinds: string[]
  sideEffectClasses: string[]
}

interface WrkfAssignmentRequirement {
  role: string
  handlerContract: string
  workClasses?: string[]
  projectScope?: string
  sideEffectClasses: string[]
  riskClass?: string
  eligibleAgents?: Array<{
    agentRef: string
  }>
  unsupportedReason?: "no_handler" | "no_assignable_agent" | "capability_mismatch"
}

interface WrkfRunnerCapability {
  handlerContract: string
  handlerId: string
  handlerVersion?: string
  sideEffectClasses: string[]
  structuredOutput: boolean
  workspaceModes: string[]
  assignee: {
    agentRef: string
  }
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
  token, selected assignee, and expiry.
- Match the candidate's template-declared handler contract, handler id/version,
  and HRC-returned ASPC agent capability against `capabilities` inside the claim
  transaction before claiming it. Unsupported or unassignable candidates remain
  unclaimed and visible as unsupported.
- Do not call HRC inside the claim transaction. The runner supplies the
  HRC-returned capability/assignee tuple; `wrkf` validates it against
  scheduler-visible facts and current workflow state.
- Use the existing wrkq/wrkf idempotency behavior. The semantic action identity
  remains the canonical action-run idempotency key.

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
state: canonical action identity, handler contract assignment, continuation and
pinning source binding, prior-claim links, optional workspace/worktree fencing,
and action-run reconciliation.

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
  handler_contract TEXT NOT NULL,
  handler_id TEXT NOT NULL,
  handler_version TEXT,
  output_evidence_kinds TEXT NOT NULL,
  side_effect_classes TEXT NOT NULL,
  assignee_agent_ref TEXT NOT NULL,
  assignee_role TEXT NOT NULL,
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

Action-run audit identity must also be persisted at action start, before any
external side effect. Claim rows and terminal evidence are not enough: an
active or orphaned action run must still expose the handler assignment needed
for reaper and operator diagnosis. The implementation may add immutable
`workflow_runs` columns or write an immutable start-time metadata/evidence row,
but it must carry at least:

```text
handler_contract
handler_id
handler_version
output_evidence_kinds
side_effect_classes
assignee_agent_ref
assignee_role
```

The reaper must be able to read these fields without relying on a live scheduler
claim row or terminal evidence.

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
and exact source binding.

## Dynamic Worksets and Cross-Repo Lanes

Simple tasks should not require the user to predict repository boundaries. A
normal executable task can start with the same `triage -> implement -> verify`
shape and expand only when an early action proves that multiple repositories or
workspaces are required.

Dynamic lanes are not stored as agent-local arrays and are not predeclared as
static template branches. They are stored as durable workflow ledger facts:

- A planning or impact-discovery action writes a typed workset evidence row,
  for example `cross_repo_workset` or `decomposition_plan`.
- That workset evidence contains stable lane ids, repo/project/workspace
  identity, required changes, verification commands, and integration
  requirements.
- Scheduler discovery derives lane-scoped runnable candidates from the current
  instance state plus the latest accepted workset evidence.
- Scheduler claims, action runs, and result evidence all carry the same
  `worksetId` and `laneId`.
- The workset is append-only evidence. Corrections are new evidence plus a
  transition/revision, not in-place mutation of prior decomposition facts.

Candidate workset evidence shape:

```ts
interface CrossRepoWorkset {
  worksetId: string
  sourceActionRunId: string
  discoveredAtRevision: number
  lanes: CrossRepoLane[]
  integration: {
    requiredAfter: string[]
    verifyAction: "integration_verify"
  }
}

interface CrossRepoLane {
  laneId: string
  project: string
  repoPath: string
  workspaceRef: string
  requiredChange: string
  verifyPlan: string[]
  status: "pending" | "implemented" | "verified" | "blocked" | "operator_required"
}
```

For a one-repo task, the workset may contain a single lane, or the scheduler may
use the ordinary action path directly. For a cross-repo task, `claimNext`
materializes repo-scoped candidates from the workset:

```text
implement lane=repo:wrkq
verify    lane=repo:wrkq
implement lane=repo:taskboard
verify    lane=repo:taskboard
integration_verify lane=workset:ws_001
```

Lane identity must be part of the canonical action identity and the action-run
idempotency key. A scheduler claim for a dynamic lane must include:

```ts
interface SchedulerClaim {
  // existing fields omitted
  action: {
    id: string
    kind: string
    role: string
    lane?: string
  }
  dynamicLane?: {
    worksetId: string
    laneId: string
    workspaceRef: string
    sourceWorksetEvidenceId: string
  }
}
```

Execution then records normal action runs and evidence, but every row that is
lane-specific carries the lane identity:

- `workflow_runs.action = "implement"` and `workflow_runs.lane = "repo:wrkq"`.
- Scheduler claim `lane = "repo:wrkq"` and `workspace_ref = "wrkq:/path/to/repo"`.
- Implement evidence includes `worksetId`, `laneId`, `actionRunId`, and source
  commit.
- Verify evidence includes `worksetId`, `laneId`,
  `sourceImplementActionRunId`, and verified commit.

The lane is considered `verified` only after its mandatory
`implement -> verify` continuation terminalizes successfully. `integration_verify`
does not become runnable until all required lanes are verified or explicitly
disposed by workflow state. If integration fails, `wrkf` should open targeted
repair actions for the affected lane ids rather than falling back to a broad
unscoped implement action.

If an action starts as ordinary `implement` and discovers that cross-repo work
is required, it should not keep editing ad hoc across repositories. It should
complete or block with evidence such as:

```ts
interface NeedsReplanResult {
  result: "needs_replan"
  reason: "cross_repo_required"
  proposedWorkset: CrossRepoWorkset
}
```

`wrkf` then transitions the instance into a planned/decomposed phase, and
subsequent scheduler discovery emits lane-scoped candidates. This keeps
decomposition inside the workflow ledger instead of inside a coordinator's
memory.

## agent-loop Runner Concept

Add a generic runner package, for example:

```text
packages/agent-loop-wrkf-runner/
```

Responsibilities:

- Open an `AgentLoopWorkContext`.
- Query HRC for ASPC-compiled agent manifests or `hrc.agentManifest.match` results
  that satisfy candidate handler/role/work requirements.
- Claim runnable work from `wrkf.scheduler.claimNext` using handler capability
  and HRC-returned agent assignability tuples.
- Resolve an action handler by exact handler contract, handler id/version,
  and declared side-effect/workspace requirements.
- Select only an assignee whose HRC-returned ASPC agent manifest capability
  allows that handler contract, role, side-effect class, project scope, and work
  class.
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
  handlerManifest: AspHandlerManifest
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
  readonly handlerContract: string
  readonly handlerId: string
  readonly handlerVersion?: string
  run(input: WrkfActionHandlerInput): Promise<{
    status: "completed" | "blocked" | "failed" | "skipped"
    summary: string
  }>
}
```

Handler registration is manifest-driven. Templates declare handler contracts;
ASP handler manifests provide the executable contract; ASPC agent manifests
provide the assignability contract; HRC returns eligible agents; the local
registry only proves that this runner has code/harness mechanics for the
selected handler contract.

```ts
const registry = createHandlerRegistry([
  {
    handlerContract: "praesidium.wrkq-simple-task.triage@1",
    handlerId: "@praesidium/agent-loop-triage/runTriageTaskRunner",
    handlerVersion: "1",
    assignee: {
      agentRef: "agent:cody",
      roles: ["triager"],
      handlerContracts: ["praesidium.wrkq-simple-task.triage@1"],
      workClasses: ["task.triage"],
      projectScopes: ["wrkq"],
      sideEffectClasses: [],
      workspaceModes: ["read-only"],
    },
    run: runTriageTaskRunner,
  },
  {
    handlerContract: "praesidium.wrkq-simple-task.implement@1",
    handlerId: "@praesidium/agent-loop-impl/runImplTaskRunner",
    handlerVersion: "1",
    assignee: {
      agentRef: "agent:cody",
      roles: ["implementer"],
      handlerContracts: ["praesidium.wrkq-simple-task.implement@1"],
      workClasses: ["task.implementation"],
      projectScopes: ["wrkq"],
      sideEffectClasses: ["worktree.write", "git.commit"],
      workspaceModes: ["pinned-worktree"],
    },
    run: runImplTaskRunner,
  },
  {
    handlerContract: "praesidium.wrkq-simple-task.verify@1",
    handlerId: "@praesidium/agent-loop-impl/runImplTaskRunner",
    handlerVersion: "1",
    assignee: {
      agentRef: "agent:smokey",
      roles: ["tester"],
      handlerContracts: ["praesidium.wrkq-simple-task.verify@1"],
      workClasses: ["task.verification"],
      projectScopes: ["wrkq"],
      sideEffectClasses: ["install", "e2e"],
      workspaceModes: ["pinned-worktree"],
    },
    run: runImplTaskRunner,
  },
])
```

The runner obtains the assignee portion from HRC, then advertises exact
handler-plus-agent capability tuples to `claimNext`. It must not claim by
workflow/action string alone, by role name alone, or by a generic "do the next
thing" prompt. A non-production spike may keep a local map only if it is loudly
marked throwaway and preferably generated from handler manifest fixtures plus
HRC/ASPC agent-manifest fixtures.

The current `loops/wrkq-task-triage/wrkq-task-triage.ts` scanner becomes a
compatibility shim or disappears after task creation/attachment policy is
settled. The packaged `@praesidium/agent-loop-triage` handler remains.

The current `@praesidium/agent-loop-impl` implementation and verification cores
already match the bound-action execution shape: they accept a structured
already-open action binding, validate it against the live `wrkf` action record,
and complete/fail only that bound action. They do not yet publish durable
handler manifests; that is required before production scheduler admission.

## Implementation Flow

For a ready task under `wrkq-simple-task@1`:

```text
1. Scheduler discovers active/ready instance.
2. Runner asks HRC hrc.agentManifest.match for agents assignable to
   action=implement role=implementer with the required handler contract,
   work class, side effects, project scope, and workspace mode.
3. Scheduler claims action=implement role=implementer with the selected
   handler contract/id/version and assignee.
4. Runner starts/binds or resumes wrkf action run.
5. Implement handler runs:
   - red author names bounded bar
   - core executes red proof
   - implementer makes code changes
   - core re-runs frozen bar
   - core checks clean git and commit
   - complete implement with implement_result evidence
6. wrkf transitions to active/implemented.
7. Scheduler re-queries same instance.
8. Continuation policy requires verify before unrelated work.
9. Runner asks HRC hrc.agentManifest.match for agents assignable to verify the
   exact source implement action/commit.
10. Scheduler claims action=verify role=tester with exact source binding and
    selected assignee capability.
11. Verify handler runs:
   - loads exact source implement action
   - runs install
   - runs smoke/e2e
   - checks clean git and same commit
   - complete verify with verify_result evidence
12. wrkf closes task completed, or rewinds/blocks on semantic verification failure.
```

If `verify` fails operationally after implementation, the scheduler must not
silently move to unrelated work. It should either keep the instance pinned,
release as `operator_required`, or let the claim expire into a reaper path.

For a cross-repo workset, the same flow runs per lane and then gates aggregate
verification:

```text
1. impact_discover writes cross_repo_workset evidence.
2. Scheduler emits implement(repo A), implement(repo B), ...
3. Each repo lane runs implement -> verify with exact source binding.
4. integration_verify runs only after required lane verifies pass.
5. publish/open_prs/merge runs only after integration verification passes.
```

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
- Use manifest fixtures or a generated local registry for handler dispatch; any
  hand-written workflow/action map must be labeled throwaway.
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
- Add ASP handler manifest publication, `wrkf` manifest import/show snapshots,
  ASPC agent manifest compilation, HRC
  `hrc.agentManifest.list`/`hrc.agentManifest.match` endpoints, and
  template-declared handler contract refs before enabling production claim
  admission.
- Add fencing semantics before enabling multi-runner use.
- Add deterministic action-run start/resume idempotency for claimed actions.
- Add release/action-run reconciliation.
- Add capability-aware handler/agent-assignability claim selection and
  caller-owned scoping.
- Add tests for concurrent claimers, stale revision/context, expired leases,
  stolen fences, release/action mismatch, missing handler support, unassignable
  agents, and mandatory continuation recovery.

This phase is not production-ready by itself unless handler contracts, ASPC
agent manifests, HRC agent-manifest query endpoints, durable continuation
policy, and ACP verify-launch dedupe/precedence are all resolved.

### Phase 3: continuation hardening

- Encode continuation policy in workflow template metadata or a registered
  scheduler policy catalog.
- Publish ASP production manifests for `wrkq-simple-task@1` triage, implement,
  and verify handlers, including evidence schemas.
- Publish ASPC agent work manifests for the agents eligible to triage,
  implement, verify, and perform cross-repo integration work.
- Add an agent-loop HRC client path that calls `hrc.agentManifest.match` before
  advertising assignee capabilities to `claimNext`.
- Ensure `implement -> verify` is state/discovery-pinned across restarts.
- Resolve scheduler verify authority against the existing ACP verify-launch
  effect producer contract.
- Define typed workset evidence and dynamic lane materialization for
  cross-repo implementation tasks.
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
  `sourceImplementActionRunId` plus source commit.
- If a runner dies with an active action, the reaper can fail/reclaim it and the
  task does not stay hidden behind an active run forever.
- Production templates cannot expose runnable actions unless each executable
  action declares a handler contract, required output evidence, side-effect
  classes, and source-binding requirements where needed.
- `discover` exposes required handler contract/capabilities and marks
  unsupported work without leasing it.
- HRC can list agents that can perform a proposed task/action from ASPC-compiled
  manifests without agent-loop parsing ASP source files.
- `discover` distinguishes unsupported work caused by missing handler support
  from unsupported work caused by no HRC/ASPC assignable agent.
- `claimNext` refuses unsupported or unassignable runner
  capabilities inside the claim transaction.
- `claimNext` does not call HRC inside its database transaction; it validates a
  caller-supplied HRC/ASPC capability tuple against current wrkf state.
- An agent cannot be assigned a claim unless the HRC-returned ASPC agent
  manifest capability permits the handler contract, role, side-effect classes,
  project scope, and work class.
- Retried action start with the same canonical action key replays rather than
  forking a second semantic action run.
- The `agent-loop` runner refuses to execute a claim if its local registry cannot
  satisfy the selected handler contract and handler id/version.
- Action-run audit identity is visible for active and orphaned runs, not only
  after terminal evidence.
- Terminal action evidence includes the handler contract, handler id/version,
  and assignee agent ref used.
- ACP verify-launch and scheduler continuation cannot launch duplicate verify
  actions for the same source implement action.
- Runner/worktree guards refuse unrelated work while mandatory verify is
  unresolved for that worktree.
- Cross-repo decomposition is stored as typed workset evidence, not agent
  memory.
- Scheduler candidates for dynamic lanes include stable `worksetId`, `laneId`,
  `workspaceRef`, and source workset evidence id.
- Per-lane `implement -> verify` continuations must verify the exact lane commit
  before `integration_verify` becomes runnable.
- Existing triage and implementation cores can run from structured
  scheduler-provided bindings without starting their own scanner loops once they
  publish matching handler manifests.
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
- Is dynamic workset state projected entirely from evidence, or should `wrkf`
  add a first-class workset/lane table once the evidence contract stabilizes?
- Should normal `wrkq touch` automatically attach `wrkq-simple-task@1`, or should
  attachment remain explicit until task kind/purpose is clearer?
