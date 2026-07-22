# RPC-backed wrkq CLI Migration Proposal

> Historical proposal. The production cutover is complete; the direct-store
> command package was removed by T-06762. Current code lives in
> `internal/rpccli`, `internal/admincli`, and `internal/wrkqd`.

## Goal

Gradually migrate `wrkq` CLI command implementations to use the existing JSON-RPC interface internally, while preserving the current CLI behavior until parity is proven.

The preferred migration shape is a new Cobra implementation that mirrors the current subcommand list. The new implementation can be compared against the current CLI before any cutover.

The longer-term goal is to make JSON-RPC the semantic boundary for durable `wrkq` domain behavior, then expose that same boundary through `wrkqd` so CLIs on other machines can operate against the canonical max3-local database without each machine opening or syncing its own task database.

This document intentionally does not design the future HTTP transport. The HTTP endpoint should be treated as a later transport adapter over the same RPC/domain boundary, not as a separate API design effort during the CLI migration.

## Current State

`wrkq rpc --stdio` already serves the unified JSON-RPC interface through `internal/workrpc`.

Normal `wrkq` CLI commands do not currently call JSON-RPC internally. They bootstrap config and SQLite directly, then use store/API/query helpers in-process.

The current RPC catalog covers a useful subset:

- Task create/show/list/update/move/acknowledge/delete/restore
- Comment add/list/show/delete
- Attachment add/list/show/remove
- Relation add/list/remove
- Container create/delete/show/list
- Workflow attach/inspect/timeline/refresh
- The `wrkf.*` workflow/run/effect/action/check/evidence surfaces

Several CLI areas are not yet RPC-covered and should be tracked explicitly instead of bypassing the migration boundary.

`wrkqd` already exists as a daemon wrapper over the same configured database. The product model treats it as a service wrapper, not a separate source of truth. That matches the desired future state: max3 can remain the authoritative local host for the canonical SQLite database while other machines use a daemon transport to request semantic operations.

## Planned Future State

The intended end state has three layers:

1. `internal/workrpc` owns the stable semantic contract for durable `wrkq` behavior.
2. `wrkq` Cobra commands become terminal UX adapters over a Go JSON-RPC client seam.
3. `wrkqd` later exposes the same semantic boundary to remote CLIs that should use the max3 canonical database.

In that model, local and remote CLIs should differ mainly by transport selection:

- Local development can use in-process or stdio JSON-RPC for fast parity tests.
- Installed local CLI usage can use stdio or daemon transport depending on configuration.
- Remote CLI usage can target `wrkqd` on max3 once the daemon transport is added.

The important architectural property is that command behavior lives behind the same method names, DTOs, error codes, optimistic concurrency checks, actor attribution rules, and audit/event writes regardless of transport.

The future remote CLI should not be a sync client. It should issue operations to the max3-owning service and render the returned DTOs locally. Search indexes, attachment storage, event streams, and audit history should have a single authoritative owner unless a cache is explicitly designed and marked as rebuildable.

## Assessment

This approach is sound, provided the migration is framed as moving durable domain behavior behind JSON-RPC rather than forcing every CLI concern into RPC.

The strong parts of the approach:

- It preserves the current CLI while creating a parity harness before cutover.
- It creates one reusable contract for local CLI, future remote CLI, MCP/client surfaces, and daemon transports.
- It keeps `wrkqd` aligned with the existing product model as a service wrapper over the canonical database.
- It avoids treating HTTP as a new source of domain semantics.

The main risk is over-migrating local/process behavior. Some commands should remain local client behavior or move to a separate control-plane surface later, not become `wrkq` data RPC methods.

Recommended command categories:

- `domain-rpc`: task, container, comment, attachment, relation, handoff, event/history, search, and workflow-linked operations that read or mutate canonical `wrkq` state.
- `client-local`: help, usage, shell completion, pager/editor integration, local config inspection, local output rendering, and transport selection.
- `daemon-control`: `server start/stop/restart/status/health` and launchd/process management. These control the local daemon and should not be mixed with data RPC.
- `admin-local-or-explicit-admin`: destructive DB lifecycle, migrations, snapshots, repair, import/export, and privileged actor/admin operations. These may need a separate admin RPC story, but should not be smuggled into day-to-day `wrkq` CLI parity.

For the future max3-backed remote CLI, the boundary must preserve:

- Optimistic concurrency and `etag` behavior at the service boundary.
- Actor/principal attribution for every mutation.
- Stable JSON-RPC error codes mapped back to current CLI exit codes.
- Audit/event writes from the canonical service path, not from remote clients.
- Attachment semantics that do not rely on a server seeing a client-local filesystem path.
- Search/index semantics owned by the canonical host, unless a cache is explicitly introduced.
- Streaming/watch semantics as first-class protocol behavior rather than polling hidden inside CLI code.

The practical conclusion: build the mirror CLI and fill RPC gaps first. Once the CLI is genuinely RPC-backed, exposing a daemon transport becomes a transport problem instead of a second CLI/database migration.

## Proposed Architecture

Add a parallel implementation rather than modifying the current command tree in place.

Suggested layout:

- `internal/rpccli/`: new Cobra root and command tree
- `cmd/wrkq-rpccli/`: temporary comparison binary
- `internal/workrpcclient/` or `internal/rpccli/client/`: small Go JSON-RPC client seam
- `docs/rpc-cli-migration.md`: command-by-command coverage matrix
- `test/compare-rpccli.sh`: parity harness

The existing `cmd/wrkq` remains untouched until parity is good enough to cut over.

## Compatibility Invariant

The existing `wrkq` CLI must not change semantically while the mirror RPC-backed Cobra surface is being created.

During the mirror phase:

- Do not change current command behavior to make parity easier.
- Do not change output defaults, exit codes, flag meanings, prompts, dry-run behavior, or mutation semantics in the production CLI unless that change is independently required and approved outside this migration.
- Treat current CLI behavior as the oracle for comparison, even when it exposes quirks or inconsistent presentation.
- If the RPC-backed mirror reveals a bug or inconsistency in the existing CLI, record it separately and keep the mirror comparison explicit about whether the target is current behavior or a separately approved semantic fix.

## Core Rule

The RPC-backed CLI must not call `store`, `wrkqapi`, direct SQL,
`internal/admincli`, or `internal/wrkqd` for business behavior.

If a command needs behavior that is not exposed through JSON-RPC, mark it as an RPC gap and add the missing RPC method deliberately. Do not create a local shortcut inside `rpccli`.

Small shared concerns such as output formatting, flag parsing helpers, and exit-code mapping can be extracted into neutral packages when needed.

## Transport

Use two transports:

1. In-process transport for fast tests and command execution during development.
   - Build the same `workrpc.Server` used by `wrkq rpc --stdio`.
   - Call through a JSON-RPC client seam.
   - This keeps the CLI implementation honest without process-spawning overhead.

2. Subprocess stdio transport for final validation.
   - Spawn the real `wrkq rpc --stdio`.
   - Proves the public RPC entrypoint and the new CLI agree.

The CLI should depend on a transport interface, not on the server implementation directly.

## Command Tree Mirroring

First milestone: mirror the current command surface before implementing behavior.

The mirror should cover:

- Command paths
- Aliases
- Args shape
- Flags
- Flag defaults
- Hidden/deprecated state
- Output mode flags
- Help text where practical

Add a test that walks both Cobra trees and fails on drift. This gives a standing guardrail while implementation migrates incrementally.

## Coverage Matrix

Create a coverage matrix with one row per current command path.

Recommended statuses:

- `rpc-backed`: implemented through JSON-RPC
- `rpc-gap`: should be RPC-backed, but the RPC method/DTO is missing
- `local-only`: should remain local because it controls local process/runtime behavior
- `passthrough`: temporary compatibility bridge, with an explicit removal condition
- `not-started`: mirrored but not implemented yet

Likely first `rpc-backed` candidates:

- `touch`
- `cat`
- `set`
- `mv`
- `ack`
- `rm`
- `restore`
- `comment add`
- `comment ls`
- `comment cat`
- `comment rm`
- `attach ls`
- `attach put`
- `attach get`
- `attach rm`
- `relation add`
- `relation ls`
- `relation rm`
- `container cat`
- `container set` after RPC coverage is verified

Likely initial gaps or local-only areas:

- `search`
- `index`
- `monitor`
- `server`
- `handoff`
- `webhook`
- `bundle`
- `cp`
- `diff`
- `log`
- `watch`
- `agent`
- `usage`
- `version`
- most `wrkqadm` admin surfaces

## Byte-compare Harness

Add `test/compare-rpccli.sh`.

For read-only commands:

1. Seed a fixture database.
2. Run old `wrkq`.
3. Run new `wrkq-rpccli`.
4. Compare exit code, stdout, and stderr byte-for-byte.

For mutating commands:

1. Seed a fixture database.
2. Copy it to `old.db` and `new.db`.
3. Run old `wrkq` against `old.db`.
4. Run new `wrkq-rpccli` against `new.db`.
5. Compare exit code, stdout, stderr, and canonical DB snapshots.

Strict byte comparison for mutating command output will need either a deterministic test clock or a small, explicit normalizer. Current code uses SQLite `now`, `CURRENT_TIMESTAMP`, and some `time.Now()` paths, so timestamps will otherwise make byte-level comparisons flaky.

## Migration Phases

### Phase 1: Shell and Harness

- Add `cmd/wrkq-rpccli`.
- Add `internal/rpccli` root command.
- Mirror command tree and flags.
- Add Cobra-tree parity test.
- Add initial comparison harness.

Exit criterion: the new binary exposes the same command surface, even if most commands return `not implemented`.

### Phase 2: Read-only Task and Container Commands

- Implement task/container detail and list commands where RPC DTOs already exist.
- Focus on output parity before mutations.

Exit criterion: selected read-only commands pass byte comparison against fixtures.

### Phase 3: Simple Task Mutations

- Implement create/update/move/ack/delete/restore through RPC.
- Add DB snapshot comparison.
- Decide whether to add deterministic clock support before requiring strict stdout parity.

Exit criterion: core task lifecycle commands match old behavior on fixtures and smoke tests.

### Phase 4: Comments, Attachments, Relations

- Implement the remaining resource CRUD surfaces already covered by RPC.
- Add attachment filesystem fixture cases.

Exit criterion: CRUD resource commands pass parity tests.

### Phase 5: Fill RPC Gaps

- For commands that should be RPC-backed but lack methods, add RPC methods and DTOs first.
- Then implement the mirrored CLI command through the same client seam.

Exit criterion: no command is marked `rpc-gap` without an issue/task or explicit decision.

### Phase 6: Cutover

- Run `just verify`, RPC client tests, and comparison harness.
- Install and smoke the new path.
- Replace `cmd/wrkq` command registration with the RPC-backed implementation only after the comparison harness is green for the chosen cutover surface.

## Non-goals

- Do not migrate process-control commands just to satisfy purity.
- Do not replace the public JSON-RPC protocol with a CLI-private API.
- Do not route through the TypeScript `@wrkq/client` from the Go CLI.
- Do not cut over command-by-command in the production binary until the mirror harness proves parity.
- Do not design the future HTTP endpoint as part of the CLI mirror work.
- Do not let remote CLI support turn into database synchronization between machines.
- Do not expose a second set of daemon-only semantics that can drift from JSON-RPC.

## Risks

- Output parity may expose current CLI inconsistencies that RPC DTOs do not model.
- Some current commands combine multiple store operations and presentation-specific SQL.
- Mutating command byte comparison will be noisy without deterministic timestamps.
- Help text and flag defaults can drift unless the Cobra mirror test is strict.

## Recommended First Implementation Task

Create the skeleton only:

1. Add `cmd/wrkq-rpccli`.
2. Add `internal/rpccli/root.go`.
3. Mirror all current command paths and flags.
4. Add a command-tree parity test.
5. Add `docs/rpc-cli-migration.md` with initial coverage statuses.

This gives a safe reviewable base before migrating any command behavior.
