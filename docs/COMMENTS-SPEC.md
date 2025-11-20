Here’s a drop‑in replacement for the **comments** bits of the spec, written in the same voice and structure. You can paste these into `SPEC.md` to replace/augment the current comment section.  [oai_citation:0‡SPEC.md](sediment://file_00000000153871f5bb95d8d8540b4b29)  

---

## 4.4 Comment (replace existing 4.4)

Represents an immutable (append‑only) note attached to a task, authored by a human or agent actor.

Comments are first‑class resources with friendly IDs, event log entries, and a machine‑friendly JSON shape. They are intended as the primary collaboration channel between humans and coding agents.

**Fields**

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

**Invariants**

- Comments are **immutable in v1**:
  - After creation, `body` and `meta` are not modified.
  - `updated_at` remains `NULL`; `etag` is only incremented on delete/undelete if those are implemented.
  - “Edit” is modeled as a new comment referencing the prior one (if desired) via `meta`.
- Soft delete:
  - Setting `deleted_at` (and `deleted_by_actor_id`) hides the comment in default human views.
  - Soft‑deleted comments remain in the DB for auditability and machine access.
- Hard delete (purge) is optional and only used by explicitly destructive commands (see `wrkq comment rm --purge`).

**Event log integration**

All comment changes write to the canonical event log (see 4.6):

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

---

### 5.x Typed Comment Selector (add under “5. Addressing and Pathspecs”)

In addition to the typed task selector `t:<token>`, comments have a typed selector for CLI and API use.

**Typed comment selector** (optional): `c:<token>`

- `<token>` can be:
  - Friendly ID (`C-00012`),
  - UUID, or
  - Any future short handle for comments.
- Accepted wherever a comment ID is required:
  - `wrkq comment cat c:C-00012`
  - `wrkq comment rm c:3d7c5c2a-…`

Resolution and error semantics mirror the typed task selector.

---

### 8.x Task Document Comments Block (replace the `--include-comments` bullet)

Update the Task Document Format section to define `--include-comments` precisely.

> `--include-comments` appends a **read‑only comments block** after the task body. This block is primarily for human consumption; agents and tools SHOULD use `wrkq comment ls --json` instead of parsing it.

**Layout (example)**

```markdown
---
id: T-00123
uuid: 2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1
project_id: P-00007
slug: login-ux
title: Login UX
state: open
priority: 1
# ...
---

Task body here...

---

<!-- wrkq-comments: do not edit below -->

> [C-00012] [2025-11-19T10:01:31Z] lance (human)
> Investigating login 500s…

> [C-00013] [2025-11-19T10:05:02Z] agent-codex (agent)
> Proposed fix in branch `feature/login-rate-limit`. See run_id=abc123.
```

**Rules**

- The comments block is always separated from the body by:
  - A horizontal rule (`---`) and
  - A sentinel HTML comment: `<!-- wrkq-comments: do not edit below -->`.
- Each comment is rendered as:
  - A single header line, then one or more quoted lines:
    - Header:  
      `> [<comment-id>] [<ISO8601 timestamp>] <actor-slug> (<actor-role>)`
    - Body: subsequent lines in the same `>` block.
- Only **non‑deleted** comments are included by default.
- The comments block is **ignored** by `wrkq apply`:
  - Comment lines are not parsed or applied back to the DB.
  - Comments are write‑only via dedicated comment commands.

This keeps `wrkq cat` human‑friendly while preserving a clean machine API for comments.

---

## 9.11 Comments (new section under 9. Command Spec)

Commands for listing, creating, inspecting, and removing comments on tasks.

All mutating commands use normal actor resolution (`--as`, env, config) and ETag semantics where applicable. They participate in the event log as described in 4.4.

---

### 9.11.1 `wrkq comment ls`

List comments attached to one or more tasks.

**Synopsis**

```sh
wrkq comment ls <TASK-PATH|t:<id>...> \
  [--json|--ndjson|--yaml|--tsv] \
  [--fields=...] \
  [--include-deleted] \
  [--limit N] [--cursor CURSOR] \
  [--sort=created_at] [--reverse] \
  [--porcelain]
```

**Behavior**

- Resolves each argument to a single task:
  - Container paths (e.g. `portal/auth/login-ux`) or `t:<token>`.
- Returns all comments for the given tasks, ordered by `created_at` ascending by default.
- Pagination:
  - `--limit` and `--cursor` use the standard opaque cursor mechanism.
  - `--porcelain` prints a `next_cursor` on stderr when more results exist.
- By default, soft‑deleted comments (`deleted_at IS NOT NULL`) are **excluded**:
  - `--include-deleted` includes them, with `deleted_at` and `deleted_by_actor_id` fields visible.

**Fields**

Typical fields (selectable via `--fields`):

- `id` (friendly, `C-xxxxx`)
- `uuid`
- `task_id`
- `task_path` (resolved path, optional / derived)
- `actor_id`
- `actor_slug`
- `actor_role`
- `body`
- `meta`
- `etag`
- `created_at`
- `updated_at`
- `deleted_at`
- `deleted_by_actor_id`

**Output**

- Human default: table, one row per comment (truncated body):
  ```text
  ID       Task                 Created At              Actor        Body
  C-00012  portal/auth/login-ux 2025-11-19T10:01:31Z    lance        Investigating login 500s…
  C-00013  portal/auth/login-ux 2025-11-19T10:05:02Z    agent-codex  Proposed fix in branch feature/login…
  ```
- `--json`: array of comment objects.
- `--ndjson`: one comment object per line.

**Exit codes**

- `0` success
- `2` usage error (bad flags, unresolved selector)
- `3` not found (no matching tasks)

---

### 9.11.2 `wrkq comment add`

Create a new comment on a task.

**Synopsis**

```sh
# Inline message
wrkq comment add <TASK-PATH|t:<id>> -m "Short message"

# Multi-line from stdin
wrkq comment add <TASK-PATH|t:<id>> -

# From file
wrkq comment add <TASK-PATH|t:<id>> ./comment.md

# Optional meta via JSON string
wrkq comment add <TASK-PATH|t:<id>> -m "…" \
  --meta '{"run_id":"run_abc123","kind":"analysis"}'
```

**Flags**

- `-m, --message <text>`  
  Inline comment text. If present, takes precedence over file/stdin.
- `<FILE>` or `-`  
  Read the body from the given file or stdin when `-m` is not supplied.
- `--meta <json>`  
  Optional JSON string to populate `meta` (validated as JSON object).
- `--if-match <task-etag>`  
  Optional optimistic concurrency check against the task’s `etag`.
- `--as <actor>`  
  Override actor resolution (slug or friendly `A-xxxxx`).
- `--dry-run`  
  Validate without writing.

**Behavior**

1. Resolve the task (path or `t:<token>`).
2. Resolve current actor (see Actor section).
3. If `--if-match` is provided:
   - Read the task’s current `etag`.
   - If it differs, abort with exit code `4` (conflict).
4. Insert a new comment row:
   - Set `task_id`, `actor_id`, `body`, `meta`, `created_at`, `etag=1`.
5. Insert a `comment.created` event into the event log.

**Output**

- Human default:

  ```text
  C-00037 2025-11-19T10:23:10Z agent-codex Added comment on T-00123
  ```

- `--json`:

  ```json
  {
    "id": "C-00037",
    "uuid": "…",
    "task_id": "T-00123",
    "actor_slug": "agent-codex",
    "created_at": "2025-11-19T10:23:10Z",
    "etag": 1
  }
  ```

**Exit codes**

- `0` success
- `2` usage error
- `3` task not found
- `4` conflict (`--if-match` mismatch)

---

### 9.11.3 `wrkq comment cat`

Show one or more comments in a human‑readable format.

**Synopsis**

```sh
wrkq comment cat <COMMENT-ID|c:<token>...> [--json|--ndjson|--raw]
```

**Behavior**

- Resolves each argument to a comment (`C-xxxxx`, UUID, or `c:<token>`).
- By default, shows a human‑oriented view with headers and Markdown bodies.

**Output**

- Human default:

  ```text
  C-00012 (2025-11-19T10:01:31Z, lance / human, task T-00123)

  Investigating login 500s...

  ---
  C-00013 (2025-11-19T10:05:02Z, agent-codex / agent, task T-00123)

  Proposed fix in branch `feature/login-rate-limit`. See run_id=abc123.
  ```

- `--raw`:
  - Print only the `body` for each comment, separated by `\n---\n`.
- `--json` / `--ndjson`:
  - Full structured objects, same schema as `comment ls`.

**Exit codes**

- `0` success
- `2` usage error
- `3` comment not found

---

### 9.11.4 `wrkq comment rm`

Soft‑delete or hard‑delete comments.

**Synopsis**

```sh
wrkq comment rm <COMMENT-ID|c:<token>...> \
  [--yes] [--purge] [--if-match <etag>] [--dry-run]
```

**Behavior**

- By default (no `--purge`):
  - Soft delete:
    - Set `deleted_at` and `deleted_by_actor_id` for each comment.
    - Increment `etag`.
    - Insert `comment.deleted` event(s).
  - Soft‑deleted comments are hidden from human views:
    - `wrkq comment ls` (without `--include-deleted`)
    - `wrkq cat --include-comments`.
- With `--purge`:
  - Hard delete:
    - Remove rows from the `Comment` table.
    - Insert `comment.purged` event(s) with `payload.purged=true`.
- `--if-match <etag>`:
  - If supplied, `etag` must match the current comment `etag` for each deletion.
  - Mismatches cause exit code `4` (conflict) for that comment.

**Flags**

- `--yes`  
  Skip interactive confirmation (if any).
- `--purge`  
  Hard delete records instead of soft delete.
- `--if-match <etag>`  
  Concurrency guard per comment.
- `--dry-run`  
  Show what would be deleted, but do not write.

**Exit codes**

- `0` success
- `2` usage error
- `3` not found (no matching comments)
- `4` conflict (etag mismatch)
- `5` partial success (some comments removed, some failed)

---

That’s everything wired up as a coherent, first‑class “comments spec”: schema, event log, addressing, task doc integration, and CLI commands, in the same tone and structure as the rest of `SPEC.md`.