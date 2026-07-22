# wrkq CLI to RPC Gap Analysis

> Historical pre-cutover audit. The direct-store `internal/cli` surface it
> compared was removed by T-06762; production commands live in `internal/rpccli`.

Date: 2026-06-22  
Scope at audit time: the former direct-store `wrkq` command surface, compared
against the unified JSON-RPC registry in `internal/workrpc`.

This audit is for the proposed RPC-backed `wrkq` CLI mirror. It does not treat `wrkqadm` as part of the cutover target, and it does not re-audit the `wrkf` binary except where `wrkf.*` RPC methods explain existing coverage.

## Source Basis

- Current production `wrkq` command surface: `internal/rpccli/*.go`
- RPC method registry: `internal/workrpc/registry.go`
- `wrkq.*` RPC DTOs and params: `internal/wrkqapi/types.go`
- Authoritative RPC contract: `docs/wrkq-wrkf-rpc.md`
- Prior migration proposal: `rpcwrkqcli.md`

The existing RPC protocol is JSON-RPC 2.0 over NDJSON and is served by both:

```text
wrkq rpc --stdio
wrkf rpc --stdio
```

## Executive Summary

The RPC interface covers the core resource model, but it is not yet a complete implementation substrate for the current `wrkq` CLI.

Covered well enough for a first RPC-backed CLI pass:

- Basic task create/show/list/update/move/acknowledge/delete/restore
- Basic comment add/list/show/soft-delete
- Basic attachment add/list/show/remove
- Basic relation add/list/remove
- Basic container create/show/list/delete/deleteRecursive
- Legacy actor list/create/update
- Workflow attach/inspect/timeline/refresh

Missing or materially incomplete for CLI parity:

- Search and index operations
- Event log, watch, monitor, and history surfaces
- Handoff operations
- Webhook operations
- Rich `cat`, `ls`, `tree`, `find`, `stat`, and `projects` projections
- Patch/apply/diff/copy/bundle operations
- Container rename/move/update-webhooks
- Task update fields beyond the current `TaskPatch`
- Exact CLI output contracts, dry-run behavior, bulk behavior, stdin modes, and confirmation semantics

The highest-risk gap is not one missing method. It is the mismatch between current CLI presentation/operation commands and the narrower RPC resource DTOs. A mirror CLI can start with RPC calls, but it needs additional RPC methods or projection DTOs before byte-for-byte parity is realistic.

## Existing RPC Method Catalog

### Protocol

```text
rpc.initialize
rpc.shutdown
rpc.exit
$/cancelRequest
```

### wrkq Namespace

```text
wrkq.task.create
wrkq.task.show
wrkq.task.list
wrkq.task.update
wrkq.task.move
wrkq.task.acknowledge
wrkq.task.delete
wrkq.task.restore

wrkq.comment.add
wrkq.comment.list
wrkq.comment.show
wrkq.comment.delete

wrkq.attachment.add
wrkq.attachment.list
wrkq.attachment.show
wrkq.attachment.remove

wrkq.relation.add
wrkq.relation.list
wrkq.relation.remove

wrkq.admin.legacyActor.list
wrkq.admin.legacyActor.create
wrkq.admin.legacyActor.update

wrkq.container.create
wrkq.container.delete
wrkq.container.deleteRecursive
wrkq.container.show
wrkq.container.list

wrkq.workflow.attach
wrkq.workflow.inspect
wrkq.workflow.timeline
wrkq.workflow.refresh
```

### wrkf Namespace

The registry also exposes `wrkf.workflow.*`, `wrkf.instance.*`, `wrkf.evidence.*`, `wrkf.role.*`, `wrkf.obligation.*`, `wrkf.check.*`, `wrkf.hook.*`, `wrkf.transition.apply`, `wrkf.run.*`, `wrkf.action.*`, and `wrkf.effect.*`. These are useful for the `wrkf` binary and workflow-related `wrkq` integrations, but they do not fill most `wrkq` CLI projection gaps.

## Command-by-command Gap Matrix

| CLI command | Existing RPC coverage | Gap classification | Notes |
|---|---:|---|---|
| `touch` | `wrkq.task.create` | Partial | Missing `--force-uuid`, requested/assigned project, resolution, and exact multi-create output/artifact_dir behavior. RPC create has parent/assignee/labels/meta/specification coverage. |
| `cat` / `show` | `wrkq.task.show`, plus comments/relations | Partial | CLI emits a richer task document: artifact_dir, project_id, parent task IDs, assignee display, requested/assigned project, resolution, CP/session/run fields, blockers, comments, relations, raw markdown/front matter. RPC task DTO is narrower and camelCase. |
| `set` / `edit` | `wrkq.task.update` | Partial | RPC `TaskPatch` lacks slug, parent_task/parent_id, requested_by, assigned_project, resolution, cp_project_id, cp_work_item_id, cp_run_id, session_id/cp_session_id, and run_status. Bulk flags, stdin task refs, dry-run, ordered jobs, and continue-on-error are CLI behavior not modeled by RPC. |
| `ack` | `wrkq.task.acknowledge` | Mostly covered | Core state rule and force flag are present. CLI returns aggregate counts across many task refs; RPC operates one task at a time and returns task DTOs. |
| `restore` | `wrkq.task.restore` | Partial | RPC restores task state only. CLI also restores containers, supports `--to`, title, description, priority, labels, assignee, CAS, and restore comment. |
| `rm` | `wrkq.task.delete`, `wrkq.container.delete`, `wrkq.container.deleteRecursive` | Partial / semantic mismatch | CLI default archives; RPC `task.delete` sets `state=deleted` and `deleted_at`. CLI supports purge, recursive, force, yes, dry-run, nullglob, jobs, continue-on-error, NDJSON/porcelain. |
| `mv` | `wrkq.task.move` | Partial | RPC moves a task to an existing target container. CLI also handles rename, multi-source move, container move/rename, dry-run, nullglob, overwrite-task, and type forcing. |
| `cp` | None | Missing | Copy semantics, recursive copy, attachment copy, overwrite/nullglob/bulk/dry-run all need new RPC surface or should stay local. |
| `apply` | `wrkq.task.update` can receive parsed fields | Partial | CLI parses markdown/YAML/JSON, handles metadata gating, dry-run, size warnings, and file/stdin input. RPC has no apply/parse method; a client could parse locally but byte parity would need shared parser/output behavior. |
| `diff` | None | Missing | CLI compares selected fields between tasks and renders human/JSON diff. Could be a client-side projection over `task.show`, but there is no RPC diff method. |
| `log` | None | Missing | No `wrkq.event.log` or history RPC for event_log by resource, since/until, cursor, oneline, patch, JSON/NDJSON. |
| `watch` | None | Missing | Deprecated in favor of `monitor watch --raw`, but still a CLI surface. No streaming/event-log RPC. |
| `monitor watch` | None | Missing | Typed event stream, terminal line contract, raw mode, state-only mode, until/timeout/stall semantics are not exposed by RPC. |
| `monitor wait` | None | Missing | Blocking condition evaluator is not exposed by RPC. |
| `find` | `wrkq.task.list`, `wrkq.container.list` | Partial | RPC lacks mixed task/container result projection, slug glob, due-before/after, parent-task, requested-by, assigned-project, ack-pending, path glob behavior, and current snake_case result shape. |
| `search` | None | Missing | Local search sidecar, dense/FTS ranking, freshness checks, candidate-limit, explain, and snippet rendering have no RPC methods. |
| `index status/rebuild/update/vacuum/pause/resume` | None | Missing / likely local-only | These operate on the local search sidecar and background indexing state. They may be intentionally local rather than wrkq JSON-RPC. |
| `ls` / `list` | `wrkq.task.list`, `wrkq.container.list` | Partial | CLI returns a combined child listing with containers and tasks, task_count/active_task_count rollups, recursive flag, one/nul/porcelain output, hidden handling, and sort-by-slug behavior. RPC has separate task/container list DTOs. |
| `tree` | None directly | Missing | Hierarchical tree, collapse/all-done behavior, depth, open-only, fields, raw/JSON/NDJSON output are not represented by RPC. |
| `projects` | `wrkq.container.list` | Partial | RPC can list top-level containers only if semantics line up; CLI intentionally ignores project-root scoping and has one/nul/porcelain output plus archived filtering. |
| `stat` | `wrkq.task.show`, `wrkq.container.show` | Partial | CLI returns a compact metadata union for task/container. RPC returns full resource DTOs and does not expose the same result shape. |
| `mkdir` | `wrkq.container.create` | Partial | RPC create can create one container, but CLI supports `--parents`, top-level project/directory kind rules, multiple paths, and current output shape. |
| `rmdir` | `wrkq.container.delete`, `wrkq.container.deleteRecursive` | Partial | RPC covers empty and recursive delete primitives. CLI confirmation, `--force`, `--yes`, multi-path output, and exact behavior still need adapter work. |
| `rename-container` | None exact | Missing | RPC has no container update/rename/move method. |
| `container cat` | `wrkq.container.show` | Partial | RPC gives base container DTO. CLI emits extra local formatting and legacy snake_case fields. |
| `container set` | None | Missing | No container update method. Current CLI manages per-container webhook URLs, including `--all` batch mutation. |
| `webhook list/add/rm` | None | Missing | Global webhook URLs are stored on the root container; no RPC method exposes global webhook list/mutation or root-container webhook mutation. |
| `comment add` | `wrkq.comment.add` | Partial | RPC adds a comment with body/meta/actor. CLI also supports file/stdin/message source arbitration, `--if-match`, `--dry-run`, `--as`, and webhook side effects/outputs. |
| `comment ls` | `wrkq.comment.list` | Partial | RPC lists one task with includeDeleted/limit/cursor. CLI supports many task args, fields selection, sort/reverse, YAML/TSV, porcelain, and richer actor/deleted metadata. |
| `comment cat` | `wrkq.comment.show` | Partial | RPC returns DTO, but CLI supports multiple comment refs, raw mode, c: token stripping, rich actor/deleted metadata, and human formatting. |
| `comment rm` | `wrkq.comment.delete` | Partial | RPC soft-deletes only. CLI supports purge, dry-run, yes, if-match skip behavior, multi-ref, and output summary. |
| `attach ls` | `wrkq.attachment.list` | Partial | Core list is covered. Output shape differs (`relativePath` vs `relative_path`), and CLI supports table/porcelain conventions. |
| `attach put` | `wrkq.attachment.add` | Partial | RPC copies a local path and stores metadata. CLI supports stdin (`-`) with required name, exact output text, and current attach config defaults. |
| `attach get` | `wrkq.attachment.show` only metadata | Missing | No RPC method streams or returns attachment file bytes/content. Metadata alone cannot implement `attach get`. |
| `attach rm` | `wrkq.attachment.remove` | Partial | Core remove is covered. CLI has confirmation and multi-ref behavior. |
| `relation add` | `wrkq.relation.add` | Mostly covered | Command maps cleanly, but output format parity remains. |
| `relation ls` | `wrkq.relation.list` | Partial | RPC relation DTO is compact; CLI output includes local formatting and may need incoming/outgoing parity checks. |
| `relation rm` | `wrkq.relation.remove` | Mostly covered | Command maps cleanly, but output format parity remains. |
| `check blocked` | None exact | Missing | Could be composed from `relation.list` plus `task.show`, but no RPC method returns blocker check result or preserves exit-code semantics. |
| `check-inbox` | None exact | Missing | Convenience projection combines inbox open tasks and ack-pending requested work. RPC task.list lacks requested-by and ack-pending filters. |
| `handoff create/list/get/acknowledge/search` | None | Missing | Handoffs have store APIs and CLI commands, but no `wrkq.handoff.*` RPC namespace. |
| `bundle create` | None | Missing / likely local-only | Exports Git-ops bundle files and optional attachments/events. This may remain local, but not RPC-covered. |
| `agent` | None | Local-only | Thin pass-through to `hrcchat turn seshat`; should not be pushed into wrkq RPC. |
| `agent-context` | None | Local-only | Runtime/context helper. No DB resource RPC needed. |
| `usage` / `agent-info` | None | Local-only | Embedded docs output. |
| `version` | `rpc.initialize` contains server metadata | Partial / local-only | CLI version output includes build metadata, supported command claims, formats, flags, capabilities. RPC initialize has protocol/server/database metadata. |
| `whoami` | None | Missing / local config | Resolves current principal/scope/db path from CLI config/env. Could be local-only or a new `wrkq.context.whoami`. |
| `server start/serve/stop/restart/status/health` | None | Local-only | Controls the wrkqd daemon/launchd/process health. Should not be routed through wrkq data RPC unless there is a separate control-plane RPC. |
| `rpc --stdio` | Itself | Covered | This is the RPC server entrypoint, not a command to migrate internally. |

## Cross-cutting RPC Gaps

### 1. Projection DTOs are not CLI DTOs

Most RPC DTOs are clean camelCase resource DTOs. The current CLI often emits legacy snake_case, aggregate projections, compact metadata views, raw markdown, table rows, porcelain IDs, or NDJSON event lines.

This is good for a public API, but it means an RPC-backed CLI needs either:

- CLI-side translation layers, or
- new RPC projection methods that intentionally match CLI machine contracts.

For byte-for-byte comparison, CLI-side translation is unavoidable unless RPC adds every legacy presentation shape.

### 2. `TaskPatch` is too narrow for current `set`

Current RPC `TaskPatch` supports:

```text
title
description
specification
state
priority
kind
riskClass
labels
meta
assigneePrincipalRef
dueAt
startAt
```

Current `wrkq set` also supports:

```text
slug
parent_task_uuid via --parent-task / --parent-id
requested_by_project_id
assigned_project_id
resolution
cp_project_id
cp_work_item_id
cp_run_id
cp_session_id via --session-id
run_status
```

Without widening `TaskPatch`, `set` cannot become RPC-backed.

### 3. Task detail is missing current CLI fields

The CLI `cat` JSON output includes fields not present in `WrkqTask`:

```text
artifact_dir
project_id
requested_by_project_id
assigned_project_id
parent_task_id
parent_task_uuid
assignee display fields
resolution
cp_project_id
cp_work_item_id
cp_run_id
session_id
run_status
blocked_by
comments
relations
created_by / updated_by display fields
created_by_scope_ref
```

Some of these can be composed from several RPC calls, but several are absent from current RPC DTOs.

### 4. Lifecycle semantics differ in places

The largest concrete mismatch is delete:

- CLI `rm` default archives.
- RPC `wrkq.task.delete` performs a reversible delete by setting `state=deleted` and `deleted_at`.

An RPC-backed mirror cannot silently substitute this behavior.

### 5. No event or stream namespace

`log`, `watch`, `monitor watch`, and `monitor wait` have no `wrkq.event.*` or `wrkq.monitor.*` RPC support.

The existing `wrkf.event.query` is workflow-event scoped; it is not a replacement for wrkq event_log streaming/history.

### 6. No search/index namespace

The current `search` and `index` commands operate a local search sidecar with FTS/dense ranking and freshness checks. There is no `wrkq.search.*` or `wrkq.index.*` namespace.

These commands may intentionally remain local, but they cannot be implemented through current RPC.

### 7. No handoff namespace

The `handoff` command group has no RPC coverage. If agents are expected to use an RPC-backed CLI for handoffs, add:

```text
wrkq.handoff.create
wrkq.handoff.list
wrkq.handoff.show
wrkq.handoff.acknowledge
wrkq.handoff.search
```

### 8. No container update/move namespace

Current container RPC supports create, show, list, delete, and recursive delete. It does not support:

```text
container rename
container move
container title update
container webhook URL update
global webhook update on root container
batch webhook mutation across all containers
```

## Recommended RPC Additions for CLI Migration

### Tier 1: Required before core task CLI parity

```text
wrkq.task.update
  widen patch with:
    slug
    parentTask
    requestedByProjectId
    assignedProjectId
    resolution
    cpProjectId
    cpWorkItemId
    cpRunId
    sessionId
    runStatus

wrkq.task.archive
  archive semantics used by rm default

wrkq.task.purge
  hard-delete task and attachment cleanup semantics used by rm --purge

wrkq.task.restoreRich
  restore with --to/title/description/priority/labels/assignee/comment/CAS
```

Alternative: fold archive/purge/rich restore into existing methods with explicit params, but do not change existing behavior incompatibly.

### Tier 2: Required before read/projection parity

```text
wrkq.task.detail
  rich CLI cat projection: task, artifact_dir, comments, relations, blockers, display fields

wrkq.item.listChildren
  combined container/task child listing for ls

wrkq.item.find
  mixed task/container find projection with current filters

wrkq.item.stat
  compact task/container metadata union

wrkq.item.tree
  hierarchical tree projection, or expose enough child/count data to build it client-side

wrkq.project.list
  top-level projects, intentionally ignoring project-root scoping
```

### Tier 3: Required before operational parity

```text
wrkq.event.log
wrkq.event.watch
wrkq.monitor.watch
wrkq.monitor.wait

wrkq.search.query
wrkq.index.status
wrkq.index.update
wrkq.index.rebuild
wrkq.index.vacuum
wrkq.index.pause
wrkq.index.resume

wrkq.handoff.create
wrkq.handoff.list
wrkq.handoff.show
wrkq.handoff.acknowledge
wrkq.handoff.search
```

### Tier 4: Optional or likely local-only

Keep these local unless there is a clear reason to remote them through data RPC:

```text
wrkq agent
wrkq agent-context
wrkq usage
wrkq agent-info
wrkq server *
wrkq rpc --stdio
wrkq version
```

`server *` is process control over `wrkqd`; it belongs in a control-plane surface if it ever becomes RPC-backed.

## Recommended Migration Order

1. Build the mirror CLI and command-tree parity test.
2. Implement strictly read-only, low-projection commands first:
   - `container cat`
   - simple `relation ls`
   - basic `attach ls`
   - basic `comment cat`
3. Widen task DTOs/patches before attempting `cat`, `set`, `touch`, or `find`.
4. Add `task.archive` / `task.purge` or equivalent before attempting `rm`.
5. Add projection methods for `ls`, `find`, `tree`, `stat`, and `projects`.
6. Decide intentionally which operational commands stay local.

## Cutover Constraint

Do not cut over a command in the production `wrkq` binary until the comparison harness proves:

- exit code parity
- stdout parity
- stderr parity
- DB snapshot parity for mutations
- attachment filesystem parity where applicable
- event log parity for mutations that emit events

For mutating commands, strict byte comparison may require a deterministic clock hook or explicit normalizers because current code uses SQLite `now`, `CURRENT_TIMESTAMP`, and `time.Now()` in several paths.
