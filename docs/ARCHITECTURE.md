# wrkq Architecture

This document describes the technical architecture of wrkq, including the two-binary design, package structure, database schema, and key design decisions.

---

## Two-Binary Architecture

wrkq ships as **two complementary binaries** with distinct responsibilities:

| Binary | Purpose | Audience |
|--------|---------|----------|
| **wrkq** | Agent + human collaboration surface | Agents + Humans |
| **wrkqadm** | Administrative + infrastructure surface | Administrators only |

### Why Two Binaries?

The separation ensures:

1. **Agents get a focused, safe API** — Only task/container/comment CRUD, content editing, and history
2. **Admins retain full control** — Database lifecycle, actor management, bundle application
3. **Clear security boundary** — Agents cannot initialize databases or manage actors
4. **Simpler mental model** — Each binary has a clear, non-overlapping purpose

### wrkq (Agent + Human Surface)

Operations *within* a database: projects, tasks, comments, attachments, search, history.

```text
wrkq
  ls            # list containers/tasks
  tree          # tree view
  stat          # metadata only
  cat           # task doc (md+frontmatter)
  edit          # $EDITOR + 3-way merge
  apply         # apply doc from file/stdin
  set           # quick field updates
  mkdir         # create containers
  touch         # create tasks
  mv            # move/rename containers/tasks
  cp            # copy tasks/containers
  rm            # archive/purge
  restore       # un-archive
  find          # metadata search
  log           # history from event log
  diff          # compare tasks/docs
  watch         # stream events
  attach        # ls, get, put, rm
  comment       # ls, add, cat, rm
  relation      # add, rm, ls
  bundle create # export PR bundle
  whoami        # resolved actor + DB
  version       # version info
  completion    # shell completions
```

### wrkqadm (Administrative Surface)

Operations on *database lifecycle* and *global configuration*: initialization, migrations, actors, bundle application.

```text
wrkqadm
  init              # create/migrate DB, seed defaults
  migrate           # apply pending migrations
  db snapshot       # WAL-safe point-in-time copy
  bundle apply      # apply bundle into canonical DB
  actors ls         # list all actors
  actors add        # create actors
  state export      # canonical JSON snapshot
  state import      # import snapshot
  state verify      # verify snapshot
  doctor            # health checks
  config            # view configuration
  version           # version info
```

---

## Package Structure

```text
cmd/
  wrkq/main.go           # wrkq binary entry point
  wrkqadm/main.go        # wrkqadm binary entry point

internal/
  cli/                   # Command implementations (Cobra)
    root.go              # wrkq root command
    rootadm.go           # wrkqadm root command
    appctx/              # Shared CLI bootstrap (config, DB, actor)
    *.go                 # Individual command files

  config/                # Configuration loading
                         # Precedence: flags > env > .env.local > config.yaml

  db/                    # SQLite wrapper and migrations
    db.go                # Connection, migration runner
    migrations/          # Embedded SQL migrations (//go:embed)

  domain/                # Domain types and validation
    types.go             # Actor, Container, Task, Comment, etc.
    validation.go        # Slug rules, kind validation

  store/                 # Persistence layer
    task_store.go        # Task CRUD with etags
    container_store.go   # Container CRUD with etags

  paths/                 # Path resolution
    glob.go              # Pathspec and glob resolver
    resolver.go          # Unified path resolution

  selectors/             # Resource selector parsing
                         # Paths, friendly IDs, UUIDs, typed selectors

  render/                # Output formatting
                         # json, ndjson, yaml, tsv, table, porcelain

  edit/                  # 3-way merge for edit/apply

  attach/                # Attachment path resolution and IO

  actors/                # Actor resolution logic

  events/                # Event log write/read helpers

  bundle/                # Bundle create/apply logic

  id/                    # Friendly ID generation (T-00001, P-00007)
```

---

## Database Design

### Storage

- **Single SQLite file** with WAL mode enabled
- **Concurrent access** supported via WAL + busy_timeout
- **Migrations** embedded at compile time and tracked in `schema_migrations` table

### Core Tables

| Table | Purpose |
|-------|---------|
| `actors` | Human, agent, and system entities |
| `containers` | Projects and subprojects (hierarchical) |
| `tasks` | Work items with state, priority, kind |
| `comments` | Immutable notes on tasks |
| `attachments` | File metadata (bytes on filesystem) |
| `event_log` | Append-only audit trail |
| `sections` | Kanban columns (schema exists, CLI pending) |
| `task_relations` | Dependencies between tasks |

### Key Schema Features

**Optimistic Concurrency**
- Every mutable row has `etag INTEGER`
- All writes increment etag
- `--if-match <etag>` enables conflict detection

**Soft Delete**
- `archived_at` timestamp for soft deletion
- `--purge` for hard deletion when needed

**Actor Attribution**
- `created_by_actor_uuid` and `updated_by_actor_uuid` on all mutable tables
- All actions attributed to an actor (human, agent, or system)

**Hierarchical Containers**
- `parent_uuid` enables arbitrary nesting
- Slugs unique among siblings

### Container Kinds

| Kind | Description |
|------|-------------|
| `project` | Top-level project (default) |
| `feature` | Feature area within a project |
| `area` | Cross-cutting concern |
| `misc` | Miscellaneous/catch-all |

### Task Kinds

| Kind | Description |
|------|-------------|
| `task` | Standard actionable item (default) |
| `subtask` | Child of another task (max depth: 1) |
| `spike` | Research/investigation |
| `bug` | Defect to fix |
| `chore` | Maintenance work |

### Task States

| State | Description |
|-------|-------------|
| `open` | Not started |
| `in_progress` | Currently being worked on |
| `completed` | Done |
| `blocked` | Waiting on external dependency |
| `cancelled` | Won't be done |

Note: `archived` is a visibility filter via `archived_at` timestamp, not a workflow state.

### Task Relations

| Kind | Meaning |
|------|---------|
| `blocks` | Task A must complete before Task B can start |
| `relates_to` | Informational link (no dependency) |
| `duplicates` | Task A is a duplicate of Task B |

---

## Key Design Decisions

### 1. No Authentication, Only Attribution

wrkq uses actors for **provenance**, not access control. All actions are attributed to an actor (human or agent) but there's no authentication layer. This enables:

- Simple single-user + multiple-agents deployment
- Clear audit trail without auth complexity
- Agents can self-identify via environment variables

### 2. Filesystem-Flavored Commands

Commands mirror Unix utilities for familiarity:

| wrkq | Unix | Purpose |
|------|------|---------|
| `ls` | `ls` | List contents |
| `cat` | `cat` | Print content |
| `mkdir` | `mkdir` | Create directory/container |
| `touch` | `touch` | Create file/task |
| `mv` | `mv` | Move/rename |
| `rm` | `rm` | Remove |
| `tree` | `tree` | Hierarchical view |

### 3. Pipe-Friendly Design

- Default output is human-readable
- `--json`, `--ndjson`, `--yaml`, `--tsv` for machine consumption
- `--porcelain` for stable, scriptable output
- `-0` for NUL-separated output (xargs -0)
- Commands accept `-` for stdin

### 4. Optimistic Concurrency

Multiple agents and CLIs can work concurrently:

1. Read resource with current `etag`
2. Make changes
3. Write with `--if-match <etag>`
4. On conflict (etag mismatch), re-read and retry

Exit code `4` indicates conflict.

### 5. Event Log as Audit Trail

All changes write to an append-only event log:

- Complete history of all changes
- Enables `wrkq log` and `wrkq watch`
- Supports future event replay/sync

### 6. Attachments on Filesystem

- Metadata in database
- Bytes stored at `attach_dir/tasks/<task_uuid>/...`
- Task moves don't affect attachment paths (keyed by UUID)
- Soft delete preserves files; `--purge` removes them

### 7. Comments are Immutable

- Once created, comment body cannot be edited
- Soft delete via `deleted_at` timestamp
- Hard delete with `--purge`
- Designed for async human-agent collaboration

### 8. Slug-Based Addressing

Resources can be addressed by:

| Format | Example | Description |
|--------|---------|-------------|
| Path | `myproject/feature/task` | Hierarchical, human-readable |
| Friendly ID | `T-00123` | Stable, memorable |
| UUID | `2fa0a6d6-...` | Canonical identifier |
| Typed selector | `t:T-00123` | Explicit type prefix |

Slugs are normalized: lowercase `[a-z0-9-]`, max 255 bytes, unique among siblings.

---

## Configuration

### Precedence (highest to lowest)

1. CLI flags (`--db`, `--as`)
2. Environment variables (`WRKQ_DB_PATH`, `WRKQ_ACTOR`)
3. `./.env.local` (dotenv)
4. `~/.config/wrkq/config.yaml`

### Key Environment Variables

| Variable | Description |
|----------|-------------|
| `WRKQ_DB_PATH` | Path to SQLite database |
| `WRKQ_ACTOR` | Default actor slug |
| `WRKQ_ACTOR_ID` | Default actor friendly ID |
| `WRKQ_ATTACH_DIR` | Base directory for attachments |

### Actor Resolution Order

1. `--as <actor>` flag
2. `WRKQ_ACTOR_ID` env (friendly ID)
3. `WRKQ_ACTOR` env (slug)
4. `default_actor` in config
5. Seeded default (e.g., `local-human`)

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Generic error (db, io) |
| `2` | Usage error (bad flags, args) |
| `3` | Not found (no matches) |
| `4` | Conflict (etag mismatch) |
| `5` | Partial success (with `--continue-on-error`) |

---

## Performance Targets

- List ~5k open tasks in < 200ms (p95)
- NDJSON streaming for constant memory
- Opaque cursors for pagination
- SQLite WAL mode for concurrent access

---

## Future Considerations

### Implemented in Schema, Pending CLI

- **Sections**: Kanban columns with WIP limits (schema ready, no CLI commands)
- **Section assignment**: Containers can belong to sections

### Not Yet Implemented

- **Full-text search** (`wrkq rg`)
- **Multiple assignees** (currently single assignee per task)
- **Locked tasks** (prevent concurrent edits)
- **Pin/unpin tasks** (highlight important items)
