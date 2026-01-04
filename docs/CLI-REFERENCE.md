# wrkq CLI Reference

A feature-oriented overview of the wrkq and wrkqadm command-line interfaces.

## Two-Binary Architecture

wrkq ships as two separate binaries with distinct roles:

| Binary | Purpose | Audience |
|--------|---------|----------|
| **wrkq** | Task/container CRUD, content editing, history, collaboration | Agents + Humans |
| **wrkqadm** | Database lifecycle, actor management, bundle application, health checks | Administrators only |

This separation ensures agents get a focused, safe API while admins retain full control over infrastructure.

---

## Command Purposes

### wrkq (Agent + Human Surface)

| Command | Purpose |
|---------|---------|
| **touch** | Create new tasks with metadata |
| **ls** | List containers and tasks at a path |
| **cat** | Print tasks as markdown with YAML front matter |
| **stat** | Print machine-friendly metadata for resources |
| **find** | Search tasks and containers with filters |
| **tree** | Display hierarchical tree structure |
| **set** | Update task fields (state, priority, title, etc.) |
| **ack** | Acknowledge completed tasks |
| **mv** | Move or rename tasks and containers |
| **cp** | Copy tasks |
| **rm** | Archive (soft delete) or purge (hard delete) tasks |
| **mkdir** | Create containers (projects/subprojects) |
| **rmdir** | Remove empty containers |
| **comment** | Manage task comments (ls, add, rm, cat) |
| **attach** | Manage file attachments (ls, put, get, rm) |
| **relation** | Manage task relations (add, rm, ls) |
| **log** | Show change history from event log |
| **apply** | Update task description from file or stdin |
| **edit** | Interactive 3-way merge editing |
| **diff** | Compare task versions |
| **whoami** | Show current actor identity |
| **bundle create** | Create Git-ops bundle for PR workflow |
| **version** | Show version information |

### wrkqadm (Administrative Surface)

| Command | Purpose |
|---------|---------|
| **init** | Initialize database, run migrations, seed defaults |
| **migrate** | Apply pending database migrations |
| **actors ls** | List all actors |
| **actors add** | Create new actor |
| **bundle apply** | Apply PR bundle into canonical database |
| **state export** | Export database to canonical JSON snapshot |
| **state import** | Import snapshot into database |
| **state verify** | Verify snapshot is canonical |
| **doctor** | Health checks and diagnostics |
| **config** | View/modify configuration |

---

## Task Lifecycle

```
touch ──→ [open] ──→ set --state in_progress ──→ [in_progress]
             │                                        │
             │                    ┌───────────────────┘
             │                    │
             ▼                    ▼
        [blocked] ◄──────────► set --state completed ──→ [completed]
             │                                               │
             │                                               │
             ▼                                               ▼
       [cancelled]                                      rm ──→ [archived]
                                                              │
                                                              ▼
                                                        rm --purge (permanent)
```

### Task States

| State | Description |
|-------|-------------|
| `open` | Not started, default for new tasks |
| `in_progress` | Currently being worked on |
| `completed` | Done successfully |
| `blocked` | Waiting on something external |
| `cancelled` | Won't be done |
| `archived` | Soft-deleted (via `archived_at` timestamp) |

### State Transitions

| From | To | Command |
|------|-----|---------|
| Any | `open` | `wrkq set T-00001 --state open` |
| Any | `in_progress` | `wrkq set T-00001 --state in_progress` |
| Any | `completed` | `wrkq set T-00001 --state completed` |
| Any | `blocked` | `wrkq set T-00001 --state blocked` |
| Any | `cancelled` | `wrkq set T-00001 --state cancelled` |
| Any | archived | `wrkq rm T-00001` |
| archived | (deleted) | `wrkq rm T-00001 --purge` |

---

## Discovery & Reading

| Command | Scope | Use Case |
|---------|-------|----------|
| **ls** | Path-scoped | List children of a container or root |
| **find** | Repository-wide | Search with filters (state, kind, assignee, dates) |
| **tree** | Hierarchical | Visual tree of containers and tasks |
| **cat** | Single resource | Full task details with comments |
| **stat** | Single resource | Machine-friendly metadata |

### Find Filtering Capabilities

| Filter | Description |
|--------|-------------|
| `--type t\|p` | Filter by type (task or project/container) |
| `--state` | Filter by task state |
| `--kind` | Filter by task kind (task, subtask, spike, bug, chore) |
| `--assignee` | Filter by assignee actor |
| `--parent-task` | Filter subtasks of a parent |
| `--requested-by` | Filter by requester project ID |
| `--assigned-project` | Filter by assignee project ID |
| `--ack-pending` | Tasks completed/cancelled with no acknowledgment |
| `--slug-glob` | Filter by slug pattern |
| `--due-before` | Tasks due before date |
| `--due-after` | Tasks due after date |
| Path patterns | Glob patterns like `portal/**` |

---

## Metadata Management

### Task Properties

| Property | Set at Create | Modify via Set | Notes |
|----------|---------------|----------------|-------|
| title | `-t, --title` | `--title` | Required (defaults to slug) |
| description | `-d, --description` | `--description` | Markdown, supports `@file.md` or `-` for stdin |
| state | `--state` | `--state` | Default: `open` |
| priority | `--priority` | `--priority` | 1-4 (1 is highest), default: 3 |
| kind | `--kind` | `--kind` | task, subtask, spike, bug, chore |
| assignee | `--assignee` | `--assignee` | Actor slug or ID |
| requested_by | `--requested-by` | `--requested-by` | Requester project ID |
| assigned_project | `--assigned-project` | `--assigned-project` | Assignee project ID |
| resolution | `--resolution` | `--resolution` | done, wont_do, duplicate, needs_info |
| labels | `--labels` | `--labels` | JSON array |
| due_at | `--due-at` | `--due-at` | Date/datetime |
| start_at | `--start-at` | `--start-at` | Date/datetime |
| parent_task | `--parent-task` | N/A | For subtasks |

### Task Kinds

| Kind | Description |
|------|-------------|
| `task` | Standard actionable item (default) |
| `subtask` | Child of another task |
| `spike` | Research/investigation |
| `bug` | Defect to fix |
| `chore` | Maintenance/housekeeping |

### Container Kinds

| Kind | Description |
|------|-------------|
| `project` | Top-level project (default) |
| `feature` | Feature area |
| `area` | Organizational area |
| `misc` | Miscellaneous |

### Bulk Operations

`set` supports updating multiple tasks:

```bash
# Update multiple by ID
wrkq set T-00001 T-00002 T-00003 --state completed

# Update from stdin
echo -e "T-00001\nT-00002" | wrkq set - --state completed

# Parallel processing
wrkq set T-00001 T-00002 --state completed -j 4
```

### Description Input Methods

| Method | Syntax | Use Case |
|--------|--------|----------|
| Inline | `--description "text"` | Short descriptions |
| File | `--description @file.md` | Prepared content |
| Stdin | `--description -` | Piped content |

---

## Collaboration Features

### Comments

| Action | Command |
|--------|---------|
| Add comment | `wrkq comment add T-00001 -m "message"` |
| List comments | `wrkq comment ls T-00001` |
| Delete comment | `wrkq comment rm C-00001` |
| View single comment | `wrkq comment cat C-00001` |

Comments are append-only and immutable. Deletion is soft (via `deleted_at`), with `--purge` for hard delete.

### Attachments

| Action | Command |
|--------|---------|
| Attach file | `wrkq attach put T-00001 file.pdf` |
| Attach from stdin | `wrkq attach put T-00001 - --name doc.txt` |
| List attachments | `wrkq attach ls T-00001` |
| Download attachment | `wrkq attach get ATT-00001 --as output.pdf` |
| Remove attachment | `wrkq attach rm ATT-00001` |

Attachments are stored at `attach_dir/tasks/<task_uuid>/` and survive task moves/renames.

### Task Relations

| Relation | Meaning | Command |
|----------|---------|---------|
| `blocks` | Task A blocks Task B | `wrkq relation add T-00001 blocks T-00002` |
| `relates_to` | Informational link | `wrkq relation add T-00001 relates_to T-00002` |
| `duplicates` | Task A duplicates Task B | `wrkq relation add T-00001 duplicates T-00002` |

List relations: `wrkq relation ls T-00001`

---

## Container Hierarchy

| Command | Action |
|---------|--------|
| `mkdir myproject` | Create root container |
| `mkdir -p myproject/feature/subfeature` | Create with parents |
| `rmdir myproject` | Remove empty container |
| `mv myproject/old myproject/new` | Rename container |
| `mv task.md myproject/` | Move task to container |

---

## Output Modes

| Mode | Flag | Description |
|------|------|-------------|
| Table | (default) | Human-readable table |
| JSON | `--json` | Pretty-printed JSON |
| NDJSON | `--ndjson` | Newline-delimited JSON (best for piping) |
| YAML | `--yaml` | YAML output |
| TSV | `--tsv` | Tab-separated values |
| Porcelain | `--porcelain` | Stable machine-readable (no ANSI) |

Most commands support `--json`, `--ndjson`, and `--porcelain`.

### JSON Fields (cat --json)

```
id, uuid, project_id, project_uuid, slug, title, state, priority,
kind, parent_task_id, parent_task_uuid, assignee, assignee_uuid,
start_at, due_at, labels, description, etag, created_at, updated_at,
completed_at, archived_at, created_by, updated_by, comments, relations
```

---

## Resource Addressing

Resources can be referenced by multiple identifiers:

| Format | Example | Description |
|--------|---------|-------------|
| Path | `myproject/feature/task-slug` | Hierarchical path |
| Friendly ID | `T-00123`, `P-00007`, `C-00012` | Human-readable ID |
| UUID | `2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1` | Stable identifier |
| Typed selector | `t:T-00123`, `c:C-00012` | Explicit type prefix |

### Friendly ID Prefixes

| Prefix | Resource Type |
|--------|---------------|
| `T-` | Task |
| `P-` | Project/Container |
| `C-` | Comment |
| `A-` | Actor |
| `ATT-` | Attachment |

### Glob Patterns

```bash
# All tasks under portal
wrkq ls 'portal/**'

# Tasks matching pattern
wrkq find --slug-glob 'login-*'

# Recursive listing
wrkq ls -R myproject
```

---

## Actor Attribution

wrkq uses actors for attribution (provenance), not authentication.

### Actor Resolution Order

1. `--as <actor>` flag (slug or friendly ID)
2. `WRKQ_ACTOR_ID` environment variable (friendly ID)
3. `WRKQ_ACTOR` environment variable (slug)
4. `default_actor` in config file
5. Seeded default (e.g., `local-human`)

### Actor Roles

| Role | Description |
|------|-------------|
| `human` | Human user |
| `agent` | Automated agent (Claude, etc.) |
| `system` | System operations |

```bash
# Check current actor
wrkq whoami

# Override actor for a command
wrkq set T-00001 --state completed --as claude-agent
```

---

## Concurrency Model

### Optimistic Locking (ETags)

Every mutable resource has an `etag` integer that increments on write:

```bash
# Conditional update (only if etag matches)
wrkq set T-00001 --state completed --if-match 5

# Check current etag
wrkq stat T-00001 --json | jq '.etag'
```

If the etag doesn't match, the command exits with code `4` (conflict).

### Parallel Processing

```bash
# Parallel workers for bulk operations
wrkq set T-00001 T-00002 T-00003 --state open -j 4

# Continue on errors
wrkq set ... --continue-on-error
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Generic error (db, io) |
| `2` | Usage error (bad flags, args) |
| `3` | Not found (no matches) |
| `4` | Conflict (etag mismatch, merge conflict) |
| `5` | Partial success (with `--continue-on-error`) |

---

## Common Workflows

### Agent Task Workflow

```bash
# 1. Pick up a task
wrkq cat T-00001
wrkq set T-00001 --state in_progress

# 2. Add progress comments
wrkq comment add T-00001 -m "Implemented core logic"
wrkq comment add T-00001 -m "Added test coverage"

# 3. Complete the task
wrkq comment add T-00001 -m "Done. All tests passing."
wrkq set T-00001 --state completed
```

### Triage Workflow

```bash
# Find unassigned tasks
wrkq find --state open --json | jq '.[] | select(.assignee == null)'

# Assign and prioritize
wrkq set T-00001 --assignee agent-claude --priority 1

# Quick close invalid
wrkq rm T-00002  # Archive
```

### Create Feature with Subtasks

```bash
# Create container
wrkq mkdir -p myproject/auth-feature

# Create parent task
wrkq touch myproject/auth-feature/implement-oauth \
  -t "Implement OAuth login" \
  -d "Add Google and GitHub OAuth providers"

# Create subtasks
wrkq touch myproject/auth-feature/oauth-google \
  --kind subtask --parent-task T-00001 \
  -t "Add Google OAuth"

wrkq touch myproject/auth-feature/oauth-github \
  --kind subtask --parent-task T-00001 \
  -t "Add GitHub OAuth"
```

### Review Task History

```bash
# Show change log
wrkq log T-00001

# Compact one-line format
wrkq log T-00001 --oneline

# Show detailed changes
wrkq log T-00001 --patch

# Filter by time
wrkq log T-00001 --since 2025-01-01
```

### Bundle Workflow (Git-ops)

```bash
# Create bundle for PR
wrkq bundle create --out .wrkq

# Apply bundle (admin only)
wrkqadm bundle apply --from .wrkq
```

---

## Administrative Operations (wrkqadm)

### Database Initialization

```bash
# Initialize new database
wrkqadm init

# Custom actor slugs
wrkqadm init --human-slug my-name --agent-slug my-agent

# Custom paths
wrkqadm init --db /path/to/db.sqlite
```

### Actor Management

```bash
# List actors
wrkqadm actors ls

# Create new actor
wrkqadm actors add my-agent --name "My Agent" --role agent
```

### State Snapshots

```bash
# Export canonical snapshot
wrkqadm state export --out state.json

# Import snapshot
wrkqadm state import --from state.json

# Verify snapshot is canonical
wrkqadm state verify state.json
```

### Migrations

```bash
# Check migration status
wrkqadm migrate --status

# Apply pending migrations
wrkqadm migrate

# Dry run
wrkqadm migrate --dry-run
```

---

## Command Relationships

| If you want to... | Use... | Then maybe... |
|-------------------|--------|---------------|
| Start work on a task | `cat` to read it | `set --state in_progress` |
| Find open tasks | `find --state open` | `set` to assign yourself |
| Track progress | `comment add` | `set --state completed` when done |
| Organize work | `mkdir` for containers | `mv` to reorganize |
| Archive done work | `rm` to archive | `rm --purge` to delete |
| Review history | `log` for changes | `diff` to compare |
| Collaborate | `comment add` | `relation add` for dependencies |
| Export for Git | `bundle create` | PR workflow |

---

## Configuration

### Environment Variables

| Variable | Description |
|----------|-------------|
| `WRKQ_DB_PATH` | Path to SQLite database |
| `WRKQ_PROJECT_ROOT` | Default project root path for CLI commands (auto-prefixes paths) |
| `WRKQ_ACTOR` | Default actor slug |
| `WRKQ_ACTOR_ID` | Default actor friendly ID |

### Config File

`~/.config/wrkq/config.yaml`:

```yaml
db_path: /path/to/wrkq.db
default_actor: my-actor
attach_dir: /path/to/attachments
project_root: my-project
```

### Global Flags

| Flag | Description |
|------|-------------|
| `--db` | Override database path |
| `--as` | Override actor for this command |

---

## Data Model Summary

| Entity | Description | Friendly ID |
|--------|-------------|-------------|
| **Actor** | Human, agent, or system entity | `A-00001` |
| **Container** | Project or subproject (hierarchical) | `P-00001` |
| **Task** | Actionable item under a container | `T-00001` |
| **Comment** | Immutable note on a task | `C-00001` |
| **Attachment** | File reference (metadata + filesystem) | `ATT-00001` |
| **Event** | Audit log entry | N/A |

---

## Slug Conventions

- Lowercase `[a-z0-9-]` only
- Must start with `[a-z0-9]` (not hyphen)
- Maximum 255 bytes
- Unique among siblings
- Auto-normalized on input

---

## References

- Specification: `docs/SPEC.md`
- Domain Model: `docs/DOMAIN-MODEL.md`
- Architecture: `docs/ARCHITECTURE.md`
- Quick reference: `WRKQ-USAGE.md`
- Project instructions: `CLAUDE.md`
