# Domain Model Operations

## Domain model essentials

**Resources:** Actor, Container (project/subproject, hierarchical via `parent_uuid`), Task, Comment (append-only, soft-deletable), Attachment, Event (append-only audit log).

**Addressing** — any of:
- Path: `project/subproject/task`
- Friendly ID: `T-00123`, `P-00007`, `A-00001`, `C-00012`
- UUID
- Typed selector: `t:T-00123`, `c:C-00012`
- Globs: `*`, `?`, `**` (always quote: `wrkq ls 'portal/**/login-*'`)

**Task states:** `idea`, `draft`, `open`, `in_progress`, `completed`, `blocked`, `cancelled`, `archived`, `deleted`.

**Slug rules:** lowercase `[a-z0-9-]`, must start with `[a-z0-9]`, max 255 bytes, unique among siblings. Source: `internal/domain/validation.go`.

**Attribution, not auth:** every mutating command requires an actor (`--as`, `WRKQ_ACTOR_ID`, `WRKQ_ACTOR`, or config default). Resolution order is in `internal/actors/`.

## Concurrency

Every mutable row carries `etag INTEGER` (increments on write). All mutating commands accept `--if-match <etag>` for optimistic concurrency. Conflicts exit `4`. SQLite is opened in WAL + 5000ms busy_timeout, so concurrent CLI/agent access is expected.

## Attachments

Metadata in DB, bytes on disk at `<attach_dir>/tasks/<task_uuid>/...`. Paths are keyed by task UUID, so task moves don't rewrite paths. Soft delete preserves files; `--purge` removes the directory. Default `attach_dir` is `~/.local/share/wrkq/attachments` — user-specific, untracked.

## Comments

Append-only. Soft delete via `deleted_at`; `--purge` for hard delete. `wrkq cat` includes comments by default; use `--exclude-comments` to omit them. The emitted comments block is **read-only** — `wrkq apply` does not parse it back.

## Migrations

SQL files live in `internal/db/migrations/` with `000NNN_description.sql` filenames, embedded via `//go:embed`. Applied filenames tracked in `schema_migrations`. `wrkqadm migrate` (idempotent), `--status`, `--dry-run`. `wrkqadm init` runs migrations automatically.
