## Verdict

Build **`wrkf rpc --stdio` with JSON-RPC 2.0 over NDJSON**, plus a first-class TypeScript client package. Treat the current `wrkf --json` CLI as a useful bridge and smoke-test surface, not the long-term application boundary.

The refactor document correctly frames wrkf as the canonical workflow authority and ACP as an execution facade; it also says an initial ACP `WrkfClient` may shell out to `wrkf --json`, but should hide that detail so a future API can replace it.  [oai_citation:0‡CANONICAL_WORKFLOW_REFACTOR.md](sediment://file_00000000d28071fd91d336096de40c0d) The right wrkf-side change is that future API: a stable, typed, long-lived local RPC process that calls `workflow.Service` directly, not Cobra command handlers.

The key caveat: RPC should not merely expose the current CLI shape. It should force the unresolved operational semantics to become explicit: atomic transition compare-and-set, run idempotency and external run binding, effect claim/lease/ack, typed domain errors, and stable DTOs.

I did not complete a full build/test run. A targeted Go test attempt timed out before yielding useful output, so this is a source-level design review rather than a verified implementation result.

---

## Frame

### Objective

Create a durable wrkf client substrate suitable for ACP, TS tools, dashboards, agents, and future automation.

### Non-objectives

Do not replace the human CLI. Do not make ACP or TS clients read SQLite directly. Do not encode human CLI strings as a permanent API. Do not use MCP as the primary application protocol; MCP can wrap the client later.

### Critical invariants

- wrkf remains the only workflow authority.
- All workflow mutations enter through wrkf service methods.
- Client APIs expose workflow mechanisms, not CLI command syntax.
- stdout is protocol-only in RPC mode.
- errors are machine-readable and stable.
- mutations are idempotent where retries are expected.
- effect delivery is lease-protected when external clients perform side effects.
- stale revision/context checks are enforced atomically, not as best-effort preflight.

---

## Current source findings

The wrkf domain core is already strong. `internal/workflow/service.go`, `ledger.go`, `types.go`, `facts.go`, and the workflow schema contain the right canonical concepts: templates, instances, revisions, events, evidence, obligations, effects, runs, checks, context hashes, idempotency keys, and stale-context detection.

The current client surface is weaker than the domain core. `internal/wrkfcli/root.go` exposes a broad Cobra CLI with `--json`, but JSON output is produced by ad hoc `printAny`/`printJSON` calls, command results are partly maps, and failures are mostly human `fmt.Errorf` strings. `cmd/wrkf/main.go` prints `Error: ...` to stderr. That is acceptable for operators; it is not a stable contract for ACP or TS clients.

The existing method inventory is close to the desired long-term API:

```text
workflow validate/install/show/list/diff
task attach/inspect/timeline/refresh/sync-meta
next
evidence add/list/show/suggest/exec
obligation list/show/satisfy/waive/cancel
effect list/show/deliver/ack/fail/retry
check preflight/run/show/list
transition
run start/bind/finish/fail/show/list
hook list/show/run
supervisor start/call/action
```

The important gaps are not “more commands.” They are protocol stability and write-side correctness under retry/concurrency.

---

## Target architecture

```text
ACP / TS tools / dashboard / agents
              |
              v
        @wrkf/client
              |
              v
 JSON-RPC 2.0 over NDJSON stdio
              |
              v
        wrkf rpc --stdio
              |
              v
      internal/wrkfrpc
   codec, dispatcher, errors,
   initialize/shutdown/cancel
              |
              v
      internal/wrkfapi
  stable method adapter + DTOs
              |
              v
   internal/workflow.Service
              |
              v
          wrkq SQLite
```

The architectural split should be:

- `internal/workflow`: domain engine and persistence.
- `internal/wrkfapi`: stable application API over the domain engine.
- `internal/wrkfrpc`: transport/protocol machinery.
- `internal/wrkfcli`: human CLI, still useful, but not the client contract.
- TypeScript package: process lifecycle, JSON-RPC channel, typed method facade, errors, tests.

This gives wrkf a transport-neutral API core. Stdio is first because ACP and local TS processes can spawn wrkf cheaply and inherit local trust boundaries. A Unix socket or HTTP transport can later reuse the same `wrkfapi` method registry.

---

## Protocol decision

Use **JSON-RPC 2.0 framed as NDJSON**.

This matches the local-process shape: stdin for requests, stdout for responses/notifications, stderr for diagnostics. NDJSON is enough because JSON strings escape newlines and each frame is one compact JSON object followed by `\n`.

Rules:

```text
stdout: JSON-RPC frames only
stderr: logs and human diagnostics only
stdin: JSON-RPC request/notification frames
framing: one compact JSON object per line
pretty JSON: forbidden on stdout
max frame size: bounded
request timeout: client-side and server-side context-aware
```

Example:

```json
{"jsonrpc":"2.0","id":"req_7","method":"wrkf.transition.apply","params":{"task":"T-00001","transition":"plan_ready","role":"coordinator","actor":"human:local","expectRevision":0,"idempotencyKey":"acp:T-00001:plan_ready:0"}}
```

Response:

```json
{"jsonrpc":"2.0","id":"req_7","result":{"task":"T-00001","instanceId":"wfi_...","state":"ready","revision":1,"contextHash":"sha256:...","eventId":"wfe_..."}}
```

Initialize handshake:

```json
{"jsonrpc":"2.0","id":"init_1","method":"wrkf.initialize","params":{"protocolVersion":"2026-06-01","client":{"name":"acp","version":"..."}}}
```

Result:

```json
{"protocolVersion":"2026-06-01","server":{"name":"wrkf","version":"...","pid":12345},"database":{"path":"/.../wrkq.db"},"capabilities":{"cancel":true,"effectClaimLease":true,"runExternalBinding":true},"schemaHash":"..."}
```

Shutdown should follow the normal JSON-RPC shape:

```text
wrkf.shutdown request
wrkf.exit notification or stdin close
```

Cancellation should use `$/cancelRequest`. It can be best-effort initially, but method handlers should accept `context.Context` from day one so cancellation can become real without API churn.

---

## Error contract

Define typed wrkf domain errors and map them to JSON-RPC errors with stable `data.code`.

Example stale revision error:

```json
{
  "jsonrpc": "2.0",
  "id": "req_12",
  "error": {
    "code": -32009,
    "message": "workflow revision mismatch",
    "data": {
      "code": "WRKF_STALE_REVISION",
      "instanceId": "wfi_...",
      "expectedRevision": 3,
      "actualRevision": 4,
      "retryable": true
    }
  }
}
```

Minimum error codes:

```text
WRKF_NOT_FOUND
WRKF_VALIDATION
WRKF_STALE_REVISION
WRKF_CONTEXT_MISMATCH
WRKF_TRANSITION_BLOCKED
WRKF_ROLE_DENIED
WRKF_IDEMPOTENCY_MISMATCH
WRKF_LEASE_CONFLICT
WRKF_EFFECT_NOT_DELIVERABLE
WRKF_HOOK_FAILED
WRKF_DB_MIGRATION_REQUIRED
WRKF_INTERNAL
```

Do not require TS clients to parse stderr or human messages. Human-readable messages are useful, but `data.code` is the contract.

---

## RPC method surface

The RPC methods should be semantic and stable, not one-to-one Cobra wrappers.

A good v1 surface:

```text
wrkf.workflow.validate
wrkf.workflow.install
wrkf.workflow.show
wrkf.workflow.list
wrkf.workflow.diff

wrkf.task.attach
wrkf.task.inspect
wrkf.task.timeline
wrkf.task.refresh
wrkf.task.syncMeta

wrkf.next

wrkf.evidence.add
wrkf.evidence.list
wrkf.evidence.show
wrkf.evidence.suggest

wrkf.obligation.list
wrkf.obligation.show
wrkf.obligation.satisfy
wrkf.obligation.waive
wrkf.obligation.cancel

wrkf.check.preflight
wrkf.check.run
wrkf.check.show
wrkf.check.list

wrkf.transition.apply

wrkf.run.start
wrkf.run.bindExternal
wrkf.run.finish
wrkf.run.fail
wrkf.run.show
wrkf.run.list

wrkf.effect.list
wrkf.effect.show
wrkf.effect.claim
wrkf.effect.ack
wrkf.effect.fail
wrkf.effect.retry
wrkf.effect.deliver

wrkf.hook.list
wrkf.hook.show
wrkf.hook.run
```

I would not promote `supervisor start/call/action` unchanged into the long-term RPC API. Keep supervisor commands in the CLI if they are operationally useful, but remodel durable supervisor work as ordinary wrkf runs, effects, evidence, obligations, and transitions. If a supervisor RPC surface remains, it should be explicitly provisional.

---

## Required service hardening

### 1. Atomic transition compare-and-set

Current transition logic checks expected revision/context before the write path, but the final update should enforce those preconditions in the database write itself. Long-lived RPC clients and multiple ACP processes make concurrency realistic.

Target behavior:

```text
BEGIN IMMEDIATE
  load latest instance
  check idempotency key + request hash
  check revision/context
  check role/guards/blockers/obligations
  insert event
  update workflow_instances
    where id = ?
      and revision = expectedRevision
      and optionally context_hash = expectedContextHash
  verify rows affected = 1
COMMIT
```

If the update affects zero rows, return `WRKF_STALE_REVISION` or `WRKF_CONTEXT_MISMATCH`.

Idempotency should also become stricter. Same key + same request should return the same committed result. Same key + different request should return `WRKF_IDEMPOTENCY_MISMATCH`. That likely means storing a request hash in the event payload or adding a request-hash column.

### 2. Run idempotency and external binding

ACP/HRC launch is a split transaction: wrkf run start and external runtime launch cannot be atomic together. wrkf should provide the primitives needed to reconcile that boundary.

Add to `workflow_runs`:

```sql
idempotency_key text
request_hash text
external_run_ref text -- already present in schema, but make it first-class
```

Add indexes:

```sql
unique(instance_id, idempotency_key) where idempotency_key is not null
unique(external_run_ref) where external_run_ref is not null
```

API shape:

```text
wrkf.run.start({
  task,
  role,
  actor,
  idempotencyKey,
  deliveryRef?,
  lane?,
  externalRunRef?
})

wrkf.run.bindExternal({
  runId,
  externalRunRef,
  deliveryRef?,
  lane?,
  idempotencyKey?
})
```

`finish` and `fail` should be idempotent for terminal states. Repeated completion with identical terminal payload should return the existing terminal run. Conflicting terminal payload should return a conflict.

### 3. Effect claim/lease

The schema already has leased-looking fields on effects, but the service needs a real claim protocol before ACP can safely run multiple delivery loops.

API shape:

```text
wrkf.effect.claim({
  adapter: "acp",
  limit: 10,
  leaseMs: 60000,
  task?: "...",
  kind?: "..."
}) -> claimed effects with leaseToken

wrkf.effect.ack({
  effectId,
  leaseToken,
  receipt?
})

wrkf.effect.fail({
  effectId,
  leaseToken,
  reason,
  retryable?
})
```

The claim operation should atomically select pending/failed/expired effects and mark them leased. `ack` and `fail` should require the matching lease token unless an operator-only `force` flag is used through the CLI.

This is the difference between “idempotent by hope” and “idempotent by protocol.”

### 4. Role binding vs execution run

The schema has `workflow_role_bindings`, but the CLI currently leans on run/bind concepts. For long-term clients, avoid baking “an active run with a delivery ref is a role binding” into the TS API.

Preferred split:

```text
wrkf.role.bind       -> durable role/delivery target binding
wrkf.run.start       -> execution attempt
wrkf.run.bindExternal -> attach external runtime identity to execution attempt
```

This can be deferred if too large, but the RPC API should not make the current conflation permanent.

### 5. Stable DTOs

Do not expose `map[string]any` results from the RPC boundary except for explicitly raw template/debug fields. Add named result structs for install, inspect, transition, run, effect claim, evidence add, and check run.

`NextAction.Command` can remain as human guidance, but clients should not treat command strings as authority. Clients should call `wrkf.transition.apply`, `wrkf.evidence.add`, etc.

---

## TypeScript binding design

Package shape:

```text
packages/wrkf-client/
  src/client.ts
  src/transport.ts
  src/stdio-transport.ts
  src/json-rpc-channel.ts
  src/errors.ts
  src/types.ts
  src/generated-schema.ts   optional
```

User-facing API:

```ts
const client = await WrkfClient.spawn({
  command: "wrkf",
  args: ["rpc", "--stdio"],
  dbPath,
  actor: "acp",
  role: "coordinator",
  hookCatalogPath,
});

const task = await client.task.inspect({ task: "T-00001" });

const result = await client.transition.apply({
  task: "T-00001",
  transition: "plan_ready",
  expectRevision: task.revision,
  contextHash: task.contextHash,
  role: "coordinator",
  actor: "acp",
  idempotencyKey: "acp:T-00001:plan_ready:0",
});
```

Expose domain namespaces:

```text
client.workflow.*
client.task.*
client.evidence.*
client.obligation.*
client.check.*
client.transition.*
client.run.*
client.effect.*
client.hook.*
```

Transport requirements:

- use `child_process.spawn`, not shell execution;
- pass DB/hook config as argv or initialize params, not environment-only magic;
- maintain pending request map;
- support `AbortSignal`;
- bound stderr tail for error diagnostics;
- reject all pending requests on process exit;
- detect protocol corruption if stdout contains non-JSON;
- expose `close()` and `kill()` distinctly;
- never parse human CLI output.

The TS binding should treat JSON-RPC as an internal transport detail. Application code should see typed methods and typed `WrkfRpcError`.

---

## Implementation plan

### Phase 0 — Contract inventory

Freeze the method catalog, DTO names, error codes, and protocol version. Decide whether TS types are hand-maintained initially or generated from Go schemas later. I would hand-maintain v0/v1 types and add fixture-based compatibility tests before investing in generation.

Deliverable: `docs/wrkf-rpc.md` with protocol, lifecycle, errors, and method inventory.

### Phase 1 — Add `internal/wrkfapi`

Create a typed API adapter over `workflow.Service`.

```text
internal/wrkfapi/
  api.go
  types.go
  errors.go
  workflow.go
  task.go
  transition.go
  evidence.go
  obligation.go
  effect.go
  run.go
  check.go
```

This layer should accept `context.Context`, request DTOs, and return result DTOs or typed errors. It should not import Cobra and should not write stdout/stderr.

Human CLI commands can gradually call this adapter too, but that is not required on day one.

### Phase 2 — Fix write-side semantics

Implement the three required correctness primitives before relying on RPC for ACP launch/reconcile paths:

```text
atomic transition CAS
run idempotency + external binding
effect claim/lease/ack/fail token
```

These are more important than transport polish. Without them, JSON-RPC merely makes fragile operations easier to call at higher concurrency.

### Phase 3 — Add `internal/wrkfrpc`

Implement:

```text
JSON-RPC message structs
NDJSON reader/writer
method registry
initialize/shutdown/exit
cancel notification
error mapper
frame size limits
stdout purity tests
```

Then add:

```text
wrkf rpc --stdio
```

RPC mode should load config/db once, keep the service process warm, and close cleanly on shutdown or stdin EOF.

### Phase 4 — Add TS package

Implement the TS client against the RPC server. Include fake-transport unit tests and real-process integration tests against a temp wrkq DB.

Minimum v1 client coverage should match ACP’s retained workflow needs:

```text
task.inspect
task.timeline
next
evidence.add/list
transition.apply
obligation.list/waive/cancel
effect.list/show/claim/ack/fail/retry
run.start/bindExternal/finish/fail/list/show
workflow.validate/install/show/list
```

### Phase 5 — Wire ACP to RPC client

ACP’s `WrkfClient` should depend on the TS wrkf client, not on raw JSON-RPC frames. Keep the existing shell-out client as a temporary fallback behind a config flag only.

Completion criterion: ACP route handlers cannot tell whether wrkf is reached through stdio, future socket RPC, or tests. They only see the typed `WrkfClient`.

### Phase 6 — Preserve CLI compatibility

Keep `test/smoke-wrkf.sh` passing. Add a parallel RPC smoke test that performs the same workflow lifecycle through JSON-RPC.

Do not break operator muscle memory while creating the machine surface.

---

## Validation gates

Automated gates:

```text
go test ./internal/workflow ./internal/wrkfapi ./internal/wrkfrpc ./internal/wrkfcli
existing wrkf CLI smoke test
new wrkf RPC smoke test
TS unit tests with fake transport
TS integration tests with real wrkf rpc --stdio
ACP WrkfClient tests
ACP workflow facade tests
```

Concurrency-specific tests:

```text
two clients apply same transition with same expected revision -> one commit
same idempotency key + same transition request -> replay same result
same idempotency key + different payload -> WRKF_IDEMPOTENCY_MISMATCH
two clients claim effects -> no duplicate lease
ack/fail with wrong lease token -> WRKF_LEASE_CONFLICT
run.start repeated with same key -> same run
run.start repeated with same key but different role/task -> conflict
stdout receives hook stderr/log noise -> test fails
```

Manual smoke:

```text
wrkf rpc --stdio starts against real local db
initialize returns capabilities/schema hash
install/attach/inspect/next works
transition with expected revision changes revision exactly once
stale transition returns typed stale error
evidence facts update context hash
participant run start + bindExternal + finish works
effect claim + external ack/fail works
ACP can launch HRC participant run through TS client
ACP can reconcile wrkf effects through claim/ack/fail
human wrkf CLI still works
```

---

## Rejected alternatives

### Keep shelling out to `wrkf --json`

Acceptable as a first ACP bridge; wrong as the long-term boundary. It pays process startup per call, has no clean cancellation, encourages parsing CLI-shaped results, and makes error contracts depend on human strings.

### Build HTTP first

HTTP is not wrong, but it brings service lifecycle, auth, ports, readiness, and deployment questions too early. Stdio preserves the local parent-child trust model and is easier for ACP/Bun/Node tools to manage. A later HTTP or Unix-socket transport can reuse `internal/wrkfapi`.

### Make MCP the primary interface

MCP is a tool/agent affordance, not the application authority boundary. Build MCP on top of the TS client later. Do not make ACP’s correctness depend on MCP tool-call semantics.

### Let TypeScript read wrkq SQLite directly

That violates the core invariant. TS would inevitably recreate transition legality, idempotency, effect status, and context-hash logic outside wrkf.

### Expose Cobra as RPC

Tempting because it is fast, but it preserves the wrong abstraction. RPC methods should call typed service adapters, not command handlers.

---

## Main risks

The highest-risk area is **write-side race behavior**. A long-lived RPC process makes concurrent clients natural. If transition revision checks remain preflight-only, RPC will expose lost-update hazards more reliably than the current human CLI.

The second risk is **effect duplication**. ACP effect reconciliation requires claim/lease semantics, not just `effect list` followed by `ack/fail`.

The third risk is **API cement around accidental concepts**. Be careful not to make current CLI artifacts permanent: command strings in `next`, `run bind` as role binding, ad hoc maps, and human error strings.

The fourth risk is **stdout contamination**. In stdio RPC mode, a single log line on stdout corrupts the protocol. All logs, hook output, and diagnostics must go to stderr or structured result fields.

---

## Final recommendation

Build the wrkf long-term client substrate in this order:

```text
1. internal/wrkfapi typed method layer
2. atomic transition/idempotency hardening
3. run external binding + run idempotency
4. effect claim/lease protocol
5. internal/wrkfrpc JSON-RPC NDJSON server
6. wrkf rpc --stdio
7. @wrkf/client TypeScript binding
8. ACP integration through the TS binding
9. RPC smoke/concurrency/contract tests
```

The core design constraint is: **do not make JSON-RPC a prettier CLI.** Make it the stable application protocol over wrkf’s workflow authority.
