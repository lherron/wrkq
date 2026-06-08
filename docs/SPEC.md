# wrkq Canonical Specification

Status: current implementation contract as of 2026-06-07.

This is the source of truth for wrkq's product model, CLI behavior, local daemon
surface, configuration, and validation rules. Supporting docs may explain how to
use or operate the system, but when they conflict with this file, this file wins.

The wrkf JSON-RPC stdio contract is intentionally separate at
[`docs/wrkf-rpc.md`](wrkf-rpc.md) because source files and tests reference it as
a machine contract.

## 1. Purpose

wrkq is a local-first task collaboration system for humans and coding agents.
It uses a filesystem-flavored CLI over a SQLite database so agents can discover,
claim, update, comment on, hand off, and audit work using predictable shell
commands and machine-readable output.

Goals:
- Use familiar verbs: `ls`, `cat`, `touch`, `mkdir`, `mv`, `rm`, `tree`, `find`.
- Keep all durable state local by default: SQLite for metadata, filesystem for
  attachment bytes, sidecar SQLite for search.
- Attribute every mutation to an actor without pretending to provide
  multi-user authentication.
- Support concurrent CLIs and agents through WAL, transactions, busy timeouts,
  and optimistic `etag` checks.
- Provide stable JSON, NDJSON, and porcelain output for scripts, MCP, daemon
  clients, and agent harnesses.
- Preserve an append-only audit/event log for state changes.

Non-goals:
- Cloud synchronization, SSO, team authorization, or hosted multi-tenant mode.
- FUSE mounting or treating the database as a real filesystem.
- A complete browser UI contract. `wrkqd` exposes selected HTTP endpoints, but
  the CLI and database model remain primary.

## 2. Shipped Binaries

`just build` and `just install` build four binaries:

| Binary | Role | Canonical use |
| --- | --- | --- |
| `wrkq` | Human and agent task surface | Task/container/comment/attachment CRUD, search, handoffs, server lifecycle helpers. |
| `wrkqadm` | Administrative surface | Init, migrations, snapshots, actors, merge, patches, bundle apply. |
| `wrkqd` | Local daemon | Token-auth HTTP API over the same database. |
| `wrkf` | Workflow CLI/RPC surface | Workflow templates, evidence, obligations, effects, transitions, and JSON-RPC stdio. |

`wrkq` is intentionally safe for day-to-day agent use. `wrkqadm` is for database
and administrative operations. `wrkqd` is a local service wrapper, not a separate
source of truth. `wrkf` builds workflow semantics on top of wrkq tasks.

## 3. Repository Boundaries

Important implementation packages:

| Path | Responsibility |
| --- | --- |
| `cmd/wrkq`, `cmd/wrkqadm`, `cmd/wrkqd`, `cmd/wrkf` | Binary entry points. |
| `internal/cli` | Cobra commands for `wrkq`, `wrkqadm`, and daemon helpers. |
| `internal/config` | Config/env loading and defaults. |
| `internal/db` | SQLite open, migrations, schema status. |
| `internal/domain` | Domain structs and validation enums. |
| `internal/store` | Persistence and transactional domain operations. |
| `internal/selectors`, `internal/paths` | Friendly IDs, UUIDs, typed selectors, paths, slug normalization. |
| `internal/search` | Sidecar search service over task, comment, and handoff chunks. |
| `internal/workflow`, `internal/wrkfapi`, `internal/wrkfrpc`, `internal/wrkfcli` | wrkf workflow engine, API layer, RPC, and CLI. |
| `packages/wrkf-client` | TypeScript client for the wrkf JSON-RPC contract. |
| `mcp-server` | MCP stdio server exposing selected wrkq operations. |
| `pbc/` | Sample wrkf workflow preset and artifact templates. |

## 4. Configuration

Configuration precedence:

1. CLI flags, such as `--db`, `--as`, and `--project`.
2. Environment variables.
3. Nearest `./.env.local`, walking upward to the user's home directory.
4. `~/.config/wrkq/config.yaml`.
5. Built-in defaults.

Key variables:

| Variable | Meaning |
| --- | --- |
| `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` | Canonical database path. |
| `WRKQ_ATTACH_DIR` | Attachment byte storage root. |
| `WRKQ_ACTOR_ID` | Friendly actor ID for mutations, such as `A-00001`. |
| `WRKQ_ACTOR` | Actor slug for mutations. |
| `WRKQ_PROJECT_ROOT` | Default project/container path for relative task paths. |
| `ASP_PROJECT` | Runtime project fallback when `WRKQ_PROJECT_ROOT` is not explicitly exported. |
| `WRKQ_OUTPUT` | Default output mode. |
| `WRKQ_SEARCH_*` | Search sidecar and dense embedding configuration. |

Database defaults:
- If `.wrkq/wrkq.db` exists in the current directory, it is used.
- Otherwise the default is `~/.local/share/wrkq/wrkq.db`.

Attachment defaults:
- If the DB is `.wrkq/wrkq.db`, attachments default to `.wrkq/attachments`.
- Otherwise attachments default to `~/.local/share/wrkq/attachments`.

Project-root precedence:

1. `--project` flag, resolved through container selectors.
2. Explicitly exported `WRKQ_PROJECT_ROOT`.
3. `ASP_PROJECT`.
4. `.env.local` or config file project root.

Actor resolution for mutating commands:

1. `--as <actor>` flag.
2. `WRKQ_ACTOR_ID`.
3. `WRKQ_ACTOR`.
4. `default_actor` in config.

The actor may be an actor friendly ID, UUID, or slug. Actor resolution is
attribution only; wrkq does not implement authz/authn.

## 5. Domain Model

### Actors

Actors represent humans, agents, and system processes. Valid actor roles are:

`human`, `agent`, `system`

### Containers

Containers organize tasks hierarchically.

Valid user-visible container kinds:

`project`, `directory`, `feature`, `area`

There is also an internal singleton `root` container created by migration
`000024_root_container.sql`. It is path-invisible and not a user-creatable
container kind.

Top-level project paths are direct children of the root container. Nested
containers use `parent_uuid`.

### Tasks

Tasks are the primary work item.

Valid task kinds:

`task`, `subtask`, `spike`, `bug`, `chore`

Valid task states:

| State | Meaning |
| --- | --- |
| `idea` | Pre-triage captured thought. Hidden from default `find`; ignored as a blocker. |
| `draft` | Triage-ready but not yet committed to execution. |
| `open` | Ready to be worked. Default state for `touch`. |
| `in_progress` | Actively being worked. |
| `blocked` | Cannot proceed without external progress. |
| `completed` | Done. Terminal for dependency blocking. |
| `cancelled` | Will not be done. Terminal for dependency blocking. |
| `archived` | Soft-deleted/archive state; `archived_at` is set by triggers or restore/rm code. |
| `deleted` | Deleted state used before or during purge/restore flows; `deleted_at` is set. |

Common lifecycle:

`idea -> draft -> open -> in_progress -> completed`

Other transitions are intentionally permissive. The validator accepts any valid
state value, and commands do not enforce a strict state-transition graph.

Task priority is `1` through `4`; `1` is highest.

Resolution values:

`done`, `wont_do`, `duplicate`, `needs_info`

Async/control-plane linkage fields currently present on tasks:

`cp_project_id`, `cp_work_item_id`, `cp_run_id`, `session_id`
(`cp_session_id` in storage), `run_status`

Valid `run_status` values:

`queued`, `running`, `completed`, `failed`, `cancelled`, `timed_out`

Workflow-assist fields currently present on tasks:

`workflow_preset`, `preset_version`, `phase`, `risk_class`

Valid `risk_class` values:

`low`, `medium`, `high`

Valid task role assignment roles:

`triager`, `owner`, `implementer`, `tester`, `reviewer`, `release_manager`

### Subtasks

A task with `--parent-task` is stored as kind `subtask` unless `--kind` is
explicitly provided. Parent and child tasks must be in the same container.
Subtasks cannot have subtasks.

### Comments

Comments are append-only task notes. They can be soft-deleted with
`deleted_at` and `deleted_by_actor_uuid`, or purged. Default comment listings
and `wrkq cat` output exclude deleted comments.

`wrkq cat` includes comments by default. Use `--exclude-comments` to omit them.
There is no canonical `--include-comments` flag.

### Attachments

Attachment metadata is stored in SQLite. Bytes live under:

`<attach_dir>/tasks/<task_uuid>/...`

Task moves and renames do not move attachment bytes because storage is keyed by
task UUID. Purging a task removes its attachment directory.

### Task Relations

Valid relation kinds:

`blocks`, `relates_to`, `duplicates`

For blocker checks, incoming `blocks` relations count as blocking when the
blocking task is not in:

`completed`, `archived`, `deleted`, `cancelled`, `idea`

Therefore `draft`, `open`, `in_progress`, and `blocked` tasks still block.

### Handoffs

Handoffs are intentional context records that an agent leaves for a later
session of the same agent. They are not tasks, comments, or memory.

Handoffs are scoped to an agent/project canonical scope ref in v1, for example:

`agent:cody:project:wrkq`

Accepted input forms include full scope refs and compact handles such as
`cody@wrkq`. Task/role variants may parse, but v1 normalizes storage to project
scope for default handoff flows.

Handoff statuses:

`pending`, `acknowledged`

Creation requires a non-empty title and body. Acknowledgement is the only
retirement mechanism. Handoffs do not auto-expire.

`wrkq handoff search` uses the shared sidecar search index and runs
`IndexPending` before querying so fresh handoffs are visible without a manual
rebuild when indexing succeeds.

## 6. Slugs, Paths, and Selectors

Slug normalization:

- Lowercase.
- Replace spaces and underscores with hyphens.
- Drop invalid characters.
- Trim leading/trailing hyphens.
- Must start with `[a-z0-9]`.
- May contain only `[a-z0-9-]`.
- Maximum length is 255 bytes.

Validated slug regex:

`^[a-z0-9][a-z0-9-]*$`

Paths are slash-separated container/task slugs. Selectors accepted by most
commands:

| Selector | Example |
| --- | --- |
| Task path | `wrkq/inbox/docs-spec-cleanup` |
| Container path | `wrkq/inbox` |
| Friendly ID | `T-02017`, `P-00061`, `C-03700`, `A-00001`, `H-00001` |
| UUID | `550e8400-e29b-41d4-a716-446655440000` |
| Typed task selector | `t:T-02017` |
| Typed container selector | `c:P-00061` |

Globs are supported by commands that explicitly document path pattern support.
Quote globs in shells.

## 7. CLI Contract

### Global Flags

Common root flags:

- `--db <path>`
- `--as <actor>`
- `--project <container>`
- `--output table|human|json|ndjson|porcelain|yaml|tsv|raw`

Command-local `--json`, `--ndjson`, `--human`, and similar flags override
global output defaults when present.

### Output Defaults

Non-TTY defaults:

| Shape | Default |
| --- | --- |
| list/search/history stream | NDJSON |
| singleton/mutation | JSON |
| content, such as `cat` | raw markdown |

TTY defaults vary by command. Porcelain is a stability modifier: compact,
machine-oriented output and no width/ANSI formatting where applicable.

### Core `wrkq` Commands

Primary task/container surface:

`projects`, `ls`, `tree`, `find`, `search`, `index`, `stat`, `cat`, `touch`,
`set`, `apply`, `diff`, `log`, `watch`, `mkdir`, `rmdir`, `mv`, `cp`, `rm`,
`restore`, `container`, `rename-container`, `comment`, `attach`, `relation`,
`check`, `ack`, `handoff`, `agent-context`, `whoami`, `bundle create`,
`webhook`, `server`, `usage`, `agent-info`, `version`, `completion`.

Important behavior:

- `touch` creates tasks. Default state is `open`; default priority is `3`.
- `set` updates task fields and supports bulk operation over refs or stdin.
- `apply` updates description/specification from markdown, YAML, JSON, or stdin.
  Metadata in input is ignored unless `--with-metadata` is passed.
- `cat` prints markdown with YAML front matter and comments by default.
- `rm` archives by default; `--purge --yes` permanently deletes.
- `restore` restores archived/deleted tasks to `open` by default or to a
  provided non-archived/non-deleted state.
- `tree` defaults to visible containers and draft/open tasks; `--open` narrows
  to open tasks; `-a/--all` includes archived and empty containers.
- `find` defaults to active items and excludes `archived`, `deleted`, and
  `idea`.
- `search` defaults task/comment results to `state=open`; `--state all`
  includes non-deleted states, including `archived`.
- `handoff list` and `handoff search` default to `status=pending`.

### `wrkqadm` Commands

Administrative surface:

`init`, `migrate`, `db snapshot`, `actors ls`, `actors add`, `bundle apply`,
`state export`, `state import`, `state verify`, `patch create`, `patch validate`,
`patch apply`, `patch rebase`, `patch summarize`, `merge`, `attach path`,
`doctor`, `config doctor`, `version`.

### Exit Codes

General contract:

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Generic runtime, IO, DB, or validation error unless a command defines a narrower code. |
| `2` | Usage error, invalid selector context, or unresolvable handoff scope for handoff commands. |
| `3` | Not found in the general contract; handoff create uses it for idempotency payload mismatch. |
| `4` | Conflict or not found for command-specific handoff get behavior. |
| `5` | Partial success for bulk operations; already acknowledged for handoff acknowledge. |
| `6` | Handoff acknowledge `etag` mismatch. |

Command-specific structured errors may refine these meanings. Automation should
prefer machine output plus the command-specific help/contract.

## 8. Search

wrkq search uses a derived sidecar SQLite database at:

`<canonical-db>.search.sqlite`

The index stores chunks for:

`task`, `comment`, `handoff`

FTS5 requires the `sqlite_fts5` build tag; `just build` and `just install`
include it. Without FTS5, the search service falls back to a LIKE-style plain
lexical table.

Dense embeddings are optional. Defaults:

| Setting | Default |
| --- | --- |
| Provider | `llama-cpp` |
| Base URL | `http://127.0.0.1:18480` |
| Model | `Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M` |
| Dimension | `4096` |
| Index batch size | `8` |

Set `WRKQ_SEARCH_DENSE_PROVIDER=none` for FTS-only indexing/search.

Index lifecycle:

`wrkq index status`, `rebuild`, `update`, `vacuum`, `pause`, `resume`

The sidecar is rebuildable and not the canonical source of task state.

## 9. Daemon API

`wrkqd` serves token-auth HTTP endpoints over the configured DB. `wrkq server`
wraps daemon lifecycle commands; `wrkqd` can also run directly.

Current route surface:

| Route | Purpose |
| --- | --- |
| `/v1/health` | Health check. |
| `/v1/containers/tree` | Container/task tree. |
| `/v1/tasks/list` | List tasks. |
| `/v1/tasks/get` | Get task. |
| `/v1/tasks/create` | Create task. |
| `/v1/tasks/update` | Update task. |
| `/v1/tasks/archive` | Archive/delete task. |
| `/v1/tasks/restore` | Restore task. |
| `/v1/comments/list` | List comments. |
| `/v1/comments/create` | Create comment. |
| `/v1/relations/list` | List relations. |
| `/v1/relations/create` | Create relation. |
| `/v1/relations/delete` | Delete relation. |
| `/v1/actors/list` | List actors. |
| `/v1/actors/create` | Create actor. |
| `/v1/actors/update` | Update actor. |
| `/v1/bundle/create` | Create bundle. |
| `/v1/bundle/apply` | Apply bundle. |

There are no handoff HTTP routes in the current daemon.

## 10. Persistence and Concurrency

Canonical state lives in one SQLite database. Connections are opened with WAL
mode and a busy timeout. Migrations live under `internal/db/migrations` and are
embedded into the binaries.

Mutable rows carry `etag INTEGER`. Mutating commands that expose `--if-match`
must reject stale writes when the current etag differs from the expected etag.

Events are appended to `event_log` with resource type, UUID, event type, etag,
actor UUID, timestamp, and JSON payload. The event log is an audit trail, not a
hash-chain ledger.

`state export`, `state import`, and `state verify` provide canonical snapshot
operations. `bundle create` and `bundle apply` support git-ops style state
transfer.

## 11. wrkf Integration

wrkf is the workflow engine built in this repository. It attaches workflow
instances, evidence, checks, obligations, effects, runs, and transitions to wrkq
tasks without replacing the wrkq task lifecycle.

Current implementation facts:

- wrkf stores workflow state in workflow tables and task metadata, not in
  `tasks.state` as a direct projection.
- `tasks.state` remains normal wrkq lifecycle state.
- Installed workflow templates are persisted by id/version/hash.
- Workflow evidence supports small typed `facts` for routing and larger
  freeform `data` for human/agent context.
- Transition idempotency, run external binding, and effect lease token hardening
  are implemented by the current migration series.
- The JSON-RPC stdio contract is frozen in `docs/wrkf-rpc.md`.

Known limits:

- Role/actor authority is still shallow compared with a real authorization
  system.
- Evidence facts are validated as small decision surfaces; they do not prove the
  truth of referenced artifacts.
- Hook/effect handler catalogs are runtime filesystem dependencies, not
  content-pinned workflow dependencies.

## 12. Supporting Markdown Disposition

This cleanup intentionally collapses the former wrkq PRD, CLI reference,
architecture note, domain model, deleted/restore proposal, async-run linkage
note, and state-machine proposal into this file.

Canonical markdown that remains:

| File | Status |
| --- | --- |
| `README.md` | User-facing overview and install quick start. |
| `AGENTS.md` / `CLAUDE.md` | Agent operating instructions; `CLAUDE.md` is a symlink. |
| `docs/SPEC.md` | Canonical wrkq specification. |
| `docs/wrkf-rpc.md` | Frozen wrkf JSON-RPC machine contract. |
| `internal/cli/embedded/WRKQ-USAGE.md` | Embedded full agent usage block. |
| `internal/cli/embedded/AGENT-WRKQ-USAGE.md` | Embedded compact quick reference. |
| `mcp-server/README.md` | Local package README for the MCP stdio wrapper. |
| `pbc/templates/*.md` | Workflow artifact templates used by the PBC sample preset. |
| `vendor/**/*.md` | Third-party vendored documentation; not a wrkq spec surface. |

Historical scratch/proposal markdown should live in git history or wrkq tasks,
not as top-level repo docs.
