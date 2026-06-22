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
wrkq.task.catView     [CLI compatibility projection — see note]
wrkq.task.lsView      [CLI compatibility list projection — see note]
wrkq.task.findListView [CLI compatibility list projection — see note]
wrkq.task.treeView    [CLI compatibility tree projection — see note]
wrkq.task.blockedView [CLI compatibility projection — see note]
wrkq.task.inboxView   [CLI compatibility list projection — see note]
wrkq.task.list        [required]
wrkq.task.update      [required]
wrkq.task.acknowledge
wrkq.task.delete
wrkq.task.restore
```

> **`wrkq.task.lsView`** is a CLI compatibility list read model for `wrkq ls`,
> **not** canonical `wrkq.task.list`. The server owns mixed task/container
> listing, container rollup counts (recursive CTE), in-memory merge-sort by the
> requested sort field, and cursor pagination over the merged set — INCLUDING the
> multi-path merge (the per-path query runs the cursor's WHERE/LIMIT in SQL, then
> all paths' entries are merge-sorted and limit+1/next-cursor truncated over the
> COMBINED set, exactly as legacy `runLs`):
> `{ path?, paths?, sort?, reverse?, limit?, cursor?, type?, includeHidden? }` →
> `{ items: WrkqLsEntry[], next_cursor }`. `path` is the single-path form; `paths`
> is the multi-path form (the CLI sends `paths` when more than one path is given,
> `path` otherwise). When both are empty the view lists the top-level (root)
> containers. Rows are legacy-shaped (snake_case). `task_count`/`active_task_count`
> are container rollups. Cataloged + fingerprinted.
> The mirror `ls` command is command-parity-green across its FULL read surface:
> --json/--ndjson/--porcelain, table/human/yaml/tsv, --one/--nul, --type/--all/
> --sort/--reverse/--limit/--cursor, multi-path, and --recursive (a legacy no-op).

> **`wrkq.task.findListView`** is a CLI compatibility list read model for
> `wrkq find`, **not** canonical `wrkq.task.list`. The server owns recursive
> path-prefix matching (`path = ? OR path LIKE ?||'/%'`, or GLOB for `*` paths),
> all metadata filters (state/type/kind/slug-glob/assignee/parent-task/
> requested-by/assigned-project/due-before/due-after/ack-pending), assignee
> normalization + parent-task selector→UUID resolution, cursor.Apply + limit+1 +
> sort-validation + BuildNextCursor over the filtered set, and the legacy
> mixed-type in-memory merge-sort:
> `{ paths?, type?, slugGlob?, state?, dueBefore?, dueAfter?, kind?, assignee?,
> parentTask?, requestedBy?, assignedProject?, ackPending?, sort?, reverse?,
> limit?, cursor? }` → `{ items: WrkqFindEntry[], next_cursor }`. Rows are
> legacy-shaped (snake_case `findResult`). PINNED PARITY QUIRKS the server
> reproduces exactly: (1) when NO `--type` is given (searchBoth) the cursor is
> IGNORED — pagination/limit run over the merged in-memory set with no
> cursor.Apply; only a single `type` (`t`|`p`) applies the cursor SQL-side; (2)
> empty result renders as `[]` (legacy inits `[]findResult{}`), distinct from
> `lsView`'s `null`; (3) findTasks/findContainers errors carry the
> `finding tasks:` / `finding containers:` message prefix. Project-root scoping is
> caller-side (the mirror scopes paths + `--parent-task` before sending). Compat
> read model, not a canonical resource. Cataloged + fingerprinted (registering it
> changes the method catalog and `protocolSchemaHash`).
> **`wrkq.task.treeView`** is a CLI compatibility tree read model for `wrkq tree`,
> **not** a canonical resource. The server owns the entire recursive hierarchy
> walk: child-container + task traversal, empty-container pruning (default; `inbox`
> always shown), the recursive "all done" rollup (`all_tasks_completed`), in-set
> subtask nesting (a task whose parent is also visible nests under it), and the
> hidden-container count. Params:
> `{ path?, maxDepth?, includeArchived?, openOnly? }` →
> `WrkqTreeView{ path, project_id?, children: WrkqTreeNode[], hidden_containers_not_displayed, wire_raw_path? }`.
> The nested `children` reproduce legacy's `tree --json` node shape byte-for-byte.
> Three `wire_*` carriers (`wire_created_at`, `wire_parent_task_uuid` on each node;
> `wire_raw_path` on the view) carry state the JSON projection hides but the CLI
> needs to rebuild the NDJSON stream + nesting; the CLI strips them for `--json`.
> The CLI owns ONLY byte rendering (json / ndjson / porcelain); the interactive
> pretty/human renderer is TTY-only and not mirrored (non-deterministic
> `opened N ago` + ANSI). Cataloged + fingerprinted (TestTreeViewDTOFingerprint).

> **`wrkq.task.catView` is a CLI compatibility read model, not a domain
> resource.** Unlike `wrkq.task.show` (the stable camelCase `WrkqTask` DTO), it
> returns the exact legacy `wrkq cat --json` per-task object: snake_case field
> names, legacy time formats, and nested `comments` / `relations` (both
> directions) / `blocked_by` (incomplete blockers only), assembled server-side
> under a single read transaction. The snapshot covers the **resolved task
> UUID's** projection; selector→UUID resolution happens before the transaction,
> so it is not part of the snapshot. Params: `{ task: string; includeComments?:
> boolean /* default true */ }`. `artifact_dir` is a **server-local host path
> hint** (meaningful on the canonical host, not a remote-filesystem guarantee).
> Do not add its projection fields to `wrkq.task.show`. Registering it changes
> the method catalog and `protocolSchemaHash`.

> **`wrkq.task.blockedView`** is a CLI compatibility read model for
> `wrkq check blocked`, **not** a canonical resource. It resolves the (already
> project-root-scoped) selector and enumerates the incomplete tasks that block it
> via the same store query legacy uses (`store.Tasks.BlockedBy`: `blocks`-relation
> sources whose state is `NOT IN (completed,archived,deleted,cancelled,idea)`).
> Params: `{ task: string }` → `WrkqTaskBlockedView { task_id, task_uuid,
> is_blocked, blockers: WrkqTaskBlockedEntry[] }` (each entry `{ id, uuid, slug,
> title, state }`), reproducing the legacy `BlockedResult` JSON exactly (`blockers`
> is always a non-null array). A selector that does not resolve surfaces the **raw**
> resolve error (the CLI re-wraps it as `failed to resolve task: <err>`). Cataloged
> + fingerprinted.

> **`wrkq.task.inboxView`** is a CLI compatibility list read model for
> `wrkq check-inbox`, **not** a canonical resource. The CLI passes the already
> project-root-scoped inbox container path and (optionally) the configured project
> id — the view never reads project-root env/flags. Params:
> `{ inboxPath: string; projectId?: string }` → `WrkqInboxView { items:
> WrkqInboxEntry[] }`. It reproduces the two legacy queries in order: open tasks
> under the inbox container path, then (when `projectId` is set) `completed`/
> `cancelled` tasks requested by that project with `acknowledged_at IS NULL`. Rows
> are legacy `inboxTask`-shaped (snake_case); `items` is always a non-null array
> (empty → `[]`). Cataloged + fingerprinted.

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
  summary?: boolean;     // omit description/specification bodies; keep has* booleans
}

When `summary` is true, `wrkq.task.list` returns `description: ""` and
`specification: ""` on each item to keep list payloads small. The server still
returns `hasDescription` and `hasSpecification`, computed with
`TRIM(COALESCE(body,''))`, so whitespace-only bodies report `false`. Omitting
`summary` preserves full-body list behavior.

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
  hasDescription: boolean;    // trim-aware presence of description
  hasSpecification: boolean;  // trim-aware presence of specification
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
wrkq.comment.catView  [CLI compatibility projection]
wrkq.comment.listView [CLI compatibility list projection]
wrkq.comment.list     [required]
wrkq.comment.show
wrkq.comment.delete
```

> **`wrkq.comment.listView`** is a CLI compatibility list read model for
> `wrkq comment ls`, **not** the canonical `wrkq.comment.list`. It owns the legacy
> cursor pagination server-side: `{ task?, tasks?, limit?, cursor?, includeDeleted?,
> sort?, desc? }` → `{ items: WrkqCommentCatView[], next_cursor }`, using
> `cursor.Apply` + `limit+1` + `BuildNextCursor` so the cursor token is
> byte-identical to legacy. Legacy accepts MULTIPLE task arguments: `tasks` carries
> the list (`task` is the single-task back-compat form, used when `tasks` is empty).
> With multiple tasks the server reproduces legacy accumulation EXACTLY — it applies
> the same cursor predicate + `limit+1` to each task's query, appends the rows in
> task order, then truncates the combined set at `limit` and builds the next cursor
> from the last surviving row. `sort` accepts `created_at`/`updated_at`/`id` (others
> are a clean WRKQ_VALIDATION error — a deliberate, pinned divergence from legacy's
> raw "no such column" leak); `desc` reverses the order. The RPC CLI scopes each
> raw task arg through the project-root scoper (caller semantics) before the call,
> renders items (JSON array / NDJSON / YAML / TSV / table), and routes `next_cursor`
> to stderr only in porcelain mode, exactly as legacy does. Cataloged + fingerprinted.

> **`wrkq.comment.catView`** is a CLI compatibility read model, **not** a canonical
> resource DTO. It returns the legacy `wrkq comment cat` per-comment object
> (snake_case, alphabetical keys, conditional actor/scope/deletion-provenance
> fields) for one comment ref (`{ comment: string }`). Do not add its display
> fields (`actor_slug`/`actor_role`/scope refs) to `WrkqComment`. Cataloged +
> fingerprinted; changing its field shape is a deliberate contract change.

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
wrkq.attachment.listView  [CLI compatibility list projection]
wrkq.attachment.list
wrkq.attachment.show
wrkq.attachment.remove
```

> **`wrkq.attachment.listView`** is a CLI compatibility list read model for
> `wrkq attach ls`, **not** canonical `wrkq.attachment.list`. DB-only (does not
> touch attachment storage); server owns cursor pagination
> (`{ task, limit?, cursor? }` → `{ items, next_cursor }`, created_at ASC,
> `cursor.Apply` + `limit+1` + `BuildNextCursor`). Rows are legacy-shaped
> (snake_case, alphabetical keys). Cataloged + fingerprinted.

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
wrkq.relation.listView  [CLI compatibility list projection]
wrkq.relation.remove
```

> **`wrkq.relation.listView`** is a CLI compatibility list read model, **not** the
> canonical `WrkqRelation` resource. It returns the legacy `wrkq relation ls` rows
> (`CatViewRelation`: direction/kind/task_id/task_uuid/task_slug/task_title/
> created_at/created_by_id, outgoing then incoming, ordered by kind,id) for one
> task (`{ task: string }`). **No cursor** — legacy `relation ls` exposes no
> limit/cursor and the set is bounded by the task. Do not back-propagate these
> rows into `WrkqRelation`. Cataloged + fingerprinted (via `CatViewRelation`).

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
wrkq.container.catView  [CLI compatibility projection]
wrkq.container.list
```

> **`wrkq.container.catView`** is a CLI compatibility read model, **not** a
> canonical resource DTO. It returns the legacy `wrkq container cat` object
> (snake_case: `description`, friendly `parent_id`, `parent_path`, `sort_index`,
> `webhook_urls`, `created_by`/`updated_by` actor slugs) for one container
> (`{ container?: string; path?: string }`), assembled under a single read
> transaction over the **resolved container UUID** (selector resolution happens
> before the snapshot). Do not back-propagate these fields into `WrkqContainer`.
> Cataloged + fingerprinted. All `wrkq container cat` render modes
> (json/ndjson/porcelain/markdown/raw) are produced **CLI-side** from this one
> projection — no scalar is missing — so no per-mode RPC surface exists.

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

#### Actions (low-ceremony composition)

The `wrkf.action.*` surface composes the run / evidence / transition primitives
into single semantic task-lifecycle calls. It is not a second ledger: an action
run *is* a `workflow_runs` row (carrying a semantic `action` label), evidence is
recorded via `wrkf.evidence.add`, and state moves go through `wrkf.transition.apply`.
When no workflow is attached and none is supplied, the built-in
`wrkq-simple-task@1` workflow is installed and attached automatically. The action
surface never reads or writes legacy `cp_*` / `run_status` task fields.

```
wrkf.action.start
wrkf.action.bindExternal
wrkf.action.complete
wrkf.action.fail
wrkf.action.show
wrkf.action.list
```

```ts
interface WrkfActionStartParams {
  task?: string;
  instanceId?: string;
  workflow?: string;            // defaults to built-in wrkq-simple-task@1
  action: "triage" | "implement" | "review" | "verify" | (string & {});
  role?: string;                // defaults from action (triage→triager, ...)
  actor?: string;
  lane?: string;                // defaults from action
  deliveryRef?: string | object;
  externalRunRef?: string;
  idempotencyKey?: string;
}

interface WrkfActionRun {
  actionRunId: string;          // == runId (workflow_runs.id)
  runId: string;
  task: string;
  instanceId: string;
  workflow: { id: string; version: string; hash?: string };
  action: string;
  role: string;
  actor?: string;
  lane?: string;
  deliveryRef?: string;
  externalRunRef?: string;      // HRC bindings standardized as hrc:<runId>
  status: string;
  startedAt: string;
  completedAt?: string;
  terminalResult?: string;
  evidenceIds?: string[];       // run-linked (show/list)
  evidenceKinds?: string[];
  transitionEventIds?: string[];
}

interface WrkfActionCompleteParams {
  actionRunId: string;
  evidence?: {                  // default kind <action>_result, ref wrkf-action:<id>
    kind?: string; ref?: string; summary?: string;
    facts?: object; data?: object; contentHash?: string; idempotencyKey?: string;
  };
  transition?: string | false;  // omit: default-resolve; false: skip
  transitionIdempotencyKey?: string;
  runSummary?: string;
}

interface WrkfActionListParams {
  task?: string;
  instanceId?: string;
  includeClosedInstances?: boolean;   // span all instances of the task
  status?: string;
  action?: string;
  limit?: number;
  cursor?: string;
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
