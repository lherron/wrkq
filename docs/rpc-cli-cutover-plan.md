# RPC CLI cutover completion plan

Date: 2026-06-29

This is the remaining plan to cut over from the legacy `wrkq` Cobra
implementation to the RPC-backed CLI implementation in `internal/rpccli`.

Daedalus reviewed this plan on 2026-06-29 and ruled **approve with
conditions**: local RPC cutover should land first, bundle must be removed before
the RPC CLI becomes production `wrkq`, T-05094 must land before the production
RPC boundary is considered stable, T-04317 must land before remote support is
declared production, and `WRKQ_DB` / production `wrkq --db` must be locators
while path-owning admin/daemon surfaces remain path-only.

## Current state

- `cmd/wrkq` imports `internal/rpccli`, so the production `wrkq` entrypoint is
  RPC-backed.
- `cmd/wrkq-legacy` imports `internal/cli` as the temporary direct-store oracle,
  and `cmd/wrkq-rpccli` remains the temporary mirror used by parity tests.
- `just install` installs only shipped binaries and removes stale
  `wrkq-rpccli` installs.
- `internal/rpccli/root.go` has no remaining top-level stubs.
- `TestPrimaryCutoverInventoryGate` fails on top-level stubs, `partial`,
  `not-started`, `rpc-gap`, or `seam-smoke` rows in the migration matrix.
- `TestCoreRuleImportGuard` keeps `internal/rpccli` from importing durable
  behavior through `store`, `wrkqapi`, direct `db`, SQL, search sidecars, or
  `internal/cli`.
- `docs/rpc-cli-migration.md` now reads like a completed implementation matrix,
  not just a gap report, and records bundle as sunset.
- `internal/cli` cannot be deleted wholesale: it also contains `wrkqadm` and
  `wrkqd` HTTP daemon code. The cutover target is `cmd/wrkq`, then legacy
  day-to-day command files inside `internal/cli`.
- Configuration now has `DBLocator`, local `DBPath`, and `RemoteEndpoint`.
  `WRKQ_DB` accepts local paths or `rpc://host[:port]`; path-owning
  compatibility inputs reject `rpc://` before `db.Open`.
- Installed validation passed on 2026-06-29:
  `just verify-full`; installed local smoke for production `wrkq` read/write,
  attachments, handoff sparse-env fallback, stdio RPC, and server status;
  installed loopback remote smoke through `wrkqd`; and installed max3/mini
  remote smoke from `mini` to `max3` with read/write, search/index,
  monitor wait, stdin attachment put/get, handoff create/acknowledge, remote
  `whoami`, max3 event-log verification, and no mini-side SQLite/search files.

## Definition of done

Local RPC CLI cutover is complete when:

1. The installed `wrkq` binary uses `internal/rpccli` for all retained
   day-to-day commands.
2. `wrkq-rpccli` is no longer installed as a production migration artifact.
3. Retained commands obtain durable wrkq behavior through the JSON-RPC boundary.
4. Local-only commands are explicitly local-only: `server`, `agent`, `usage`,
   `version`, `whoami`, `agent-context`, and caller-host file/prompt/output work.
5. Bundle is sunset rather than promoted into the production RPC contract.
6. Legacy direct-store task/container/comment/attachment/search/handoff command
   code is removed or quarantined behind tests that cannot be reached by
   `cmd/wrkq`.
7. Docs, generated contracts, smoke scripts, and installed binaries agree.
8. `just verify-full`, `just install`, and manual installed-binary smokes pass.

Remote production support is complete only after local cutover plus T-04317, and
when a remote `wrkq` installed on `mini` can run against the canonical `max3`
database by setting `WRKQ_DB=rpc://max3[:port]`, with `wrkqd` on `max3` owning
the database and RPC method execution.

## Principles

- Preserve existing `wrkq` semantics for retained commands until the binary swap
  is proven.
- Treat intentional sunsets as explicit product changes, not parity failures.
- Do not preserve obsolete APIs just because the mirror already implemented
  byte parity.
- Keep the JSON-RPC boundary semantic and transport-neutral. `wrkqd` should
  later expose the same method family to remote CLIs; it must not become a
  second semantic API or a sync peer.
- Remote CLIs cannot rely on server-local host paths. File bytes must cross the
  protocol when the caller and canonical DB host differ.
- Treat database configuration as a locator, not always a filesystem path:
  `WRKQ_DB_PATH` stays a path-only compatibility alias, while new `WRKQ_DB`
  accepts either a local SQLite path or an `rpc://` endpoint.
- Auth material must not be embedded in `rpc://` locators. Use token/file config
  separately.
- Remote experiments behind an explicit unstable/dev flag may happen early, but
  they must not be documented or treated as the durable production contract
  before T-04317 lands.

Daedalus proposed durable invariant `wrkq.rpc.remote-transport-locator`; it is
not active architecture law until implementation and e2e validation land. The
plan must keep these active records unchanged:

- `wrkq.project-root.caller-semantics`
- `wrkq.mutation.caller-owned-confirmation`
- `wrkq.wrkf-rpc.search-index-server-owned`
- `wrkq.wrkf-rpc.attachment-byte-transfer`
- `wrkq.wrkf-rpc.bounded-polling-streaming`
- `wrkq.attribution.caller-principal-exact`

Proposed invariant:

> A day-to-day `wrkq` command may select either a local SQLite database or a
> remote `wrkqd` workrpc endpoint through a locator (`WRKQ_DB` / production
> `wrkq --db`), but every path-owning admin/daemon/config surface remains
> path-only. Remote mode is only a transport to the same workrpc
> method/DTO/error contract: the canonical host owns durable state, migrations,
> search/index, attachments, attribution, and read-model computation; the caller
> owns CLI scoping, prompts, local file/stdin/stdout handling, presentation, and
> bounded polling. Parent process path hints or server-local file paths are never
> a correctness path across a remote boundary.

## Phase 0: update the plan of record

1. Update `docs/rpc-cli-migration.md` from "migration matrix" to "final retained
   command coverage":
   - remove the stale "Open gaps" section that says `cp` still awaits RPC;
   - mark bundle as "sunset" instead of `rpc-backed`;
   - keep command evidence markers for retained commands.
2. Update or add a cutover gate that distinguishes:
   - retained commands must be `rpc-backed` or `local-only`;
   - sunset commands must be absent from production help and docs;
   - no command may remain `partial`, `rpc-gap`, `not-started`, or `seam-smoke`.
3. Record the bundle decision on T-04371 before implementation: this task should
   expand from "delete bundle apply" to "sunset bundle create/apply and its RPC
   read model unless a current consumer is proven."

## Phase 1: remove bundle instead of migrating it

Bundle should not be part of the post-cutover contract.

Delete or disable these surfaces deliberately:

- `wrkq bundle create`
- `wrkqadm bundle apply`
- `wrkqd` `/v1/bundle/create` and `/v1/bundle/apply`
- JSON-RPC `wrkq.bundle.exportView`
- `WrkqBundleExportView` / `WrkqBundleTaskDoc` DTO catalog entries and
  fingerprints
- bundle parity tests and transport-equivalence rows
- bundle sections in `docs/SPEC.md`, `docs/wrkq-wrkf-rpc.md`,
  `docs/wrkq-wrkf-rpc-client-forward-spec.md`, generated HTML contracts, and
  smoke scripts

If the repository still needs archival state movement, route it through the
existing admin surfaces that are meant to survive: `state export/import/verify`,
patches, merge, or future explicit migration tooling. Do not carry forward the
Git-ops bundle workflow as a remote CLI feature.

Validation for this phase:

- `rg "bundle.?apply|handleBundleApply|/v1/bundle/apply"` is clean except
  historical notes that intentionally mention the removal.
- `rg "wrkq.bundle.exportView|WrkqBundleExportView|bundle create"` is clean from
  production docs, method catalogs, production command registration, and smoke
  scripts.
- `just verify-full` passes.

## Phase 2: clear cutover blockers from the live queue

Recommended order:

1. T-05094: harden `protocolSchemaHash` so DTO field/tag shape perturbs the
   hash, not only catalog names. This must land before treating RPC as a stable
   production boundary.
2. T-04385: fix `wrkqadm init` so smoke runs stop dirtying repo `.gitignore`.
   This should land before the heavier validation loop.
3. T-01853: fix `handoff acknowledge` scope fallback before swapping the
   installed `wrkq` binary. The current legacy and mirror paths still fail early
   when runtime env is sparse; the task asks for exact handoff ID fallback from
   the row when unambiguous.

T-04317 is larger than the binary swap, but it matters because the current RPC
surface still advertises `wrkq.admin.legacyActor.*`, accepts compat actor slugs
in some places, and returns actor fields in compatibility projections. That was
acceptable for byte parity and local cutover; it must not be frozen as the
remote production contract. Do not block local RPC cutover on T-04317. Do block
remote production support on it.

## Phase 3: swap the installed `wrkq` entrypoint

1. Change `cmd/wrkq/main.go` to execute `internal/rpccli`. **Done.**
2. Make `internal/rpccli` configurable for the production command name:
   - `wrkq` help should say `wrkq`, not `wrkq-rpccli`;
   - `cmd/wrkq-rpccli` can pass `wrkq-rpccli` while it still exists for test
     comparison.
3. Keep `wrkq rpc --stdio` on the same bootstrap/registry path as the mirror.
4. Update `Justfile`: **Done for install/build split.**
   - build production `bin/wrkq` from the RPC-backed command;
   - stop installing `wrkq-rpccli` by default;
   - keep a test-only or developer-only build target for the old-vs-new oracle
     only until the post-cutover cleanup removes it.
5. Add a structural guard:
   - `cmd/wrkq` must not import `internal/cli`;
   - retained `internal/rpccli` commands must not import forbidden durable
     packages;
   - production help must not include bundle after the sunset phase.
6. Update `docs/SPEC.md` and embedded usage docs so the binary list and command
   list match the post-cutover product.
7. Treat the new production `wrkq --db` as a locator flag, not a path-only flag:
   it accepts local paths and `rpc://host[:port]`. Path-owning admin/daemon
   flags stay separate and path-only.

## Phase 4: validation before declaring local cutover complete

Run before the swap while both binaries exist:

- `go test -tags sqlite_fts5 ./internal/rpccli`
- the old-vs-new parity harness, including PTY cases
- transport-equivalence tests for in-process vs subprocess stdio
- search/index transport tests
- protocol init tests proving version, schema hash, capability/method presence,
  and migration compatibility are validated before command execution

Run after the swap:

- `just verify`
- `just verify-full`
- `just install`

Manual installed-binary smoke must use the real installed `wrkq` on PATH, not
`go run` and not only unit tests. Minimum smoke:

- `wrkq version --json`
- `wrkq whoami --json`
- `wrkq projects --json`
- `wrkq ls --json --limit 3`
- `wrkq cat <existing task> --json`
- create a temp task with `wrkq touch`, update it with `wrkq set`, add/comment,
  attach put/get/rm with a temp file, then archive it with `wrkq rm --yes`
- `wrkq handoff list --scope cody@wrkq --json --limit 1`
- exact-ID `wrkq handoff acknowledge` fallback test from a sparse env on a temp
  handoff fixture
- `wrkq rpc --stdio` initialize/show smoke
- `wrkq server status --json`

Record the exact installed commands and outputs on the cutover task before
closing it.

## Phase 5: remove legacy day-to-day CLI code

After installed `wrkq` is RPC-backed and smoke-tested:

1. Delete or quarantine legacy `internal/cli` day-to-day command files:
   task/container/comment/attachment/relation/search/index/handoff/watch/monitor
   command handlers that are no longer called by `cmd/wrkq`.
2. Keep or move the `wrkqadm` and `wrkqd` pieces:
   - either leave `internal/cli` as admin/daemon-only;
   - or split to `internal/admincli` and `internal/wrkqdhttp` in a follow-up.
3. Convert old-vs-new parity tests into RPC-backed command contract tests:
   - retained commands get golden/semantic tests against `cmd/wrkq`;
   - transport tests stay in `internal/rpccli`;
   - no test should require production legacy command handlers to compile.
4. Remove `cmd/wrkq-rpccli` after the oracle is retired.
5. Remove build/install references to `wrkq-rpccli`.
6. Update generated docs and `docs/html/*` artifacts that list old commands,
   bundle routes, actor admin routes, or the mirror binary.

## Phase 6: remote transport readiness

Do this after local cutover, not before it. The concrete target is:

```bash
ssh lherron@mini
WRKQ_DB=rpc://max3:7171 wrkq ls --json
```

`max3` remains the canonical database host. `mini` runs only the CLI process and
connects back to `max3` through `wrkqd`.

1. Introduce a database locator config model:
   - `WRKQ_DB` is the primary locator env var;
   - `WRKQ_DB=/path/to/wrkq.db` selects local SQLite/in-process stdio behavior;
   - `WRKQ_DB=rpc://max3` or `WRKQ_DB=rpc://max3:7171` selects remote `wrkqd`;
   - `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` remain local-path compatibility inputs
     and must reject `rpc://` values with a clear validation error;
   - `--db` should accept the same locator grammar as `WRKQ_DB`, or a new
     `--db-locator` / `--endpoint` flag must be added and documented if keeping
     `--db` path-only is preferred.
   - **Implemented locally:** production `wrkq --db` and `WRKQ_DB` accept local
     paths or `rpc://host[:port]`; `WRKQ_DB_PATH`/`WRKQ_DB_PATH_FILE` and
     path-owning daemon/admin flags reject endpoints.
2. Split config fields so code cannot accidentally pass an endpoint to
   `db.Open`:
   - `DBLocator` or equivalent stores the raw locator;
   - `DBPath` is populated only for local SQLite mode;
   - `RemoteEndpoint` is populated only for `rpc://` mode;
   - `whoami`, `agent-context`, `doctor`, and `config doctor` report the selected
     mode explicitly.
   - **Partially implemented:** `whoami` and `agent-context` report remote mode;
     admin-only doctor/config surfaces remain local-path oriented.
3. Add a `Transport` implementation that targets `wrkqd` while preserving the
   same method names and DTO semantics as stdio JSON-RPC.
   - `rpc://max3[:port]` is the user-facing scheme for the wrkq semantic RPC
     endpoint, regardless of whether `wrkqd` internally serves HTTP today.
   - The client should perform `rpc.initialize` and verify protocol version,
     schema hash, required methods/capabilities, migration compatibility, and
     authentication before command execution.
   - The transport should share the existing retry/error mapping behavior; remote
     network failures must not be confused with `WRKQ_DB_BUSY`.
   - **Implemented locally:** `internal/rpccli` has an HTTP workrpc transport
     selected by `WRKQ_DB=rpc://...`, with the same initialize/method checks and
     domain-error mapping as stdio.
4. Update `wrkqd` so it can expose the workrpc registry as the remote semantic
   RPC surface, not a parallel REST-only surface:
   - `wrkqd` opens the canonical max3 SQLite database;
   - the same registered method catalog used by `wrkq rpc --stdio` is reachable
     through the daemon transport;
   - auth is checked before workrpc business dispatch;
   - non-loopback binding without a token fails by default, unless an explicit
     unsafe/dev flag is passed;
   - legacy daemon routes can remain temporarily for existing clients, but they
     are not the future CLI contract.
   - **Implemented locally:** `wrkqd` exposes `/v1/rpc` through the same
     workrpc registry and existing token auth. Non-loopback TCP bind without a
     token is rejected unless `--unsafe-no-token` is passed.
5. Keep server ownership on the canonical host for:
   - etags and CAS;
   - attribution and scope validation;
   - audit/event writes;
   - search/index sidecar ownership;
   - monitor/watch event filtering and pagination.
6. Preserve caller-owned project-root scoping:
   - `WRKQ_PROJECT_ROOT`, `ASP_PROJECT`, and `--project` interpretation stays in
     the caller-side CLI layer;
   - remote mode cannot open the max3 DB directly for scope lookups, so add
     transport-backed scope lookup/preflight reads where needed;
   - do not move caller-context interpretation into server business methods.
7. Make file-moving commands capability-aware:
   - local stdio may keep the host-path fast path for `attach put <file>`;
   - remote transport must read the caller-local file and use chunked
     `wrkq.attachment.addBytes`;
   - `attach get` already uses protocol bytes and is the model to preserve.
8. Keep `server` local-only by default. From `mini`, `wrkq server status` should
   report the local client machine state unless an explicit remote-status command
   is added. A remote CLI should not start/stop the canonical `max3` daemon
   unless that becomes an explicit authenticated admin surface.
9. Re-audit every remaining server-local host path hint (`artifact_dir`,
   attachment relative paths, search sidecar paths) and document them as hints,
   not remote filesystem guarantees.
10. Add max3/mini validation:
   - start `wrkqd` on `max3` bound to an address reachable from `mini`, not only
     `127.0.0.1`;
   - configure token/auth material without storing secrets in the repo;
   - from `ssh lherron@mini`, run read, write, search, watch/monitor, attach
     put/get, and handoff smoke commands with `WRKQ_DB=rpc://max3:7171`;
   - verify the writes appear in the canonical max3 database and event log;
   - verify no SQLite file or search sidecar is created on `mini` for remote
     commands.
   - **Validated 2026-06-29:** installed mini `wrkq` ran against max3 `wrkqd`
     at `rpc://192.168.50.35:19177` using token auth. The smoke covered
     read/write, search/index, monitor wait, stdin attachment put/get, handoff
     create/acknowledge including sparse-env fallback, remote `whoami`, max3
     event-log verification, and no SQLite/search files in mini's temp cwd.

Remote locator/transport implementation and installed e2e validation have
landed. Remote production declaration is still gated on T-04317 and any durable
architecture-law update that promotes `wrkq.rpc.remote-transport-locator` from
proposal to active invariant.

## Phase 7: required remote transport tests

Before remote support is declared stable, add tests for:

1. Locator parsing:
   - `WRKQ_DB` and production `wrkq --db` accept local paths and
     `rpc://host[:port]`;
   - `WRKQ_DB_PATH`, `WRKQ_DB_PATH_FILE`, `wrkqadm --db`, `wrkqd --db`, and
     `wrkq server --db-path` reject endpoint URLs before any `db.Open`.
2. Protocol initialization:
   - DTO field/tag shape changes perturb the schema hash;
   - client init rejects protocol-version mismatch, schema-hash mismatch,
     missing capability/method, and migration incompatibility.
3. Auth:
   - unauthenticated remote calls are rejected before workrpc business dispatch;
   - token/file auth succeeds;
   - non-loopback `wrkqd` startup without token fails unless an explicit
     unsafe/dev flag is passed.
4. Transport equivalence:
   - in-process, stdio subprocess, and `wrkqd` remote return equivalent DTOs and
     domain errors for representative read, write, search, attachment,
     monitor/watch, and handoff commands.
5. Server ownership:
   - remote search/index operations update/use only max3 server-side state;
   - mini does not open the SQLite DB, search DB, embedder, or attachment store
     directly.
6. File transfer:
   - remote `attach put <local-file>` and `attach get` use explicit chunks,
     checksum, and size validation;
   - the server never treats the caller path as openable on max3.
7. Monitor/watch:
   - remote follow/watch uses bounded polling;
   - timeout, stall, and network failures are transport failures, distinct from
     `WRKQ_DB_BUSY` and ordinary domain errors;
   - terminal NDJSON behavior remains stable.
8. Server command behavior:
   - with `WRKQ_DB=rpc://max3`, `wrkq server` commands remain local-only and do
     not implicitly control the max3 daemon.
9. Installed max3/mini e2e:
   - after `just install`, run from `ssh lherron@mini` with
     `WRKQ_DB=rpc://max3:PORT`;
   - cover `ls`, `cat`, `touch`, `set`, `search`, `index`, `attach`,
     `monitor/watch`, and `handoff`;
   - verify writes land in the max3 canonical DB and event log.

## Live wrkq task disposition

Local cutover blockers:

- T-04371 `bundle-commands-cleanup`: expand to full bundle sunset, not migration.
- T-01853 `handoff-ack-scope-fallback`: fix before local binary swap.
- T-04385 `wrkqadm-init-gitignore-wart`: fix before repeated smoke runs.
- T-05094 `protocol-schema-hash-shape`: finish before production RPC boundary is
  declared stable.

Pre-remote-production work:

- T-04317 `remove-actors-table-principal-only`: not a local cutover blocker, but
  mandatory before remote transport is declared production-ready.

Cutover-adjacent but not blockers:

- T-05264 `dto-contract-docs`: update after the final RPC/public DTO contract is
  settled and bundle/actor decisions land.
- T-04229 `caused-by-task-field`: implement after cutover through the RPC/API
  path only; avoid double-implementing it in both legacy and rpccli unless it
  becomes urgent before the binary swap.
- T-01869 `consistent-task-handoff-cli`: keep as draft guidance; do not make it
  a broad cosmetic blocker. Use it only to justify additive aliases/output-mode
  consistency that does not weaken handoff scope/idempotency semantics.

Not part of the RPC CLI cutover:

- T-05067 `orphaned-wrkf-action-reaper`: workflow runtime reliability.
- T-04891 `wrkq-smoketest-trigger-design`: product/event-hook design.
- T-01923 `design-obligation-audit`: authority decision for wrkf obligation
  audit/role/idempotency.
- T-04962 `smithers-wrkf-comparison-info`: already draft/reference only; do not
  execute.

## Recommended sequence

1. Update T-04371 and docs to record bundle sunset.
2. Remove bundle production surfaces.
3. Land T-05094.
4. Fix T-04385.
5. Fix T-01853.
6. Run pre-swap parity and transport tests.
7. Swap `cmd/wrkq` to `internal/rpccli`.
8. Stop installing `wrkq-rpccli`.
9. Run `just verify-full`, `just install`, and installed smokes.
10. Remove/quarantine legacy day-to-day `internal/cli` code and retire
    old-vs-new oracle tests.
11. Finish T-04317 before remote `wrkqd` transport is called production.
12. Add `WRKQ_DB=rpc://max3[:port]` locator support and the `wrkqd` remote
    workrpc transport.
13. Validate the remote CLI from `ssh lherron@mini` against max3.
14. Refresh DTO docs/contracts (T-05264).
15. Resume feature work such as caused-by (T-04229) on the RPC-backed surface.

As of 2026-06-29, items 1-10, 12, and 13 are implemented and validated. Item
11 remains the remote-production blocker; item 14 is follow-up documentation
refresh after the final public contract is accepted.
