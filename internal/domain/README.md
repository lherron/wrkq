# Domain Model Operations

## Domain model essentials

**Resources:** Principal-attributed Container (project/subproject, hierarchical via `parent_uuid`), Task, Comment (append-only, soft-deletable), Attachment, and Event (append-only audit log). Legacy Actor rows remain as admin/display/history compatibility storage.

**Addressing** — any of:
- Path: `project/subproject/task`
- Friendly ID: `T-00123`, `P-00007`, `A-00001`, `C-00012`
- UUID
- Typed selector: `t:T-00123`, `c:C-00012`
- Globs: `*`, `?`, `**` (always quote: `wrkq ls 'portal/**/login-*'`)

**Task states:** `idea`, `draft`, `open`, `in_progress`, `completed`, `blocked`, `cancelled`, `archived`, `deleted`.

**Slug rules:** lowercase `[a-z0-9-]`, must start with `[a-z0-9]`, max 255 bytes, unique among siblings. Source: `internal/domain/validation.go`.

**Attribution, not auth:** every mutating command requires a canonical caller
principal (`agent:<id>`), supplied by `--principal-ref` / `--as`, the product's
principal env, a valid runtime scope, or config `default_principal_ref`.
`WRKQ_ACTOR`, `WRKQ_ACTOR_ID`, `WRKF_ACTOR`, bare slugs, actor IDs/UUIDs, and
`default_actor` are legacy compatibility inputs and never caller authority.
Resolution lives in `internal/attribution/`.

## Concurrency

Every mutable row carries `etag INTEGER` (increments on write). All mutating commands accept `--if-match <etag>` for optimistic concurrency. Conflicts exit `4`. SQLite is opened in WAL + 5000ms busy_timeout, so concurrent CLI/agent access is expected.

## Attachments

Metadata in DB, bytes on disk at `<attach_dir>/tasks/<task_uuid>/...`. Paths are keyed by task UUID, so task moves don't rewrite paths. Soft delete preserves files; `--purge` removes the directory. The project-local `.wrkq/wrkq.db` database defaults to `.wrkq/attachments`; other database paths require an explicit `WRKQ_ATTACH_DIR` or `attach_dir`.

## Comments

Append-only. Soft delete via `deleted_at`; `--purge` for hard delete. `wrkq cat` includes comments by default; use `--exclude-comments` to omit them. The emitted comments block is **read-only** — `wrkq apply` does not parse it back.

## Migrations

SQL files live in `internal/db/migrations/` with `000NNN_description.sql` filenames, embedded via `//go:embed`. Applied filenames tracked in `schema_migrations`. `wrkqadm migrate` (idempotent), `--status`, `--dry-run`. `wrkqadm init` runs migrations automatically.
