# wrkf RPC — Frozen Contract (v1)

> **Status:** P0 contract freeze. This document is the *machine contract* for the wrkf
> JSON-RPC stdio substrate. It is authoritative for method names, DTO field names, and the
> protocol/lifecycle shape. **Authority exception:** the `WRKF_*` error-code set and their
> retryability are owned by the durable architecture contract record
> [architecture/contracts/wrkf-rpc.yaml](architecture/contracts/wrkf-rpc.yaml)
> (`wrkq.contract.wrkf-rpc`) — for that slice this document is explanatory/projection and must
> conform to the record. Implementation phases (wrkfapi, hardening, workrpc, @wrkq/client) must
> conform to this file. Design rationale now lives in git history and tracked wrkq tasks; this
> file is the maintained contract.

Protocol version string: **`2026-06-01`**.

> **Durable-law anchor:** the error-recovery half of this contract is captured as durable
> architecture law in [architecture/contracts/wrkf-rpc.yaml](architecture/contracts/wrkf-rpc.yaml)
> (record `wrkq.contract.wrkf-rpc`), with provenance in
> [architecture/adr/0001-wrkf-rpc-recovery-contract.md](architecture/adr/0001-wrkf-rpc-recovery-contract.md).
> That record is the authority for `WRKF_*` codes + retryability; this file is the explanatory
> machine contract that conforms to it. (Link paths here are repo-root-relative.)

---

## 1. Transport

- **JSON-RPC 2.0 framed as NDJSON.** One compact JSON object per line on stdout, terminated by `\n`.
- **stdout:** JSON-RPC frames ONLY. A single stray log/print line corrupts the protocol and is a test failure.
- **stderr:** logs, hook output, human diagnostics.
- **stdin:** JSON-RPC request/notification frames.
- **Pretty JSON on stdout: forbidden.**
- **Max frame size:** bounded (default 8 MiB; over-limit inbound frame → `WRKF_VALIDATION`, connection stays open).
- All method handlers accept `context.Context` from day one. Cancellation is **best-effort** in v1 (handlers and hook execution are not yet fully context-aware — do NOT advertise strong cancel; `capabilities.cancel` reflects best-effort).

---

## 2. Lifecycle

| Method | Kind | Notes |
|---|---|---|
| `wrkf.initialize` | request | First call. Returns capabilities + schema hash. Methods other than `initialize` before init → `WRKF_VALIDATION`. |
| `wrkf.shutdown` | request | Graceful drain. After it returns, no new requests accepted. |
| `wrkf.exit` | notification | Process exits. `stdin` EOF is equivalent. |
| `$/cancelRequest` | notification | `{ "id": <id> }`. Best-effort in v1. |

**initialize params:**
```json
{ "protocolVersion": "2026-06-01", "client": { "name": "acp", "version": "..." } }
```
**initialize result:**
```json
{
  "protocolVersion": "2026-06-01",
  "server": { "name": "wrkf", "version": "...", "pid": 12345 },
  "database": { "path": "/.../wrkq.db" },
  "capabilities": { "cancel": true, "effectClaimLease": true, "runExternalBinding": true },
  "schemaHash": "sha256:..."
}
```
`cancel: true` means *accepted, best-effort*. Protocol-version mismatch on initialize → `WRKF_VALIDATION` with `data.expected`/`data.actual`.

RPC mode loads config + DB once and keeps the `workflow.Service` warm for the process lifetime. Config (db path, hook catalog path, principal_ref/role defaults) arrives via argv flags on `wrkf rpc --stdio` and/or `initialize` params — never environment-only magic.

---

## 3. Error contract

JSON-RPC `error` object. `data.code` is the stable contract; `message` is human-readable and may change.

```json
{ "jsonrpc": "2.0", "id": "req_12",
  "error": { "code": -32009, "message": "workflow revision mismatch",
    "data": { "code": "WRKF_STALE_REVISION", "instanceId": "wfi_...",
              "expectedRevision": 3, "actualRevision": 4, "retryable": true } } }
```

| `data.code` | JSON-RPC `code` | `retryable` | Meaning |
|---|---|---|---|
| `WRKF_NOT_FOUND` | -32004 | false | task/instance/effect/run/template not found |
| `WRKF_VALIDATION` | -32602 | false | malformed params, bad protocol version, frame too large, pre-init call |
| `WRKF_STALE_REVISION` | -32009 | true | `expectRevision` ≠ committed revision (CAS) |
| `WRKF_CONTEXT_MISMATCH` | -32010 | true | `contextHash` ≠ committed context hash (CAS) |
| `WRKF_TRANSITION_BLOCKED` | -32011 | false | guards/blockers/obligations not satisfied (carries `blocksOn`) |
| `WRKF_ROLE_DENIED` | -32012 | false | principal/role not permitted for transition |
| `WRKF_IDEMPOTENCY_MISMATCH` | -32013 | false | same key, different request hash |
| `WRKF_LEASE_CONFLICT` | -32014 | true | ack/fail with wrong/expired lease token |
| `WRKF_EFFECT_NOT_DELIVERABLE` | -32015 | false | effect not in a deliverable state |
| `WRKF_HOOK_FAILED` | -32016 | context-dependent; value supplied in `data.retryable` | hook/check execution failed |
| `WRKF_KIND_ROLE_DENIED` | -32018 | false | evidence kind is not producible by the supplied role (template `producibleBy` conformance — supplied-role only, **not** an authenticated-principal boundary) |
| `WRKF_LINKAGE_UNRESOLVED` | -32019 | false | a declared evidence `data` linkage ref did not resolve to a live evidence id on the same instance (template `linkageRefs`) |
| `WRKF_LINKAGE_STALE` | -32020 | false | a `linkageRefs` entry with `latest:true` points at a superseded (non-current) evidence of the expected kind; `data.fix` names the current id |
| `WRKF_SUSPENDED` | -32026 | false | the instance has an active suspension; `data.suspension` carries its `id`, `reason`, `at`, and optional `causeRef` |
| `WRKF_INTERNAL` | -32603 | false | unclassified internal error |

`data.retryable` is **always a boolean** on every error instance (no `maybe`/absent). `data.code` is always present for WRKF_* domain errors.

**Structured validation detail.** `WRKF_VALIDATION`, `WRKF_KIND_ROLE_DENIED`, `WRKF_LINKAGE_UNRESOLVED`, and `WRKF_LINKAGE_STALE` carry a uniform self-correction payload in `data`: `field`, `message`, and (when applicable) `expected`, `allowed` (array), and `fix`. The same shape is emitted by the `wrkf` CLI in `--json` mode as `{"error":{code,field,message,expected,allowed,fix}}` on **stdout** (exit non-zero); text-mode CLI errors stay on stderr. This lets an agent driving `wrkf` directly (`next` → `evidence add` → `transition`) parse one deterministic stream and self-correct in-turn.

**Standard JSON-RPC protocol errors** — parse error (-32700), invalid request (-32600), method not found (-32601), invalid params (-32602 when not a domain `WRKF_VALIDATION`) — use the standard JSON-RPC codes and MAY omit `data.code`. Clients must treat a missing `data.code` as a transport/protocol-level error, not a domain error.

Typed Go errors (`internal/wrkfapi/errors.go`) MUST exist and carry these codes before P3 maps anything. No `fmt.Errorf` string ever reaches the protocol boundary.

**Canonical Go error surface (frozen — all reds + impl agree on exactly this):**
- A `wrkfapi.Error` type implementing `interface { error; Code() string; Retryable() bool }`, unwrappable via `errors.As`. `Code()` returns the `WRKF_*` string; `Retryable()` returns the boolean. (Tests may match a narrower local `interface { Code() string }` — that is a subset and is fine.)
- Constructors (one per code), so call sites never hand-build errors:
  `NewNotFoundError(ref, kind)`, `NewValidationError(msg string, data any)`, `NewStaleRevisionError(instanceID string, expected, actual int64)`, `NewContextMismatchError(instanceID, expected, actual string)`, `NewTransitionBlockedError(instanceID, transition string, blocksOn []Blocker)`, `NewRoleDeniedError(instanceID, transition, role string)`, `NewIdempotencyMismatchError(key string)`, `NewLeaseConflictError(effectID, token string)`, `NewEffectNotDeliverableError(effectID, status string)`, `NewInternalError(err error)`.
- `workrpc.MapError(err error) *RPCError` maps a `wrkfapi.Error` to the JSON-RPC error (numeric `code` + `data.code` + boolean `data.retryable`). A plain non-domain error maps to the unified internal error (-32603).

---

## 4. Method catalog (v1)

Semantic, stable, NOT one-to-one Cobra wrappers. `supervisor *` is **excluded** from v1 (CLI-only, provisional).

### 4.1 Full v1 catalog (all methods, by namespace)
```
wrkf.workflow.validate   wrkf.workflow.show   wrkf.workflow.list   wrkf.workflow.diff   wrkf.workflow.install
wrkf.task.attach   wrkf.task.inspect   wrkf.task.timeline   wrkf.task.refresh   wrkf.task.syncMeta
wrkf.next
wrkf.evidence.add   wrkf.evidence.list   wrkf.evidence.show   wrkf.evidence.suggest
wrkf.ledger.append  wrkf.ledger.list
wrkf.obligation.list   wrkf.obligation.show   wrkf.obligation.satisfy   wrkf.obligation.waive   wrkf.obligation.cancel
wrkf.check.preflight   wrkf.check.run   wrkf.check.show   wrkf.check.list
wrkf.hook.list   wrkf.hook.show   wrkf.hook.run
wrkf.transition.apply
wrkf.run.start   wrkf.run.bindExternal   wrkf.run.finish   wrkf.run.fail   wrkf.run.show   wrkf.run.list
wrkf.effect.list   wrkf.effect.show   wrkf.effect.claim   wrkf.effect.ack   wrkf.effect.fail   wrkf.effect.retry   wrkf.effect.deliver
```

### 4.2 Phase mapping

**P1a — strictly read / no-persist / no-external-action.** These do not write workflow state and trigger no external side effects:
```
wrkf.workflow.validate   wrkf.workflow.show   wrkf.workflow.list   wrkf.workflow.diff
wrkf.task.inspect   wrkf.task.timeline
wrkf.next
wrkf.evidence.list   wrkf.evidence.show   wrkf.evidence.suggest
wrkf.obligation.list   wrkf.obligation.show
wrkf.check.preflight   wrkf.check.show   wrkf.check.list
wrkf.run.show   wrkf.run.list
wrkf.effect.list   wrkf.effect.show
wrkf.hook.list   wrkf.hook.show
```
(`check.preflight` is read-only: it evaluates legality without persisting a check run.)

**P1-general — unhardened mutators** (persist state but need NO CAS/idempotency/lease; plain typed wrappers, land with P1a in task B):
```
wrkf.workflow.install   wrkf.task.attach   wrkf.task.refresh   wrkf.task.syncMeta
wrkf.evidence.add
wrkf.obligation.satisfy   wrkf.obligation.waive   wrkf.obligation.cancel
wrkf.check.run   wrkf.hook.run
wrkf.ledger.append
```

**P1b / P2a — transition (hardened):** `wrkf.transition.apply`
**P1c / P2b — run (hardened):** `wrkf.run.start   wrkf.run.bindExternal   wrkf.run.finish   wrkf.run.fail`
**P1d / P2c — effect (hardened):** `wrkf.effect.claim   wrkf.effect.ack   wrkf.effect.fail   wrkf.effect.retry   wrkf.effect.deliver`

**Ledger:** `wrkf.ledger.append` records an immutable, instance-scoped forensic event. It resolves the task to its latest instance (including a settled instance), stamps `seq`, `ts`, and `writtenBy` from the configured canonical `agent:<id>` caller, and accepts open workflow-specific `kind` values plus a free-form JSON body. `wrkf.ledger.list` replays a task in `(instanceId, seq)` order or projects entries about one principal in frozen `(ts, instanceId, seq)` cursor order. There are no ledger mutation methods.

---

## 5. DTOs

DTOs are stable named structs. They are envelopes over existing `internal/workflow` runtime types,
whose JSON tags are already correct and are hereby frozen as the wire shape. **No `map[string]any`
crosses the RPC boundary** except explicitly-raw template/debug fields.

Frozen result shapes (existing Go types, JSON tags as-is):
`Instance`, `Event`, `Evidence`, `Obligation`, `Effect`, `Run`, `CheckRun`, `NextActionResponse`,
`ValidateResult`, `Template` (raw allowed).

New named DTOs to add (replace current `map[string]interface{}` returns). All fields frozen:

- `TemplateSummary { id, version, hash, kind, description, installedAt, installedBy }`
- `WorkflowListResult { templates: []TemplateSummary }` (was `ListTemplates` `[]map`)
- `WorkflowShowResult { template: Template, hash }` (was `ShowTemplate` tuple)
- `InstallResult { id, version, hash, installed }` — `installed` is `true` when this call wrote a new template version, `false` when the identical version+hash already existed. (No redundant `alreadyInstalled`; it would just be `!installed`.)
- `TransitionResult { task, instanceId, state: State, revision, contextHash, eventId, effects: []Effect, obligations: []Obligation }` (was `Transition` map)
- `SuggestResult { transition, required: []EvidenceRequirementSpec, missing: []string, checks: []string, warnings: []string }` (was `SuggestEvidence` map)
- `DiffResult { old: TemplateSummary, new: TemplateSummary, sameHash: bool }` (was `DiffTemplateFiles` map)
- `SyncMetaResult { updated: int }` (was `SyncMeta` int)
- `EffectClaim { effects: []Effect, leaseToken, leaseExpiresAt }`
- `CheckRunResult { runs: []CheckRun }`

### 5.1 Write-side required-field contracts

**`wrkf.transition.apply` params** (maps onto `TransitionOptions`):
```json
{ "task": "T-00001", "transition": "plan_ready", "role": "coordinator", "principal_ref": "human:local",
  "expectRevision": 0, "contextHash": "sha256:...", "idempotencyKey": "acp:T-00001:plan_ready:0",
  "runChecks": false, "dryRun": false }
```
- `expectRevision` and `contextHash` are CAS preconditions enforced **in the DB write**, not preflight.
- `idempotencyKey` (optional but recommended). Replay semantics: **same key + same request hash → return the original committed `TransitionResult`** (not current-latest + old event id). Same key + different request hash → `WRKF_IDEMPOTENCY_MISMATCH`.

**`wrkf.run.start` params:**
```json
{ "task": "...", "role": "...", "principal_ref": "...", "idempotencyKey": "...",
  "deliveryRef": "?", "lane": "?", "externalRunRef": "?" }
```
- `idempotencyKey` unique per `(instance_id, idempotencyKey)`. Same key → same `Run` (replay). Same key + different role/task → conflict (`WRKF_IDEMPOTENCY_MISMATCH`).
- `externalRunRef` unique when non-empty.

**`wrkf.run.bindExternal` params:** `{ runId, externalRunRef, deliveryRef?, lane?, idempotencyKey? }`.
**`wrkf.run.finish` / `wrkf.run.fail` params:** `{ runId, summary? }`. Idempotent for terminal states. Repeated terminal with identical payload → existing terminal `Run`; conflicting terminal payload → conflict. For `wrkf.run.fail`, the failure text is returned on `Run.terminalResult`.

**`wrkf.effect.claim` params:** `{ adapter, limit, leaseMs, task?, kind? }` → `EffectClaim`.
- Atomically selects pending/failed/expired effects and marks them `leased` with a generated `leaseToken`.
**`wrkf.effect.ack` params:** `{ effectId, leaseToken, receipt? }`. Wrong/expired token → `WRKF_LEASE_CONFLICT`.
**`wrkf.effect.fail` params:** `{ effectId, leaseToken, reason, retryable? }`. Wrong/expired token → `WRKF_LEASE_CONFLICT`.
**`wrkf.effect.retry` params:** `{ effectId }`.
**`wrkf.effect.deliver`:** in RPC, MUST own claim+lease internally (claim → run handler → ack/fail under the lease). It is NOT a bare `ack` shortcut. Operator `force` is CLI-only.

> Backward compatibility: `wrkf.effect.retry` also accepts legacy `{ id }`, but `{ effectId }` is the canonical RPC surface.

> **Run/role-binding caveat:** the current schema conflates "an active run with a delivery ref" with role binding (`workflow_role_bindings` exists but is underused). v1 TS API exposes `run.start`/`run.bindExternal` as the *execution-attempt* shape and must NOT cement this conflation as a durable role binding. A future `wrkf.role.bind` may split it; v1 names the run shape as a compatibility shape only.

---

## 6. Write-side hardening invariants (P2)

These are correctness gates, grounded in the actual schema (migration `000014_wrkf_schema.sql`):

1. **Atomic transition CAS.** A dedicated `BEGIN IMMEDIATE` tx (the existing `withTx` is plain deferred `db.Begin()` — do NOT assume it is IMMEDIATE; add a deliberate immediate-tx helper). Order: load instance → check idempotency key+request hash → check role/guards/blockers/obligations → `UPDATE workflow_instances SET ... WHERE id=? AND revision=? [AND context_hash=?]` → **verify rows-affected = 1** (else `WRKF_STALE_REVISION`/`WRKF_CONTEXT_MISMATCH`) → THEN insert event/effects. Stale CAS must fail *before* any event/effect insert.

   **contextHash scope (frozen):** the transition `contextHash` precondition and the `contextHash` in `TransitionResult` are the **instance context hash over committed instance state** (status/phase/outcome/revision + task-doc etag/hash). It does **NOT** incorporate effects/obligations created by the *same* transition — those surface on the next `task.inspect`/`task.refresh`. Implementations must not require pre-hashing newly-created effect/obligation IDs into the CAS.

2. **Idempotency replay material (not just a hash).** A request hash detects *mismatch*; it cannot reconstruct the original result (obligations have no revision column in `000014`, so post-hoc reconstruction is lossy). Therefore store the **committed result itself**: persist `result_json` (the `TransitionResult`, including created effect IDs and created obligation IDs) in the event payload (or a dedicated column) keyed by `(instance_id, idempotency_key)`. Replay = same key + same request hash → return the stored `result_json` verbatim. Same key + different request hash → `WRKF_IDEMPOTENCY_MISMATCH`.
3. **Run idempotency + external binding.** Add to `workflow_runs`: `idempotency_key TEXT`, `request_hash TEXT`. `external_run_ref` already exists — make it first-class. Indexes (partial, non-empty guarded):
   - `UNIQUE(instance_id, idempotency_key) WHERE idempotency_key IS NOT NULL`
   - `UNIQUE(external_run_ref) WHERE external_run_ref IS NOT NULL AND external_run_ref <> ''`
   - Preflight duplicate non-empty `external_run_ref` before adding the index (guard hand-edited DBs).
4. **Effect lease token + predicates.** `leased_by`/`leased_until` already exist; add a real `lease_token TEXT`. `claim` atomically selects pending/failed/expired effects + sets `status='leased'`, `leased_by`, `leased_until`, `lease_token`. `ack`/`fail` MUST update only `WHERE id=? AND status='leased' AND lease_token=? AND leased_until > now` (else `WRKF_LEASE_CONFLICT`). Terminal (`delivered`/`failed`/`cancelled`) and `retry` paths MUST clear `lease_token`/`leased_by`/`leased_until`. Operator `force` (CLI-only) may bypass the token predicate.

All new migrations are **additive** (nullable columns + partial unique indexes) — safe for existing rows.

---

## 7. @wrkf/client TS package (P4)

- Lives **in this repo**, quarantined: `packages/wrkf-client/` with its OWN `package.json`, lockfile, `tsconfig.json`. NOT in the root Go build; `just build`/`just install` must be unaffected.
- Just targets (net-new): `wrkf-client-install`, `wrkf-client-test`, `wrkf-client-integration`, and `verify-rpc`.
- Bun is acceptable as the dev runtime (ACP is Bun) but the client API stays Node-ish (`child_process.spawn`, not Bun-only APIs) unless a Bun-only choice is intentional.
- Layout: `src/{client,transport,stdio-transport,json-rpc-channel,errors,types}.ts`.
- Transport: `child_process.spawn` (no shell); pending-request map; `AbortSignal`; bounded stderr tail for diagnostics; reject all pending on process exit; detect protocol corruption (non-JSON on stdout); distinct `close()` vs `kill()`; never parse human CLI output.
- Namespaces: `client.{workflow,task,evidence,obligation,check,transition,run,effect,hook}.*`.
- Errors surface as typed `WrkfRpcError` carrying `data.code`.
- v1 hand-maintained TS types (no codegen yet); add fixture-based compatibility tests.

**Acceptance harness (replaces ACP for this pass):** a TS integration test in `packages/wrkf-client/` that `child_process.spawn`s the real `wrkf rpc --stdio` binary against a temp, freshly-migrated wrkq DB and drives the lifecycle (initialize → install/attach/inspect → next → transition with CAS → evidence → run start/bindExternal/finish → effect claim/ack). Go tests still cover codec/registry/error-mapper/stdout-purity; the TS integration test is the substrate acceptance.

---

## 8. Acceptance gates

```
go test ./internal/workflow ./internal/wrkfapi ./internal/workrpc ./internal/wrkfcli
existing wrkf CLI smoke test (test/smoke-wrkf.sh) still green
new wrkf RPC smoke test (test/smoke-wrkf-rpc.sh)
TS unit tests (fake transport) + TS integration test (real wrkf rpc --stdio vs temp DB)
```
Concurrency-specific:
```
two clients apply same transition w/ same expectRevision -> exactly one commit
same idempotencyKey + same request -> replay original committed result
same idempotencyKey + different payload -> WRKF_IDEMPOTENCY_MISMATCH
two clients claim effects -> no duplicate lease
ack/fail with wrong lease token -> WRKF_LEASE_CONFLICT
run.start repeated same key -> same run; different role/task -> conflict
hook stderr/log on stdout -> test fails (stdout purity)
```
