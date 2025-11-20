Below is a pragmatic, **MVP‑first milestone plan** that turns your spec into shippable slices. Each milestone lists concrete deliverables (CLI surface, schema, internals), acceptance criteria, and a tiny demo script. Timelines are intentionally omitted; sequence is optimized for risk‑reduction and preserving stable machine contracts early.

---

## Milestone Overview (high level)

| Milestone | Theme | What You Get |
|---|---|---|
| **M0 — Core CLI + DB** | “Hello, durable world.” | WAL SQLite DB, migrations, actors, containers, tasks, slugs, friendly IDs, pathspecs & globs, ETag concurrency, event log (create/update/delete), core CRUD commands, machine outputs (`--json`, `--ndjson`, `--porcelain`). |
| **M1 — Editing, History & Search** | “Work with content safely.” | `edit`/`apply` with 3‑way merge + `--if-match`, `log`, `watch`, `diff`, `tree`, `find` filters, sorting/field selection everywhere, perf indices, p95 checks. |
| **M2 — Attachments, Scale & Packaging** | "Operate at scale; ship binaries." | Attachment put/get/ls/rm + purge semantics, pagination cursors, bulk‑ops knobs (`--jobs`, `--continue-on-error`), `cp`, `rm --purge`, doctor tools, completions, install scripts, GoReleaser artifacts & SBOM. |
| **M3 — API‑ready Contracts + Comments** | "Browser‑ready." | Machine interface v1 freeze, `version --json` contract, comments table + CLI, minimal HTTP/JSON façade spec (Go or Node) aligned to CLI porcelain, doc set for integrators. |
| **M4 — Stretch (optional)** | "Nice to have." | SQLite FTS seed for future `rg`, advanced diffs/patches, sample Node server stub & Postman collection, additional diagnostics. |
| **M5 — Git-ops & Bundle Commands** | "PR-based agent workflows." | `bundle create/apply`, `db snapshot`, typed selectors (`t:<token>`), `apply --base` for 3‑way merge, attachment bundling, manifest validation, conflict detection on bundle apply, ephemeral DB workflow for agents. |

---

## M0 — Core CLI + DB (MVP)

### Deliverables

**Schema & migrations**
- `0001_init.sql` (SQLite dialect)
  - Tables: `actors`, `containers`, `tasks`, `events`.
  - Columns per spec incl. `etag INTEGER`, timestamps, FKs.
  - Indices:  
    - `containers(parent_id, slug)`,  
    - `tasks(project_id, slug)`,  
    - `tasks(state, due_at)`,  
    - `tasks(updated_at)`,  
    - `events(resource_type, resource_id, id DESC)`.
  - Sequences (or counters) table for friendly IDs (`next_actor`, `next_project`, `next_task`).
- WAL on open; `busy_timeout` + retry policy in code.

**Internals**
- `internal/db`: handle + migrations runner; pragmas.
- `internal/id`: friendly IDs (`A-xxxxx`, `P-xxxxx`, `T-xxxxx`) + UUIDs; zero‑padded stable width.
- `internal/paths`: slug normalization + pathspec/glob resolver (`*`, `?`, `**`) with sibling uniqueness guarantees.
- `internal/domain`: validation + ETag helpers.
- `internal/events`: append helpers for `created|updated|deleted|moved`.
- `internal/actors`: resolution precedence (`--as`, env, config, default seeded).
- `internal/render`: table, JSON, NDJSON, YAML, TSV; `--columns`, `--porcelain` (stable keys, no ANSI), `-1`/`-0`.

**CLI surface**
- `wrkq init` (creates DB, WAL, runs migrations, seeds `inbox`, default actors; respects `--db`, `--actor-slug`, `--actor-name`, `--attach-dir`).
- `wrkq whoami`, `wrkq actors ls`, `wrkq actor add`.
- Navigation/metadata:
  - `wrkq ls`, `wrkq stat`, `wrkq ids`, `wrkq resolve`.
- Structure & lifecycle:
  - `wrkq mkdir`, `wrkq touch -t`, `wrkq mv`, `wrkq rm` (soft delete), `wrkq restore`.
- Content read:
  - `wrkq cat` (task only; container → usage error `2`).
- Mutations:
  - `wrkq set key=value [...]` (state, priority, title, slug, labels, dates).
- Plumbing:
  - `wrkq version` (basic), `wrkq completion`, minimal `wrkq config doctor` stub that prints effective config.
- Exit codes: `0,1,2,3,4,5` implemented and covered by tests where applicable.

**Concurrency & audit**
- ETag check on all writes; `--if-match` honored by `set`, `mv`, `rm`, `restore`, `touch` metadata updates, `mkdir` metadata updates.
- Events appended with resulting `etag`.

**Docs & tests**
- Golden tests for outputs (`--porcelain`, `--json`, `-0`).
- Pathspec & slug property tests.
- Concurrency smoke: two writers, ETag conflict returns `4`.

### Acceptance criteria
- Fresh repo: `wrkq init` → DB file exists, WAL active, `inbox` project seeded, default actors present.
- `ls 'portal/**' -type t -1 | xargs -n1 wrkq cat` yields deterministic Markdown with front matter.
- `cat` on a container exits `2` with message “cat only supports tasks; got container …”.
- ETag mismatch reliably returns `4` without partial writes.
- `--json`, `--ndjson`, `--porcelain`, `-0` behave across `ls`, `stat`, `ids`, `resolve`.
- Basic perf: list **5k** tasks under **200 ms p95** locally (single run, warm cache).

### Demo script (happy path)
```sh
wrkq init --db ~/.local/share/wrkq/wrkq.db --actor-slug lance --actor-name "Lance (human)"
wrkq whoami
wrkq mkdir portal/auth -p
wrkq touch 'portal/auth/login-ux' -t "Login UX"
wrkq ls 'portal/**' -type t --columns=id,path,slug,title -1
wrkq set 'portal/auth/login-ux' priority=1 labels='["backend"]'
wrkq cat 'portal/auth/login-ux'
wrkq mv 'portal/auth/login-ux' 'portal/auth/login-experience' -type t
wrkq rm 'portal/auth/login-experience' --yes
wrkq restore 'portal/auth/login-experience'
```

---

## M1 — Editing, History & Search

### Deliverables

**Schema**
- `0002_event_payloads.sql` (if needed to widen event `payload`/indexes).

**Internals**
- `internal/edit`: 3‑way merge (base/current/edited), conflict markers; integrates `--if-match`.
- `internal/render` enhancements: `--sort`, richer `--fields` across commands.

**CLI surface**
- `wrkq edit <task>`: opens `$EDITOR`, applies on save via 3‑way merge; writes event with patch summary.
- `wrkq apply [<task>] [-]`: accepts md/yaml/json; `--body-only`, `--format`, `--dry-run`, `--if-match`.
- `wrkq log <path|id>`: oneline & detailed; `--since`, `--until`, `--oneline`, `--patch`.
- `wrkq watch [PATH...] --since <cursor> --ndjson`: streams events.
- `wrkq diff <A> [B]`: unified or JSON diff; supports task vs DB.
- `wrkq tree [PATH...] -L <depth>`.
- `wrkq find [PATH...]` with `-type`, `--slug-glob`, `--state`, `--due-before/after`, `--limit`, `--cursor`.

**Performance**
- Verify and document indices; micro-benchmarks for `find` and `ls` sorts.

### Acceptance criteria
- Editing a task body with concurrent update produces merge conflict and exits `4`.
- `apply --dry-run` prints plan, does not mutate.
- `watch` prints NDJSON events with cursor continuity.
- `find` filters by due date & state correctly and supports `-0` for piping.

### Demo script
```sh
wrkq find 'portal/**' -type t --state open --slug-glob 'login-*' --json
wrkq edit 'portal/auth/login-ux'   # make a change
wrkq log 'portal/auth/login-ux' --oneline
wrkq diff 'portal/auth/login-ux' --unified=3
wrkq watch --since 0 --ndjson | head -n5
```

---

## M2 — Attachments, Scale & Packaging

### Deliverables

**Schema**
- `0003_attachments.sql`: `attachments` table as spec; FKs to tasks and actors; indexes on `(task_id)` and `(created_at DESC)`.

**Internals**
- `internal/attach`: IO moving files to `attach_dir/tasks/<task_uuid>/...`; checksum optional; size/mime capture.
- Pagination/cursors: Opaque `--cursor` support on `ls`, `find`, `log`, `attachments ls`; `--limit`.

**CLI surface**
- `wrkq attach ls <task>`
- `wrkq attach get <ATT-ID> [--as PATH|-]`
- `wrkq attach put <task> <FILE|-> --mime <type> [--name <filename>]`
- `wrkq attach rm <ATT-ID...>` (metadata + delete file; respects `--yes`).
- `wrkq rm --purge` (hard delete rows + delete `attach_dir/tasks/<task_uuid>`).
- `wrkq cp <SRC...> <DST>` with `--with-attachments|--shallow`.
- Bulk & ops:
  - `--jobs N`, `--batch-size`, `--ordered`, `--continue-on-error`.
- Housekeeping & packaging:
  - `wrkq doctor` (pragmas, WAL, indices, attach_dir checks, attachment size limits).
  - `wrkq version --json` (add `commit`, `build_date`, `machine_interface_version`, supported formats/commands).
  - `install.sh`, completions for bash/zsh/fish.
  - GoReleaser config: darwin/linux/windows, amd64/arm64, checksums + SBOM.

### Acceptance criteria
- Attachments survive task moves/slug changes (path is `<task_uuid>` anchored).
- Soft delete of task leaves files; `--purge` removes files and metadata.
- Cursor pagination: deterministic `--sort` + `--cursor` round‑trips; `--porcelain` prints `next_cursor` on stderr.
- Binaries install via `install.sh` and completions load.

### Demo script
```sh
wrkq attach put 'portal/auth/login-ux' ./specs/login-flow.pdf --mime application/pdf --name "Login Flow Spec"
wrkq attach ls 'portal/auth/login-ux' --json
wrkq attach get ATT-00001 --as ./out/login-flow.pdf
wrkq rm 'portal/auth/login-ux' --yes
wrkq rm 'portal/auth/login-ux' --purge --yes
```

---
## M3 – Comments & Collaboration

Goal: ship comments as a first-class, machine-friendly collaboration channel between humans and coding agents, wired into the event log and surfaced via dedicated CLI commands.

### Scope

- Comments are append-only notes attached to a task.
- Each comment is a first-class resource with:
  - UUID and friendly ID (`C-xxxxx`).
  - Actor attribution (`actor_id`, `actor_slug`, role).
  - Markdown `body` and optional JSON `meta` for agents/tools.
  - ETag/versioning and soft-delete (`deleted_at`, `deleted_by_actor_id`).
- Event log and CLI surfaces are updated so agents never have to scrape human-oriented output.

### Tasks

1. **DB schema & migrations**
   - Add a `comments` table with fields:
     - `uuid`, `id` (friendly `C-xxxxx`), `task_id`, `actor_id`, `body`, `meta` (JSON), `etag`, `created_at`, `updated_at` (nullable), `deleted_at` (nullable), `deleted_by_actor_id` (nullable).
   - Add appropriate indexes (at minimum):
     - `(task_id, created_at)` for listing comments on a task.
     - `(actor_id, created_at)` for future tooling.
   - Wire migrations into the existing migrations runner.

2. **Domain model & ID generation**
   - Add a `Comment` domain type in `internal/domain` with validation helpers.
   - Extend `internal/id` to generate friendly comment IDs (`C-xxxxx`).
   - Ensure comment `meta` follows the same JSON handling patterns as actor `meta`.

3. **Event log integration**
   - Extend the event log schema/enums:
     - Add `comment` to `resource_type`.
     - Add events: `comment.created`, `comment.deleted`, `comment.purged`.
   - Ensure all comment mutations emit events via `internal/events` with payload fields:
     - `task_id`, `comment_id` (friendly ID), `actor_id`, and soft-/hard-delete flags.
   - Update `wrkq log` / `wrkq watch` to support `resource_type=comment` filters.

4. **CLI: comment commands**
   - Implement `wrkq comment ls <TASK-PATH|t:<id>...>`:
     - Resolve container paths and `t:<token>` to tasks.
     - List comments ordered by `created_at` ascending.
     - Support `--json|--ndjson|--yaml|--tsv`, `--fields`, `--include-deleted`, `--limit`, `--cursor`, `--porcelain`, `--sort=created_at`, `--reverse`.
   - Implement `wrkq comment add <TASK-PATH|t:<id>> [FILE|-]`:
     - Accept comment text via `-m/--message`, file argument, or stdin when `-` is used.
     - Support `--meta <json>`, `--if-match <task-etag>`, `--as <actor>`, `--dry-run`.
     - Return the new comment ID and metadata; JSON shape matches the spec.
   - Implement `wrkq comment cat <COMMENT-ID|c:<token>...>`:
     - Resolve friendly ID, UUID, or `c:<token>`.
     - Default human output: header line (ID, timestamp, actor, task) + body.
     - Support `--json|--ndjson` and `--raw` (body only).
   - Implement `wrkq comment rm <COMMENT-ID|c:<token>...>`:
     - Default to soft-delete: set `deleted_at`, `deleted_by_actor_id`, increment `etag`, emit `comment.deleted`.
     - `--purge` for hard-delete and `comment.purged` events.
     - Support `--yes`, `--dry-run`, `--if-match <etag>`.

5. **CLI: `wrkq cat --include-comments`**
   - Update `wrkq cat` task rendering to:
     - When `--include-comments` is set, query non-deleted comments for the task ordered by `created_at`.
     - Append the comments block after the task body as specified in the spec:
       - Separator `---` and sentinel `<!-- wrkq-comments: do not edit below -->`.
       - One `>`-quoted block per comment with header:
         - `> [<comment-id>] [<ISO8601 timestamp>] <actor-slug> (<actor-role>)`.
   - Ensure `wrkq apply` explicitly ignores this comments block (no parsing/writes back to DB).

6. **Machine contracts & tests**
   - Add integration tests covering:
     - Creating, listing, and soft-deleting comments via CLI.
     - JSON / NDJSON shapes for `wrkq comment ls/add/cat/rm` and their stability.
     - Event log entries for `comment.created` and `comment.deleted`.
     - `wrkq cat --include-comments` layout (smoke tests, not strict parsing tests).
   - Update docs/help (`wrkq help comment`, SPEC.md references) to match the implemented behavior.

7. **Agent usage examples (docs only)**
   - Document recommended patterns for agents:
     - Emitting progress and summary comments with `--meta` (`run_id`, `kind`, etc.).
     - Polling comments via `wrkq comment ls --ndjson` instead of scraping `wrkq cat`.
     - Using `t:<token>` and `c:<token>` selectors for stable addressing.
---

## M4 — Stretch (optional)

- SQLite FTS virtual table for tasks/comments bodies; scaffold `wrkq rg` (hidden/experimental).
- `wrkq diff --json` emits structured hunks suitable for UI patch views.
- Minimal Node or Go HTTP server stub implementing the spec (single‑binary dev server).
- Additional `wrkq doctor` checks (VACUUM/ANALYZE guidance, WAL checkpoints).

---

## M5 — Git-ops & Bundle Commands

### Deliverables

**Internals**
- `internal/bundle`: bundle creation, manifest generation, task export/import with `base_etag` tracking.
- `internal/selectors`: typed selector parsing (`t:<token>`) for tasks, accepting UUID, friendly ID, or path.
- `internal/snapshot`: SQLite online backup API for ephemeral DB creation.
- Enhanced `internal/edit`: support `--base` flag for 3-way merge in `apply` command.

**CLI surface**
- `wrkq bundle create [--out <dir>] [--actor <slug|ID>] [--since <ts>] [--until <ts>] [--with-attachments] [--no-events] [--json]`
  - Creates bundle directory (default `.wrkq/`) with:
    - `manifest.json` (version, machine_interface_version, metadata)
    - `events.ndjson` (event log slice for audit trail)
    - `containers.txt` (container paths to ensure exist)
    - `tasks/<path>.md` (task documents with `base_etag` for conflict detection)
    - `attachments/<task_uuid>/*` (attachment files when `--with-attachments`)
  - Filters by actor and time window.
  - Computes `base_etag` from earliest event per task.

- `wrkq bundle apply [--from <dir>] [--dry-run] [--continue-on-error] [--json]`
  - Validates `machine_interface_version` compatibility.
  - Creates containers from `containers.txt`.
  - Applies tasks using `t:<uuid>` selector (fallback to `t:<path>` for new tasks).
  - Honors `base_etag` with `--if-match` semantics; exits `4` on conflict.
  - Re-attaches files from bundle's `attachments/` directory.
  - Shows `wrkq diff` output to aid conflict resolution.

- `wrkq db snapshot --out <path> [--json]`
  - Creates WAL-safe point-in-time DB copy via SQLite online backup.
  - Emits JSON manifest with timestamp, source path, `machine_interface_version`.
  - Does not copy attachments (agents use branch-scoped `WRKQ_ATTACH_DIR`).

- `wrkq apply --base <FILE>` (flag enhancement)
  - Performs 3-way merge (base/current/edited) using `internal/edit` machinery.
  - Honors `--if-match` for final write.
  - Exits `4` on unresolvable conflicts.

- `wrkq attach path <ATTACHMENT-ID|relative_path> [--json]` (helper)
  - Resolves and prints absolute filesystem path for attachment.
  - Useful for bundle exporters and tooling.

- `wrkq bundle replay [--from <dir>] [--dry-run] [--strict-etag]` (optional/future)
  - Replays `events.ndjson` in order.
  - With `--strict-etag`, validates each event's etag and exits `4` on divergence.

**Typed selectors**
- `t:<token>` syntax for task identification in all commands.
- `<token>` can be:
  - Friendly ID (`T-00123`)
  - UUID
  - Path (`portal/auth/login-ux`)
- Extends existing pathspec resolution; backward compatible.

### Acceptance criteria
- `bundle create --actor agent-foo` produces deterministic bundle with only agent-foo's changes.
- `bundle apply --dry-run` validates without writes; shows conflicts without failing.
- `bundle apply` on main DB detects etag conflicts and exits `4` with helpful diff output.
- `db snapshot` creates standalone DB file usable with `WRKQ_DB_PATH` env.
- Attachments in bundle round-trip correctly via `bundle create --with-attachments` → `bundle apply`.
- `manifest.json` version check prevents applying bundles from incompatible CLI versions.
- `apply --base` successfully merges non-conflicting concurrent edits; exits `4` on conflicts.
- Typed selector `t:T-00123` resolves identically to `T-00123` in all commands.

### Demo script

```sh
# Agent workflow: snapshot DB for ephemeral work
wrkq db snapshot --out /tmp/wrkq.$BRANCH.db
export WRKQ_DB_PATH=/tmp/wrkq.$BRANCH.db
export WRKQ_ATTACH_DIR=/tmp/wrkq.$BRANCH.attach
export WRKQ_ACTOR=agent-$BRANCH

# Agent makes changes
wrkq touch portal/auth/mfa -t "Add MFA support"
wrkq set portal/auth/mfa priority=1 state=in_progress
wrkq edit portal/auth/mfa  # add implementation spec

# Export bundle for PR
wrkq bundle create --actor agent-$BRANCH --with-attachments --out .wrkq/

# On main branch: validate before merge
wrkq bundle apply --from .wrkq --dry-run

# After PR review: apply to main DB
unset WRKQ_DB_PATH  # back to main DB
wrkq bundle apply --from .wrkq

# Check for conflicts
echo $?  # 0=success, 4=conflicts detected

# Use typed selectors
wrkq cat t:T-00123
wrkq set t:portal/auth/login-ux priority=2
wrkq apply t:T-00123 new-spec.md --base old-spec.md --if-match 47

# Helper for attachment paths
wrkq attach path ATT-00005  # prints /path/to/attach_dir/tasks/<uuid>/file.pdf
```

### Notes

- Bundle format uses existing `wrkq cat` task document format for reviewability and deterministic round-trips.
- `base_etag` computed from event log enables conflict detection without breaking existing schema.
- All commands honor existing exit codes, addressing conventions, and machine interface stability guarantees.
- Attachments use established `attach_dir/tasks/<task_uuid>/` layout; bundle just copies this structure.

---

## Cross‑cutting Implementation Notes

- **Slug normalization**: single function used by `mkdir`, `touch`, `mv`, `set slug=...`; rejects invalid per global regex; max 255 bytes enforced at insert/update.
- **Friendly IDs**: allocate inside transaction to avoid gaps; render fixed width (5 digits) to match examples.
- **ETag**: numerically increments per row write; all mutating commands accept `--if-match`; `edit`/`apply` use 3‑way merge on body + field‑wise merge for metadata (no silent drops).
- **Event payloads**: store structured deltas (changed fields, previous→next); keep small to avoid bloat.
- **Globs**: implement internally (do not rely on shell); patterns must be quoted in docs.
- **Zero matches**: default error exit `3`; `--nullglob` converts to no‑op for bulk ops.
- **Perf guardrails**: NDJSON for bulk listings; avoid loading large bodies in `ls`/`find`.
- **Testing strategy**: CLI e2e (golden), unit tests for domain, property tests on resolver, race/concurrency tests (WAL+busy_timeout), Windows path handling in CI.

---

## Cut‑Scope Levers (if needed)

- Defer `tree` to M1 (kept).
- Defer `cp` to M2 (kept).
- Start M1 `log/watch` with minimal payloads before `--patch`.
- Start M2 without checksum for attachments; add in patch release.
- Limit `find` to simple predicates in M1; add more fields later without breaking porcelain.

---

## “Definition of Done” (overall)

- `wrkq version --json` exposes `machine_interface_version: 1`.
- All commands listed for a milestone have:
  - Stable `--porcelain` shape + documented fields.
  - `--json`/`--ndjson` parity where applicable.
  - Exit codes implemented and tested.
- CLI passes golden tests across macOS/Linux/Windows in CI.
- GoReleaser publishes signed checksums + SBOM; install script works; completions install.
- Docs: quickstart, piping examples, machine contracts, HTTP façade spec, migration/backup notes.

---

### Why this order?
- **M0** lays foundational contracts (IDs, slugs, ETag, events) and core UX so you can actually use the tool.
- **M1** makes content editing safe and auditable—the biggest day‑2 necessity.
- **M2** completes operational needs (attachments, pagination, packaging) to scale real usage.
- **M3** freezes machine interfaces and readies the ecosystem for a browser UI without locking you into a server implementation yet.