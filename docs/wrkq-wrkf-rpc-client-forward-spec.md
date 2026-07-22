# Forward spec: unified wrkq/wrkf RPC protocol and Bun TypeScript client

Status: implementation spec  
Scope: changes inside the `wrkq` repository only  
Audience: wrkq/wrkf implementation team or a coding agent running in the `wrkq` repository  
Primary consumer: external orchestrators such as `agent-loop` that need durable task and workflow primitives through one programmatic substrate

---

## 1. Executive summary

Build one unified public Bun TypeScript client package backed exclusively by a unified stdio JSON-RPC protocol. Both command entrypoints:

```text
wrkq rpc --stdio
wrkf rpc --stdio
```

must start the same RPC server facade, expose the same protocol version, support the same method catalog, and return the same DTO and error shapes. The choice of binary must not change client semantics.

The public TypeScript package should be a single combined package:

```text
@wrkq/client
```

with a single client object shaped around two explicit namespaces:

```ts
const client = await createClient({ command: "wrkq", dbPath });

await client.wrkq.task.create(...);
await client.wrkq.workflow.attach(...);

await client.wrkf.workflow.install(...);
await client.wrkf.evidence.add(...);
await client.wrkf.transition.apply(...);
```

The current top-level wrkf client facade must be replaced by the `client.wrkf.*` namespace. A new `client.wrkq.*` namespace must own task-related operations. There should be no public `client.workflow.*`, `client.task.*`, `client.evidence.*`, `client.transition.*`, `client.run.*`, or `client.effect.*` root namespaces.

The TypeScript API is blocked on `wrkq rpc --stdio`. Do not implement a CLI-parsing task client. Do not integrate with `wrkqd` HTTP. The package should not claim support outside the Bun ecosystem.

This is a breaking replacement of the current `packages/wrkf-client` surface. Do not publish aliases, re-export shims, deprecated entrypoints, or old namespace shapes. Broken imports and type errors are desired so callers using the old client surface are visible immediately.

---

## 2. Required invariants

### 2.1 Ownership boundary

`wrkq` owns task records and all direct task mutation.

`wrkf` owns workflow templates, workflow instances, workflow evidence, obligations, checks, transitions, runs, and effects.

The `wrkf` protocol must not expose direct task mutation methods. A `wrkf.*` method may accept a task selector only to resolve the workflow instance attached to that task. It may not provide verbs that directly create, update, delete, acknowledge, comment on, attach files to, relate, move, or otherwise mutate tasks.

### 2.2 Workflow attachment is a wrkq verb

Attaching a workflow to a task is task ownership and must be exposed under `wrkq`, not `wrkf`:

```text
wrkq.workflow.attach
```

There must be no public RPC method named:

```text
wrkf.task.attach
```

and no public TypeScript method named:

```ts
client.wrkf.task.attach(...)
client.wrkf.workflow.attach(...)
```

`wrkf.workflow.*` is reserved for workflow template registry operations such as validate/show/list/diff/install.

### 2.3 Indirect task projection from wrkf transitions is allowed

`wrkf.transition.apply` may update wrkq task projection fields indirectly as a consequence of a committed workflow state transition. This must remain internal to the transition path and must not become a standalone public task mutation method under `wrkf`.

Examples of allowed indirect behavior:

```text
wrkf.transition.apply commits workflow revision N
  -> creates workflow event/effects/obligations
  -> optionally projects workflow phase/status/run state onto the attached wrkq task
```

Examples of forbidden public wrkf methods:

```text
wrkf.task.create
wrkf.task.update
wrkf.task.setState
wrkf.task.comment.add
wrkf.task.attachment.add
wrkf.task.relation.add
wrkf.task.syncMeta
wrkf.workflow.attach
```

### 2.4 No alternate transports in the TypeScript client

The public TypeScript client must use the unified stdio JSON-RPC protocol. The implementation must not call `wrkq` human/JSON CLI commands as a task API, and must not call `wrkqd` HTTP.

### 2.5 Dual-selector methods must reject mismatch before mutation

Any method that accepts both a `task` selector and an `instanceId` selector (for example `wrkf.instance.show`, `wrkf.instance.next`, `wrkf.evidence.add`, `wrkf.transition.apply`, `wrkf.run.start`) must require at least one selector, and when both are supplied must verify they resolve to the same workflow instance. A mismatch must fail with a validation error before any read-modify-write, event, effect, obligation, or task projection occurs. This check is part of the committing transaction for mutating methods.

### 2.6 Bun-native package

The package is Bun-native. Use `bun:test`, Bun subprocess APIs, `bun-types`, and Bun-first package scripts. Do not add runtime promises for other JavaScript runtimes. Do not add `engines.node`, Node-focused support notes, or Node-runtime conformance tests.

---

## 3. Existing repository context

The repository currently has these user-facing binaries:

```text
wrkq    - task CLI
wrkqadm - admin/migration CLI
wrkqd   - local daemon HTTP API
wrkf    - workflow CLI/RPC API
```

The current machine contract is documented at:

```text
docs/wrkf-rpc.md
```

with protocol version:

```text
2026-06-01
```

The current TypeScript client package is:

```text
packages/wrkf-client/
  package.json               # private package named @wrkf/client
  src/client.ts              # WrkfClient
  src/types.ts               # hand-maintained DTOs
  src/errors.ts              # WrkfRpcError
  src/transport.ts           # transport interface
  src/stdio-transport.ts     # stdio transport
  src/json-rpc-channel.ts    # NDJSON request correlation
  test/fake-transport.test.ts
  test/integration.test.ts
```

The current `WrkfClient` has top-level namespaces such as:

```ts
client.workflow.validate(...)
client.task.attach(...)
client.task.inspect(...)
client.evidence.add(...)
client.transition.apply(...)
client.run.start(...)
client.effect.claim(...)
```

That shape must be replaced by:

```ts
client.wrkf.workflow.validate(...)
client.wrkq.workflow.attach(...)
client.wrkq.workflow.inspect(...)
client.wrkf.evidence.add(...)
client.wrkf.transition.apply(...)
client.wrkf.run.start(...)
client.wrkf.effect.claim(...)
```

Current high-value implementation pieces to reuse internally:

```text
- NDJSON JSON-RPC framing
- request id correlation
- pending request rejection on process exit
- bounded stderr diagnostics
- protocol-corruption detection on stdout
- typed domain errors carrying stable data.code
- fake transport tests
- real integration test against temp migrated wrkq DB
```

Current pieces to replace:

```text
- private @wrkf/client package identity
- source-TS public entrypoint
- top-level client namespaces
- wrkf.task.* RPC namespace
- wrkf task attachment surface
- Record<string, any> result DTOs where named DTOs exist
- evidence add params that cannot bind evidence to a run
```

---

## 4. Package decision

Use one package, not separate wrkq and wrkf packages.

Package name:

```text
@wrkq/client
```

Recommended location:

```text
packages/client/
```

Rationale:

`wrkf` instances are attached to `wrkq` tasks. External orchestrators need task creation, workflow attachment, evidence, transitions, runs, and effects in one flow. A single package prevents version skew between task DTOs, workflow DTOs, transport behavior, idempotency rules, and domain error classes. Separate TypeScript packages would force every consumer to manually keep wrkq and wrkf protocol versions aligned while providing no practical benefit for the primary usage pattern.

Use subpath exports for source organization and import hygiene, but ship one package:

```ts
import { createClient, WorkRpcError } from "@wrkq/client";
import type { WrkqTask, WrkqWorkflowAttachResult } from "@wrkq/client/wrkq";
import type { WrkfTransitionResult, WrkfEvidence } from "@wrkq/client/wrkf";
```

There should be no public `@wrkf/client` package in the final state.

Recommended package shape:

```json
{
  "name": "@wrkq/client",
  "version": "0.1.0",
  "private": false,
  "type": "module",
  "main": "./dist/index.js",
  "types": "./dist/index.d.ts",
  "exports": {
    ".": {
      "types": "./dist/index.d.ts",
      "import": "./dist/index.js"
    },
    "./wrkq": {
      "types": "./dist/wrkq/index.d.ts",
      "import": "./dist/wrkq/index.js"
    },
    "./wrkf": {
      "types": "./dist/wrkf/index.d.ts",
      "import": "./dist/wrkf/index.js"
    },
    "./testing": {
      "types": "./dist/testing/index.d.ts",
      "import": "./dist/testing/index.js"
    }
  },
  "files": ["dist", "README.md", "LICENSE"],
  "sideEffects": false,
  "scripts": {
    "build": "bun x tsc -p tsconfig.build.json",
    "typecheck": "bun x tsc --noEmit",
    "test": "bun test",
    "test:unit": "bun test test/**/*.test.ts",
    "test:integration": "bun test test/**/*.integration.test.ts"
  },
  "devDependencies": {
    "@types/bun": "latest",
    "bun-types": "latest",
    "typescript": "^5.0.0"
  }
}
```

Recommended source layout:

```text
packages/client/
  package.json
  tsconfig.json
  tsconfig.build.json
  README.md
  src/
    index.ts
    client.ts
    errors.ts
    protocol.ts
    transport.ts
    stdio-transport.ts
    json-rpc-channel.ts
    wrkq/
      index.ts
      facade.ts
      types.ts
    wrkf/
      index.ts
      facade.ts
      types.ts
    testing/
      index.ts
      fake-transport.ts
  test/
    fake-transport.test.ts
    rpc-entrypoint-equivalence.integration.test.ts
    lifecycle.integration.test.ts
    namespace-contract.test.ts
```

Delete or replace `packages/wrkf-client` as part of this change. Do not leave a publishable client package with the old namespace shape.

---

## 5. Unified RPC protocol

### 5.1 Authoritative protocol document

Create a single authoritative machine contract:

```text
docs/wrkq-wrkf-rpc.md
```

This document replaces the current wrkf-only framing with a unified protocol. It must define:

```text
- transport
- lifecycle
- method catalog
- DTOs
- error contract
- idempotency semantics
- CAS semantics
- lease semantics
- entrypoint equivalence tests
```

Recommended protocol version for this breaking replacement:

```text
2026-06-30
```

### 5.2 Entrypoint equivalence

Both entrypoints must invoke the same internal server implementation:

```text
wrkq rpc --stdio
wrkf rpc --stdio
```

Implementation recommendation:

```text
internal/workrpc/        # protocol codec, registry, lifecycle, shared server
internal/wrkqapi/        # task-oriented API methods
internal/wrkfapi/        # workflow-oriented API methods
cmd/wrkq                 # adds rpc command that calls internal/workrpc
cmd/wrkf                 # rpc command calls the same internal/workrpc
```

Equivalent means:

```text
- same protocol version
- same protocol schema hash
- same capabilities object
- same method catalog
- same DTO JSON field names
- same error shapes and data.code values
- same stdout/stderr discipline
- same behavior for every method
```

`initialize` may report which binary launched the server, but this must be diagnostic only:

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

The response from `wrkf rpc --stdio` may set `entrypoint: "wrkf"`; no other behavior may differ.

### 5.3 Transport

Transport is JSON-RPC 2.0 framed as NDJSON:

```text
- one compact JSON object per line
- stdout is JSON-RPC frames only
- stderr is logs, hook output, and diagnostics
- stdin is JSON-RPC request and notification frames
- pretty JSON on stdout is forbidden
- inbound frames have a bounded max size; default 8 MiB
- over-limit frames return a structured validation error and leave the connection usable
```

The protocol supports request ids as string or number. Responses must preserve the request id exactly.

### 5.4 Lifecycle methods

Use protocol-control methods outside the business namespaces:

```text
rpc.initialize
rpc.shutdown
rpc.exit
$/cancelRequest
```

`rpc.initialize` must be the first request. Business methods before initialization return validation errors.

Initialize params:

```json
{
  "protocolVersion": "2026-06-30",
  "client": { "name": "@wrkq/client", "version": "0.1.0" }
}
```

Initialize result:

```json
{
  "protocolVersion": "2026-06-30",
  "server": {
    "name": "wrkq-wrkf-rpc",
    "version": "0.1.0",
    "pid": 12345,
    "entrypoint": "wrkq"
  },
  "database": { "path": "/absolute/path/to/wrkq.db", "migrationHash": "sha256:..." },
  "capabilities": {
    "cancel": true,
    "wrkq": true,
    "wrkf": true,
    "effectClaimLease": true,
    "runExternalBinding": true
  },
  "protocolSchemaHash": "sha256:...",
  "methods": [
    "wrkq.task.create",
    "wrkq.workflow.attach",
    "wrkf.workflow.install",
    "wrkf.workflow.discontinue",
    "wrkf.workflow.reinstate",
    "wrkf.transition.apply"
  ]
}
```

`cancel: true` means best-effort cancellation is accepted. Do not document strong cancellation unless every method handler and hook runner is context-aware.

### 5.5 Error contract

Use one JSON-RPC error envelope for both namespaces. `error.data.code` is the stable machine-readable code. `message` is diagnostic text.

Example:

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
{
  "code": "WRKQ_* or WRKF_*",
  "retryable": false
}
```

Recommended shared error families:

| `data.code` | JSON-RPC code | Retryable | Meaning |
|---|---:|---:|---|
| `WRKQ_NOT_FOUND` | -32004 | false | task/comment/attachment/container/relation not found |
| `WRKQ_VALIDATION` | -32602 | false | malformed wrkq params or invalid task mutation |
| `WRKQ_CONFLICT` | -32021 | true | task update conflict, stale etag, or conflicting unique constraint |
| `WRKQ_PERMISSION_DENIED` | -32022 | false | principal/scope cannot perform wrkq operation |
| `WRKQ_DB_MIGRATION_REQUIRED` | -32023 | false | DB schema behind required migration |
| `WRKQ_DB_BUSY` | -32024 | true | SQLite write contention that outlasted `busy_timeout`; `data.reason="sqlite_busy"`. Back off and retry the operation. |
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
| `WRKF_KIND_ROLE_DENIED` | -32018 | false | supplied role cannot produce evidence kind |
| `WRKF_LINKAGE_UNRESOLVED` | -32019 | false | declared evidence linkage did not resolve |
| `WRKF_LINKAGE_STALE` | -32020 | false | latest linkage points at superseded evidence |
| `WRKF_SUSPENDED` | -32026 | false | instance has an active suspension; `data.suspension` carries that record |
| `WORKRPC_INTERNAL` | -32603 | false | unclassified internal error |

Standard JSON-RPC protocol errors such as parse error, invalid request, and method not found may omit `data.code`; the TypeScript client must classify those as protocol errors, not domain errors.

Structured validation data should use this shape when applicable:

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

## 6. RPC method catalog

### 6.1 Protocol controls

```text
rpc.initialize
rpc.shutdown
rpc.exit
$/cancelRequest
```

### 6.2 wrkq namespace

`wrkq.*` owns task records and direct task-related mutations.

#### Task methods

```text
wrkq.task.create
wrkq.task.show
wrkq.task.list
wrkq.task.update
wrkq.task.move
wrkq.task.acknowledge
wrkq.task.delete
wrkq.task.restore
wrkq.task.copy        # server-owned deep copy (T-05111); see WrkqTaskCopy* below
```

> **Tranche B client catch-up (this section).** The authoritative method/DTO
> shapes are `docs/wrkq-wrkf-rpc.md` §6.2 — this forward spec is descriptive and
> must not contradict it. As of Tranche B the published `@wrkq/client` surface
> adds `wrkq.task.copy`, `wrkq.container.update`, and the EXTENDED
> `wrkq.task.restore` params (`toPath`/`title`/`description`/`priority`/`labels`/
> `assignee`/`comment`/`ifMatch`). The older single-field `WrkqTaskRestoreParams`
> below is **superseded** by the extended shape.

Required minimum for agent-loop readiness:

```text
wrkq.task.create
wrkq.task.show
wrkq.task.list
wrkq.task.update
```

Recommended params/results:

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
  labels?: string[];
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}

interface WrkqTaskShowParams {
  task: string;
}

interface WrkqTaskListParams {
  path?: string;
  state?: WrkqTaskState | WrkqTaskState[];
  kind?: string | string[];
  assignee?: string;
  labels?: string[];
  includeDeleted?: boolean;
  limit?: number;
  cursor?: string;

  // Additive ordering + subtree (T-04851 / gap4).
  sort?: "created_at" | "updated_at" | "priority" | "id" | "path"; // default created_at
  direction?: "asc" | "desc"; // default asc
  recursive?: boolean; // default false
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
  expectEtag?: number;
  idempotencyKey?: string;
}
```

Task result DTO:

```ts
interface WrkqTask {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  projectUuid: string;
  path: string;
  state: WrkqTaskState;
  priority: number;
  kind: string;
  description: string;
  specification: string;
  labels: string[];
  meta: Record<string, unknown>;
  etag: number;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  createdByPrincipalRef?: string;
  updatedByPrincipalRef?: string;
}
```

Use camelCase JSON field names for the new RPC DTOs. Do not leak DB column names such as `project_uuid` into the new RPC surface.

**Canonical `path` is server-derived (daedalus standing condition).** `WrkqTask.path` is the authoritative container path of the task, computed by the server from the container tree (`v_container_paths`) and returned on every task DTO (`task.create`, `task.show`, `task.list`, `task.update`, `task.move`). Clients consume `task.path` verbatim and must never reconstruct it from `projectUuid`, slugs, or any client-side container model. The same rule holds for `WrkqContainer.path`.

##### `wrkq.task.list` sort / direction / recursive (gap4)

- `sort` selects the ordering field from the whitelist `created_at` (default), `updated_at`, `priority`, `id`, `path`. Any non-whitelisted value is rejected with `WRKQ_VALIDATION` (`error.data.field = "sort"`).
- `direction` is `asc` (default) or `desc`, matched case-sensitively. An empty value preserves the default; any other value is rejected with `WRKQ_VALIDATION` (`error.data.field = "direction"`).
- `recursive` (default `false`) keeps the historical direct-container-only filter. When `true`, the `path` filter includes tasks in containers nested under `path` (the whole subtree).
- **Cursor identity rule.** `nextCursor` encodes the active `(sort, direction)` tuple. A cursor may only be replayed under the *same* sort field and direction it was produced with. Reusing a cursor with a different `sort` or `direction` (e.g. a `direction=asc` cursor submitted with `direction=desc`) is rejected with `WRKQ_VALIDATION` (`error.data.field = "cursor"`). A malformed/undecodable cursor is likewise `WRKQ_VALIDATION`.

#### `wrkq.task.move` (gap5)

Cross-container / cross-project move of a task by canonical path.

```ts
interface WrkqTaskMoveParams {
  task: string;        // selector for the ROOT task to move
  targetPath: string;  // existing destination container path
  expectEtag?: number; // optional CAS on the root task only
  actor?: string;
}
// → WrkqTask (the moved root, with server-derived canonical `path`)
```

Contract (daedalus HIGH-risk ruling, T-04847 C-04823 §2):

- `targetPath` must resolve to an **existing** container; an unknown destination is `WRKQ_NOT_FOUND` (`kind = "container"`). The move does not create containers.
- **Resident descendant cascade in one transaction.** Moving a root task moves same-residency descendant subtasks in the same DB transaction. Each moved resident task gets the new `project_uuid`, a bumped `etag`, an updated attribution, and its own `task.moved` event. Cross-project children are external backlinks: they are not moved through the parent edge and keep their own `project_uuid`.
- `expectEtag` is a CAS on the **root task only**; a stale value is `WRKQ_CONFLICT` (carrying `expectEtag` + the current etag). Descendants are not individually CAS-checked.
- Moving an **independent same-residency subtask** across containers (one whose parent would remain in the old container) is rejected with `WRKQ_VALIDATION` — same-residency subtrees move coherently. A cross-project child may be moved when it is explicitly selected by the caller.
- A **same-container move is a stable no-op** (idempotent; no spurious etag churn beyond the contract).
- Move origin metadata is honest: events/attribution record origin `rpc` (never `cli`).
- The `task.moved` event payload carries the old/new container uuid + path and, for cascaded descendants, the `cascadeRootTaskUuid` (the tests also accept `cascade_root_task_uuid`).

#### `wrkq.task.restore` (extended — Tranche B)

`wrkq.task.restore` carries the WHOLE legacy `wrkq restore` semantic op
SERVER-side (caller-owned-confirmation B-ruling, T-05100) rather than composing
move/field-update/comment client-side. The extended params (authoritative:
`docs/wrkq-wrkf-rpc.md` §6.2) **supersede** the single-field shape above:

```ts
interface WrkqTaskRestoreParams {
  task: string;
  state?: string;       // target state (default "open"); archived/deleted rejected
  toPath?: string;      // move-on-restore destination (parent path + final slug)
  title?: string;       // field update on restore (empty = unchanged)
  description?: string; // field update on restore (empty = unchanged)
  priority?: number;    // field update on restore (1-4; 0/omitted = unchanged)
  labels?: string;      // JSON array string; field update ("" = unchanged)
  assignee?: string;    // compat actor/principal ref; field update on restore
  comment?: string;     // appended as a comment on restore
  ifMatch?: number;     // conditional etag precondition; mismatch → WRKQ_CONFLICT
}
// → WrkqTask
```

Error precedence mirrors legacy: state/priority/labels/assignee validation →
not-deleted-or-archived check → `ifMatch` mismatch (`WRKQ_CONFLICT`) → `toPath`
resolve + slug-conflict (`WRKQ_CONFLICT`). `labels` is a JSON **array string**
(not a `string[]`), matching the Go `TaskRestoreParams.Labels` json tag.

#### `wrkq.task.copy` (server-owned deep copy — Tranche B)

`wrkq.task.copy` (T-05111, daedalus hrcchat#10196) backs `wrkq cp`. It is the
server-owned ONE source-task copy envelope (source resolution, destination
resolution, source `expectEtag` CAS, create-or-overwrite, task-row write,
attachment-metadata cascade, optional SAME-STORE file copy, `task.copied` event,
post-commit `created` webhook). The CLI owns multi-source fan-out, prompts,
dry-run, and output rendering — it calls this once per source.

```ts
interface WrkqTaskCopyParams {
  source: string;
  destination: string;
  overwrite?: boolean;
  withAttachments?: boolean;
  shallow?: boolean;
  expectEtag?: number;     // source-task etag CAS
  actor?: string;
  idempotencyKey?: string; // mandatory-style: a retried copy must not duplicate
}

// Result keys are DELIBERATELY snake_case — the LEGACY `copyResult` output keys,
// preserved verbatim for byte-parity with legacy `wrkq cp` machine output.
interface WrkqTaskCopyResult {
  source_id: string;
  source_uuid: string;
  dest_id: string;
  dest_uuid: string;
  dest_path: string;
  attachments_copied?: number;
  with_files?: boolean;
}
```

`withAttachments` and `shallow` are mutually exclusive (→ `WRKQ_VALIDATION`).
File-copy safety: physical attachment files are staged into a temp `.copy-*`
under the destination task dir before commit and atomically renamed only after
the DB tx commits, so a failed copy leaves no RPC-visible partial durable state.

#### Comment methods

```text
wrkq.comment.add
wrkq.comment.list
wrkq.comment.show
wrkq.comment.delete
```

Required minimum for agent-loop readiness:

```text
wrkq.comment.add
wrkq.comment.list
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

```text
wrkq.attachment.add
wrkq.attachment.list
wrkq.attachment.show
wrkq.attachment.remove
```

Attachment RPC can accept local paths because this is a local stdio protocol:

```ts
interface WrkqAttachmentAddParams {
  task: string;
  path: string;
  filename?: string;
  mimeType?: string;
  idempotencyKey?: string;
}
```

The protocol should not stream binary attachment contents in v1. It should store/copy from a local path and return metadata.

#### Relation methods

```text
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
```

#### Container/project methods

Read + full mutation surface (container mutation shipped in T-04849 / gap1):

```text
wrkq.container.show
wrkq.container.list
wrkq.container.create
wrkq.container.update         # in-place rename (Tranche B); see below
wrkq.container.delete
wrkq.container.deleteRecursive
wrkq.project.listView         # top-level projects + stored checkout roots
wrkq.project.setRoot          # dedicated registry mutation; not container.update
```

Task creation may take a `path` or `project` selector to resolve the destination container.

The container DTO (also returned by mutations) is:

```ts
interface WrkqContainer {
  uuid: string;
  id: string;
  slug: string;
  title: string;
  kind: string;          // "project" | "directory" | ...
  parentUuid?: string;
  path: string;          // server-derived canonical path
  etag: number;
  createdAt: string;
  updatedAt: string;
  archivedAt?: string;
}
```

##### `wrkq.container.create`

```ts
interface WrkqContainerCreateParams {
  // Either give a full `path`, or `parentPath` + `slug`. `project` selects the
  // project subtree when resolving a relative path.
  path?: string;
  project?: string;
  parentPath?: string;
  slug?: string;
  title?: string;
  kind?: string;         // default "directory"; "project" only directly under root
  actor?: string;
}
// → WrkqContainer
```

A duplicate slug under the same parent is `WRKQ_CONFLICT`; an invalid kind/placement (e.g. a `project` nested under a non-root container) is `WRKQ_VALIDATION`.

##### `wrkq.container.update` (in-place rename — Tranche B)

`wrkq.container.update` (T-05112, daedalus hrcchat#10196) renames a container in
place. The FIRST patch surface is deliberately **NARROW** — only `{ slug?, title? }`;
any other key (including `kind`/`parentUuid`/`webhookUrls`/`archived`) →
`WRKQ_VALIDATION`. It is identity-preserving (UUID/friendly-id/children/history
survive; an in-place `UPDATE`, never delete+recreate) and `v_container_paths`
rebuilds so the container path and all descendant paths reflect the new slug.

```ts
interface WrkqContainerUpdateParams {
  container: string;                          // path / friendly-id / uuid selector
  patch: { slug?: string; title?: string };   // NARROW — any other key → WRKQ_VALIDATION
  expectEtag?: number;                        // optional etag CAS; stale → WRKQ_CONFLICT
  actor?: string;
  idempotencyKey?: string;
}
// → WrkqContainer (the updated container)
```

Error mapping is typed (never a raw store leak): empty/absent patch, an unknown
patch key, or an invalid slug → `WRKQ_VALIDATION`; a slug collision with an
existing sibling or a stale `expectEtag` → `WRKQ_CONFLICT`; an unresolvable
`container` selector → `WRKQ_NOT_FOUND`.

##### `wrkq.project.listView` / `wrkq.project.setRoot` (project-root registry)

The project-root registry is a dedicated project family; it does **not** widen
the narrow `wrkq.container.update` patch. `listView` is the read model used by
`wrkq projects`, and `setRoot` assigns or clears the nullable checkout root on
exactly one top-level `kind=project` container. `wrkq.project.listView` predates
this change: adding its `root` field is the read-surface delta, while
`wrkq.project.setRoot` is the sole new producer method.

```ts
interface WrkqProjectEntry {
  type: "project";
  id: string;
  slug: string;
  title?: string;
  path: string;
  root: string | null;
}

interface WrkqProjectListViewParams {
  includeArchived?: boolean;
  limit?: number;
  cursor?: string;
}

interface WrkqProjectsListView {
  items: WrkqProjectEntry[];
  next_cursor?: string; // legacy projects cursor envelope
}

interface WrkqProjectSetRootParams {
  project: string;      // project slug / P-* friendly ID / UUID
  root: string;         // empty clears; otherwise stored verbatim
  expectEtag?: number;  // stale → WRKQ_CONFLICT with no write/event/attribution change
  actor?: string;       // canonical caller principal (agent:<id> or full agent ScopeRef); empty uses configured default
}
// setRoot → WrkqProjectEntry
```

`wrkq set <project> --root <path>` owns caller-host normalization: an absolute
path beneath `$HOME` becomes `~/...`, an absolute path outside `$HOME` remains
absolute, and empty clears. The RPC server stores the supplied string verbatim.
Readers also return it verbatim; consumers expand `~/...` for the host on which
they run. A task ID or any non-top-level/non-project container is
`WRKQ_VALIDATION`.

`expectEtag` is an optional compare-and-swap guard. A stale value returns
`WRKQ_CONFLICT` and leaves the registered root, container etag, row attribution,
and `container.updated` event history unchanged. The wire key remains `actor`,
but it carries caller-principal attribution: explicit values must be canonical
`agent:<id>` identities or full agent ScopeRefs (stored as their durable
`agent:<id>` principal), while omission uses the server's configured principal
default. Bare legacy identities, `system:*` sentinels, actor UUIDs, and malformed
ScopeRefs return `WRKQ_VALIDATION` before any write or event.

The published facade shape is:

```ts
client.wrkq.project.listView(params?)
client.wrkq.project.setRoot(params)
```

During decoupled rollout, consumers must tolerate `root` and
`wrkq.project.setRoot` being absent until the wrkq producer has landed.

##### `wrkq.search.*` / `wrkq.index.*` (server-owned search + index — Tranche D)

The search + index family (T-05114, daedalus hrcchat#10211) puts the derived
`<db>.search.sqlite` sidecar + the dense embedder BEHIND the RPC boundary. The
server owns sidecar open/migrate, freshness, FTS5/vec/lexical queries, RRF fusion,
status, and lifecycle mutation; `EnsureLlamaReady` MOVES to the server host
(`wrkq.index.update`/`rebuild` kickstart ONLY the server's configured embedder,
never the caller's launchd). The client owns project-root path scoping (paths are
pre-scoped) + presentation ONLY; the published `@wrkq/client` surface adds
`client.wrkq.search.listView` and `client.wrkq.index.{status,update,rebuild,vacuum,
pause,resume}`. The result DTOs keep the LEGACY snake_case output keys
(`search.Response` / `search.Result` / `indexdb.Status` struct shapes); the
lifecycle methods return legacy map-alphabetical acks.

```ts
interface WrkqSearchListViewParams {
  query: string;
  paths?: string[];               // pre-scoped path prefixes
  state?: string; kind?: string; assigneePrincipalRef?: string;
  limit?: number; candidateLimit?: number;
  sort?: string; reverse?: boolean; fresh?: boolean; explain?: boolean;
}
// → WrkqSearchListView{ query, stale, status: WrkqIndexStatus|null,
//                       results: WrkqSearchResult[], total_matches, offset }

interface WrkqIndexLifecycleParams { foreground?: boolean }
// wrkq.index.status   → WrkqIndexStatus
// wrkq.index.update   → { status: WrkqIndexStatus, updated: true }
// wrkq.index.rebuild  → { rebuilt: true, status: WrkqIndexStatus }
// wrkq.index.vacuum   → { vacuumed: true }
// wrkq.index.pause    → { status: "paused" }
// wrkq.index.resume   → { status: "ready" }
```

Search disabled on the server → `WRKQ_VALIDATION` "search is disabled". Invalid
sort / `--fresh` on a stale index → `WRKQ_VALIDATION` (legacy message verbatim).

##### `wrkq.container.delete` (empty-only HARD delete)

```ts
interface WrkqContainerDeleteParams {
  container?: string;    // selector; or use path (+ project)
  path?: string;
  project?: string;
  expectEtag?: number;   // optional CAS
  actor?: string;
}
// → { deleted: true }
```

This is a **hard delete restricted to empty containers**. A non-empty container is rejected with `WRKQ_VALIDATION` ("not empty"); a stale `expectEtag` is `WRKQ_CONFLICT` (carrying `currentEtag`); the root container can never be deleted (`WRKQ_VALIDATION`); unknown selector is `WRKQ_NOT_FOUND`.

##### `wrkq.container.deleteRecursive` (destructive subtree purge)

Two-phase: **preflight** (`dryRun: true`) returns the impact; **commit** requires the caller to echo that impact back as `expected` (an impact CAS).

```ts
interface WrkqContainerDeleteRecursiveParams {
  container?: string;
  path?: string;
  project?: string;
  dryRun?: boolean;
  expectEtag?: number;   // optional CAS on the root container
  expected?: {           // REQUIRED on commit (dryRun=false)
    containers: number;
    tasks: number;
    attachments: number;
    bytes: number;
  };
  actor?: string;
}

// dryRun=true  → { container, containers, tasks, attachments, bytes }
// dryRun=false → { deleted, containersDeleted, tasksDeleted,
//                  attachmentsDeleted, bytesFreed, fileCleanupErrors? }
```

Contract (daedalus, T-04847 C-04823 §3):

- Commit recomputes the true impact inside the purge transaction after the root `expectEtag` check. If it differs from `expected`, the call fails `WRKQ_CONFLICT` carrying the *current* impact under `error.data.current` — the client re-previews and retries.
- `expected` is required on commit; omitting it is `WRKQ_VALIDATION`.
- Removal order: emit `task.purged` (per contained task) + `container.deleted` events, delete tasks then containers leaf-to-root in one transaction; attachment **files** are removed *after* the DB commit, and any file-removal failures are reported (non-fatally) in `fileCleanupErrors`.
- A path-invisible / root container is rejected (`WRKQ_VALIDATION`).

#### Global webhook methods (DEDICATED family — Tranche D)

`wrkq.webhook.add` / `wrkq.webhook.remove` / `wrkq.webhook.listView` (T-05119,
daedalus #10211) manage the **GLOBAL** webhook subscriptions stored on the
**singleton root container** and inherited by every project. This is a DEDICATED
family, deliberately **separate** from `wrkq.container.update` (whose narrow
`{ slug?, title? }` patch surface **rejects** `webhookUrls`). The server owns the
root resolution, URL validation, the idempotent add/remove delta, and the
`webhook_urls` write + attribution + `container.updated` event. `add`/`remove` are
**producer** mutations; `listView` is a CLI compatibility list projection.

```text
wrkq.webhook.add
wrkq.webhook.remove
wrkq.webhook.listView
```

```ts
interface WrkqWebhookMutateParams {
  url: string;
  expectEtag?: number;   // OPTIONAL root-container etag CAS; absent = legacy no-CAS
  actor?: string;
}
// → WrkqWebhookMutateResult, in MAP-ALPHABETICAL key order (overrides the
//   struct-field-order convention):
//     changed:   { changed: true,  count, target, webhook_urls }
//     no-change: { changed: false, webhook_urls }
interface WrkqWebhookMutateResult {
  changed: boolean;
  count?: number;          // present only when changed
  target?: string;         // present only when changed
  webhook_urls: string[];
}

type WrkqWebhookListViewParams = Record<string, never>;   // empty
interface WrkqWebhookRow { url: string }                  // legacy {url:<value>} row
// wrkq.webhook.listView → WrkqWebhookRow[]
```

- `add` validates the URL server-side (http/https with a host); invalid →
  `WRKQ_VALIDATION` (`invalid webhook url: <url>`). It is idempotent (duplicate add
  = no-change).
- `remove` matches by exact value (no validation, legacy parity); removing an
  absent URL = no-change.
- `expectEtag` is **OFF by default** (legacy no-CAS last-writer-wins). The CLI
  `wrkq webhook` mirror never sends it; a raw RPC / client caller MAY opt in to
  reduce the concurrent last-writer-wins risk (stale etag → `WRKQ_CONFLICT`).
#### Task workflow methods

Workflow attachment to a task lives here:

```text
wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
wrkq.workflow.refresh
wrkq.workflow.syncMeta
```

Required minimum for agent-loop readiness:

```text
wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
```

`wrkq.workflow.attach` creates or returns the workflow instance attached to the task. It is a wrkq verb because it mutates the task/workflow binding.

```ts
interface WrkqWorkflowAttachParams {
  task: string;
  workflow: string;
  supersede?: boolean;
  predecessorInstanceId?: string;
  predecessorRevision?: number;
  attachDiscontinued?: boolean;
  actor?: string;
  idempotencyKey?: string;
}

interface WrkqWorkflowAttachResult {
  task: string;
  instance: WrkfInstance;
  attached: boolean;
}
```

`wrkq.workflow.inspect` and `wrkq.workflow.timeline` are task-scoped accessors for the attached workflow instance:

```ts
interface WrkqWorkflowInspectParams {
  task: string;
}

interface WrkqWorkflowTimelineParams {
  task: string;
}
```

`wrkq.workflow.refresh` may update wrkf instance context from the current task document. It must not be exposed as `wrkf.task.refresh`.

`wrkq.workflow.syncMeta { task?, actor? }` explicitly refreshes the task-owned
workflow projection and returns `{ synced }`. It is never exposed as
`wrkf.task.syncMeta`.

#### Legacy actor admin methods

Actor records are a **legacy read-only display cache**, NOT task-write authority — task writes work with a `principalRef` and no actor row. Per daedalus (T-04847 C-04823 §1) the only actor mutation surface is the explicit admin namespace `wrkq.admin.legacyActor.*` (there is intentionally **no** `wrkq.actor.create` / `wrkq.actor.update`):

```text
wrkq.admin.legacyActor.list
wrkq.admin.legacyActor.create
wrkq.admin.legacyActor.update
```

```ts
interface WrkqLegacyActor {
  uuid: string;
  id: string;
  slug: string;
  displayName?: string;
  role: string;
  meta?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
  // NOTE: no principalRef — principal identity is not an actor field.
}

interface WrkqLegacyActorListParams {}            // → { items: WrkqLegacyActor[] }

interface WrkqLegacyActorCreateParams {
  slug: string;
  displayName?: string;
  role?: string;                                  // default "agent"
  meta?: Record<string, unknown>;
  idempotencyKey?: string;
}                                                 // → WrkqLegacyActor

interface WrkqLegacyActorUpdateParams {
  actor: string;                                  // selector
  patch: {                                        // only these keys are mutable
    displayName?: string;
    role?: string;
    meta?: Record<string, unknown> | null;        // null clears meta
  };
  expectUpdatedAt?: string;                       // optimistic concurrency
  idempotencyKey?: string;
}                                                 // → WrkqLegacyActor
```

- `slug`, `id`, `uuid`, `createdAt` are immutable; an unknown patch key is `WRKQ_VALIDATION`.
- A duplicate slug on create is `WRKQ_CONFLICT`; a stale `expectUpdatedAt` on update is `WRKQ_CONFLICT`.
- `meta: null` in a patch clears meta (distinct from omitting the key).
- Mutations emit `actor.created` / `actor.updated` audit events.

#### Handoff methods (Tranche D, T-05117)

```text
wrkq.handoff.create        # producer; caller-owned scope (NOT project-root)
wrkq.handoff.get
wrkq.handoff.listView      # caller-scoped page
wrkq.handoff.acknowledge   # producer; server-owned etag CAS + handoff.acknowledged
```
(`wrkq.handoff.searchView` is DEFERRED until the search/index slice lands.)

The published `@wrkq/client` surface adds `client.wrkq.handoff.{create,get,
listView,acknowledge}`. Scope is **caller-owned but NOT project-root**: the caller
resolves the effective agent/project scope (and, for `create`, enforces self-scope)
and passes EXPLICIT `scopeRef`/`agentId`/`projectId` + actor fields. The server
reads NO agent-runtime env (`ASP_SCOPE_REF`/`ASP_HANDLE`/`ASP_AGENT_ID`/
`ASP_PROJECT`). Authoritative shapes are `docs/wrkq-wrkf-rpc.md` §6.2 ("Handoff
methods").

```ts
interface WrkqHandoffCreateParams {
  scopeRef: string; // caller-resolved canonical project scope
  agentId: string;
  projectId: string;
  title: string;
  body: string;
  meta?: string; // raw JSON-object STRING (legacy --meta)
  idempotencyKey?: string;
  actorAgentId?: string; // defaults to the scope agent
  principalRef?: string;
  dryRun?: boolean; // project the prospective handoff, no write
}
interface WrkqHandoffCreateResult {
  handoff: WrkqHandoff; // snake_case DTO (legacy handoffJSON byte-parity)
  idempotentReplay: boolean;
}
interface WrkqHandoffListViewParams {
  scopeRef: string;
  status?: "pending" | "acknowledged" | "all";
  limit?: number;
  cursor?: string;
}
interface WrkqHandoffListResult {
  items: WrkqHandoff[];
  nextCursor?: string;
}
interface WrkqHandoffAcknowledgeParams {
  handoff: string;
  note?: string;
  actorAgentId: string;
  principalRef?: string;
  scopeRef?: string;
  dryRun?: boolean;
  ifMatch?: number; // server etag CAS; mismatch → WRKQ_CONFLICT
}
```

- A same-key `create` replay with the same payload returns `idempotentReplay:true`;
  with a different title/body it is `WRKQ_CONFLICT`.
- `acknowledge` on an already-acknowledged handoff is `WRKQ_CONFLICT`
  (`reason:"already_acknowledged"`); an `ifMatch` mismatch is `WRKQ_CONFLICT`.
- `WrkqHandoff` fields are DELIBERATELY snake_case (the wrkq CLI re-marshals them
  verbatim) — unlike the camelCase task/comment DTOs.

### 6.3 wrkf namespace

`wrkf.*` owns workflow behavior. It may reference task selectors to resolve attached workflow instances, but direct task mutation methods are forbidden.

#### Workflow template registry

```text
wrkf.workflow.validate
wrkf.workflow.show
wrkf.workflow.list
wrkf.workflow.diff
wrkf.workflow.install
wrkf.workflow.discontinue
wrkf.workflow.reinstate
```

These are template registry operations. They do not attach a workflow to a task.

Template curation is explicit and version-addressed:

```ts
interface WrkfWorkflowLifecycleParams {
  ref: string; // exact id@version; bare ids and "latest" are not accepted
  principal_ref?: string;
}

interface WrkfWorkflowTemplateSummary {
  id: string;
  version: string;
  hash: string;
  discontinuedAt?: string;
  discontinuedBy?: string;
}

interface WrkfWorkflowShowResult {
  template: Record<string, unknown>;
  hash: string;
  discontinuedAt?: string;
  discontinuedBy?: string;
}
```

`wrkf.workflow.discontinue` and `wrkf.workflow.reinstate` accept
`WrkfWorkflowLifecycleParams` and return the extended current-row
`WrkfWorkflowShowResult`. Discontinuation belongs to the registry key
`id@version`, not to the definition hash: idempotent install, same-version
built-in supersede, and catalog amendment preserve the marker. Only reinstate
clears it. No template-registry events are emitted; instance `workflow_events`
must not be used as a registry audit log.

`wrkq.workflow.attach` refuses a discontinued version unless the caller sets
`attachDiscontinued: true`. Lookup, marker guard, and instance insert are one
IMMEDIATE transaction. Existing instances continue operating after their
template version is discontinued. There is no `wrkf.workflow.attach` or
`wrkf.task.attach`; attachment authority remains exclusively under `wrkq`.

#### Instance state access

Use `instance`, not `task`, for wrkf-side workflow instance inspection:

```text
wrkf.instance.show
wrkf.instance.next
```

`wrkf.instance.show` accepts either `instanceId` or `task`:

```ts
interface WrkfInstanceShowParams {
  instanceId?: string;
  task?: string;
}
```

`wrkf.instance.next` accepts either `instanceId` or `task` plus an optional role:

```ts
interface WrkfInstanceNextParams {
  instanceId?: string;
  task?: string;
  role?: string;
}
```

Do not expose `wrkf.task.inspect`, `wrkf.task.timeline`, `wrkf.task.refresh`, or `wrkf.task.syncMeta`.

#### Action discovery and source bindings

`wrkf.action.next` candidates and `wrkf.action.claim` run bindings carry the
same source shape when an action consumes prior evidence:

```ts
interface WrkfActionSourceBinding {
  sourceRunId: string;
  sourceEvidenceId?: string;
  sourceIdentity?: string;
  artifactRef?: string;
}
```

`sourceIdentity` is an opaque, lane-computed token. The client and engine pass
it through and compare it as a string; neither parses or derives meaning from
it. No commit-derived source-binding field is part of the RPC DTO.

#### Evidence

```text
wrkf.evidence.add
wrkf.evidence.list
wrkf.evidence.show
wrkf.evidence.suggest
wrkf.evidence.schema
```

Evidence belongs to workflow state. Evidence add must support run linkage and richer metadata:

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
  runId?: string;
  idempotencyKey?: string;
}
```

Required server-side change: persist `runId` to the workflow evidence record. The output DTO already needs to expose `runId`.

`wrkf.evidence.schema { task, kind }` returns the template-declared evidence
contract.

#### Obligations

```text
wrkf.obligation.list
wrkf.obligation.show
wrkf.obligation.satisfy
wrkf.obligation.waive
wrkf.obligation.cancel
wrkf.obligation.create
```

These mutate workflow obligations, not wrkq tasks directly.

#### Supervisor and watch

```text
wrkf.supervisor.call
wrkf.supervisor.escalate
wrkf.watch.snapshot
wrkf.watch.events
```

Supervisor calls accept `{ task, reason? }`. Watch is bounded request/response:
snapshot evaluates one durable predicate, while events returns one capped page
`{ events, nextCursor }`. The opaque cursor binds resolved target identity
(`instanceId` and, for run watches, `runId`) plus sequence. The client owns the
poll loop, interval, timeout, terminal record, and exit code; the server never
long-polls.

#### Checks and hooks

```text
wrkf.check.preflight
wrkf.check.run
wrkf.check.show
wrkf.check.list
wrkf.hook.list
wrkf.hook.show
wrkf.hook.run
```

Hook output must go to stderr or structured RPC result fields, never raw stdout.

#### Transitions

```text
wrkf.transition.apply
```

Transition params:

```ts
interface WrkfTransitionApplyParams {
  task?: string;
  instanceId?: string;
  transition: string;
  role?: string;
  actor?: string;
  expectRevision?: number;
  contextHash?: string;
  idempotencyKey?: string;
  runChecks?: boolean;
  dryRun?: boolean;
}
```

At least one of `task` or `instanceId` is required. If both are supplied, they must resolve to the same workflow instance.

Transition result:

```ts
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

`expectRevision` and `contextHash` are CAS preconditions. They must be enforced in the DB write path. Stale CAS must fail before any event/effect/obligation insert.

`idempotencyKey` replay semantics:

```text
same key + same canonical request hash -> return original committed TransitionResult
same key + different canonical request hash -> WRKF_IDEMPOTENCY_MISMATCH
```

The original committed result must be persisted; reconstructing it from current tables is insufficient.

#### Runs

```text
wrkf.run.start
wrkf.run.bindExternal
wrkf.run.finish
wrkf.run.fail
wrkf.run.show
wrkf.run.list
```

Run params:

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

Runs represent execution attempts. Do not model them as durable role assignment.

#### Effects

```text
wrkf.effect.list
wrkf.effect.show
wrkf.effect.claim
wrkf.effect.ack
wrkf.effect.fail
wrkf.effect.retry
wrkf.effect.deliver
```

Effect leasing invariants:

```text
- claim atomically selects deliverable effects and sets status='leased'
- claim writes leasedBy, leasedUntil, and leaseToken
- ack/fail require matching, unexpired leaseToken
- wrong or expired leaseToken returns WRKF_LEASE_CONFLICT
- terminal paths clear lease fields
```

### 6.4 Live event feed — monitor-ndjson bridge (no streaming RPC)

The unified RPC protocol is **stdio request/response only**. It will **not** add a streaming JSON-RPC method in v1 (daedalus, T-04847 C-04823 §4). A consumer that needs a live task feed (e.g. Taskboard's SSE) sources it by spawning the wrkq CLI as a child process:

```text
wrkq monitor watch --ndjson [--since <id> | --last <n>] [--event-type <t,...>] [TASK...]
```

Contract:

- **Events are invalidation, not state.** Each NDJSON line (`{"type":"wrkq.monitor.event","id":<int>,"event_type":"task.created|task.moved|task.purged|...","resource_id":"T-...","resource_uuid":"...","payload":"..."}`) tells the bridge *what changed*; the bridge then **refetches the canonical DTO via request/response RPC** (`wrkq.task.show`, `wrkq.task.list`, `wrkq.container.list`). The event payload is a hint and must never be treated as the authoritative record.
- **Feed scope.** `monitor watch` surfaces `task.*` and `comment.*` events only. Container-level mutations are observed through their task-scoped side effects — e.g. a `container.deleteRecursive` surfaces a `task.purged` event for each contained task; the bridge then refetches the container listing. (`container.created` / `container.deleted` are emitted to the event log but are not part of the typed monitor feed.)
- **Resume.** `--since <id>` replays strictly events with `id > cursor` (exclusive — no duplicates, no gaps); `--last <n>` replays the tail of the log then follows. After a disconnect the bridge resumes from the last id it durably handled.
- **Lifecycle.** With no `--until`, the child follows indefinitely. The bridge owns teardown: when its consumer disconnects it cancels/kills the child process (e.g. `exec.CommandContext` cancel), and the child is reaped promptly.
- The TypeScript `@wrkq/client` exposes this, if at all, as a **monitor child-process helper** — never as a JSON-RPC streaming method.

**Bounded-polling read models (T-05115 / T-05116, daedalus #10211).** The `monitor watch|wait` / `watch` CLI surfaces are now themselves RPC-backed, but still as **request/response bounded polling — NOT streaming**. The server exposes three stateless bounded read models the CLI poll-loops over: `wrkq.monitor.eventsView` (one bounded ASCENDING filtered `event_log` page + high-water cursor), `wrkq.monitor.stateView` (a SINGLE `--until` condition snapshot — never sleeps/emits terminal lines), and `wrkq.history.tailView` (one bounded ASCENDING raw `event_log` page, the watchEvent shape). The CLIENT owns the blocking loop, timeout/stall clocks, the high-water cursor across polls, the exactly-one terminal NDJSON record, and exit codes (see the `wrkq.wrkf-rpc.bounded-polling-streaming` arch record). These remain **read-only CLI-compatibility projections**, so — consistent with the other compat read views (`*.catView` / `*.listView` / `history.listView`) — they are **NOT added as `@wrkq/client` JSON-RPC facade methods**; a TS consumer that wants a live feed still uses the monitor child-process helper above (which these read models now back end-to-end).

This contract is proven end-to-end by `internal/workrpc/event_bridge_e2e_test.go` (mutate → observe event → refetch DTO; `--since`/`--last` resume; child reaped on disconnect).

---

## 7. TypeScript client API

### 7.1 Public entrypoint

The root package should expose a unified client:

```ts
import { createClient, WorkRpcError } from "@wrkq/client";

const client = await createClient({
  command: "wrkq",
  dbPath: "/path/to/wrkq.db",
  actor: "agent:agent-loop",
  role: "coordinator",
  clientInfo: { name: "agent-loop", version: "0.1.0" },
});

try {
  const task = await client.wrkq.task.create({
    title: "Implement feature",
    description: "...",
    kind: "task",
    state: "open",
    idempotencyKey: "agent-loop:create:...",
  });

  await client.wrkf.workflow.install({ path: "./workflow.json" });

  const attached = await client.wrkq.workflow.attach({
    task: task.id,
    workflow: "code_change@1",
    idempotencyKey: `agent-loop:attach:${task.id}:code_change@1`,
  });

  const evidence = await client.wrkf.evidence.add({
    task: task.id,
    kind: "implementation",
    ref: "/path/to/artifacts/output.json",
    summary: "Implemented feature",
    facts: { verdict: "ready" },
    data: { artifactsDir: "/path/to/artifacts" },
    role: "implementer",
    actor: "agent:agent-loop",
  });

  const inspect = await client.wrkq.workflow.inspect({ task: task.id });

  const transition = await client.wrkf.transition.apply({
    task: task.id,
    transition: "implementation_ready",
    role: "implementer",
    actor: "agent:agent-loop",
    expectRevision: inspect.instance.revision,
    contextHash: inspect.instance.contextHash,
    idempotencyKey: `agent-loop:transition:${task.id}:implementation_ready:${inspect.instance.revision}`,
  });
} finally {
  await client.close();
}
```

### 7.2 Client object shape

```ts
interface WorkClient {
  readonly rpc: {
    initialize(params?: InitializeParams): Promise<InitializeResult>;
    shutdown(): Promise<void>;
  };

  readonly wrkq: WrkqFacade;
  readonly wrkf: WrkfFacade;

  call<R = unknown>(method: string, params?: unknown): Promise<R>;
  close(): Promise<void>;
  kill(): void;
}
```

There must be no root business namespaces:

```ts
client.workflow        // forbidden
client.task            // forbidden
client.evidence        // forbidden
client.obligation      // forbidden
client.check           // forbidden
client.hook            // forbidden
client.transition      // forbidden
client.run             // forbidden
client.effect          // forbidden
```

### 7.3 Factory options

```ts
interface CreateClientOptions {
  command?: "wrkq" | "wrkf" | string;
  args?: string[];
  dbPath?: string;
  actor?: string;
  role?: string;
  hookCatalogPath?: string;
  cwd?: string;
  env?: Record<string, string | undefined>;
  signal?: AbortSignal;
  stderrTailBytes?: number;
  closeTimeoutMs?: number;
  clientInfo?: { name: string; version: string };
  autoInitialize?: boolean;
}
```

Defaults:

```ts
{
  command: "wrkq",
  args: ["rpc", "--stdio"],
  autoInitialize: true,
}
```

When `command: "wrkf"` is used, the client must behave identically.

### 7.4 Bun stdio transport

Implement the real transport with Bun subprocess APIs. Required behavior:

```text
- spawn without shell
- global flags before `rpc --stdio`
- compact NDJSON request frames
- parse stdout as JSON-RPC frames only
- collect bounded stderr tail for diagnostics
- reject all pending requests on process exit
- detect non-JSON stdout as protocol corruption
- support AbortSignal by killing the child and rejecting pending requests
- close() gracefully sends rpc.shutdown or closes stdin, then waits for process exit
- kill() terminates immediately
```

No human CLI output parsing is allowed anywhere in the package.

### 7.5 Typed errors

Expose one error class:

```ts
class WorkRpcError extends Error {
  readonly rpcCode: number;
  readonly data?: WorkRpcErrorData;
  readonly domainCode?: string;
  readonly retryable?: boolean;
  readonly requestId?: string | number | null;
  readonly method?: string;
}
```

Convenience predicates:

```ts
isWorkRpcError(error): error is WorkRpcError
isWrkqError(error): error is WorkRpcError & { domainCode: `WRKQ_${string}` }
isWrkfError(error): error is WorkRpcError & { domainCode: `WRKF_${string}` }
```

Do not expose separate error classes that force consumers to catch two unrelated types.

### 7.6 Subpath exports

`@wrkq/client/wrkq` should export wrkq types and the `WrkqFacade` type only.

`@wrkq/client/wrkf` should export wrkf types and the `WrkfFacade` type only.

The concrete client factory should live at the root package:

```ts
import { createClient } from "@wrkq/client";
```

---

## 8. Server implementation requirements

### 8.1 Shared server package

Create an internal shared RPC package rather than duplicating wrkq and wrkf RPC code:

```text
internal/workrpc/
  codec.go
  server.go
  registry.go
  lifecycle.go
  errors.go
  stdio.go
```

The registry should dispatch both method families:

```text
wrkq.* -> internal/wrkqapi
wrkf.* -> internal/wrkfapi
```

### 8.2 wrkq API package

Create a programmatic wrkq API package if one does not already exist:

```text
internal/wrkqapi/
  api.go
  types.go
  errors.go
  tasks.go
  comments.go
  attachments.go
  relations.go
  workflow.go
```

This package should call the existing store/domain services directly and return named DTOs. Do not route through Cobra command handlers.

### 8.3 wrkf API changes

Refactor existing wrkf RPC methods to the new namespace split.

Replace:

```text
wrkf.initialize
wrkf.shutdown
wrkf.exit
wrkf.task.attach
wrkf.task.inspect
wrkf.task.timeline
wrkf.task.refresh
wrkf.task.syncMeta
wrkf.next
```

With:

```text
rpc.initialize
rpc.shutdown
rpc.exit
wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
wrkq.workflow.refresh
wrkq.workflow.syncMeta
wrkf.instance.show
wrkf.instance.next
```

Continue to expose:

```text
wrkf.workflow.validate
wrkf.workflow.show
wrkf.workflow.list
wrkf.workflow.diff
wrkf.workflow.install
wrkf.workflow.discontinue
wrkf.workflow.reinstate
wrkf.evidence.*
wrkf.obligation.*
wrkf.check.*
wrkf.hook.*
wrkf.transition.apply
wrkf.run.*
wrkf.effect.*
```

Patch evidence add server-side params:

```go
type EvidenceAddParams struct {
    Task           string                 `json:"task,omitempty"`
    InstanceID     string                 `json:"instanceId,omitempty"`
    Kind           string                 `json:"kind"`
    Ref            string                 `json:"ref,omitempty"`
    Summary        string                 `json:"summary,omitempty"`
    Facts          map[string]any         `json:"facts,omitempty"`
    Data           any                    `json:"data,omitempty"`
    Actor          string                 `json:"actor,omitempty"`
    Role           string                 `json:"role,omitempty"`
    RunID          string                 `json:"runId,omitempty"`
    IdempotencyKey string                 `json:"idempotencyKey,omitempty"`
}
```

Persist `RunID` to the evidence row and return it in the evidence DTO. Implement `IdempotencyKey` per §9.7: persist the committed evidence keyed by `(instanceId, idempotencyKey)` with a canonical request hash, or reject the key with `WRKF_VALIDATION` if enforcement is deferred. Do not accept the key without enforcing it.

### 8.4 DTO naming and JSON casing

New RPC DTOs should use camelCase JSON. Internal DB/domain structs may keep their existing tags; RPC DTOs should map internal fields into a stable public shape.

Do not return `map[string]any` for stable resources. Use named DTOs for:

```text
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

Raw `Record<string, unknown>` is acceptable only for explicit metadata/template/facts/data fields.

---

## 9. CAS, idempotency, and leases

### 9.1 wrkq task update CAS

`wrkq.task.update` should support `expectEtag`. If supplied, the update must be atomic:

```sql
UPDATE tasks
SET ...
WHERE uuid = ? AND etag = ?
```

Rows affected = 0 should return `WRKQ_CONFLICT` with current etag when available.

### 9.2 wrkq idempotency

Mutating wrkq methods should accept `idempotencyKey` where duplicate client submissions are plausible:

```text
wrkq.task.create
wrkq.task.update
wrkq.comment.add
wrkq.attachment.add
wrkq.relation.add
wrkq.workflow.attach
```

Replay semantics:

```text
same namespace + same idempotency key + same canonical request hash -> original result
same namespace + same idempotency key + different canonical request hash -> WRKQ_CONFLICT
```

If implementing persistent idempotency for every wrkq mutator is too large for the first implementation, `wrkq.workflow.attach` and `wrkq.task.create` are mandatory. The TypeScript types should still expose the field consistently.

### 9.3 wrkf transition CAS

`wrkf.transition.apply` must enforce `expectRevision` and `contextHash` in the committing transaction, not through a preflight-only check.

Required order:

```text
BEGIN IMMEDIATE
  resolve instance
  check idempotency key + canonical request hash
  check role/guards/blockers/obligations
  UPDATE workflow_instances ... WHERE id=? AND revision=? AND context_hash=?
  verify rows affected = 1
  insert event/effects/obligations
  store committed TransitionResult for idempotency replay
  perform allowed task projection as an internal transition side effect
COMMIT
```

Stale revision/context must fail before event/effect/obligation inserts and before task projection.

### 9.4 wrkf transition idempotency

Persist the committed `WrkfTransitionResult` keyed by `(instanceId, idempotencyKey)`.

Do not reconstruct the replay result from current state because obligations/effects may have changed after the original transition.

### 9.5 wrkf run idempotency

`wrkf.run.start` should persist `idempotencyKey` and canonical request hash. The same key and same request hash returns the same run. Same key with different request hash returns `WRKF_IDEMPOTENCY_MISMATCH`.

`externalRunRef` should be unique when non-empty.

### 9.6 wrkf effect leases

Effect lease state must include a real `leaseToken`. `ack` and `fail` must require the matching unexpired token.

### 9.7 wrkf evidence idempotency

`wrkf.evidence.add` exposes `idempotencyKey` because agent-loop and other orchestrators retry evidence submission. The replay/mismatch contract is mandatory; an exposed key with undefined semantics is forbidden.

Persist the committed evidence result keyed by `(instanceId, idempotencyKey)`, where `instanceId` is the workflow instance resolved from the supplied `task` or `instanceId` selector. Compute a canonical request hash over the normalized evidence params (`kind`, `ref`, `summary`, `facts`, `data`, `actor`, `role`, `runId`) excluding the key itself.

Replay semantics:

```text
same (instanceId, idempotencyKey) + same canonical request hash -> return the original committed evidence; create no new evidence/event/obligation/effect
same (instanceId, idempotencyKey) + different canonical request hash -> WRKF_IDEMPOTENCY_MISMATCH
```

The original committed evidence DTO must be persisted and returned on replay; reconstructing it from current tables is insufficient because downstream workflow state may have advanced. A replayed `wrkf.evidence.add` must be a no-op with respect to side effects: no duplicate evidence row, no duplicate event, and no re-triggered obligation/effect.

If persistent evidence idempotency is deferred in a first implementation, `wrkf.evidence.add` must reject `idempotencyKey` with `WRKF_VALIDATION` rather than silently ignoring it. Silent acceptance of an unenforced key is not permitted.

---

## 10. Documentation changes

Create or rewrite:

```text
docs/wrkq-wrkf-rpc.md
packages/client/README.md
```

`docs/wrkq-wrkf-rpc.md` should be the machine contract. It should include the method catalog and DTOs in enough detail that the TypeScript client can be maintained from it.

`packages/client/README.md` should include:

```text
- install/import examples
- createClient example
- wrkq task creation example
- wrkq workflow attach example
- wrkf evidence/transition/run/effect example
- error handling example
- entrypoint equivalence note for wrkq rpc and wrkf rpc
```

Do not document `wrkqd` HTTP as a client transport.

---

## 11. Implementation order

### P0 — Protocol contract rewrite

1. Create `docs/wrkq-wrkf-rpc.md`.
2. Define protocol version `2026-06-30`.
3. Define lifecycle as `rpc.*`.
4. Define `wrkq.*` and `wrkf.*` method catalogs.
5. Make the direct-task-mutation invariant explicit.
6. Add red tests for forbidden method registration:

```text
wrkf.task.attach
wrkf.task.inspect
wrkf.task.timeline
wrkf.task.refresh
wrkf.task.syncMeta
wrkf.workflow.attach
```

### P1 — Shared RPC server entrypoints

1. Add `wrkq rpc --stdio`.
2. Move or generalize the existing wrkf RPC server into `internal/workrpc`.
3. Make `wrkf rpc --stdio` call the same server implementation.
4. Add an equivalence test that starts both entrypoints against temp DBs and compares:

```text
protocolVersion
protocolSchemaHash
capabilities
methods
sample method results
sample error results
```

### P2 — wrkq API and RPC methods

1. Add `internal/wrkqapi`.
2. Implement required task methods:

```text
wrkq.task.create
wrkq.task.show
wrkq.task.list
wrkq.task.update
```

3. Implement required comment methods:

```text
wrkq.comment.add
wrkq.comment.list
```

4. Implement required task-workflow methods:

```text
wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
```

5. Return named camelCase DTOs.
6. Use typed `WRKQ_*` domain errors.

### P3 — wrkf namespace refactor

1. Move task-scoped attach/inspect/timeline/refresh out of `wrkf.task.*`.
2. Add `wrkf.instance.show` and `wrkf.instance.next`.
3. Keep template registry under `wrkf.workflow.*`.
4. Keep evidence/obligation/check/hook/transition/run/effect under `wrkf.*`.
5. Remove `syncMeta` from the public RPC surface.
6. Patch evidence add params and persistence for `actor`, `role`, `data`, `runId`, and `idempotencyKey`.
7. Ensure workflow transitions can project task fields only as internal transition side effects.

### P4 — Unified Bun TypeScript package

1. Create `packages/client` named `@wrkq/client`.
2. Implement Bun stdio transport.
3. Implement `createClient` returning `{ rpc, wrkq, wrkf }`.
4. Implement root `call()` for low-level access.
5. Implement typed facades from the new method catalog.
6. Implement `WorkRpcError` and predicates.
7. Export wrkq and wrkf types through subpaths.
8. Delete or replace the old package surface.

### P5 — End-to-end acceptance

Create integration tests that use only the TypeScript package and real binaries:

```text
1. create temp DB
2. migrate DB
3. start client with command: "wrkq"
4. initialize
5. create wrkq task
6. install wrkf workflow template
7. attach workflow via wrkq.workflow.attach
8. inspect via wrkq.workflow.inspect
9. start wrkf run
10. add wrkf evidence with runId
11. apply wrkf transition with revision/context CAS
12. list/claim/ack wrkf effect when template produces one
13. finish wrkf run
14. close client
15. repeat the same flow with command: "wrkf"
16. assert equivalent observable behavior
```

---

## 12. Acceptance criteria

The work is complete when all of the following are true.

### Protocol

```text
- `wrkq rpc --stdio` exists.
- `wrkf rpc --stdio` and `wrkq rpc --stdio` expose the same facade.
- stdout from both RPC entrypoints contains JSON-RPC frames only.
- `rpc.initialize` returns protocol version `2026-06-30`.
- `rpc.initialize` returns the complete method list.
- Business methods before initialize return structured validation errors.
- `$/cancelRequest` is accepted and documented as best-effort.
```

### Namespace split

```text
- No public `wrkf.task.*` methods are registered.
- No public `wrkf.workflow.attach` method is registered.
- Workflow attachment is available as `wrkq.workflow.attach`.
- Task create/show/list/update are available as `wrkq.task.*`.
- Comments are available as `wrkq.comment.*`.
- Workflow template registry remains under `wrkf.workflow.*`.
- Workflow transitions remain under `wrkf.transition.apply`.
- Runs and effects remain under `wrkf.run.*` and `wrkf.effect.*`.
```

### TypeScript package

```text
- `@wrkq/client` is public and builds to dist.
- The package is Bun-native and tested with bun test.
- The client uses only stdio JSON-RPC.
- The client does not call wrkqd HTTP.
- The client does not shell out to human/JSON task CLI commands.
- `createClient()` returns `client.rpc`, `client.wrkq`, and `client.wrkf`.
- Old root business namespaces do not exist on the public type.
- Root, `./wrkq`, `./wrkf`, and `./testing` exports work.
- `WorkRpcError` exposes `domainCode`, `retryable`, RPC numeric code, request id, and method.
```

### DTOs and typing

```text
- Stable resources use named DTOs, not `Record<string, any>`.
- New RPC DTO JSON uses camelCase.
- `wrkq.task.create/show/list/update` return typed task DTOs.
- `wrkq.workflow.attach/inspect/timeline` return typed workflow/task DTOs.
- `wrkf.evidence.add` accepts and persists `runId`.
- `wrkf.transition.apply` returns typed transition result including effects and obligations.
- `wrkf.effect.claim` returns effects plus lease token and expiry.
```

### Correctness

```text
- `wrkq.task.update` supports atomic `expectEtag` CAS.
- `wrkq.workflow.attach` is idempotent by key.
- `wrkf.transition.apply` enforces revision/context CAS in the commit transaction.
- transition idempotency returns the original committed result.
- `wrkf.evidence.add` idempotency returns the original committed evidence with no duplicate evidence/event/obligation/effect; a mismatched request hash returns `WRKF_IDEMPOTENCY_MISMATCH`.
- dual-selector methods reject a `task`/`instanceId` mismatch before any mutation.
- effect ack/fail requires a valid lease token unless the explicit operator
  override `force: true` is supplied.
- same flow works through `wrkq rpc --stdio` and `wrkf rpc --stdio`.
```

---

## 13. Negative tests to add

Add tests that prove these fail:

```text
method not found: wrkf.task.attach
method not found: wrkf.task.inspect
method not found: wrkf.task.timeline
method not found: wrkf.task.refresh
method not found: wrkf.task.syncMeta
method not found: wrkf.workflow.attach
TypeScript compile failure: client.workflow
TypeScript compile failure: client.task
TypeScript compile failure: client.evidence
TypeScript compile failure: client.transition
TypeScript compile failure: client.run
TypeScript compile failure: client.effect
```

Add tests that prove these succeed:

```text
wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
wrkq.task.create
wrkq.task.show
wrkq.task.list
wrkq.task.update
wrkf.workflow.install
wrkf.instance.show
wrkf.instance.next
wrkf.evidence.add
wrkf.transition.apply
wrkf.run.start
wrkf.effect.claim
```

---

## 14. Minimal agent-loop-ready flow

The following flow must be possible using only `@wrkq/client` and the unified RPC protocol:

```ts
import { createClient } from "@wrkq/client";

const client = await createClient({
  command: "wrkq",
  dbPath,
  actor: "agent:agent-loop",
  role: "coordinator",
});

const task = await client.wrkq.task.create({
  title: "Implement durable workflow primitive",
  description: "Wire agent execution to wrkq/wrkf primitives.",
  kind: "task",
  state: "open",
  idempotencyKey: "demo:create-task",
});

await client.wrkf.workflow.install({
  path: "./wrkf/templates/code-change.json",
});

const attached = await client.wrkq.workflow.attach({
  task: task.id,
  workflow: "code_change@1",
  actor: "agent:agent-loop",
  idempotencyKey: `demo:attach:${task.id}:code_change@1`,
});

const run = await client.wrkf.run.start({
  task: task.id,
  role: "implementer",
  actor: "agent:agent-loop",
  externalRunRef: "agent-loop-run-123",
  idempotencyKey: `demo:run:${task.id}:implementer:1`,
});

const evidence = await client.wrkf.evidence.add({
  task: task.id,
  kind: "implementation",
  ref: "/tmp/agent-loop/artifacts/output.json",
  summary: "Implementation complete",
  facts: { verdict: "ready" },
  data: { artifactsDir: "/tmp/agent-loop/artifacts" },
  role: "implementer",
  actor: "agent:agent-loop",
  runId: run.id,
  idempotencyKey: `demo:evidence:${run.id}:implementation`,
});

const inspected = await client.wrkq.workflow.inspect({ task: task.id });

await client.wrkf.transition.apply({
  task: task.id,
  transition: "implementation_ready",
  role: "implementer",
  actor: "agent:agent-loop",
  expectRevision: inspected.instance.revision,
  contextHash: inspected.instance.contextHash,
  idempotencyKey: `demo:transition:${task.id}:implementation_ready:${inspected.instance.revision}`,
});

await client.wrkf.run.finish({
  runId: run.id,
  summary: "Agent-loop run completed",
});

await client.close();
```

No step in this flow may use `wrkqd` HTTP, parse CLI JSON output, or call a root `client.*` business namespace.

---

## 15. Notes for implementation agents

Treat this as a forward replacement of the client and RPC surface. The easiest path is:

```text
1. write docs/wrkq-wrkf-rpc.md first
2. add failing registry tests for required/forbidden method names
3. implement shared internal/workrpc server
4. add wrkq rpc command
5. move task-workflow attachment to wrkq.workflow.attach
6. add internal/wrkqapi for task/comment/workflow methods
7. refactor wrkf API namespaces
8. create packages/client
9. port the existing NDJSON transport tests to the new package
10. add the real-binary integration flow
```

Keep the public surface small and strict. When in doubt, prefer a missing method over a poorly named method that violates the wrkq/wrkf ownership boundary.
