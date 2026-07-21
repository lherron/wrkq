# wrkf Remote-Transport Client Migration Spec

- **Status:** RATIFIED-AS-AMENDED (2026-07-21) — daedalus drafting consult #15699, mable disposition #15700 under Lance-delegated ratification authority. Build dispatch awaits Lance go.
- **Owner:** mable (campaign T-06754..T-06762, container `wrkq/wrkf-rpc-client`)
- **Ruling authority:** Lance authorized the campaign and the legacy-CLI decomm (2026-07-21) and delegated spec ratification to mable. Standing Lance rulings folded in: the campaign's test surface is frozen (no dedicated proof-harness bars beyond ordinary slot unit coverage); daedalus surface economy (new-invariant proposal deferred to post-S7 evidence).
- **Companion contracts:** `docs/wrkq-wrkf-rpc.md` (machine contract), `docs/wrkq-wrkf-rpc-client-forward-spec.md` (TS client), `architecture/records/invariants/wrkq.rpc.remote-transport-locator.yaml` (remote transport invariant)

## 1. Motivation

The canonical wrkq DB re-homed to node `mini` (F-1, T-06599): `WRKQ_DB=rpc://mini`, served by wrkqd. The wrkq CLI migrated to the rpccli transport and works federated. The wrkf CLI did not: `internal/wrkfcli/root.go:115-116` hard-rejects rpc:// locators ("wrkf requires a local database path") and opens SQLite directly. Consequence: every wrkf-task-loop room on max3 is blocked (H-00277); the adornable-nodes campaign (T-06738) is paused on it.

This spec migrates the wrkf CLI to the same transport seam wrkq uses, adds the small set of missing RPC methods, resolves the filesystem-coupled method families, and then decommissions the legacy non-rpccli command surfaces in **both** binaries.

## 2. Ground truth (scouted 2026-07-21)

- **Server side already exists.** The unified registry (`internal/workrpc/registry.go`) serves 58 `wrkf.*` methods next to the full `wrkq.*` surface. wrkqd mounts the same registry (`internal/cli/daemon.go:96-99`), so wrkqd on mini serves `wrkf.*` over `/v1/rpc` today. `wrkf rpc --stdio` serves the same catalog (entrypoint equivalence enforced by `internal/workrpc/entrypoint_equivalence_test.go`).
- **The gap is client-side only.** wrkf commands run `withApp(needsDB)` → `db.Open` + `workflow.NewService` per command (`internal/wrkfcli/root.go:86-131`); no Transport seam.
- **The pattern to mirror** is `internal/rpccli`: one `Transport.Call(ctx, method, params)` interface; InProcess / Subprocess / Remote implementations; switch once at construction (`internal/rpccli/transport.go:425-452`); `rpc://host[:port]` locator parsing with default port 7171 in `internal/config` (`config.go:215-243`); bearer/node auth from `WRKQD_TOKEN`/`WRKQD_TOKEN_FILE` (`transport.go:413-423`); mandatory `rpc.initialize` + schema-hash validation before business dispatch; importguard forbidding store/db imports from command paths.
- **Coverage matrix:** most wrkf commands map 1:1 onto existing methods. Six commands have no covering method (§5). Two families are structurally coupled to the daemon host's filesystem (§6, §7).

## 3. D1 — Transport seam: share rpccli's, do not fork

**Decision (ratified):** extract the `Transport` interface plus the Remote/InProcess/Subprocess implementations from `internal/rpccli` into **`internal/workrpc/client`** — it is workrpc protocol machinery and that location fits the existing layerguard `workrpc-ownership` exception. Move only protocol/client machinery (`Transport`, `Error`, conn, transports, initialize validation); Cobra/presentation stays outside. wrkfcli constructs it with `RegistryOptions{Entrypoint: "wrkf"}` for the local InProcess registry label; a remote wrkqd is the unified registry and is never required to report entrypoint wrkf.

**Initialize client profile:** the handshake is parameterized per client. Today it hardcodes `capabilities.wrkq` + `wrkq.task.show` as the probe (`internal/rpccli/transport.go:154-172`); the wrkf profile requires `capabilities.wrkf` + a minimal wrkf method instead.

- Locator/config semantics are byte-identical to wrkq: `WRKQ_DB=rpc://mini` → endpoint `mini:7171`, `cfg.RemoteEndpoint` set, `cfg.DBPath` cleared. The pathOnly rejection in wrkfcli is removed; `wrkqadm`/`wrkq server` keep pathOnly.
- Token precedence, auth-error fidelity (commit 82ea18d), and the token-file-over-dotenv rule (commit b691b7a) come along for free by sharing code.
- Local mode = InProcess transport over the same registry — NOT the old direct `workflow.Service` calls. Local and remote take the identical method path; only the transport differs. This is what makes S8 decomm safe.
- Alternative considered and rejected: parallel wrkf-specific transport — duplicates auth/handshake/error logic and lets the two CLIs drift.

## 4. D2 — Namespaces (ratifies the Lance constraint)

- `wrkq.*` and `wrkf.*` remain sibling top-level service prefixes on the shared registry, dotted `service.domain.verb` (camelCase for multiword verbs: `bindExternal`, `claimValidate`). This is the aspc-style consolidation-safe shape: any future single-RPC-server consolidation multiplexes `aspc.*`/`wrkq.*`/`wrkf.*` with zero renames.
- Namespace is the authority boundary (unchanged from the ratified forward-spec): task-record mutation is `wrkq.*` only; `wrkf.task.*` and `wrkf.workflow.attach` stay forbidden (`internal/workrpc/registry_contract_test.go:21-32`).
- Consequence: the missing sync-meta method lands as **`wrkq.workflow.syncMeta`**.

## 5. D3 — Six missing methods (slot S4)

| CLI command | New method | Wraps |
|---|---|---|
| `wrkf task sync-meta` | `wrkq.workflow.syncMeta` | `service.SyncMeta` (`internal/workflow/service.go:1765`) |
| `wrkf evidence schema` | `wrkf.evidence.schema` | `service.EvidenceSchema` (`service.go:2445`) |
| `wrkf supervisor call` | `wrkf.supervisor.call` | `service.SupervisorCall` (`ledger.go:2459`) |
| `wrkf supervisor action escalate` | `wrkf.supervisor.escalate` | `service.SupervisorEscalate` (`ledger.go:2482`) |
| `wrkf supervisor action create-obligation` | `wrkf.obligation.create` | `service.CreateObligation` (`ledger.go:821`) |
| `wrkf watch` | `wrkf.watch.snapshot` + `wrkf.watch.events` (D4) | `service.WatchSnapshot` / `WatchEvents` |

Domain rulings (ratified): `wrkf.supervisor.*` is correct — these create supervisor effects against an instance/task; `wrkf.run.*` denotes execution attempts and would be a false ownership claim. `wrkq.workflow.syncMeta` enforces the boundary in implementation as well as naming: the handler is owned in the wrkq API/register region, not behind wrkfapi.

Every new method carries the full contract obligation set (standing ruling): registry + catalog entry, handler, `docs/wrkq-wrkf-rpc.md` update, protocolSchemaHash regeneration, `@wrkq/client` facade + types + unit coverage, forward-spec update. Idempotent mutators use the shared JSON canonicalizer — no ad-hoc marshaling.

## 6. D4 — Watch surface: bounded polling, client-side loop

`wrkf watch` today polls `WatchSnapshot`/`WatchEvents` against the local DB (`internal/wrkfcli/watch.go:82-127`). Design per the forward-spec §6.4 doctrine (bounded polling, never streaming):

- `wrkf.watch.snapshot` — durable-predicate snapshot for a selector set (same params the service call takes today).
- `wrkf.watch.events` — cursor-bounded event page (`afterCursor`, `limit`), no long-poll, no server-side wait.
- **Cursor identity (daedalus refinement):** the events cursor binds to resolved target identity plus seq (`instanceId`/`runId`, `seq`), never bare seq — a task selector can resolve to a successor instance whose seq restarts, and a bare carried cursor would suppress its early events. Snapshot and events remain independent and race-tolerant.
- The poll LOOP stays in the CLI client; interval/`--until` predicate evaluation is client-side. The server never holds a request open. Preserves active invariant `wrkq.wrkf-rpc.bounded-polling-streaming`: server owns one snapshot/page and a hard cap; client owns interval, timeout, terminal line, exit.

## 7. D5 — Template ops: content-bearing params

`wrkf.workflow.install/validate/diff` handlers currently read a **path on the daemon's disk** (`internal/wrkfapi/api.go:129, 44, 114`) — unusable for non-colocated callers.

**Decision (ratified):** content-bearing request shapes — client reads the file, sends the template body (and for `diff`, both bodies) in params. The path-based variant is **deleted with this change** (not deferred to S8): local InProcess uses the same content DTO, so a colocated path fast path buys only split semantics. Caps: **1 MiB per decoded template body, 2 MiB aggregate for diff**, server-authoritative with client preflight. A diagnostic `sourceName` may accompany the body but is never interpreted as a daemon path. `workflow validate`/`diff` route through RPC in remote mode for engine-version-consistent validation; in local mode InProcess makes this equivalent.

**Envelope bound (daedalus find):** wrkqd's HTTP endpoint currently decodes an unbounded body (`internal/cli/daemon.go:348-355`); the existing 8 MiB workrpc envelope cap applies only in the stdio codec. Apply `http.MaxBytesReader` to `/v1/rpc` before JSON decode.

## 8. D6 — Hook execution locus (the real design question)

`wrkf.check.run`, `wrkf.hook.run`, `wrkf.effect.deliver`, and `wrkf.transition.apply --run-checks` execute hooks via the **daemon-configured** `hookCatalog`/`templateDir` (`api.go:339, 428, 432`; `effect.go:53`). The caller's `--hook-catalog` flag is not transmitted.

**Decision (ratified, daedalus-amended): daemon-selected law, isolated execution.** Daemon-side authority is upheld — hooks are workflow law; the canonical host owns durability and law enforcement; a client-supplied catalog over RPC would let any caller substitute law. Two binding conditions shape the implementation:

**Condition 1 — pinned catalog identity** (invariant: a template-bound hook definition cannot be silently substituted by caller input or daemon filesystem discovery):

- Remote `--hook-catalog` hard-refuses ("hook catalog is canonical-node configuration"); local (InProcess) mode may honor it as today.
- wrkqd hook configuration is an **explicit deployed path/bundle**. The daemon never consults `ResolveHookCatalogPath("")` autodiscovery (which searches cwd and every `~/praesidium/*` checkout, `internal/workflow/service.go:3763-3817`).
- Execution for check/run/effect/transition selects the HookSpec from the template version's **stored `hook_catalog_json`**. v1 single-bundle deployments compare the stored `hook_catalog_hash` against the configured catalog and **fail closed on mismatch**. The daemon bundle supplies the executable root.
- `wrkf hook list/show` expose the daemon's active catalog in remote mode but never redefine already-installed template law.
- The workspace-scoped class is **live, not hypothetical**: `.wrkq/wrkf-agent-tasker/hook-catalog.json` invokes `scripts/*` with `cwd=template_dir` and its effect handler invokes `hrcchat`. That catalog + executable bundle (scripts, `jq`, `hrcchat`) must actually be deployed to mini — an explicit item on the §10 landing checklist. A future caller-workspace-data hook class gets an explicit prepare/execute/record verb; never a catalog override.
- `wrkf evidence exec` remains the sanctioned client-side pattern: execute locally via os/exec, record via `wrkf.evidence.add`.

**Condition 2 — execution isolated from the canonical control plane** (invariant: external execution cannot monopolize RPC availability, hold the SQLite writer lock, or commit after the client received a transport timeout). Current hazards: `workrpc.Server.mu` covers the entire handler (`internal/workrpc/server.go:86-134`), `stdoutRedirectMu` covers it again, `transition --run-checks` executes hooks inside the IMMEDIATE transaction (`internal/workflow/ledger.go:1745-1755`), and catalog timeouts (600s) exceed wrkqd's 30s HTTP write timeout. Ratified shape:

- Hook execution moves **outside** the global dispatch/stdout critical sections; no hook executes inside a writer transaction.
- CLI `transition --run-checks` decomposes into daemon `check.run` followed by `transition.apply` with the persisted check IDs; the existing input-hash revalidation in `checkCommitBlockers` makes the race fail closed.
- **Timeout ceiling (mable amendment, Lance scope-economy ruling):** in place of polled-operation machinery, the server enforces that a hook's execution timeout fits within the route's response deadline; catalog entries exceeding the bound are refused for remote execution with an explicit error. No mutation may continue ambiguously past a client's transport deadline.

Both conditions' behaviors receive ordinary unit coverage within their implementing slots; per Lance ruling 2026-07-21 no dedicated proof-harness acceptance bars are added.

## 9. D7 — Command migration tranches (slots S1-S3, S5)

- S1: seam only (§3). S2: read commands → adapters. S3: mutation commands → adapters (deferring `--run-checks`/`effect deliver` to S5 per D6). S5: template + hook families per D5/D6.
- Every migrated command: byte-identical local output vs pre-migration (golden tests), plus an `rpc://mini` smoke.
- `wrkf task attach/refresh/inspect/timeline` route to `wrkq.workflow.*` (task-side, D2).
- Guards (S6): importguard on wrkf command paths permits the shared `internal/workrpc/client` package and forbids direct bootstrap/db/workflow imports (sanctioned local-execution carve-outs: evidence exec, watch loop); entrypoint-equivalence extension; golden-parity suite as CI guard.
- Condition-2 server work (mutex narrowing, `transition --run-checks` decomposition, timeout ceiling) is its own slot (S5b) so S5 stays a client/template migration slot.

## 10. D8 — Version skew & coordinated landing

**Dev topology (Lance 2026-07-21):** development runs on mini, colocated with the canonical wrkqd/DB, so dev agents have live canonical-DB access. Local testing still uses isolated DB copies; the live daemon changes only via coordinated landing. Cross-node acceptance (S7) runs from max3 — that separation is the proof.

New methods change `protocolSchemaHash`; `rpc.initialize` hard-fails on skew (by design). Doctrine:

1. Server-side method additions land first and are deployed to mini's wrkqd (coordinated landing; operator-gated). The landing checklist includes the hook catalog + executable bundle (agent-tasker `scripts/*`, `jq`, `hrcchat`) on mini — an open risk daedalus flagged that S7 proves or dissolves.
2. Client tranches that depend on new methods land only after mini's daemon reports the new hash.
3. The skew failure mode is itself a test case in S7 (mismatched hash must fail closed with a faithful error, not fall back to local).
4. Standing migration hazard applies: smoke on isolated DBs; `just install` against live surfaces only inside the coordinated landing window.

## 11. Decomm doctrine (slot S8 — Lance ruling 2026-07-21)

Supersedes the internal/cli compat-freeze. After S7 passes on the real topology:

1. **Inventory first.** `internal/cli` splits into legacy command registrations (delete) and load-bearing plumbing (relocate): `ServeDaemon` + daemon HTTP surface (`internal/cli/daemon.go`), wrkqadm config flows, migration tooling. The deletion PR names every survivor and its new home.
2. **Caller sweep before deletion** across justfile, scripts, skills (spaces-repo, agent homes), docs, and sibling repos — deletion must be a surface no-op.
3. wrkf: remove the direct-DB `withApp` path and the rpc:// rejection branch; local mode exists only via InProcess transport.
4. wrkq: binary remains `rpccli.ExecuteAs("wrkq")` only; legacy registrations deleted.
5. Guards extended so deleted paths cannot regrow; rollback story documented before landing.

## 12. Acceptance (campaign exit)

1. Full wrkf verb surface works from max3 against `rpc://mini` with node-token auth; auth failures surface faithfully.
2. Local-mode golden parity for every command.
3. One wrkf-task-loop canary room (T-06744) passes pre-read on max3 — H-00277 resume-gate step 1 — graded from durable readback.
4. Guards green; legacy surfaces deleted (S8); `docs/wrkq-wrkf-rpc.md`, forward-spec, and `@wrkq/client` contract-green for all new methods.

## 13. Consult record (closed 2026-07-21)

Daedalus drafting consult #15699; mable disposition #15700 under Lance-delegated ratification authority. Q1/Q2/Q4/Q5 approved with refinements (folded into §3, §5, §6, §7 above); Q3 amended to daemon-selected law + isolated execution (§8). Dispositions of note, with authority:

- Daedalus required tests 1-2: **amended away** — behaviors get ordinary unit coverage in their implementing slots; no dedicated proof-harness bars (Lance ruling: campaign test surface frozen).
- Q3-C2 polled-operation alternative: **rejected** in favor of the server-enforced timeout ceiling (Lance scope-economy ruling; invariant preserved).
- Proposed new hook-execution invariant: **deferred** until S7 live evidence on mini; existing-record source/enforcement-path updates land mechanically with S8.
- Open risk (unverified mini hook bundle): acknowledged; resolved by S7.
