# wrkf Remote-Transport Client Migration Spec

- **Status:** DRAFT — under daedalus drafting consult (2026-07-21)
- **Owner:** mable (campaign T-06754..T-06762, container `wrkq/wrkf-rpc-client`)
- **Ruling authority:** Lance authorized the campaign and the legacy-CLI decomm (2026-07-21); daedalus consult on the design decisions below; build holds until joint review.
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

**Decision (recommended):** extract the `Transport` interface plus the Remote and InProcess implementations from `internal/rpccli` into a shared internal package (working name `internal/rpctransport`), consumed by both rpccli and wrkfcli. wrkfcli constructs it with `RegistryOptions{Entrypoint: "wrkf"}`.

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

Every new method carries the full contract obligation set (standing ruling): registry + catalog entry, handler, `docs/wrkq-wrkf-rpc.md` update, protocolSchemaHash regeneration, `@wrkq/client` facade + types + unit coverage, forward-spec update. Idempotent mutators use the shared JSON canonicalizer — no ad-hoc marshaling.

## 6. D4 — Watch surface: bounded polling, client-side loop

`wrkf watch` today polls `WatchSnapshot`/`WatchEvents` against the local DB (`internal/wrkfcli/watch.go:82-127`). Design per the forward-spec §6.4 doctrine (bounded polling, never streaming):

- `wrkf.watch.snapshot` — durable-predicate snapshot for a selector set (same params the service call takes today).
- `wrkf.watch.events` — cursor-bounded event page (`afterCursor`, `limit`), no long-poll, no server-side wait.
- The poll LOOP stays in the CLI client; interval/`--until` predicate evaluation is client-side. The server never holds a request open.

## 7. D5 — Template ops: content-bearing params

`wrkf.workflow.install/validate/diff` handlers currently read a **path on the daemon's disk** (`internal/wrkfapi/api.go:129, 44, 114`) — unusable for non-colocated callers.

**Decision (recommended):** add content-bearing request shapes — client reads the file, sends the template body (and for `diff`, both bodies) in params; size-bounded (proposed cap 1 MiB per body, matching typical template sizes with wide margin). The path-based variant is removed at S8 decomm, not kept as a second semantics. `workflow validate`/`diff` (currently no-DB local) route through RPC in remote mode for engine-version-consistent validation; in local mode InProcess makes this equivalent.

## 8. D6 — Hook execution locus (the real design question)

`wrkf.check.run`, `wrkf.hook.run`, `wrkf.effect.deliver`, and `wrkf.transition.apply --run-checks` execute hooks via the **daemon-configured** `hookCatalog`/`templateDir` (`api.go:339, 428, 432`; `effect.go:53`). The caller's `--hook-catalog` flag is not transmitted.

**Decision (recommended):** daemon-side execution is canonical. Rationale: hooks are workflow law; the canonical host owns durability and law enforcement (remote-transport invariant's canonical-host-owns-durability split); a client-supplied catalog over RPC would let any caller substitute law. Consequences:

- The daemon's hook catalog + template dir become deployed configuration on the canonical node (mini) — part of the coordinated-landing checklist (§10).
- `--hook-catalog` on these verbs is honored in **local (InProcess) mode only**; in remote mode it is an error ("hook catalog is canonical-node configuration").
- `wrkf hook list/show` return the **daemon's** catalog in remote mode (source-of-truth semantics); local mode reads the local catalog as today.
- `wrkf evidence exec` is the sanctioned client-side pattern and keeps its shape: execute the command locally via os/exec, record the result via `wrkf.evidence.add`.
- **Known limitation to ratify:** hooks that need caller-host workspace state (repo files on max3) cannot run daemon-side. Audit says current catalog hooks are template/DB-scoped; if a workspace-scoped hook class emerges later, it gets the evidence-exec pattern (client executes, records outcome) as a separate, explicit verb — not a silent catalog override.

## 9. D7 — Command migration tranches (slots S1-S3, S5)

- S1: seam only (§3). S2: read commands → adapters. S3: mutation commands → adapters (deferring `--run-checks`/`effect deliver` to S5 per D6). S5: template + hook families per D5/D6.
- Every migrated command: byte-identical local output vs pre-migration (golden tests), plus an `rpc://mini` smoke.
- `wrkf task attach/refresh/inspect/timeline` route to `wrkq.workflow.*` (task-side, D2).
- Guards (S6): importguard on wrkf command paths (no store/db/workflow imports outside sanctioned local-execution carve-outs: evidence exec, watch loop), entrypoint-equivalence extension, golden-parity suite as CI guard.

## 10. D8 — Version skew & coordinated landing

New methods change `protocolSchemaHash`; `rpc.initialize` hard-fails on skew (by design). Doctrine:

1. Server-side method additions land first and are deployed to mini's wrkqd (coordinated landing; operator-gated).
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

## 13. Open questions for consult

- **Q1 (D1):** shared `internal/rpctransport` extraction — package boundary/naming counsel; any layerguard implications.
- **Q2 (D5):** content-bearing template params — size cap value; should the path variant survive for colocated admin use or die at decomm (recommended: die)?
- **Q3 (D6):** daemon-side hook execution as canonical — ratify or counter. This is the one decision with real second-order effects (catalog becomes node config; workspace-scoped hooks need a future pattern).
- **Q4 (D4):** watch as two bounded-poll methods vs extending `wrkf.event.query` — recommended: dedicated methods, since event.query lacks durable-predicate snapshot semantics.
- **Q5 (D3):** `wrkf.supervisor.*` as a new domain under wrkf.* — any objection to the domain name vs folding into `wrkf.run.*`?
