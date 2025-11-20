# wrkq CLI - Informal PRD (Go + SQLite)

Filesystem-flavored CLI for managing projects, subprojects, and tasks on a SQLite backend. The tool feels like Unix utilities (`ls`, `cat`, `mv`, `rm`, `tree`, `touch`, `mkdir`) and is pipe-friendly.

The system is a collaboration surface between a human user and multiple coding agents. All actions are attributed to an **actor** (human or agent). No authentication, only attribution.

The CLI is the primary interface, but the data model and machine contracts must also support a future browser UI (likely Node/React) via a simple JSON API layered on top.

This spec is the product source of truth for behavior and UX. It is not a milestone plan.

-------------------------------------------------------------------------------

## 1. Scope and Goals

Goals
- Familiar Unix verbs and flags.
- Pipe-first UX: stdout by default, machine formats, NUL separators, stdin support.
- Deterministic addressing of resources via paths, globs, stable IDs, and normalized slugs.
- Optimistic concurrency on writes via bigint ETag.
- Explicit attribution of actions to actors (human + coding agents).
- Single local SQLite DB file (WAL), suitable for concurrent CLIs and agents.
- Stable machine interfaces (`--json`, `--ndjson`, `--porcelain`) to enable:
  - scripting, and
  - a future browser UI (Node/React) over HTTP/JSON.

Non-goals (for now)
- Multi-user auth/SSO, teams, shared projects.
- Named/saved filters and exports (simple `find` is in; richer DSL and `rg` are future).
- Calendar sync and recurrence engines.
- FUSE mount.
- Shipping the browser UI itself (only enabling it via stable contracts).

-------------------------------------------------------------------------------

## 2. Codebase Boundaries

Repo layout
```text
cmd/wrkq           cobra root and subcommands
internal/cli       command wiring, flags, args
internal/config    env + .env.local + ~/.config/todo
internal/db        SQLite handle, migrations runner (reads db/migrations)
internal/domain    domain types, validation, etag helpers
internal/id        friendly IDs, slug helpers
internal/paths     pathspec and glob resolver
internal/render    table, json, ndjson, yaml, tsv; porcelain
internal/edit      3-way merge for edit/apply
internal/attach    attachment path resolution and IO
internal/actors    actor resolution (human vs agent), current actor
internal/events    event log write/read helpers
Justfile, goreleaser.yml, CI workflows
```

- CLI consumes DB migrations from `../db/migrations`.
- DB is a single SQLite file on local disk; WAL mode on by default.
- A future HTTP/JSON service (Go or Node) and browser UI are **consumers** of the same DB + machine contracts; they are not defined here but must be supportable.

-------------------------------------------------------------------------------

## 3. Terminology

- **Actor**: entity performing actions via the CLI (human user or coding agent).
- **Actor Role**: `human` | `agent` | `system` (system = migrations, init, repair).
- **Actor Slug**: filesystem-style identifier for an actor (e.g. `lance`, `agent-codex`).
- **Project**: top-level container. Can contain subprojects and tasks.
- **Subproject**: nested container under a project (arbitrary depth).
- **Task**: actionable item. Appears as a leaf under a project/subproject.
- **Slug**: normalized, safe segment used in paths, unique among siblings.
- **Friendly ID**: human-readable stable ID, e.g. `P-00007`, `T-00123`, `A-00001`.
- **UUID**: canonical database ID.
- **ETag**: bigint/integer version field for concurrency. Increments on each write.
- **Attachment**: reference to a file on the host filesystem under a configured `attach_dir`.

### Slug Rules (Global)

Slugs are **normalized and constrained**:

- Always lower-case.
- Allowed characters: `a-z`, `0-9`, `-`.
- Must start with `[a-z0-9]`.
- Regex: `^[a-z0-9][a-z0-9-]*$`
- Max length: 255 bytes.
- Unique among siblings within the same container.

No underscores, no spaces, no dots, no `/`, no NUL. Path matching is straightforward because slugs are already canonicalized.

-------------------------------------------------------------------------------

## 4. Information Architecture (user-visible)

### 4.1 Actor

Represents either a human user, coding agent, or system process.

Fields
- `uuid`
- `id` (friendly, e.g. `A-00001`)
- `slug` (fs-safe, normalized; unique)
- `display_name` (free text, optional)
- `role` (`human` | `agent` | `system`)
- `meta` (JSON, optional)
  - `tool_name`, `tool_version`
  - `model_name`, `model_version`
  - `run_id` / `correlation_id`
- `created_at`, `updated_at`

**Current actor resolution (for all mutating commands):**

1. `--as <actor>` CLI flag  
   - Accepts actor slug (e.g. `lance`) **or** friendly ID (e.g. `A-00001`).
2. `WRKQ_ACTOR_ID` env (friendly ID, e.g. `A-00001`).
3. `WRKQ_ACTOR` env (actor slug, e.g. `lance` or `agent-codex`).
4. `default_actor` in `~/.config/wrkq/config.yaml` or current context.
5. Fallback to a seeded default human actor created by `wrkq init` (e.g. `local-human`).

If resolution fails, mutating commands should exit with a usage error (code 2).

### 4.2 Container (Project / Subproject)

Single table with parent-child relationship.

Fields
- `uuid`
- `id` (friendly, `P-xxxxx`)
- `slug` (fs-safe, normalized; unique among siblings)
- `title` (optional, defaults to slug)
- `parent_id` (nullable; null = top-level project)
- `etag` (bigint)
- `created_at`, `updated_at`, `archived_at` (nullable)
- `created_by_actor_id` (FK Actor)
- `updated_by_actor_id` (FK Actor)

### 4.3 Task

Fields
- `uuid`
- `id` (friendly, `T-xxxxx`)
- `slug` (fs-safe, normalized; unique among siblings within same container)
- `title` (free text)
- `project_id` (FK to container)
- `state` (`open` | `completed` | `archived`)
- `priority` (1..4, 1 is highest)
- `start_at` (nullable), `due_at` (nullable)
- `labels` (JSON array of strings)
- `body` (Markdown text)
- `etag` (bigint)
- `created_at`, `updated_at`, `completed_at` (nullable)
- `created_by_actor_id` (FK Actor)
- `updated_by_actor_id` (FK Actor)

### 4.4 Comment

Represents an immutable (append‑only) note attached to a task, authored by a human or agent actor.

Comments are first‑class resources with friendly IDs, event log entries, and a machine‑friendly JSON shape. They are intended as the primary collaboration channel between humans and coding agents.

Fields
- `uuid`
- `id` (friendly, `C-xxxxx`)
- `task_id` (FK Task)
- `actor_id` (FK Actor; author of the comment)
- `body` (Markdown text)
- `meta` (JSON, optional)
  - For agents and tools to attach structured information:
    - `run_id`
    - `tool_name`, `tool_version`
    - `model_name`, `model_version`
    - `thread` / `kind` / other correlation tags
- `etag` (bigint)
- `created_at`
- `updated_at` (nullable; reserved for future editable comments)
- `deleted_at` (nullable)
- `deleted_by_actor_id` (FK Actor, nullable)

Invariants

- Comments are **immutable in v1**:
  - After creation, `body` and `meta` are not modified.
  - `updated_at` remains `NULL`; `etag` is only incremented on delete/undelete if those are implemented.
  - “Edit” is modeled as a new comment referencing the prior one (if desired) via `meta`.
- Soft delete:
  - Setting `deleted_at` (and `deleted_by_actor_id`) hides the comment in default human views.
  - Soft‑deleted comments remain in the DB for auditability and machine access.
- Hard delete (purge) is optional and only used by explicitly destructive commands (see `wrkq comment rm --purge`).

Event log integration

- All comment changes write to the canonical event log (see 4.6):
  - `resource_type`: `comment`
  - `resource_id`: comment `uuid`
  - Event types:
    - `comment.created`
    - `comment.deleted` (soft delete)
    - `comment.purged` (hard delete, if implemented)
  - `payload` (JSON) SHOULD include:
    - `task_id`
    - `comment_id` (friendly, e.g. `C-00012`)
    - `actor_id`
    - For `comment.deleted` / `comment.purged`, flags indicating soft vs hard delete.

### 4.5 Attachment (Filesystem-based)

DB tracks metadata; bytes live under `attach_dir`.

Fields
- `id`
- `task_id`
- `filename` (user-facing name)
- `relative_path` (relative to `attach_dir`, e.g. `tasks/<task_uuid>/diagram.png`)
- `mime_type`
- `size_bytes`
- `checksum` (optional)
- `created_at`
- `created_by_actor_id`

**Attachment path recommendation (chosen approach):**

- Canonical directory: `attach_dir/tasks/<task_uuid>/...`
- Task moves/slug changes **do not** alter attachment paths.
- Soft delete:
  - Attachment metadata remains.
  - Files remain on disk.
- Purge:
  - Attachment metadata removed.
  - Directory `attach_dir/tasks/<task_uuid>` and its contents are deleted.

### 4.6 Event Log (Canonical Audit Trail)

Append-only event stream for all significant changes.

Fields (conceptual)
- `id`
- `timestamp`
- `actor_id`
- `resource_type` (`task` | `container` | `attachment` | `comment` | `actor` | `config` | `system`)
- `resource_id` (UUID)
- `event_type` (e.g. `task.created`, `task.updated`, `task.deleted`, `task.moved`)
- `etag` (resulting etag after change, if applicable)
- `payload` (JSON diff or structured fields)

`wrkq log` and `wrkq watch` are views onto this table.

### 4.7 Constraints Summary

- Slugs: normalized, `[a-z0-9-]`, lower-case, unique among siblings.
- No sections in the addressing model; only containers (projects/subprojects) and tasks.
- Tasks live directly under their container.

-------------------------------------------------------------------------------

## 5. Addressing and Pathspecs

Resource references accepted by all commands:

1. **Container paths** (implicit container namespace, no prefix):

   Grammar  
   ```text
   project-slug[/subproject-slug[/...[/task-slug]]]
   ```

   Examples
   - `portal`
   - `portal/auth`
   - `portal/auth/login-ux` (file-like commands treat leaf as task)

2. **IDs**
   - Friendly: `P-00007`, `T-00123`, `A-00001`
   - UUIDs: standard UUID format

3. **Typed task selector** (optional): `t:<token>`
   - `<token>` is a friendly ID, UUID, or container path that resolves to a task.

4. **Typed comment selector** (optional): `c:<token>`
   - `<token>` can be a friendly comment ID (`C-00012`), UUID, or any future short handle for comments.
   - Accepted wherever a comment ID is required (see `wrkq comment` commands).

### Globbing

- Supported patterns: `*`, `?`, `**`. Implemented inside the CLI; users should quote patterns to avoid shell expansion.
- Examples:
  - `wrkq ls 'portal/**/login-*' -type t`
  - `wrkq mv 'portal/**/login-*' 'portal/auth' -type t`

### Disambiguation

- File-like commands (`cat`, `stat`, `set`, `apply`, `edit`) treat the final segment as a **task** by default.
- Directory-like commands (`ls`, `tree`, `find`, `mkdir`) treat the final segment as a **container** by default.
- Override using `-type p|s|t`.

### Zero Matches

- By default, zero matches cause a non-zero exit (like coreutils).
- `--nullglob` changes zero matches to a no-op for bulk operations.

-------------------------------------------------------------------------------

## 6. Output, Piping, and Exit Codes

Formats
- Human defaults:
  - Pretty table for `ls`.
  - Markdown for `cat`.
- Machine formats:
  - `--json` (array or object)
  - `--ndjson` (one JSON object per line)
  - `--yaml`
  - `--tsv`
  - `--columns=field,field,...`

Selection and sort
- `--fields=id,path,type,slug,title,state,priority,labels,due,created_at,updated_at,project,parent,created_by,updated_by,actor,etag`
- `--sort=due,-priority,updated_at`

Delimiters
- `-1` one per line
- `-0` NUL separated (xargs `-0`)

Porcelain
- `--porcelain` disables ANSI, stabilizes keys/columns, and prints `next_cursor` on stderr where applicable.

Pagination
- `--limit`, `--cursor` opaque cursor support.

Exit codes
- `0` success
- `1` generic error (db, io)
- `2` usage error (bad flags, args)
- `3` not found (no matches)
- `4` conflict (etag mismatch, merge conflict)
- `5` partial success (with `--continue-on-error`)

-------------------------------------------------------------------------------

## 7. Concurrency, Transactions, and Conflicts

### SQLite Mode

- DB opened in WAL mode.
- `busy_timeout` and retry policy are configured so multiple agents/CLIs can run concurrently.

### ETag Semantics

- Each mutable row has `etag INTEGER` (bigint semantics).
- **All mutating commands** operate as:

  1. Begin transaction.
  2. Read target row(s), including current `etag`.
  3. If `--if-match <etag>` provided:
     - If current etag != provided etag, abort with exit code `4`.
  4. Apply changes.
  5. Increment `etag` on each affected row.
  6. Insert corresponding event(s) into the event log.
  7. Commit transaction.

- `edit` commands do a 3-way merge (base, current, edited). Unresolvable conflicts exit `4`.

-------------------------------------------------------------------------------

## 8. Task Document Format (cat/apply/edit)

Default `wrkq cat` output for tasks is Markdown with YAML front matter:

```markdown
---
id: T-00123
uuid: 2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1
project_id: P-00007
project_uuid: 4e44b1fb-6a3e-4c6d-9d80-0d41e742cda9
slug: login-ux
title: Login UX
state: open
priority: 1
start_at: 2025-11-19T00:00:00Z
due_at: 2025-11-20T15:00:00Z
labels:
  - backend
etag: 47
created_at: 2025-11-18T10:15:00Z
updated_at: 2025-11-18T10:20:00Z
created_by: lance
updated_by: agent-codex
---

Problem statement and acceptance criteria...
```

Options
- `--no-frontmatter` prints body only.
- `--raw-body` prints body without front matter boundaries.
- `--include-comments` appends a **read‑only comments block** after the body. This block is primarily for human consumption; agents and tools SHOULD use `wrkq comment ls --json` instead of parsing it.

### Comments block layout (`--include-comments`)

When `--include-comments` is set, `wrkq cat` renders comments as a read‑only block following the task body:

```markdown
---
id: T-00123
uuid: 2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1
project_id: P-00007
project_uuid: 4e44b1fb-6a3e-4c6d-9d80-0d41e742cda9
slug: login-ux
title: Login UX
state: open
priority: 1
start_at: 2025-11-19T00:00:00Z
due_at: 2025-11-20T15:00:00Z
labels:
  - backend
etag: 47
created_at: 2025-11-18T10:15:00Z
updated_at: 2025-11-18T10:20:00Z
created_by: lance
updated_by: agent-codex
---

Task body here...

---

<!-- wrkq-comments: do not edit below -->

> [C-00012] [2025-11-19T10:01:31Z] lance (human)
> Investigating login 500s…

> [C-00013] [2025-11-19T10:05:02Z] agent-codex (agent)
> Proposed fix in branch `feature/login-rate-limit`. See run_id=abc123.
```

Rules

- The comments block is always separated from the body by:
  - A horizontal rule (`---`) and
  - A sentinel HTML comment: `<!-- wrkq-comments: do not edit below -->`.
- Each comment is rendered as:
  - A single header line, then one or more quoted lines:
    - Header:  
      `> [<comment-id>] [<ISO8601 timestamp>] <actor-slug> (<actor-role>)`
    - Body: subsequent lines in the same `>` block.
- Only non‑deleted comments are included by default.
- The comments block is **ignored** by `wrkq apply`:
  - Comment lines are not parsed or written back to the DB.
  - Comments are write‑only via dedicated comment commands.

`wrkq apply` accepts:

- Markdown with front matter.
- YAML.
- JSON.

`--body-only` updates only the body without touching metadata.

-------------------------------------------------------------------------------

## 9. Command Spec

### 9.1 Global Behavior

- All subcommands accept pathspecs, friendly IDs, or UUIDs unless stated otherwise.
- Commands that take lists accept `-` to read newline or NUL-separated items from stdin.
- Most commands support `--json`, `--ndjson`, `--yaml`, `--tsv`, `--columns=...`, `--porcelain`.
- Actor selection (for mutating commands):
  - `--as <actor>` overrides env/config.
  - Uses actor resolution rules defined above.

---

### 9.2 Initialization

- `wrkq init [--db <path>] [--actor-slug <slug>] [--actor-name <display>] [--attach-dir <path>]`

Behavior
- If DB does **not** exist:
  - Create SQLite DB file.
  - Enable WAL mode, set pragmas.
  - Run migrations.
  - Ensure `attach_dir` exists (from flag, env, or config).
  - Seed:
    - A top-level project (e.g. slug `inbox`).
    - A default human actor:
      - slug from `--actor-slug` if provided, else `local-human`.
      - display_name from `--actor-name` if provided.
    - Optionally a default agent actor (e.g. `agent-default`) for convenience.
- If DB **does** exist:
  - Run migrations.
  - Do not re-seed projects or actors.
  - Exit 0 with “already initialized” messaging.

Output
- Human-readable summary by default.
- `--json` prints DB path, attach_dir, and seeded actor/project IDs.

---

### 9.3 Navigation and Metadata

- `wrkq ls [PATHSPEC...]`
  - List containers and tasks.
  - Flags: `-l`, `-R`, `-a` (include archived),  
    `--fields`, `--sort`,  
    `--json|--ndjson|--yaml|--tsv`, `-1`, `-0`,  
    `-type p|s|t`, `--limit`, `--cursor`, `--porcelain`.

- `wrkq tree [PATHSPEC...]`
  - Pretty tree of containers and tasks.
  - Flags: `-a`, `-L <depth>`, `--fields`, `--porcelain`.

- `wrkq stat <PATHSPEC|ID...>`
  - Print metadata only (machine friendly).
  - Flags: `--json`, `--fields`, `-0`, `--porcelain`.

- `wrkq ids [PATHSPEC...]`
  - Print canonical IDs only.
  - Flags: `-0`, `-type`.

- `wrkq resolve <QUERY...>`
  - Resolve fuzzy names to canonical IDs and paths.
  - Flags: `--json`, `--ids`, `-0`.

---

### 9.4 Content

- `wrkq cat <PATHSPEC|ID...>`
  - Print tasks as markdown with front matter (default).
  - If argument resolves to a **container**, this is a usage error:
    - Exit code: `2`.
    - Error message: “cat only supports tasks; got container `<path>`”.
  - Flags: `--no-frontmatter`, `--raw-body`, `--include-comments`.

- `wrkq edit <PATHSPEC|ID>`
  - Open in `$EDITOR`; save triggers 3-way merge and `--if-match` check.
  - Uses current actor for attribution.

- `wrkq apply [<PATHSPEC|ID>] [-]`
  - Apply full task doc from file or stdin.
  - Flags: `--format=md|yaml|json`, `--body-only`, `--if-match`, `--dry-run`.

- `wrkq set <PATHSPEC|ID...> key=value [...]`
  - Mutate task fields quickly:
    - `state=completed`
    - `priority=1`
    - `title="New Title"`
    - `slug=login-ux` (validated against slug rules)
  - Flags: `--if-match`, `--dry-run`, `--continue-on-error`, `-type t`.

---

### 9.5 Structure and Lifecycle

- `wrkq mkdir <PATH...>`
  - Create projects/subprojects (containers).
  - Last segment is treated as container slug and normalized.
  - Flags: `-p` (parents), `--meta key=val`.

- `wrkq touch <PATH...> [-t "Title"]`
  - Create tasks at leaf containers.
  - Last segment becomes task slug, normalized to lower-case `[a-z0-9-]`.
  - Flags: `--meta key=val`.

- `wrkq mv <SRC...> <DST>`
  - Move or rename tasks and containers. Globbing is default.
  - Rules:
    - Multiple sources -> DST must be an existing container; sources move into DST.
    - Single source:
      - If DST path resolves to existing container: move into container.
      - If DST does not exist: treat final segment as new slug (rename).
      - If DST is an existing task: error unless `--overwrite-task`.
  - Flags: `-type t`, `--if-match`, `--dry-run`, `--yes`, `--nullglob`, `--overwrite-task`.

- `wrkq cp <SRC...> <DST>`  (optional, but spec’d)
  - Duplicate tasks (and optionally attachments).
  - Flags: `-r`, `--with-attachments`, `--shallow`, `--dry-run`, `--yes`.

- `wrkq rm <PATHSPEC|ID...>`
  - Archive or hard delete.
  - Defaults:
    - Soft delete: set `archived_at`, log event.
    - Attachments:
      - Soft delete → attachment metadata and files remain.
  - Flags:
    - `-r` (recursive for containers),
    - `-f`, `--yes`, `--dry-run`, `--nullglob`,
    - `--purge`:
      - Hard delete rows.
      - Delete attachment directory `attach_dir/tasks/<task_uuid>`.

- `wrkq restore <PATHSPEC|ID...>`
  - Unarchive archived nodes (containers and tasks).

---

### 9.6 Search and Discovery

- `wrkq find [PATH...]`
  - Simple metadata search (present).
  - Flags:
    - `-type p|s|t`
    - `--slug-glob <glob>` (slug-only, uses normalized slug rules)
    - `--state`
    - `--due-before`, `--due-after`
    - `--print0`
    - `--limit`, `--cursor`, `--porcelain`

- `wrkq rg <pattern> [PATHSPEC...]` (Future enhancement)
  - Not required for initial implementation.
  - Intended behavior (for future compatibility):
    - Content search across task bodies and optionally comments.
    - Output (for `--json`):
      - `task_id`, `path`, `line`, `column`, `snippet`.
    - Backend may be:
      - SQLite FTS, or
      - external `rg` integration over an exported view.

---

### 9.7 History, Diff, Streaming

- `wrkq log <PATHSPEC|ID>`
  - Show change history from the event log.
  - Each entry:
    - timestamp
    - actor (slug, friendly ID)
    - resource_type
    - event_type
    - etag
    - short payload summary
  - Flags: `--since`, `--until`, `--oneline`, `--patch`.

- `wrkq diff <A> [B]`
  - Compare two tasks, or working copy vs DB.
  - Flags: `--unified=3`, `--json`.

- `wrkq watch [PATH...]`
  - Stream change events from the event log.
  - Flags: `--since <cursor>`, `--ndjson`.

---

### 9.8 Attachments

- `wrkq attach ls <PATHSPEC|ID>`
  - List attachments for a task.
  - Output: `id`, `filename`, `relative_path`, `size_bytes`, `mime_type`.

- `wrkq attach get <ATTACHMENT-ID> [--as <path>]`
  - Copy the referenced file from `attach_dir` to the given path or stdout (`-`).

- `wrkq attach put <PATHSPEC|ID> <FILE|-> --mime <type> [--name <filename>]`
  - Attach a file to a task.
  - Implementation:
    - Use canonical directory `attach_dir/tasks/<task_uuid>/`.
    - Copy file into that directory.
    - Record metadata and `relative_path`.

- `wrkq attach rm <ATTACHMENT-ID...>`
  - Remove attachment metadata and delete corresponding file.
  - Does **not** affect the rest of the task.

---

### 9.9 Actor Management

- `wrkq whoami`
  - Prints the current actor (slug, friendly id, role) and DB path.

- `wrkq actors ls`
  - List all actors.
  - Flags: `--json`, `--ndjson`, `--fields`, `--porcelain`.

- `wrkq actor add <slug> [--name <display>] [--role human|agent|system]`
  - Create a new actor (primarily for registering agents).
  - Enforce slug normalization rules.

---

### 9.10 Housekeeping & Misc

- `wrkq doctor`
  - Checks DB (pragmas, WAL), migrations, config, `attach_dir`, attachment limits.
  - Prints remediation suggestions.

- `wrkq version`
  - Prints version information and build metadata.
  - `--json` includes:
    - `version`
    - `commit`
    - `build_date`
    - `machine_interface_version`
    - list of supported commands and formats.

- `wrkq completion bash|zsh|fish`
  - Emits completion scripts.

- `wrkq config doctor`
  - Prints effective configuration and source.


### 9.11 Comments

Commands for listing, creating, inspecting, and removing comments on tasks. Comments are immutable in v1; removal is modeled via soft‑ or hard‑delete.

All mutating commands use normal actor resolution (`--as`, env, config) and write `comment.*` events to the event log.

- `wrkq comment ls <TASK-PATH|t:<id>...>`
  - List comments attached to one or more tasks.
  - Resolves each argument to a single task (container path or `t:<token>`).
  - By default, returns only non‑deleted comments ordered by `created_at` ascending.
  - Flags:
    - `--json|--ndjson|--yaml|--tsv`
    - `--fields=...`
    - `--include-deleted`
    - `--limit N`, `--cursor CURSOR`, `--porcelain`
    - `--sort=created_at`, `--reverse`

- `wrkq comment add <TASK-PATH|t:<id>> [FILE|-]`
  - Create a new comment on a task.
  - Comment text comes from `-m/--message`, from `FILE`, or from stdin when `-` is used.
  - Flags:
    - `-m, --message <text>`
    - `--meta <json>` (JSON object to populate the `meta` field)
    - `--if-match <task-etag>` (optimistic concurrency on the task)
    - `--as <actor>` (override actor resolution)
    - `--dry-run`
  - On success, prints the new comment ID and timestamp; with `--json`, returns a structured object including `id`, `uuid`, `task_id`, `actor_slug`, `created_at`, and `etag`.

- `wrkq comment cat <COMMENT-ID|c:<token>...>`
  - Show one or more comments.
  - By default, prints a header line (ID, timestamp, actor, task) and body for each comment, separated by `---`.
  - Flags:
    - `--json|--ndjson`
    - `--raw` (body only, separated by `---`)
  - Resolves `COMMENT-ID` as a friendly ID (`C-xxxxx`) or UUID; `c:<token>` uses the typed comment selector.

- `wrkq comment rm <COMMENT-ID|c:<token>...>`
  - Soft‑delete or hard‑delete comments.
  - By default, soft‑deletes: sets `deleted_at` and `deleted_by_actor_id`, increments `etag`, and logs `comment.deleted`.
  - `--purge` performs a hard delete and logs `comment.purged`.
  - Flags:
    - `--yes`
    - `--dry-run`
    - `--purge`
    - `--if-match <etag>`
  - Soft‑deleted comments are hidden from `wrkq comment ls` unless `--include-deleted` is set, and from `wrkq cat --include-comments`.

-------------------------------------------------------------------------------

## 10. Configuration

Precedence
1. Flags
2. Environment variables
3. `./.env.local` (dotenv)
4. `~/.config/wrkq/config.yaml` (YAML)

Env vars
- `WRKQ_DB_PATH` (path to SQLite DB file)
- `WRKQ_DB_PATH_FILE` (file containing path)
- `WRKQ_LOG_LEVEL`
- `WRKQ_OUTPUT` (`table|json`)
- `WRKQ_PAGER`
- `WRKQ_ATTACHMENTS_MAX_MB`
- `WRKQ_ATTACH_DIR` (base directory for attachments)
- `WRKQ_ACTOR` (actor slug)
- `WRKQ_ACTOR_ID` (friendly actor ID, e.g. `A-00001`)

YAML example
```yaml
db_path: /home/user/.local/share/wrkq/wrkq.db
attach_dir: /home/user/.local/share/wrkq/attachments
attachments_max_mb: 50

default_actor: lance
contexts:
  local:
    db_path: /home/user/.local/share/wrkq/wrkq.db
    default_actor: lance
  stage:
    db_path: /home/user/projects/todo-stage.db
    default_actor: agent-stage
```

`wrkq config doctor` shows effective values and their sources.

-------------------------------------------------------------------------------

## 11. Packaging and Installation

- Build static binaries via GoReleaser for linux, macOS, windows (amd64 and arm64).
- Artifacts: tar/zip, checksums, SBOM, shell completions.
- Install:
  - `install.sh` places binary to `~/.local/bin` or `/usr/local/bin` and installs completions.
  - `install.ps1` optional for Windows.
- No Homebrew packaging (for now).

-------------------------------------------------------------------------------

## 12. Performance and Reliability

- Listing up to ~5k open tasks under 200 ms p95 on a typical dev machine.
- NDJSON streams for constant memory pipelines.
- Opaque cursors for pagination on large sets.

Parallelism knobs
- `--batch-size` and `-j/--jobs N` for bulk ops.
- `--ordered` to preserve input order when needed.

SQLite expectations
- WAL mode enabled.
- Recommended indices:
  - containers (`parent_id`, `slug`)
  - tasks (`project_id`, `slug`)
  - tasks (`state`, `due_at`)
  - tasks (`updated_at`)
- For heavy use, periodic `VACUUM` / `ANALYZE` recommended.

Target scale
- Designed for a single user + multiple agents, up to ~50–100k tasks total.
- Beyond that, manual maintenance or archival strategies may be required.

-------------------------------------------------------------------------------

## 13. Safety and Dry Runs

- `--dry-run` prints plans for `mv`, `cp`, `rm`, `set`, `apply`.
- `--yes` skips confirmation prompts on destructive actions.
- `--nullglob` turns unmatched patterns into no-ops (default is error).
- Conflict-safe writes via `--if-match <etag>`.
- All writes are attributed to a resolved actor; the event log is the canonical audit trail.

-------------------------------------------------------------------------------

## 14. Examples

List, pipe, preview
```sh
# Open tasks under portal, print one per line, cat them
wrkq ls 'portal/**' -type t -1 | xargs -n1 wrkq cat | less
```

Bulk priority change
```sh
wrkq ls 'portal/**' -type t --json \
| jq -r '.[] | select(.state=="open" and .due <= "2025-12-01") | .id' \
| xargs -n50 wrkq set priority=1
```

Edit via sed as an agent
```sh
# Example pipeline; 'rg' here is hypothetical until wrkq rg is implemented
wrkq find 'customer-portal/**' -type t --slug-glob '*oauth*' --json \
| jq -r '.[].id' \
| xargs -n1 -I{} sh -c '
  etag=$(wrkq stat {} --json | jq -r .etag);
  wrkq cat {} --raw-body \
  | sed "s/2fa/mfa/g" \
  | wrkq apply {} - --body-only --if-match "$etag" --as agent-codex
'
```

Move with globbing
```sh
# Move all login-* tasks into auth subproject
wrkq mv 'portal/**/login-*' 'portal/auth' -type t --dry-run
wrkq mv 'portal/**/login-*' 'portal/auth' -type t --yes
```

Initialize DB and actors
```sh
wrkq init --db ~/.local/share/wrkq/wrkq.db \
  --actor-slug lance \
  --actor-name "Lance (human)" \
  --attach-dir ~/.local/share/wrkq/attachments

wrkq whoami
wrkq actors ls
```

Attachments
```sh
wrkq attach put 'portal/auth/login-ux' ./specs/login-flow.pdf \
  --mime application/pdf --name "Login Flow Spec"

wrkq attach ls 'portal/auth/login-ux'
wrkq attach get ATT-00012 --as ./out/login-flow.pdf
```

-------------------------------------------------------------------------------

## 15. Security and Privacy

- Local configuration may include DB paths and actor slugs; support reading DB path from `WRKQ_DB_PATH_FILE`.
- Attachments live under `attach_dir`; DB stores only relative paths and metadata.
- No network calls beyond local SQLite file I/O (and any external tools users explicitly invoke).
- No telemetry in default build.
- Actor attribution is for **provenance**, not auth.

-------------------------------------------------------------------------------

## 16. Compatibility Guarantees

- **Machine interfaces**:
  - `--porcelain`, `--json`, `--ndjson`, `--tsv`, `--columns` field names and order are stable across minor versions.
  - Existing fields are never removed in a minor version; new fields may be added.
- **Exit codes** are stable.
- **Path semantics** and normalized slug rules are stable.
- **Actor resolution precedence** (`--as`, env, config, default) is stable once defined.
- `machine_interface_version` (from `wrkq version --json`) increments only on breaking changes.




## 17. Branch/PR Sync Commands (New)

These commands formalize the Git‑ops workflow: agents work on ephemeral DB snapshots, export a **bundle** to version control, and on merge the bundle is applied into the authoritative `main` DB with strict `--if-match` guards. They build on the spec’s typed selectors (`t:<token>`), append‑only event log, and attachment directory conventions.  [oai_citation:1‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

### 17.1 `wrkq bundle create`

Create a portable, deterministic bundle of changes suitable to commit in a PR.

**Synopsis**
```
wrkq bundle create [--out <dir>] [--actor <slug|A-xxxxx>] [--since <ts>] [--until <ts>] \
  [--with-attachments] [--no-events] [--json|--porcelain]
```

**Behavior**
- Writes a bundle directory (default: `.wrkq/`) containing:
  - `manifest.json` (includes `machine_interface_version`, build/version info).  [oai_citation:2‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
  - `events.ndjson` (slice of the canonical audit log for review/debug; optional).  [oai_citation:3‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
  - `containers.txt` (containers to ensure exist).
  - `tasks/<path>.md` for each changed task: **exact** `wrkq cat` output plus helper keys `path` and `base_etag`. Unknown keys are ignored by core commands.  [oai_citation:4‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
  - `attachments/<task_uuid>/*` for changed tasks when `--with-attachments` is set, following `attach_dir/tasks/<task_uuid>/…`.  [oai_citation:5‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- Computes `base_etag` per task from the earliest included event for that task to enable `--if-match` on import. (ETag semantics: exit **4** on mismatch.)  [oai_citation:6‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

**Flags**
- `--out <dir>`: target directory (default `.wrkq/`).
- `--actor <slug|A-xxxxx>`: filter changes by actor for agent‑specific bundles.  [oai_citation:7‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- `--since/--until`: time window over the event log.  [oai_citation:8‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- `--with-attachments`: include attachment payloads for changed tasks.  [oai_citation:9‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- `--no-events`: omit `events.ndjson` (snapshot‑only bundle).

**Output**
- Human summary by default; `--json` prints counts and paths.

---

### 17.2 `wrkq bundle apply`

Apply a bundle into the current DB (e.g., `main`) with conflict detection and attachment re‑hydration.

**Synopsis**
```
wrkq bundle apply [--from <dir>] [--dry-run] [--continue-on-error] [--json|--porcelain]
```

**Behavior**
- Reads `manifest.json` and validates `machine_interface_version` against `wrkq version --json`.  [oai_citation:10‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- Ensures containers listed in `containers.txt` exist (`mkdir -p`).  [oai_citation:11‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- For each `tasks/<path>.md`:
  - Prefer selector `t:<uuid>`; fallback to `t:<path>` if new.  [oai_citation:12‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
  - If `base_etag` is present, perform `wrkq apply … --if-match <base_etag>`. On mismatch return **4** and show a `wrkq diff` to aid resolution.  [oai_citation:13‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- Re‑attach files from `attachments/<task_uuid>/…` via `wrkq attach put`.  [oai_citation:14‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

**Flags**
- `--from <dir>`: bundle root (default `.wrkq/`).
- `--dry-run`: prepare and validate without writing.
- `--continue-on-error`: attempt remaining items after an error.

**Exit codes**
- `0` success; `4` if any conflicts (ETag mismatch or merge conflict); see global exit codes.  [oai_citation:15‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### 17.3 `wrkq db snapshot`

Produce a **WAL‑safe** snapshot of the current SQLite DB for agents/CI to use as an ephemeral working copy.

**Synopsis**
```
wrkq db snapshot --out <path> [--json]
```

**Behavior**
- Uses SQLite’s online backup to write a consistent point‑in‑time copy of the DB to `<path>`; no WAL/SHM files required. (Wrkq uses a single local SQLite DB in WAL mode.)  [oai_citation:16‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- Prints a small JSON manifest (timestamp, source DB path, `machine_interface_version`) with `--json`.  [oai_citation:17‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- **Does not** copy attachments; agents should use branch‑scoped `WRKQ_ATTACH_DIR` and export needed files in bundles.  [oai_citation:18‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

**Use with env**
- Agents set `WRKQ_DB_PATH` to the snapshot and `WRKQ_ACTOR` to a branch‑scoped slug to preserve attribution.  [oai_citation:19‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### 17.4 `wrkq apply --base <FILE>` (flag extension)

Enable a 3‑way merge on apply (mirrors `edit` semantics) while still honoring `--if-match`.

**Synopsis**
```
wrkq apply [<PATHSPEC|ID>] <FILE|-> --base <FILE> [--if-match <etag>] [--dry-run]
```

**Behavior**
- Performs a structured 3‑way merge of front‑matter + body using `internal/edit` machinery (already used by `edit`), then applies. Conflicts exit **4**.  [oai_citation:20‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)
- If `--if-match` is provided, it is enforced before writing, consistent with global concurrency rules.  [oai_citation:21‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### 17.5 `wrkq bundle replay` (optional / future)

Replay `events.ndjson` from a bundle into the DB (useful when you know `main` hasn’t diverged).

**Synopsis**
```
wrkq bundle replay [--from <dir>] [--dry-run] [--strict-etag]
```

**Behavior**
- Reapplies events in order; with `--strict-etag`, each event is verified against stored ETags and exits **4** on divergence (leveraging the canonical audit model).  [oai_citation:22‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### 17.6 `wrkq attach path` (helper)

Expose the absolute on‑disk path for an attachment (useful for exporters).  

**Synopsis**
```
wrkq attach path <ATTACHMENT-ID|relative_path> [--json|--porcelain]
```

**Behavior**
- Resolves to the absolute file under `attach_dir/tasks/<task_uuid>/…` without copying; prints a stable path for tooling. (Attachment layout is already defined and stable.)  [oai_citation:23‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### Notes & Compatibility

- All new commands adhere to existing **addressing** (`t:<token>`) and **exit code** conventions; no breaking changes to machine interfaces.  [oai_citation:24‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)  
- Bundles intentionally use the **task document** format emitted by `wrkq cat` and consumed by `wrkq apply`, ensuring deterministic round‑trips and reviewability in Git.  [oai_citation:25‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)

---

### Minimal Examples

```sh
# Create a PR bundle for the current branch actor, including attachments
wrkq bundle create --actor agent-feature --with-attachments

# Validate a PR bundle against a copy of main (no writes)
wrkq bundle apply --from .wrkq --dry-run

# Snapshot the DB for an agent’s ephemeral workspace
wrkq db snapshot --out /tmp/wrkq.$BRANCH.db
export WRKQ_DB_PATH=/tmp/wrkq.$BRANCH.db
export WRKQ_ATTACH_DIR=/tmp/wrkq.$BRANCH.attach
export WRKQ_ACTOR=agent-$BRANCH

# Apply with a 3-way merge using an explicit base doc
wrkq apply t:T-00123 new.md --base base.md --if-match 47
```

All behaviors above are consistent with the spec’s ETag concurrency model, event log guarantees, and attachment paths.  [oai_citation:26‡SPEC.md](sediment://file_000000000494720ca24c13c2ec0a827c)