# WRKF Lean Agentic Workflow Specification

Status: target architecture specification

## 1. Purpose

`wrkq` is the local-first task substrate. It owns task identity, task lifecycle state, containers, comments, attachments, relations, search, audit events, principal attribution, and machine-readable human/agent task operations.

`wrkf` is the workflow semantic layer attached to `wrkq` tasks. It owns workflow templates, workflow instances, executable action definitions, run attempts, evidence, transitions, obligations, external effects, and workflow audit. `wrkf` must not replace the `wrkq` task lifecycle; it projects workflow conclusions into task state only where a built-in workflow explicitly declares that projection.

The goal of this specification is to evolve `wrkf` into a lean, pragmatic, high-value agentic workflow system. The system should make unattended agent execution reliable without turning the workflow engine into an agent runtime, distributed scheduler, prompt provenance system, or generic governance framework.

The core lifecycle is:

```text
wrkq task
  -> wrkf workflow instance
    -> executable semantic action
      -> run attempt
        -> evidence
          -> settlement
            -> transition
              -> external effects only when truly external
```

The default happy path for ordinary executable tasks is:

```text
triage -> implement -> verify -> done
```

Review, approval, release, cross-repo decomposition, and human intervention are workflow policy extensions, not mandatory ceremony for every task.

## 2. Design principles

### 2.1 One semantic authority

`wrkf` is the only authority for workflow legality and semantic state. Runners, HRC sessions, ACP commands, shell scripts, hooks, and task scanners may execute work, but they must not infer workflow legality by scraping `wrkq` tasks or local filesystem state.

### 2.2 Runs are execution attempts, not diagnostics only

A `workflow_run` is the durable record of an attempt to execute one semantic action. It should carry the action identity, attempt identity, agent/scope provenance, optional external runtime reference, optional workspace reference, source binding, status, and terminal result.

### 2.3 Claims are ownership on runs

A claim is a verb, not a standalone semantic object. Claiming work means creating or resuming the current active run attempt for one semantic action and attaching temporary execution ownership to that run.

There must be no independent durable scheduler-claim lifecycle that can disagree with the run lifecycle.

### 2.4 Settlement is the semantic write boundary

Agent execution does not change workflow truth. HRC run completion does not change workflow truth. Evidence files do not change workflow truth by themselves.

A fenced `wrkf` settlement changes workflow truth. Settlement atomically writes run-linked evidence, applies a transition when appropriate, terminalizes the run attempt, clears execution ownership, emits real external effects, and applies internal projections.

### 2.5 Safety belongs at irreversible boundaries

The system should concentrate safety at the boundaries that matter:

```text
claim active execution ownership
bind or observe external execution, when used
settle semantic truth
recover expired active attempts
emit external effects
```

Do not spread authority checks and lifecycle objects throughout the system as ceremony. Checks that do not protect an irreversible boundary should be removed, made diagnostic, or moved to the runner/runtime layer.

### 2.6 Exact source binding beats inference

A verification action must verify the exact implementation run and exact source artifact/commit it was derived from. It must never verify “latest implementation” by convention or filesystem inspection.

### 2.7 Effects are for external delivery

External effects are outbox items that require retry, idempotency, lease/ack/fail, or adapter delivery. Internal projections, such as setting a `wrkq` task state after a workflow closes, should be handled by `wrkf` settlement or an internal projection mechanism, not by generic effect delivery unless intentionally configured as external integration.

## 3. System boundaries

### 3.1 `wrkq`

`wrkq` owns:

- task UUIDs, friendly IDs, paths, containers, and project hierarchy;
- task lifecycle state such as `open`, `in_progress`, `blocked`, `completed`, `cancelled`, `archived`, and `deleted`;
- task descriptions/specifications, comments, attachments, relations, handoffs, and search;
- principal attribution for task mutations;
- durable local storage and audit events.

`wrkq` does not decide workflow transitions. It may display workflow state and receive internal projections from `wrkf`.

### 3.2 `wrkf`

`wrkf` owns:

- workflow templates and installed template versions;
- workflow instances attached to tasks;
- executable action specifications;
- run attempts for actions;
- run ownership leases;
- evidence and evidence fact validation;
- transition admission and transition events;
- obligations that represent real waits;
- external effects and effect leases;
- workflow audit and timeline reconstruction.

`wrkf` does not own HRC processes, agent prompt authoring, ASP spaces, worktree implementation mechanics, or cloud/team authorization.

### 3.3 `agent-loop`

`agent-loop` is an execution adapter/runtime. It may:

- query `wrkf` for executable candidates;
- claim one action run;
- invoke local handler code;
- launch HRC or another runtime;
- heartbeat active work;
- bind external runtime references to runs;
- call settlement;
- write traces and artifacts for diagnosis.

`agent-loop` traces are not workflow truth.

### 3.4 HRC

HRC owns runtime/session/process execution. It may run a selected agent under a selected scope and return an `hrcRunId` or equivalent external runtime reference.

HRC does not decide workflow legality. A completed HRC run does not complete a workflow action until `wrkf` accepts settlement.

### 3.5 ASP/ASPC

ASP/ASPC own agent and space authoring, prompt bundles, local skills, and compiled agent work capabilities. `wrkf` may consume named handler contracts and optional capability facts, but `wrkf` must not become the prompt provenance or ASP manifest authority.

### 3.6 ACP and other command launchers

ACP and similar systems may consume external effects or wake execution, but they must converge on the same `wrkf` action claim path. They must not independently start semantic action runs with separate idempotency keys for the same semantic action.

## 4. Core concepts

### 4.1 Workflow instance

A workflow instance is the mutable semantic state of one task under one installed workflow template.

Target shape:

```ts
interface WorkflowInstance {
  id: string
  taskUuid: string
  taskRef: string
  projectId?: string

  template: {
    id: string
    version: string
    hash: string
  }

  status: "active" | "waiting" | "closed"
  phase?: string
  outcome?: string

  stateRevision: number
  taskDocEtag: string
  taskDocHash: string

  createdAt: string
  updatedAt: string
  closedAt?: string
}
```

Compatibility rule: existing instances may still use `status="open"`. New built-in templates should not use workflow `open`; `open` is a `wrkq` task lifecycle state. New workflows should use `active`, `waiting`, and `closed`.

### 4.2 Executable action specification

A workflow template must declare executable actions directly. Agentic execution should not infer actions from transitions, role bindings, lanes, or prose in `nextActionModel`.

Target shape:

```ts
interface ExecutableActionSpec {
  id: string                         // "triage", "implement", "verify", ...
  description?: string

  from?: {
    status?: string
    phase?: string
    outcome?: string
  }

  role: string
  transition: string
  resultEvidenceKind: string

  handlerContract?: string           // named contract only, no prompt provenance
  workClass?: string
  riskClass?: string

  workspaceMode?: "none" | "read-only" | "exclusive"
  sideEffectClasses?: string[]       // e.g. worktree.write, git.commit, install, smoke
  sourceBinding?: SourceBindingSpec
  continuation?: ContinuationSpec

  rank?: number
}


interface SourceBindingSpec {
  kind: "previous_action"
  action: string                     // e.g. "implement"
  requiredFacts?: string[]           // e.g. ["commit.sha"]
  bindFields?: {
    sourceRunId?: string
    sourceEvidenceId?: string
    commitSha?: string
  }
}

interface ContinuationSpec {
  next: string                       // e.g. "verify"
  attentionScope?: "instance" | "workspace"
  requireExactSource?: boolean
}
```

Template guidance:

- Keep executable action specs small.
- Keep bounded mechanical handler checks inside action handlers unless they need to affect workflow state or settlement guards.
- Do not embed prompt text.
- Do not embed local TypeScript function names unless explicitly used as a handler contract adapter hint.
- Do not require ASP/ASPC manifest hashes or prompt-bundle hashes in `wrkf`.
- Use named contracts and versions where durable matching is needed.

### 4.3 Semantic action key

A semantic action key identifies the work that must be done, independent of a particular retry attempt.

Examples:

```text
triage:wfi_123:r1
implement:wfi_123:r2
verify:wfi_123:wr_run_impl_002:abc1234
implement:wfi_123:workset_7:lane_wrkq:r5
verify:wfi_123:workset_7:lane_wrkq:wr_run_impl_009:def5678
```

Invariants:

```text
same semantic work -> same semanticActionKey
different semantic work -> different semanticActionKey
at most one nonterminal run attempt per instance + semanticActionKey
retryable terminal operational failure may create a later attempt with same semanticActionKey
```

The `semanticActionKey` is the dedupe and active-concurrency primitive. The `runId` is the attempt identity.

### 4.4 Run attempt

A run attempt is one concrete attempt by an agent, runtime, command, or handler to execute an executable action.

Target shape:

```ts
interface WorkflowRunAttempt {
  id: string
  instanceId: string

  semanticActionKey: string
  action: string
  role: string
  attempt: number

  status:
    | "active"
    | "completed"
    | "semantic_blocked"
    | "operational_failed"
    | "operator_required"
    | "cancelled"

  agentRef?: string                  // e.g. "agent:cody"
  scopeRef?: string                  // e.g. "agent:cody:project:wrkq"

  handlerContract?: string
  handlerId?: string
  handlerVersion?: string

  externalRunRef?: string            // e.g. "hrc:hrc_run_7002"
  workspaceRef?: string

  source?: ActionSourceBinding

  startedAt: string
  completedAt?: string
  terminalSummary?: string
}

interface ActionSourceBinding {
  sourceRunId: string
  sourceEvidenceId?: string
  commitSha?: string
  artifactRef?: string
}
```

Compatibility rule: existing `workflow_runs.actor` should be treated as a legacy projection of `agentRef` or supplied principal. Existing `deliveryRef` should be treated as compatibility metadata, not a core binding concept.

### 4.5 Active run ownership

Active ownership is temporary write authority over a nonterminal run attempt.

Target shape:

```ts
interface WorkflowRunOwnership {
  runId: string
  runnerId: string
  ownerToken: string
  ownerGeneration: number
  leaseExpiresAt: string
  heartbeatAt: string
}
```

This can be stored in a subordinate table such as `workflow_run_execution` or as nullable columns on `workflow_runs`. It must not have an independent semantic terminal status. Terminal status belongs to the run.

### 4.6 Action candidate

An action candidate is a read-only projection from workflow state.

```ts
interface ActionCandidate {
  instanceId: string
  task: string
  semanticActionKey: string

  action: string
  transition: string
  role: string

  expectedStateRevision: number
  expectedTaskDocHash?: string
  inputHash?: string

  source?: ActionSourceBinding

  handlerContract?: string
  workspaceMode?: "none" | "read-only" | "exclusive"
  workspaceRef?: string
  sideEffectClasses?: string[]

  rank: number
  blocked?: boolean
  blockedReason?: string
}
```

A candidate is not durable work. It can disappear if the instance state changes.

### 4.7 Fenced run binding

A fenced run binding is the execution capability returned by claim.

```ts
interface FencedRunBinding {
  run: WorkflowRunAttempt

  task: {
    uuid: string
    ref: string
    path?: string
  }

  instance: WorkflowInstance

  authority: {
    runnerId: string
    ownerToken: string
    ownerGeneration: number
    leaseExpiresAt: string
  }
}
```

The runner must present the current `ownerToken` and `ownerGeneration` to heartbeat, bind external execution, and settle.

### 4.8 Evidence

Evidence is durable output/proof produced by a run, human, external system, or check.

The existing facts/data split remains the target model:

```ts
interface WorkflowEvidence {
  id: string
  instanceId: string
  kind: string
  ref: string
  summary?: string

  facts?: Record<string, unknown>     // small machine routing facts
  data?: unknown                      // rich human/agent context

  agentRef?: string
  scopeRef?: string
  role?: string
  runId?: string

  contentHash?: string
  taskEtagAtProduction?: string
  taskHashAtProduction?: string
  producedAt: string
}
```

Evidence facts should be small and branchable. Evidence data can be rich and loosely structured. Do not over-schema evidence data.

Important source relationships, such as verify depending on a specific implement run and commit, must be first-class on the run attempt. Evidence may repeat those facts for audit but should not be the only source binding mechanism.

### 4.9 Transition event

A transition event records semantic workflow state change. Transitions must be admitted by current state, role, guards, required evidence, obligations, checks, and CAS.

Target transition event payload should include:

```ts
interface WorkflowTransitionEvent {
  id: string
  instanceId: string
  seq: number

  transition: string
  outcome: string
  from: { status: string; phase?: string; outcome?: string }
  to: { status: string; phase?: string; outcome?: string }

  observedRevision: number
  nextRevision: number

  runId?: string
  evidenceIds?: string[]

  agentRef?: string
  scopeRef?: string
  role?: string

  createdAt: string
}
```

### 4.10 Settlement

Settlement is the terminal write path for a claimed run attempt.

```ts
interface ActionSettlementParams {
  runId: string
  ownerToken?: string
  ownerGeneration?: number

  result:
    | "completed"
    | "semantic_blocked"
    | "operational_failed"
    | "operator_required"
    | "cancelled"

  evidence?: {
    kind?: string
    ref?: string
    summary?: string
    facts?: Record<string, unknown>
    data?: unknown
    contentHash?: string
    idempotencyKey?: string
  }

  transition?: string | false
  terminalSummary?: string
}
```

Settlement atomically:

```text
verify active ownership when ownerToken is required
load run and instance
validate run/source/action/role against transition requirements
write run-linked evidence, if provided
apply transition when result is semantic and transition is provided/derived
terminalize the run
clear active ownership
apply internal projections
emit true external effects
return committed run/evidence/transition result
```

Result semantics:

- `completed`: the handler successfully produced semantic evidence. The workflow may close or advance.
- `semantic_blocked`: the handler successfully produced semantic evidence showing product/workflow blockage, such as verification failure. The workflow may branch to repair or waiting state.
- `operational_failed`: the attempt failed due to infrastructure/runtime/tooling. This should not imply product failure unless a workflow explicitly models that branch.
- `operator_required`: automatic retry or failure is unsafe or ambiguous.
- `cancelled`: execution attempt was intentionally cancelled without semantic completion.

### 4.11 Obligation

An obligation represents a real wait on a human, external actor, external system, or outside condition.

Use an obligation when the workflow cannot proceed until something outside agent execution occurs.

Do not use obligations as internal TODOs for normal next actions. If the system can claim and execute it, it is an action candidate. If the system must wait, it is an obligation.

### 4.12 Effect

An effect is an external outbox item. It exists for side effects that require delivery, retry, idempotency, leasing, acknowledgement, or failure tracking.

Use effects for:

- webhooks;
- external notifications;
- external command launch requests;
- external review creation;
- external issue/PR/status synchronization;
- human input requests through an adapter.

Do not use effects for routine internal projection unless the projection is intentionally externalized.

## 5. Target API surface

The target machine API should provide an action execution namespace. The exact RPC namespace may be `wrkf.action.*` or `wrkf.execution.*`; this specification uses `wrkf.action.*` because the durable object is a semantic workflow action run.

Low-level `wrkf.run.*`, `wrkf.evidence.*`, and `wrkf.transition.*` may remain for manual tooling and compatibility. Unattended runners should use the action execution API.

### 5.1 `wrkf.action.next`

Read-only machine candidate surface.

```ts
interface ActionNextParams {
  scope?: {
    project?: string
    path?: string
    recursive?: boolean
    templates?: string[]
  }

  filters?: {
    actions?: string[]
    roles?: string[]
    statuses?: string[]
    phases?: string[]
    includeBlocked?: boolean
    includeActiveRuns?: boolean
  }

  capabilities?: RunnerCapability[]

  limit?: number
  cursor?: string
}

interface ActionNextResult {
  candidates: ActionCandidate[]
  nextCursor?: string
}
```

This method is for machine execution. The existing human/operator `wrkf.next` may continue to expose command suggestions, guardrails, blocked transitions, pending effects, open obligations, and explanatory text.

### 5.2 `wrkf.action.claim`

Claim creates or resumes the current active run attempt for one candidate.

```ts
interface ClaimActionParams {
  runnerId: string

  scope?: ActionNextParams["scope"]
  prefer?: {
    instanceId?: string
    semanticActionKey?: string
    action?: string
  }

  agentRef: string
  scopeRef?: string

  capabilities?: RunnerCapability[]

  leaseMs: number
  idempotencyKey?: string
}

interface RunnerCapability {
  handlerContract?: string
  handlerId?: string
  handlerVersion?: string
  actions?: string[]
  roles?: string[]
  sideEffectClasses?: string[]
  workspaceModes?: ("none" | "read-only" | "exclusive")[]
}

interface ClaimActionResult {
  binding?: FencedRunBinding
}
```

Claim transaction requirements:

```text
BEGIN IMMEDIATE
  select candidate in stable priority order, or load preferred candidate
  recompute current instance state
  validate candidate is still legal
  derive semanticActionKey
  validate runner capability, handler contract, role, and source binding
  enforce one nonterminal run per instance + semanticActionKey
  insert new run attempt or resume existing active attempt
  attach/refresh ownerToken + ownerGeneration + leaseExpiresAt
COMMIT
```

Claim does not change workflow state.

### 5.3 `wrkf.action.heartbeat`

Extend active ownership while work is running.

```ts
interface HeartbeatActionParams {
  runId: string
  ownerToken: string
  ownerGeneration: number
  leaseMs: number
}
```

Heartbeat must CAS on current owner token and generation. A heartbeat racing with ownership transfer must have exactly one winner.

### 5.4 `wrkf.action.bindExternal`

Attach an external runtime reference to an active run.

```ts
interface BindActionExternalParams {
  runId: string
  ownerToken: string
  ownerGeneration: number
  externalRunRef: string              // e.g. "hrc:hrc_run_7002"
  metadata?: Record<string, unknown>
}
```

This is optional. Local actions may have no external runtime reference.

### 5.5 `wrkf.action.settle`

Terminal settlement path.

```ts
interface SettleActionResult {
  run: WorkflowRunAttempt
  evidence?: WorkflowEvidence
  transition?: WorkflowTransitionEvent
  effects?: Effect[]
  obligations?: Obligation[]
}
```

Settlement must be idempotent by run and terminal payload. Repeated identical terminal settlement returns the original result. Conflicting terminal settlement returns an idempotency/terminal conflict error.

### 5.6 Lease expiry

Lease TTL expiry makes the claim contestable and does nothing else. There is no
expiry-time API or background sweep: the engine does not terminalize the run,
clear ownership, synthesize evidence, or classify possible side effects.
## 6. Default workflow: simple executable task

The built-in simple executable task workflow should be minimal:

```text
active/intake
  triage -> active/ready

active/ready
  implement -> active/implemented

active/implemented
  verify -> closed/done
       or -> active/ready
       or -> waiting/operator_required
```

### 6.1 States

Recommended states:

```json
[
  { "status": "active", "phase": "intake" },
  { "status": "active", "phase": "ready" },
  { "status": "active", "phase": "implemented" },
  { "status": "waiting", "phase": "operator_required" },
  { "status": "closed", "phase": "done", "outcome": "completed" },
  { "status": "closed", "phase": "cancelled", "outcome": "cancelled" }
]
```

### 6.2 Evidence kinds

Recommended evidence kinds:

```json
{
  "triage_result": {
    "facts": {
      "required": ["result"],
      "properties": {
        "result": { "type": "string", "enum": ["ready", "needs_info", "not_executable"] },
        "risk.class": { "type": "string", "enum": ["low", "medium", "high"] },
        "task.has_specification": { "type": "boolean" }
      }
    }
  },
  "implement_result": {
    "facts": {
      "required": ["result"],
      "properties": {
        "result": { "type": "string", "enum": ["done", "blocked"] },
        "commit.sha": { "type": "string" },
        "git.clean": { "type": "boolean" },
        "base.sha": { "type": "string" },
        "postcondition": { "type": "string", "enum": ["git_committed_clean"] },
        "repair.turns": { "type": "integer", "minimum": 0 }
      }
    }
  },
  "verify_result": {
    "facts": {
      "required": ["result", "source.run_id"],
      "properties": {
        "result": { "type": "string", "enum": ["passed", "failed", "blocked"] },
        "source.run_id": { "type": "string" },
        "source.commit.sha": { "type": "string" },
        "verified.commit.sha": { "type": "string" },
        "git.clean": { "type": "boolean" }
      }
    }
  },
  "operational_failure": {
    "facts": {
      "properties": {
        "retryable": { "type": "boolean" },
        "system": { "type": "string" }
      }
    }
  },
  "operator_required": {
    "facts": {
      "properties": {
        "reason": { "type": "string" },
        "postcondition": { "type": "string" },
        "attempts": { "type": "integer" },
        "base.sha": { "type": "string" },
        "head.sha": { "type": "string" },
        "git.clean": { "type": "boolean" }
      }
    }
  }
}
```

Compatibility note: existing fact names such as `impl.disposition` and `verify.disposition` can be accepted temporarily, but new templates should use a common `result` fact.

### 6.3 Executable actions

```json
{
  "executableActions": {
    "triage": {
      "from": { "status": "active", "phase": "intake" },
      "role": "triager",
      "transition": "triage_complete",
      "resultEvidenceKind": "triage_result",
      "handlerContract": "praesidium.wrkq-simple-task.triage@1",
      "workspaceMode": "read-only"
    },
    "implement": {
      "from": { "status": "active", "phase": "ready" },
      "role": "implementer",
      "transition": "implement_complete",
      "resultEvidenceKind": "implement_result",
      "handlerContract": "praesidium.wrkq-simple-task.implement@1",
      "workspaceMode": "exclusive",
      "sideEffectClasses": ["worktree.write", "git.commit"],
      "continuation": {
        "next": "verify",
        "attentionScope": "workspace",
        "requireExactSource": true
      }
    },
    "verify": {
      "from": { "status": "active", "phase": "implemented" },
      "role": "tester",
      "transition": "verify_complete",
      "resultEvidenceKind": "verify_result",
      "handlerContract": "praesidium.wrkq-simple-task.verify@1",
      "workspaceMode": "exclusive",
      "sideEffectClasses": ["install", "smoke", "test"],
      "sourceBinding": {
        "kind": "previous_action",
        "action": "implement",
        "requiredFacts": ["commit.sha"]
      }
    }
  }
}
```

### 6.4 Transitions

Triage:

```text
triage_result.result = ready        -> active/ready
triage_result.result = needs_info   -> waiting/operator_required or active/intake + internal task blocked projection
triage_result.result = not_executable -> closed/cancelled or waiting/operator_required, per template policy
```

Implement:

```text
implement_result.result = done + commit.sha + git.clean=true -> active/implemented
implement_result.result = blocked   -> active/ready or waiting/operator_required, per facts
```

Verify:

```text
verify_result.result = passed       -> closed/done, outcome=completed
verify_result.result = failed       -> active/ready
verify_result.result = blocked      -> waiting/operator_required or active/ready, per facts
```

### 6.5 Required capability: runner-backed implement commit postcondition (`git_committed_clean`)

The MVP uses runner-backed enforcement for the `git_committed_clean` postcondition. `wrkf` does not run git commands during settlement in the MVP. The trusted runner/handler must mechanically verify the postcondition before submitting successful `implement_result` evidence, and `wrkf` validates the submitted facts against the workflow template/action contract.

This capability is part of the `implement` handler contract. It is a handler-level mechanical gate, not a separate `wrkf` action, obligation, effect, phase, or candidate. The same implement run attempt remains active while the handler performs the primary implementation turn and, if needed, one bounded corrective turn.

Required MVP behavior:

- capture `baseSha` before the agent mutates the workspace;
- after the primary implementation turn, verify clean git status, changed `HEAD`, ancestry from `baseSha`, and non-empty diff unless the template explicitly allows no-change implementations;
- on first failure, heartbeat the same run and give the same agent one corrective turn limited to staging/committing the existing intended changes and leaving the repository clean;
- on success, settle `completed` with `implement_result` facts including `result=done`, `commit.sha`, `git.clean=true`, `base.sha`, `postcondition=git_committed_clean`, and `repair.turns`;
- on second failure, settle `operator_required` through `implement_operator_required` with `reason=git_postcondition_failed`.

Use `operator_required`, not `operational_failed`, for the default second-failure path because a dirty or uncommitted worktree may contain valuable partial implementation state. `operational_failed` is acceptable only for disposable isolated workspaces that are automatically thrown away.

Review is not part of the default path. Add review only when risk/policy requires it.


## 7. Failure and recovery semantics

### 7.1 Semantic failure versus operational failure

A semantic failure is a valid action result. Example: verification runs successfully and proves the implementation is wrong. This should produce `verify_result.result=failed` and transition to repair/ready.

An operational failure is an execution infrastructure problem. Example: HRC cannot launch, the runtime crashes before producing evidence, dependency installation infrastructure is unavailable, or the handler registry is invalid. This should terminalize the run as `operational_failed` and not imply product failure unless the workflow explicitly models that branch.

### 7.2 Operator required

Use `operator_required` when automatic retry or failure would be unsafe:

- possible partial worktree mutation under lost ownership;
- external run may have committed but proof is unavailable;
- source commit cannot be found;
- workspace is dirty under unknown owner;
- a successor cannot confirm whether predecessor side effects occurred;
- verify source binding cannot be trusted.

### 7.3 Retry

Retryable operational failure may create a new run attempt with the same `semanticActionKey` after the prior attempt is terminal.

Example:

```text
semanticActionKey: verify:wfi_123:wr_run_impl_002:abc1234
attempt 1: wr_run_verify_003 -> operational_failed(retryable=true)
attempt 2: wr_run_verify_004 -> completed
```

Do not create multiple nonterminal attempts for the same semantic action key.

### 7.4 Reaping

Reaping recovers expired active run attempts. It should not be a semantic actor.

Reaper may:

- clear ownership from terminal runs;
- make a nonterminal run reclaimable if no irreversible side effect is known;
- mark a run `operator_required` when side effects may have occurred;
- reconcile from an external runtime if that runtime provides durable proof;
- fail an attempt operationally when infrastructure loss is proven.

Reaper must not:

- apply product success transitions without evidence;
- complete a verify action because an HRC process exited zero unless the verify evidence contract is satisfied;
- rerun a semantic action while another nonterminal attempt exists;
- create an alternative verify action for the same implementation source.

## 8. Internal projection to `wrkq`

Built-in workflows may project workflow terminal/blocking outcomes to `wrkq` task state.

Projection should be internal and atomic with settlement unless explicitly configured as external integration.

Recommended built-in projection:

```text
closed/done outcome=completed       -> task.state=completed, resolution=done
waiting/operator_required           -> task.state=blocked
active/ready after semantic failure  -> task.state=open or blocked, per template policy
closed/cancelled                    -> task.state=cancelled
```

Avoid generic `set_task_state` effects for normal built-in projection. Use external effects only when projecting to a remote system or when a separate adapter must deliver the change.

## 9. Simplifications and removals

### 9.1 Do not add durable scheduler claims

Do not introduce `workflow_scheduler_claims` as a semantic table with statuses such as completed, failed, blocked, or abandoned.

If an ownership table is needed, it must be subordinate to `workflow_runs` and contain only current active execution ownership:

```text
run_id
runner_id
owner_token
owner_generation
lease_expires_at
heartbeat_at
```

Semantic lifecycle belongs to the run and transition/evidence ledger.

### 9.2 Demote role bindings

`workflow_role_bindings` should not be required for ordinary agent execution. For most agentic actions, the run’s `agentRef`, `scopeRef`, and `role` are sufficient.

Keep role bindings only for durable assignment workflows, such as “this human reviewer is assigned until review closes.” Do not use role bindings as implicit active-run ownership or delivery state.

### 9.3 Deprecate `deliveryRef` as a core concept

`deliveryRef` conflates assignment, external delivery, and runtime binding. Replace it in new paths with:

```text
agentRef      // who acted
scopeRef      // under what runtime/project scope
externalRunRef // which external runtime/session/process
workspaceRef  // which workspace/worktree/artifact scope
```

Keep `deliveryRef` as compatibility metadata only.

### 9.4 Stop default lanes

Do not assign default lanes such as `triage`, `implementation`, or `verify` to ordinary sequential actions. A lane should exist only when there is actual parallel lane semantics, such as cross-repo decomposition.

### 9.5 Replace `nextActionModel` with executable actions

`nextActionModel` should not be the source of runnable work. It may remain as a compatibility/human guidance projection, but machine scheduling should use template-declared `executableActions`.

### 9.6 Keep hooks out of the core lifecycle

Hooks are adapters. Checks are deterministic validation or integration helpers. They should not be the main shape of agentic execution.

If work is long-running, agent-authored, evidence-producing, or semantically meaningful, model it as an executable action, not a hook.

### 9.7 Simplify context hashing

Do not make one broad `contextHash` carry every kind of concurrency meaning.

Target separation:

```text
stateRevision     // CAS for workflow state transitions
taskDocHash       // detects task description/spec drift
inputHash         // action-specific hash of required inputs/source facts
```

Existing `contextHash` may remain for compatibility, but new APIs should state which precondition they enforce.

### 9.8 Effects only for external delivery

Do not route internal task-state projection through generic effect lease/ack/fail unless the projection is intentionally externalized.

### 9.9 No mandatory HRC reservation

Do not require HRC pre-reservation before claiming every action. A runner may claim with `agentRef`, `scopeRef`, and advertised capabilities. If HRC launch fails, settle the run as operational failure or release/retry according to policy.

Capability and assignment systems can be added where there is real multi-agent ambiguity, but they should not become a distributed transaction requirement.

### 9.10 No global worktree lock by default

Default to isolated runner/HRC workspaces. Record `workspaceRef` for audit and exact continuation binding.

Add `wrkf`-coordinated workspace locks only when multiple runners intentionally share mutable worktrees. Do not pay that cost by default.

### 9.11 No universal pre-side-effect authorization ceremony

Always check active ownership at settlement. Heartbeat during long work. Check ownership before binding external runtime. For shared mutable worktrees, use a real workspace lock.

Do not require repeated `authorize()` calls before every internal phase unless a particular runtime truly needs them.

### 9.12 Review is policy, not default ceremony

The default executable workflow should close after successful verify. Add review only when risk, template policy, or human governance requires it.

## 10. Storage target

### 10.1 `workflow_templates`

No table change is strictly required if executable actions live inside template JSON. Template validation must understand `executableActions`.

### 10.2 `workflow_instances`

Keep existing columns. Evolve semantics:

- prefer `status in ('active','waiting','closed')` for new templates;
- keep `revision` or rename in DTOs to `stateRevision`;
- keep `task_doc_etag` and `task_doc_hash`;
- treat `context_hash` as compatibility unless a method explicitly defines its scope.

### 10.3 `workflow_runs`

Additive target columns:

```sql
ALTER TABLE workflow_runs ADD COLUMN semantic_action_key TEXT;
ALTER TABLE workflow_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN agent_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN scope_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_contract TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_id TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_version TEXT;
ALTER TABLE workflow_runs ADD COLUMN workspace_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN source_run_id TEXT REFERENCES workflow_runs(id);
ALTER TABLE workflow_runs ADD COLUMN source_evidence_id TEXT REFERENCES workflow_evidence(id);
ALTER TABLE workflow_runs ADD COLUMN source_commit_sha TEXT;
ALTER TABLE workflow_runs ADD COLUMN terminal_summary TEXT;
```

Statuses should evolve from generic run states to:

```text
active
completed
semantic_blocked
operational_failed
operator_required
cancelled
```

Compatibility mapping:

```text
running/active        -> active
completed            -> completed
failed               -> operational_failed unless evidence/transition proves semantic blockage
cancelled            -> cancelled
```

Unique active run invariant:

```sql
CREATE UNIQUE INDEX workflow_runs_active_semantic_key_unique
ON workflow_runs(instance_id, semantic_action_key)
WHERE semantic_action_key IS NOT NULL
  AND status IN ('active');
```

SQLite partial index expression should use the exact status set implemented by the migration.

### 10.4 `workflow_run_execution`

If ownership is stored separately:

```sql
CREATE TABLE workflow_run_execution (
  run_id TEXT PRIMARY KEY REFERENCES workflow_runs(id) ON DELETE CASCADE,
  runner_id TEXT NOT NULL,
  owner_token TEXT NOT NULL,
  owner_generation INTEGER NOT NULL,
  leased_until TEXT NOT NULL,
  heartbeat_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

This table must not contain semantic terminal status.

### 10.5 `workflow_evidence`

Keep the existing evidence table structure. Prefer adding `scope_ref` if provenance should be explicit:

```sql
ALTER TABLE workflow_evidence ADD COLUMN scope_ref TEXT;
```

Existing `actor` may continue to store `agentRef` or principal. New DTOs should use `agentRef` and `scopeRef`.

### 10.6 `workflow_effects`

Keep effect lease/ack/fail hardening for true external effects. Do not expand effects to model internal action scheduling.

## 11. Template validation rules

Template validation must reject:

- executable action with missing `role`;
- executable action with missing `transition`;
- executable action whose transition does not exist;
- executable action whose result evidence kind does not exist;
- executable action whose transition role does not allow the action role;
- verify/source-bound action without a source binding declaration;
- continuation that targets a missing executable action;
- `workspaceMode="exclusive"` without clear runner/runtime policy for workspace ownership;
- action fact requirements that refer to impossible evidence facts.

Template validation should warn, not reject, for:

- unused roles;
- transitions not exposed as executable actions;
- review actions in low-risk default workflows;
- hooks that look like long-running semantic work;
- effects that look like internal task-state projection.

## 12. `next` surfaces

There are two different read models.

### 12.1 Human/operator `wrkf.next`

May include:

- current instance state;
- blocked transitions;
- open obligations;
- pending effects;
- command suggestions;
- guardrails;
- explanations;
- evidence suggestions;
- check suggestions.

### 12.2 Machine `wrkf.action.next`

Must include only scheduling facts:

- instance id;
- task ref;
- semantic action key;
- action;
- role;
- transition;
- required evidence kind;
- expected revision/state;
- source binding;
- handler contract;
- workspace mode/ref;
- side-effect classes;
- blocked reason, if requested.

Do not require machine clients to parse command strings or prose explanations.

## 13. Agent and scope attribution

Use these meanings consistently:

```text
agentRef:
  principal that performed or is assigned to perform action, e.g. agent:cody.

scopeRef:
  runtime/project scope under which action ran, e.g. agent:cody:project:wrkq.

runnerId:
  process/worker identity that holds active execution ownership.

externalRunRef:
  external runtime/session/process ref, e.g. hrc:hrc_run_7002.
```

`agentRef` and `scopeRef` are audit/provenance and capability context. They are not workflow semantic identity. Do not put `scopeRef` into `semanticActionKey`.

## 14. HRC integration

HRC launch is optional per action.

Local action:

```text
claim -> local handler -> settle
```

HRC-backed action:

```text
claim -> hrc.run.create -> bindExternal(hrc:<id>) -> observe/heartbeat -> settle
```

HRC run terminality is not workflow terminality. HRC output must be converted into `wrkf` evidence and accepted by settlement.

If HRC launch fails before meaningful work starts:

```text
settle result = operational_failed, retryable=true
```

If HRC may have performed side effects but proof is missing:

```text
settle result = operator_required
```

## 15. Cross-repo and lanes

Cross-repo decomposition is not part of the default lifecycle.

When needed, model it explicitly:

```text
planning/decomposition action writes accepted workset evidence
settlement materializes workset/lane projection rows
machine next emits lane-scoped candidates
per-lane implement -> verify uses semanticActionKey with worksetId/laneId
integration verify waits for required lanes to verify
```

Do not represent lanes as default strings on ordinary sequential actions. A lane exists only when the workflow supports parallel or decomposed work.

## 16. Acceptance criteria

A compliant implementation must satisfy these invariants.

### 16.1 Semantic authority

- No runner can transition workflow state without `wrkf` settlement or explicit low-level transition API.
- HRC completion alone does not transition workflow state.
- Evidence alone does not transition workflow state.

### 16.2 Claim and active run safety

- Two concurrent claims for the same candidate produce at most one active run attempt.
- Retried claim for the same active semantic action returns/resumes the current active run attempt or reports ownership conflict.
- Retry after terminal operational failure may create a new attempt with the same semantic action key.
- There is no separate scheduler-claim terminal truth.

### 16.3 Settlement safety

- Settlement rejects stale owner token/generation.
- Settlement is atomic: evidence, transition, run terminalization, ownership cleanup, and internal projection commit together.
- Repeated identical settlement is idempotent.
- Conflicting terminal settlement is rejected.

### 16.4 Source binding

- MVP successful implement settlement requires runner-backed `git_committed_clean` evidence: clean worktree, changed HEAD from base, valid ancestry, and commit SHA. A single corrective turn may occur inside the same run attempt; second failure settles `operator_required`.
- Verify action key includes exact source implement run and exact commit/artifact identity.
- Verify run stores source binding first-class.
- Verify settlement rejects source mismatch.
- No path may verify “latest implement” by fallback.

### 16.5 Recovery

- Expired active run with no known side effects can be reclaimed or failed operationally.
- Expired active run with possible side effects becomes `operator_required` unless adapter proof allows safe reconciliation.
- Reaper does not invent semantic success.

### 16.6 Effects

- External effects are leased and idempotent.
- Internal task-state projection does not require external effect delivery by default.
- ACP or other launchers cannot create duplicate semantic verify runs.

### 16.7 Simplicity

- Ordinary triage -> implement -> verify -> done requires no durable role binding rows, no scheduler claim rows, no HRC reservation, no default lane, and no review phase.
- Handler contracts are named/versioned strings in `wrkf`; prompt/source provenance remains outside `wrkf`.

## 17. Implementation posture

The target system should evolve by tightening the existing action-run path, not by adding a parallel scheduler architecture.

The most important engineering moves are:

```text
make executable actions first-class in templates
add semanticActionKey and source binding to runs
add active run ownership lease/fence metadata
add atomic action settlement
split machine action.next from human wrkf.next
reserve effects for external delivery
simplify default workflow to triage -> implement -> verify -> done
```

The system should be boring where possible. The value is not in having many durable objects. The value is in having the right durable objects with clean boundaries:

```text
WorkflowInstance owns state.
ExecutableActionSpec owns what can be done.
RunAttempt owns who tried to do it.
Evidence owns what was produced.
Settlement owns semantic truth change.
Obligation owns real waits.
Effect owns external delivery.
```
