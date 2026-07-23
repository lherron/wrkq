---
id: wrkq/overview
title: wrkq overview and architecture
kind: reference
authority: descriptive
status: active
visibility: internal
provenance: authored
---

# wrkq overview and architecture

wrkq is a local-first task-management system for humans and coding agents: a
filesystem-flavored CLI over a SQLite database. This page orients a reader
inside the `wrkq` repository (binaries, storage layout, data model, execution
modes). It complements the platform-level system-boundary doc; for the
canonical, most-current spec of CLI/daemon behavior, `docs/SPEC.md` in this
repo wins over any other markdown when they conflict.

## Shipped binaries

The repo builds four binaries (`just build`, `just install`):

| Binary | Entry point | Role |
| --- | --- | --- |
| `wrkq` | `cmd/wrkq/main.go` | Day-to-day human/agent task surface. Routes durable operations through the `workrpc` JSON-RPC boundary rather than talking to SQLite directly. |
| `wrkqadm` | `cmd/wrkqadm/main.go` | Administrative surface: DB init/migrate/snapshot, actors, patches, state export/import/verify, doctor. Local-path-only — rejects `rpc://` locators. |
| `wrkqd` | `cmd/wrkqd/main.go` | Local daemon: token-auth HTTP + JSON-RPC API over the database. `wrkq server` wraps its lifecycle. |
| `wrkf` | `cmd/wrkf/main.go` | Separate workflow engine that layers workflow instances, evidence, obligations, and transitions on top of wrkq tasks. Same repo, own DB (`var/db/wrkf.db`), own frozen RPC contract (`docs/wrkf-rpc.md`). |

## Data model

Canonical durable state lives in one SQLite database (WAL mode, busy timeout,
`etag INTEGER` optimistic concurrency, append-only `event_log`). The core
entities:

- **Containers** — hierarchical organizational units. User-creatable kinds are
  `project`, `directory`, `feature`, `area`. There is also an internal
  singleton `root` container (added by migration `000024_root_container.sql`)
  that is path-invisible and not user-creatable; every top-level project path
  is a direct child of `root`.
- **Tasks** — the primary work item. Kinds: `task`, `subtask`, `spike`, `bug`,
  `chore`. Priority is `1`–`4` (`1` highest). A task with `--parent-task` is
  stored as kind `subtask` unless `--kind` overrides it; parent links are
  task-graph edges that may cross container boundaries, and subtasks cannot
  themselves have subtasks.
- **States** — `idea`, `draft`, `open`, `in_progress`, `blocked`, `completed`,
  `cancelled`, `archived`, `deleted`. Common path is
  `idea -> draft -> open -> in_progress -> completed`; the validator otherwise
  accepts any valid state and does not enforce a strict transition graph.
  `idea` tasks are hidden from default `find` and ignored as blockers.
- **Comments** — append-only notes on tasks, soft-deletable, included in
  `wrkq cat` output by default (`--exclude-comments` to omit).
- **Attachments** — metadata in SQLite; bytes stored at
  `<attach_dir>/tasks/<task_uuid>/...`, keyed by task UUID so moves/renames
  never move bytes.
- **Relations** — `blocks`, `relates_to`, `duplicates` edges between tasks. A
  `blocks` edge is blocking unless the blocking task is `completed`,
  `archived`, `deleted`, or `cancelled` (`idea` is also excluded) — meaning
  `draft`, `open`, `in_progress`, and `blocked` tasks still block.
- **Causal lineage (`caused_by`)** — a distinct, multi-valued edge set
  (`task_causes` table) recording which prior task(s) a defect/rework task
  traces back to. It does not participate in blocker checks, `relation`
  output, or parent/subtask nesting. Input is comma-separated `T-#####` IDs;
  hard-purging a task with surviving `caused_by` dependents is rejected unless
  they are purged together or their lineage is cleared first.
- **Handoffs** — intentional context records an agent leaves for its own
  future session, scoped to `agent:<id>:project:<proj>` (see
  `/docs/wrkq/concepts`).
- **Task claims** — atomic single-holder claim state (holder principal, exact
  scope, server-derived node, timestamp, monotonic generation, one-time claim
  token) recorded on the task row itself, distinct from relations/lineage.

## Addressing

Every task and container can be addressed by:

- **Path** — slash-separated container/task slugs, e.g. `wrkq/inbox/docs-spec-cleanup`.
- **Friendly ID** — `T-#####` (task), `P-#####` (project), `C-#####` (container),
  `A-#####` (actor), `H-#####` (handoff).
- **UUID** — the full database UUID.
- **Typed selector** — `t:T-02017`, `c:P-00061`.

Slugs are normalized: lowercased, spaces/underscores become hyphens, invalid
characters dropped, leading/trailing hyphens trimmed, must start with
`[a-z0-9]`, match `^[a-z0-9][a-z0-9-]*$`, max 255 bytes.

## Execution modes: local SQLite vs. RPC

The production `wrkq` CLI no longer talks to SQLite directly for durable
operations — it routes through `internal/workrpc`. The `WRKQ_DB` locator can
be either:

- a **local SQLite path** — direct file access, WAL mode; or
- **`rpc://host[:port]`** (default port `7171`) — JSON-RPC frames sent over
  HTTP `POST /v1/rpc` (`internal/workrpc/remote_stdio.go:17`) to a remote
  `wrkqd`.

This lets a machine with no local canonical DB use a remote one as its store
of record. A live example from this environment (`wrkq whoami` in RPC mode):

```json
{
  "db_locator": "rpc://mini",
  "db_mode": "remote",
  "principal_ref": "agent:mable",
  "remote_endpoint": "mini:7171",
  "scope_ref": "agent:mable:project:taskboard:task:primary"
}
```

Admin/daemon path-owning surfaces are intentionally local-only and reject
`rpc://`: `wrkqadm --db`, `wrkqd --db`, `wrkq server --db-path`.

## Package layout (selected)

| Path | Responsibility |
| --- | --- |
| `internal/rpccli` | RPC-backed production `wrkq` command adapters. |
| `internal/admincli` | Local-path-only `wrkqadm` database lifecycle commands. |
| `internal/wrkqd` | Authenticated daemon HTTP/workrpc surface + daemon-owned search indexer. |
| `internal/config` | Config/env loading and precedence. |
| `internal/db` | SQLite open, migrations (`internal/db/migrations`), schema status. |
| `internal/domain` | Domain structs and validation enums. |
| `internal/store` | Persistence and transactional domain operations. |
| `internal/selectors`, `internal/paths` | Friendly IDs, UUIDs, typed selectors, paths, slug normalization. |
| `internal/search` | Sidecar search service over task/comment/handoff chunks. |
| `internal/scope` | ASP runtime-scope reduction to `agent:<id>` principals. |
| `internal/webhooks` | Outbound webhook emission on task mutations. |
| `internal/workflow`, `internal/wrkfapi`, `internal/workrpc`, `internal/wrkfcli` | wrkf workflow engine, its API layer, unified RPC registry, and CLI. |
| `packages/client` | `@wrkq/client` — Bun TypeScript JSON-RPC client for wrkq/wrkf. |
| `mcp-server/` | MCP stdio server exposing selected wrkq operations to LLM hosts. |

## What wrkq is not

- Not the `wrkf` workflow engine — `wrkf` is a separate system in the same
  repo with its own DB and frozen RPC contract; `tasks.state` is plain wrkq
  lifecycle state, not a projection of workflow state.
- Not authn/authz — principal refs are attribution, not authentication.
- Not a cloud/sync service — local-first, no hosted multi-tenant mode, not a
  real filesystem (no FUSE).
- Not a web UI — `wrkqd` exposes selected HTTP endpoints; rendering is a
  downstream consumer's job (e.g. taskboard).
