# wrkq/wrkf unified RPC protocol — machine contract

Status: authoritative  
Protocol version: **2026-06-14**  
Replaces: `docs/wrkf-rpc.md` (version 2026-06-01)  
Implementation target: `internal/workrpc` (replacing `internal/wrkfrpc`)

---

## 1. Transport

Transport is **JSON-RPC 2.0** framed as **NDJSON** (newline-delimited JSON):

```
- One compact JSON object per line, terminated by exactly one '\n'
- stdout: JSON-RPC frames only (no log output, no human text)
- stderr: logs, hook output, diagnostics
- stdin: JSON-RPC request and notification frames
- Pretty-printing on stdout is forbidden
- Inbound frame max size: 8 MiB (default); over-limit frames return a
  structured validation error and leave the connection usable
- Request ids may be string or number; responses must preserve id exactly
- Null id is used for responses to notification-shaped requests and for
  error responses where the id cannot be determined
```

Both entrypoints produce identical stdio behavior:

```
wrkq rpc --stdio
wrkf rpc --stdio
```

---

## 2. Ownership boundary

**`wrkq.*` owns task records and all direct task mutation.**

`wrkq.*` methods may:
- Create, show, list, update, acknowledge, delete, or restore tasks
- Add, list, show, or remove comments, attachments, and relations
- Attach a workflow to a task (`wrkq.workflow.attach`)
- Query the workflow instance attached to a task (`wrkq.workflow.inspect`,
  `wrkq.workflow.timeline`, `wrkq.workflow.refresh`)

**`wrkf.*` owns workflow templates, instances, evidence, obligations, checks,
transitions, runs, and effects.**

`wrkf.*` methods may:
- Accept a `task` selector to resolve the attached workflow instance
- Produce side effects on the wrkq task projection as a consequence of a
  committed workflow state transition (internal transition side effect only)

`wrkf.*` methods must **never** expose direct task mutation. The following are
explicitly forbidden in the public RPC registry:

```
wrkf.task.create
wrkf.task.update
wrkf.task.setState
wrkf.task.comment.add
wrkf.task.attachment.add
wrkf.task.relation.add
wrkf.task.syncMeta
wrkf.task.attach
wrkf.task.inspect
wrkf.task.timeline
wrkf.task.refresh
wrkf.workflow.attach
```

---

## 3. Entrypoint equivalence

`wrkq rpc --stdio` and `wrkf rpc --stdio` must expose **identical** protocol
behavior:

```
- same protocolVersion (2026-06-14)
- same protocolSchemaHash
- same capabilities object
- same method catalog
- same DTO JSON field names
- same error shapes and data.code values
- same stdout/stderr discipline
- same behavior for every method
```

The only permitted difference is the diagnostic-only `server.entrypoint` field
in the `rpc.initialize` response:

```json
{
  "server": {
    "name": "wrkq-wrkf-rpc",
    "version": "0.1.0",
    "pid": 12345,
    "entrypoint": "wrkq"
  }
}
```

`wrkf rpc --stdio` may set `entrypoint: "wrkf"`. No other behavior may differ.

### protocolSchemaHash vs database.migrationHash

`rpc.initialize` returns two distinct hashes:

| Field | Meaning |
|---|---|
| `protocolSchemaHash` | SHA-256 of the canonical method catalog + DTO schema. Identical across entrypoints and across DB instances. Changes only when the RPC protocol itself changes. |
| `database.migrationHash` | SHA-256 of the applied DB migration set. Specific to the running DB instance. |

Clients use `protocolSchemaHash` to verify they are talking to a compatible
server version. Clients use `database.migrationHash` for DB-level diagnostics.

---

## 4. Lifecycle methods

Lifecycle control is scoped under `rpc.*` (not `wrkf.*`):

```
rpc.initialize
rpc.shutdown
rpc.exit
$/cancelRequest
```

`rpc.initialize` must be the first call. Business methods called before
initialization return a structured validation error (`WRKQ_VALIDATION`).

`$/cancelRequest` is accepted and treated as best-effort. The server does not
guarantee handler cancellation.

### rpc.initialize

Params:

```json
{
  "protocolVersion": "2026-06-14",
  "client": { "name": "@wrkq/client", "version": "0.1.0" }
}
```

Result:

```json
{
  "protocolVersion": "2026-06-14",
  "protocolSchemaHash": "sha256:<hex64>",
  "server": {
    "name": "wrkq-wrkf-rpc",
    "version": "0.1.0",
    "pid": 12345,
    "entrypoint": "wrkq"
  },
  "database": {
    "path": "/absolute/path/to/wrkq.db",
    "migrationHash": "sha256:<hex64>"
  },
  "capabilities": {
    "cancel": true,
    "wrkq": true,
    "wrkf": true,
    "effectClaimLease": true,
    "runExternalBinding": true
  },
  "methods": [
    "rpc.initialize",
    "wrkq.task.create",
    "wrkq.workflow.attach",
    "..."
  ]
}
```

`capabilities.cancel: true` means best-effort cancellation via `$/cancelRequest`
is accepted. Do not advertise strong cancellation unless every handler is
context-aware.

### rpc.shutdown / rpc.exit

`rpc.shutdown` is a graceful signal; the server drains in-flight requests and
stops accepting new ones. `rpc.exit` is a notification that terminates the
server immediately.

---

## 5. Error contract

All errors use one JSON-RPC error envelope. `error.data.code` is the stable
machine-readable code. `error.message` is diagnostic text only.

```json
{
  "jsonrpc": "2.0",
  "id": "req_12",
  "error": {
    "code": -32009,
    "message": "workflow revision mismatch",
    "data": {
      "code": "WRKF_STALE_REVISION",
      "retryable": true,
      "instanceId": "wfi_...",
      "expectedRevision": 3,
      "actualRevision": 4
    }
  }
}
```

Every domain error must include:

```json
{ "code": "WRKQ_* or WRKF_*", "retryable": false }
```

### Error code table

| `data.code` | JSON-RPC code | Retryable | Meaning |
|---|---:|:---:|---|
| `WRKQ_NOT_FOUND` | -32004 | false | task/comment/attachment/container/relation not found |
| `WRKQ_VALIDATION` | -32602 | false | malformed wrkq params or invalid task mutation |
| `WRKQ_CONFLICT` | -32021 | true | task update conflict, stale etag, or conflicting unique constraint |
| `WRKQ_PERMISSION_DENIED` | -32022 | false | principal/scope cannot perform wrkq operation |
| `WRKQ_DB_MIGRATION_REQUIRED` | -32023 | false | DB schema behind required migration |
| `WRKF_NOT_FOUND` | -32004 | false | workflow template/instance/effect/run/evidence not found |
| `WRKF_VALIDATION` | -32602 | false | malformed wrkf params, invalid template, bad protocol version |
| `WRKF_STALE_REVISION` | -32009 | true | transition `expectRevision` mismatch |
| `WRKF_CONTEXT_MISMATCH` | -32010 | true | transition `contextHash` mismatch |
| `WRKF_TRANSITION_BLOCKED` | -32011 | false | blockers/guards/obligations/checks prevent transition |
| `WRKF_ROLE_DENIED` | -32012 | false | role cannot perform transition |
| `WRKF_IDEMPOTENCY_MISMATCH` | -32013 | false | same idempotency key with different request hash |
| `WRKF_LEASE_CONFLICT` | -32014 | true | effect ack/fail with wrong or expired lease |
| `WRKF_EFFECT_NOT_DELIVERABLE` | -32015 | false | effect cannot be delivered in current state |
| `WRKF_HOOK_FAILED` | -32016 | context-dependent | hook/check execution failed |
| `WRKF_DB_MIGRATION_REQUIRED` | -32017 | false | workflow schema behind required migration |
| `WRKF_KIND_ROLE_DENIED` | -32018 | false | supplied role cannot produce evidence kind |
| `WRKF_LINKAGE_UNRESOLVED` | -32019 | false | declared evidence linkage did not resolve |
| `WRKF_LINKAGE_STALE` | -32020 | false | latest linkage points at superseded evidence |
| `WORKRPC_INTERNAL` | -32603 | false | unclassified internal error |

Standard JSON-RPC protocol errors (parse error, invalid request, method not
found) may omit `data.code`. Clients must classify those as protocol errors,
not domain errors.

Structured validation data shape:

```json
{
  "code": "WRKQ_VALIDATION",
  "retryable": false,
  "field": "title",
  "message": "title is required",
  "expected": "non-empty string",
  "allowed": ["task", "bug", "spike"],
  "fix": { "title": "..." }
}
```

---

## 6. Method catalog

### 6.1 Protocol controls

```
rpc.initialize
rpc.shutdown
rpc.exit
$/cancelRequest
```

### 6.2 wrkq namespace

#### Task methods

```
wrkq.task.create      [required]
wrkq.task.show        [required]
wrkq.task.list        [required]
wrkq.task.update      [required]
wrkq.task.acknowledge
wrkq.task.delete
wrkq.task.restore
```

Params/results (camelCase JSON fields throughout):

```ts
interface WrkqTaskCreateParams {
  path?: string;
  project?: string;
  title: string;
  description?: string;
  specification?: string;
  kind?: "task" | "subtask" | "spike" | "bug" | "chore";
  priority?: number;
  state?: WrkqTaskState;
  parentTask?: string;
  assigneePrincipalRef?: string | null;
  labels?: string[];
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

interface WrkqTaskShowParams { task: string }

interface WrkqTaskListParams {
  path?: string;
  state?: WrkqTaskState | WrkqTaskState[];
  kind?: string | string[];
  assignee?: string;
  labels?: string[];
  includeDeleted?: boolean;
  limit?: number;
  cursor?: string;
}

interface WrkqTaskUpdateParams {
  task: string;
  patch: {
    title?: string;
    description?: string;
    specification?: string;
    state?: WrkqTaskState;
    priority?: number;
    kind?: string;
    labels?: string[];
    meta?: Record<string, unknown>;
    assigneePrincipalRef?: string | null;
    dueAt?: string | null;
    startAt?: string | null;
  };
  expectEtag?: number;   // CAS precondition; see §9.1
  idempotencyKey?: string;
}

interface WrkqTask {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  projectUuid: string;
  path: string;          // canonical current task path
  state: WrkqTaskState;
  priority: number;
  kind: string;
  description: string;
  specification: string;
  labels: string[];
  meta: Record<string, unknown>;
  etag: number;
  startAt?: string;      // RFC3339
  dueAt?: string;        // RFC3339
  createdAt: string;     // RFC3339
  updatedAt: string;     // RFC3339
  completedAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  acknowledgedAt?: string; // RFC3339; set by wrkq.task.acknowledge
  createdByPrincipalRef?: string;
  updatedByPrincipalRef?: string;
}

type WrkqTaskState =
  | "idea" | "draft" | "open" | "in_progress"
  | "completed" | "blocked" | "cancelled" | "archived" | "deleted";

interface WrkqTaskAcknowledgeParams {
  task: string;
  force?: boolean; // allow ack on non-terminal tasks (mirrors `wrkq ack --force`)
}

interface WrkqTaskDeleteParams { task: string }

interface WrkqTaskRestoreParams {
  task: string;
  state?: string; // target state (default "open"); archived/deleted rejected
}
```

`wrkq.task.acknowledge` records a terminal-state receipt (`acknowledgedAt`).
The task must be `completed` or `cancelled` unless `force: true`
(else `WRKQ_VALIDATION`). An already-acknowledged task is a no-op: the current
`WrkqTask` is returned with its stable `acknowledgedAt` and no etag bump.

`wrkq.task.delete` is a **reversible** delete: it sets `state="deleted"` +
`deletedAt` (never `archivedAt`) and cascade-deletes subtasks. Re-deleting an
already-deleted task is a no-op. Hard delete/purge is CLI-only — there is no
purge RPC method.

`wrkq.task.restore` is the inverse: the current state must be `archived` or
`deleted` (else `WRKQ_VALIDATION`); it clears `archivedAt`/`deletedAt`/
`deletedBy`, restores to `state` (default `open`, archived/deleted targets
rejected), and cascade-restores subtasks.

#### Comment methods

```
wrkq.comment.add      [required]
wrkq.comment.list     [required]
wrkq.comment.show
wrkq.comment.delete
```

```ts
interface WrkqCommentAddParams {
  task: string;
  body: string;
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

interface WrkqCommentListParams {
  task: string;
  includeDeleted?: boolean;
  limit?: number;
  cursor?: string;
}

interface WrkqComment {
  uuid: string;
  id: string;
  task: string;
  body: string;
  meta: Record<string, unknown>;
  etag: number;
  createdAt: string;
  updatedAt?: string;
  deletedAt?: string;
  createdByPrincipalRef?: string;
}
```

#### Attachment methods

```
wrkq.attachment.add
wrkq.attachment.list
wrkq.attachment.show
wrkq.attachment.remove
```

```ts
interface WrkqAttachmentAddParams {
  task: string;
  path: string;          // local path; protocol is stdio-local only
  filename?: string;
  mimeType?: string;
  idempotencyKey?: string;
}

interface WrkqAttachmentListParams { task: string; limit?: number; cursor?: string }
interface WrkqAttachmentShowParams { id: string }
interface WrkqAttachmentRemoveParams { id: string }

interface WrkqAttachment {
  uuid: string;
  id: string;
  taskUuid: string;
  filename: string;
  relativePath?: string;
  mimeType?: string;
  sizeBytes: number;
  checksum?: string;     // content checksum (DB/CLI field name; not "sha256")
  createdAt: string;
  createdByPrincipalRef?: string;
}
```

Binary attachment contents are not streamed in v1. The server stores/copies
from a local path and returns metadata.

Attachment storage config (attach dir + max size) is sourced from the SAME wrkq
configuration on both the `wrkq` and `wrkf` rpc entrypoints. When no attach dir
is explicitly configured (`WRKQ_ATTACH_DIR` unset and no `attach_dir` in config),
`wrkq.attachment.add` returns `WRKQ_VALIDATION` rather than writing to the
per-user default. A duplicate filename for a task → `WRKQ_CONFLICT`; size over
the limit → `WRKQ_VALIDATION` (the partial file is cleaned up). `idempotencyKey`
is enforced (replay on match, `WRKQ_CONFLICT` on hash mismatch, no duplicate file
or row).

#### Relation methods

```
wrkq.relation.add
wrkq.relation.list
wrkq.relation.remove
```

```ts
interface WrkqRelationAddParams {
  fromTask: string;
  kind: "blocks" | "relates_to" | "duplicates" | string;
  toTask: string;
  idempotencyKey?: string;
}

interface WrkqRelationListParams { task: string }

interface WrkqRelationRemoveParams {
  fromTask: string;
  kind: "blocks" | "relates_to" | "duplicates" | string;
  toTask: string;
}

interface WrkqRelation {
  fromTask: string;
  toTask: string;
  kind: string;
  direction?: string;   // "outgoing" | "incoming" relative to the queried task
  createdAt?: string;
}
```

Relations are identified by the composite key `(fromTask, kind, toTask)` — there
is no synthetic `id`. `relation.add` validates the kind, rejects self-relations
(`WRKQ_VALIDATION`), maps unknown tasks to `WRKQ_NOT_FOUND`, maps a duplicate to
`WRKQ_CONFLICT`, and enforces `idempotencyKey`. `relation.list` returns both
outgoing and incoming relations, each tagged with `direction`. `relation.remove`
deletes by composite key; a 0-row delete → `WRKQ_NOT_FOUND`.

#### Container/project methods

```
wrkq.container.show
wrkq.container.list
```

```ts
interface WrkqContainerShowParams { path?: string; project?: string }
interface WrkqContainerListParams {
  project?: string;
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

interface WrkqContainer {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  kind: string;
  parentUuid?: string;
  path: string;          // computed full container path
  etag: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}

interface WrkqContainerListResult { items: WrkqContainer[]; nextCursor?: string }
```

`container.show` resolves by `path` or `project`; a miss → `WRKQ_NOT_FOUND`.

#### Task-workflow methods

```
wrkq.workflow.attach    [required]
wrkq.workflow.inspect   [required]
wrkq.workflow.timeline  [required]
wrkq.workflow.refresh
```

Workflow attachment is a `wrkq` verb because it mutates the task/workflow
binding. There must be no `wrkf.task.attach` or `wrkf.workflow.attach` method.

```ts
interface WrkqWorkflowAttachParams {
  task: string;
  workflow: string;      // template ref, e.g. "code_change@1"
  actor?: string;
  idempotencyKey?: string;
}

interface WrkqWorkflowAttachResult {
  task: string;
  instance: WrkfInstance;
  attached: boolean;     // true = newly attached, false = already existed
}

interface WrkqWorkflowInspectParams { task: string }

interface WrkqWorkflowTimelineParams { task: string }
```

There is no `wrkq.workflow.syncMeta` method. Projection from workflow state to
task fields happens through `wrkf.transition.apply` as an internal side effect.

### 6.3 wrkf namespace

#### Workflow template registry

```
wrkf.workflow.validate
wrkf.workflow.show
wrkf.workflow.list
wrkf.workflow.diff
wrkf.workflow.install   [required]
```

These are template registry operations. They do not attach a workflow to a task.

#### Instance state access

Use `instance`, not `task`, for wrkf-side workflow instance inspection:

```
wrkf.instance.show   [required]
wrkf.instance.next   [required]
```

```ts
interface WrkfInstanceShowParams {
  instanceId?: string;
  task?: string;
  // At least one must be supplied.
}

interface WrkfInstanceNextParams {
  instanceId?: string;
  task?: string;
  role?: string;
}
```

Do **not** expose `wrkf.task.inspect`, `wrkf.task.timeline`, `wrkf.task.refresh`,
or `wrkf.task.syncMeta`.

#### Evidence

```
wrkf.evidence.add     [required]
wrkf.evidence.list
wrkf.evidence.show
wrkf.evidence.suggest
```

```ts
interface WrkfEvidenceAddParams {
  task?: string;
  instanceId?: string;
  kind: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  actor?: string;
  role?: string;
  runId?: string;        // persisted; see §9.7
  idempotencyKey?: string;
}
```

`runId` must be persisted to the evidence row and returned in the evidence DTO.

#### Event Query

```
wrkf.event.query   [required]
```

`wrkf.event.query` is a replayable read-model over durable workflow events. It
returns one item per `workflow.transitioned` event in stable
`(created_at, id)` order. It is not a SQL-shaped event dump; callers filter
through typed workflow/task/project/role fields.

```ts
interface WrkfEventQueryParams {
  eventType?: "workflow.transitioned"; // default
  project?: string;                    // project uuid, id, or slug
  fromPhase?: string;
  toPhase?: string;
  riskClass?: string;
  riskClasses?: string[];
  excludeRiskClass?: string;
  excludeRiskClasses?: string[];
  boundRole?: string;                  // current wrkf role binding required
  includeRoleBindings?: boolean;       // include all current bindings
  limit?: number;                      // capped by server
  cursor?: string;                     // opaque nextCursor from prior page
}

interface WrkfEventQueryResult {
  items: WrkfTransitionEvent[];
  nextCursor?: string;
  hasMore: boolean;
}

interface WrkfTransitionEvent {
  id: string;                          // durable workflow event id
  eventType: "workflow.transitioned";
  instanceId: string;
  seq: number;
  task: {
    uuid: string;
    id: string;
    slug?: string;
    ref?: string;
    projectUuid?: string;
    projectId?: string;
    projectSlug?: string;
    riskClass?: string;
  };
  transition?: string;
  outcome?: string;
  from?: WrkfState;
  to?: WrkfState;
  fromPhase?: string;
  toPhase?: string;
  transitionedAt: string;
  actor?: string;
  actorRole?: string;
  matchingRoleBindings: WrkfRoleBinding[];
  roleBindings?: WrkfRoleBinding[];
  payload?: Record<string, unknown>;
}
```

`boundRole` and `matchingRoleBindings` use current `workflow_role_bindings` at
query time. They are current eligibility, not an event-time snapshot. Legacy
`task_role_assignments` do not qualify for this contract. Multiple bindings for
the same role return one transition item with `matchingRoleBindings` sorted
deterministically; consumers must key downstream delivery by `event.id`.

#### Obligations

```
wrkf.obligation.list
wrkf.obligation.show
wrkf.obligation.satisfy
wrkf.obligation.waive
wrkf.obligation.cancel
```

#### Checks and hooks

```
wrkf.check.preflight
wrkf.check.run
wrkf.check.show
wrkf.check.list
wrkf.hook.list
wrkf.hook.show
wrkf.hook.run
```

Hook output must go to stderr or structured RPC result fields. Never raw stdout.

#### Transitions

```
wrkf.transition.apply   [required]
```

```ts
interface WrkfTransitionApplyParams {
  task?: string;
  instanceId?: string;
  // At least one of task/instanceId required.
  // If both supplied, must resolve to same instance.
  transition: string;
  role?: string;
  actor?: string;
  expectRevision?: number;   // CAS; see §9.3
  contextHash?: string;      // CAS; see §9.3
  idempotencyKey?: string;
  runChecks?: boolean;
  dryRun?: boolean;
}

interface WrkfTransitionResult {
  task: string;
  instanceId: string;
  state: WrkfState;
  revision: number;
  contextHash: string;
  eventId: string;
  effects: WrkfEffect[];
  obligations: WrkfObligation[];
}
```

#### Runs

```
wrkf.run.start         [required]
wrkf.run.bindExternal
wrkf.run.finish
wrkf.run.fail
wrkf.run.show
wrkf.run.list
```

```ts
interface WrkfRunStartParams {
  task?: string;
  instanceId?: string;
  role?: string;
  actor?: string;
  idempotencyKey?: string;
  deliveryRef?: string;
  lane?: string;
  externalRunRef?: string;
}
```

#### Effects

```
wrkf.effect.list
wrkf.effect.show
wrkf.effect.claim      [required]
wrkf.effect.ack
wrkf.effect.fail
wrkf.effect.retry
wrkf.effect.deliver
```

---

## 7. DTO reference

All new RPC DTOs use **camelCase** JSON field names. Internal DB/domain structs
may retain their existing tags; RPC DTOs map internal fields to a stable public
shape.

Named DTOs required (no `Record<string, any>` for stable resources):

```
WrkqTask
WrkqTaskListResult
WrkqComment
WrkqCommentListResult
WrkqAttachment
WrkqRelation
WrkqWorkflowAttachResult
WrkqWorkflowInspectResult
WrkfInstance
WrkfEvent
WrkfEvidence
WrkfObligation
WrkfEffect
WrkfRun
WrkfCheckRun
WrkfTransitionResult
WrkfWorkflowTemplateSummary
WrkfWorkflowListResult
WrkfWorkflowShowResult
WrkfInstallResult
WrkfDiffResult
WrkfSuggestResult
WrkfEffectClaimResult
```

Raw `Record<string, unknown>` is acceptable for explicit metadata/template/
facts/data fields only.

---

## 8. Idempotency semantics

### 8.1 wrkq task update CAS

`wrkq.task.update` supports `expectEtag`. If supplied, the update is atomic:

```sql
UPDATE tasks SET ... WHERE uuid = ? AND etag = ?
```

Rows affected = 0 returns `WRKQ_CONFLICT` with current etag when available.

### 8.2 wrkq idempotency

Mutating wrkq methods accept `idempotencyKey`:

```
wrkq.task.create
wrkq.task.update
wrkq.comment.add
wrkq.attachment.add
wrkq.relation.add
wrkq.workflow.attach
```

Replay semantics:

```
same namespace + same key + same canonical request hash → original result
same namespace + same key + different canonical request hash → WRKQ_CONFLICT
```

`wrkq.workflow.attach` and `wrkq.task.create` are mandatory; others may be
deferred if the first implementation is large.

### 8.3 wrkf transition CAS

`wrkf.transition.apply` enforces `expectRevision` and `contextHash` inside the
committing transaction (not as a preflight-only check):

```sql
BEGIN IMMEDIATE
  resolve instance
  check idempotency key + canonical request hash
  check role/guards/blockers/obligations
  UPDATE workflow_instances ... WHERE id=? AND revision=? AND context_hash=?
  verify rows affected = 1
  insert event/effects/obligations
  store committed TransitionResult for idempotency replay
  perform allowed task projection as internal transition side effect
COMMIT
```

Stale revision/context fails before any event/effect/obligation insert.

### 8.4 wrkf transition idempotency

Persist the committed `WrkfTransitionResult` keyed by `(instanceId, idempotencyKey)`.

Do not reconstruct from current tables — obligations/effects may have changed.

Same key + same canonical request hash → return original committed result.  
Same key + different canonical request hash → `WRKF_IDEMPOTENCY_MISMATCH`.

### 8.5 wrkf run idempotency

`wrkf.run.start` persists `idempotencyKey` and canonical request hash.  
`externalRunRef` must be unique when non-empty.

### 8.6 wrkf effect leases

```
- claim atomically selects deliverable effects and sets status='leased'
- claim writes leasedBy, leasedUntil, and leaseToken
- ack/fail require matching, unexpired leaseToken
- wrong or expired leaseToken → WRKF_LEASE_CONFLICT
- terminal paths clear lease fields
```

### 8.7 wrkf evidence idempotency

`wrkf.evidence.add` exposes `idempotencyKey`. An accepted key must be enforced;
silent acceptance of an unenforced key is forbidden.

Persist committed evidence keyed by `(instanceId, idempotencyKey)`. Canonical
request hash covers normalized params (`kind`, `ref`, `summary`, `facts`, `data`,
`actor`, `role`, `runId`) excluding the key itself.

Replay semantics:

```
same (instanceId, key) + same hash → return original committed evidence; no-op
same (instanceId, key) + different hash → WRKF_IDEMPOTENCY_MISMATCH
```

If persistent evidence idempotency is deferred, `wrkf.evidence.add` must reject
`idempotencyKey` with `WRKF_VALIDATION` rather than silently ignoring it.

### 8.8 Dual-selector mismatch

Any method accepting both `task` and `instanceId` must verify they resolve to
the same workflow instance before any read-modify-write, event, effect,
obligation, or task projection. Mismatch fails with `WRKF_VALIDATION` before
mutation begins. For mutating methods this check is part of the committing
transaction.

---

## 9. Negative tests (registry contract)

The test suite at `internal/workrpc/registry_contract_test.go` asserts the
following are **method-not-found** in the unified registry:

```
wrkf.task.attach
wrkf.task.inspect
wrkf.task.timeline
wrkf.task.refresh
wrkf.task.syncMeta
wrkf.workflow.attach
wrkf.initialize
```

And the following are **present**:

```
rpc.initialize
wrkq.task.create
wrkq.workflow.attach
wrkf.workflow.install
wrkf.transition.apply
wrkf.instance.show
wrkf.instance.next
wrkf.event.query
```

These tests are RED until `internal/workrpc` is implemented (P1–P3 of T-04424).

---

## 10. Stdout discipline

The server must guarantee that nothing other than JSON-RPC 2.0 frames appears
on stdout:

- All handler output must be captured before writing
- Handlers and hooks that write to `os.Stdout` must be redirected to stderr
  before invocation
- The stdlib `fmt.Println` / `log.Println` paths must not reach the protocol
  stream

The test at `internal/wrkfrpc/stdout_purity_test.go` covers this invariant.
