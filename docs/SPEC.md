# wrkq Canonical Specification

Status: current implementation contract as of 2026-06-07.

This is the source of truth for wrkq's product model, CLI behavior, local daemon
surface, configuration, and validation rules. Supporting docs may explain how to
use or operate the system, but when they conflict with this file, this file wins.

The wrkf JSON-RPC stdio contract is intentionally separate at
[`docs/wrkf-rpc.md`](docs/wrkf-rpc.md) because source files and tests reference it as
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
- Attribute every mutation to an external principal ref, with runtime scope
  provenance recorded separately, without pretending to provide authn/authz.
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

`just build` and `just install` build four shipped binaries:

| Binary | Role | Canonical use |
| --- | --- | --- |
| `wrkq` | Human and agent task surface | RPC-backed task/container/comment/attachment CRUD, search, handoffs, and local server lifecycle helpers. |
| `wrkqadm` | Administrative surface | Init, migrations, snapshots, actors, merge, patches, and state import/export. |
| `wrkqd` | Local daemon | Token-auth HTTP API over the same database. |
| `wrkf` | Workflow CLI/RPC surface | Workflow templates, evidence, obligations, effects, transitions, and JSON-RPC stdio. |

`wrkq` is intentionally safe for day-to-day agent use and routes retained
durable behavior through the workrpc JSON-RPC boundary. `wrkqadm` is for
database and administrative operations. `wrkqd` is a local service wrapper, not
a separate source of truth. `wrkf` builds workflow semantics on top of wrkq
tasks.

## 3. Repository Boundaries

Important implementation packages:

| Path | Responsibility |
| --- | --- |
| `cmd/wrkq`, `cmd/wrkqadm`, `cmd/wrkqd`, `cmd/wrkf` | Shipped binary entry points. |
| `internal/rpccli` | RPC-backed production `wrkq` command adapters and command contract tests. |
| `internal/rpccli` | Production day-to-day `wrkq` command layer over the shared workrpc transport. |
| `internal/admincli` | Local-path-only `wrkqadm` database lifecycle and administrative commands. |
| `internal/wrkqd` | Authenticated daemon HTTP/workrpc surface and daemon-owned search indexer. |
| `internal/config` | Config/env loading and defaults. |
| `internal/db` | SQLite open, migrations, schema status. |
| `internal/domain` | Domain structs and validation enums. |
| `internal/store` | Persistence and transactional domain operations. |
| `internal/selectors`, `internal/paths` | Friendly IDs, UUIDs, typed selectors, paths, slug normalization. |
| `internal/search` | Sidecar search service over task, comment, and handoff chunks. |
| `internal/workflow`, `internal/wrkfapi`, `internal/workrpc`, `internal/wrkfcli` | wrkf workflow engine, API layer, unified RPC, and CLI. |
| `packages/client` | Unified Bun TypeScript client (`@wrkq/client`) for the wrkq/wrkf JSON-RPC contract. |
| `mcp-server` | MCP stdio server exposing selected wrkq operations. |
| `pbc/` | Sample wrkf workflow preset and artifact templates. |

## 4. Configuration

Configuration precedence:

1. CLI flags, such as `--db`, `--as`, and `--project`.
2. Environment variables.
3. Nearest `./.env.local`, walking upward to the user's home directory.
4. Platform `.env.local` at `$PRAESIDIUM_HOME/.env.local`, or
   `~/praesidium/.env.local` when `PRAESIDIUM_HOME` is unset.
5. `~/.config/wrkq/config.yaml`.
6. Built-in defaults.

Key variables:

| Variable | Meaning |
| --- | --- |
| `WRKQ_DB` | Primary database locator for production `wrkq`: local SQLite path or `rpc://host[:port]` workrpc endpoint. |
| `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` | Local SQLite database path compatibility inputs; reject `rpc://` values. |
| `WRKQD_TOKEN` / `WRKQD_TOKEN_FILE` | Bearer token used by remote `WRKQ_DB=rpc://...` calls and wrkqd HTTP auth. |
| `WRKQ_CLAIM_TOKEN` / `WRKQ_CLAIM_GENERATION` | Current task-claim authority injected into a claimed runtime; `wrkq set --state completed` forwards it with the active task scope. |
| `WRKQ_ATTACH_DIR` | Attachment byte storage root. |
| `WRKQ_PRINCIPAL_REF` | Mutation principal input: `agent:<id>` or full agent ScopeRef, reduced to `agent:<id>`. |
| `WRKF_PRINCIPAL_REF` | wrkf workflow caller principal input: `agent:<id>` or full agent ScopeRef, reduced to `agent:<id>`. |
| `WRKQ_ACTOR_ID` | Legacy actor/display-cache input; ignored for wrkq-core caller attribution. |
| `WRKQ_ACTOR` / `WRKF_ACTOR` | Legacy actor/display-cache inputs; ignored for caller authority. |
| `WRKQ_PROJECT_ROOT` | Default project/container path for relative task paths. |
| `ASP_PROJECT` | Runtime project fallback when `WRKQ_PROJECT_ROOT` is not explicitly exported. |
| `WRKQ_OUTPUT` | Default output mode. |
| `WRKQ_SEARCH_*` | Search sidecar and dense embedding configuration. |

Database defaults:
- If `.wrkq/wrkq.db` exists in the current directory, it is used.
- Otherwise there is no implicit database path; commands that need a database
  fail with a message naming `WRKQ_DB_PATH` and `--db`.
- `rpc://host` locators default to port `7171`.
- Admin and daemon path-owning surfaces (`wrkqadm --db`, `wrkqd --db`,
  `wrkq server --db-path`) remain local-path-only.

Attachment defaults:
- If the DB is `.wrkq/wrkq.db`, attachments default to `.wrkq/attachments`.
- Otherwise attachment byte storage must be configured explicitly with
  `WRKQ_ATTACH_DIR` or `attach_dir`.

Project-root precedence:

1. `--project` flag, resolved through container selectors.
2. Explicitly exported `WRKQ_PROJECT_ROOT`.
3. `ASP_PROJECT`.
4. `.env.local` or config file project root.

Principal attribution for mutating commands:

1. `--principal-ref <ref>` or `--as <ref>` flag. Accepts `agent:<id>` or a
   full agent ScopeRef such as `agent:<id>:project:<projectId>`, reduced to
   `agent:<id>`. If both flags are supplied they must resolve to the same
   agent.
2. The product principal env: `WRKQ_PRINCIPAL_REF` for wrkq or
   `WRKF_PRINCIPAL_REF` for wrkf. Both accept `agent:<id>` or a full agent
   ScopeRef and reduce it to `agent:<id>`.
3. A validated ASP scope (`ASP_SCOPE_REF`, `ASP_HANDLE`, or
   `ASP_AGENT_ID`+`ASP_PROJECT`) reduced to `agent:<agentId>`.
4. `default_principal_ref` in config.

wrkq validates principal syntax but never creates actors or requires an actor
row for ordinary writes. Bare slugs, actor UUIDs, `A-*` actor IDs, `system:*`,
`WRKQ_ACTOR`, `WRKQ_ACTOR_ID`, `WRKF_ACTOR`, and `default_actor` are not caller
attribution sources. Passing a full ScopeRef as a principal input keeps only
the agent identity; runtime/task/project provenance must travel through
`scope_ref` and delivery fields.

## 5. Domain Model

### Principals and legacy actor compatibility

Canonical caller identity is a principal ref (`agent:<id>`). It does not
require an actor row and is stored separately from runtime scope and delivery
provenance.

The physical `actors` table has **not** been dropped or compacted. It remains an
intentional compatibility surface for:

- `wrkqadm actors` and `/v1/actors/*` legacy admin/display-cache operations;
- `A-*` / actor UUID resource selection in compatibility history reads;
- actor UUID/slug/role display caches on older tasks, comments, relations, and
  `event_log` projections; and
- inert legacy workflow actor columns retained for data-preserving migration.

Those rows and columns are not caller authority. Their historical roles remain:

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

Task labels are replacement-oriented string arrays. `needs_smoketest` is a
reserved automation label that requests Smokey on the label-addition edge in
`created` / `updated` webhook payloads. It is not a lifecycle state.

Resolution values:

`done`, `wont_do`, `duplicate`, `needs_info`

Workflow-assist fields currently present on tasks:

`workflow_preset`, `preset_version`, `phase`, `risk_class`

Valid `risk_class` values:

`low`, `medium`, `high`

Valid task role assignment roles:

`triager`, `owner`, `implementer`, `tester`, `reviewer`, `release_manager`

#### Task claim authority

`wrkq claim <task> --as agent:<id> --scope <task-sessionRef>` atomically claims
an `open`, `in_progress`, or `blocked` task at its canonical wrkqd home. Success
sets the task to `in_progress`, records the holder principal, exact task scope,
server-authenticated node, timestamp, and a monotonically increasing generation,
and returns a one-time claim token. The node is derived exclusively from the
per-node bearer token; callers cannot supply or spoof it.

Normal contention returns `already_claimed` with the current holder and node.
`--take-over` explicitly replaces that holder and increments the generation;
the CLI owns confirmation and `--yes`, while the RPC remains noninteractive.
Completing a claimed task requires the exact current principal/scope/node/token/
generation tuple in the same transaction as the state mutation. Older holders
receive `claim_superseded`, but may still append diagnostic comments. `wrkq
release` clears holdership without changing task state and without resetting the
generation. Claims deliberately have no lease or TTL: task liveness belongs to
HRC and slow-but-live work must never be auto-stolen.

### Subtasks

A task with `--parent-task` is stored as kind `subtask` unless `--kind` is
explicitly provided. Parent links are task-graph edges and may cross
project/container boundaries; the child keeps its own resident container.
Subtasks cannot have subtasks.

### Comments

Comments are append-only task notes. They can be soft-deleted with
`deleted_at` and `deleted_by_principal_ref`, or purged. Default comment listings
and `wrkq cat` output exclude deleted comments.

`wrkq cat` includes comments by default. Use `--exclude-comments` to omit them.
There is no canonical `--include-comments` flag.

### Outcomes

`wrkq set <task> --outcome <text|@file|->` stores a nullable curated summary of
what changed. Blank or whitespace-only input clears it. A final comment remains
the worker's raw record; outcome is its editable plain-terms projection.
Completion never requires an outcome.

Each write appends `task.outcome_set` with the full text snapshot (or explicit
null), task UUID, and production-time resident-container/effective-campaign
stamps. Event-log identity orders the immutable history even when the current
`tasks.outcome` value is later edited or cleared. `wrkq find --has-outcome`
selects tasks whose current outcome is present.

### Campaigns and portfolio

A campaign is an ordinary container with a canonical lifecycle adornment. Its
states are `draft`, `active`, `completed`, and `cancelled`; container `kind` and
`archived_at` remain independent. Conversion supports plain-to-draft and
plain-to-active (the default). Draft campaigns can have resident and explicitly
enrolled tasks, activate explicitly, or cancel without running. Active campaigns
can complete or cancel. Terminal campaigns retain their current members but
reject every new admission path, including create, move, copy, and explicit
enrollment; moving or unenrolling a member out remains legal.

Effective membership is exclusive and is defined as resident-or-enrolled.
`wrkq set <task> --campaign <campaign>` remains the canonical explicit
enrollment mutation. A resident task cannot simultaneously enroll in a foreign
campaign. Campaigns cannot nest under another campaign.

Campaign labels use the exact task-label value semantics: an ordered JSON string
array whose whitespace, case, order, and duplicates are preserved. `[]` clears
labels and the wire value is never `null`. Campaigns have no owner, scheduling,
or priority fields. Display signals such as progress, footprint, missing
outcomes, and activity are derived rather than written back.

`wrkq.container.campaignPortfolio` is the producer-owned portfolio read model.
It returns one complete aggregate snapshot in a single read transaction, with
no pagination or member identities. The default selection is unarchived draft
and active campaigns, ordered by `createdAt` descending and then stable UUID
ascending. Each row contains canonical container data, effective-member counts,
top-level project footprint, missing-outcome count, and
`lastActivityAt = max(campaign.updated_at, effective-member.updated_at)`.
Consumers fetch identities lazily from `wrkq.container.timelineView`, whose
member project identities, footprint, labels, and `lastActivityAt` are derived
from the same producer definitions.

### Container task-count aggregate

`wrkq.container.taskCounts` returns the complete container/project subtree
task-count snapshot in one read transaction and one recursive aggregate query.
It is unpaginated and avoids the former project-linear recursive task-list
sweep. Every non-root selected row carries stable container UUID/ID/path/kind,
its nearest project UUID/ID/slug, and total/active subtree counts.

Counts follow task residency (`tasks.project_uuid`) through descendant
containers. Task parent edges and campaign enrollment are not containment.
Totals include all non-deleted states, including completed, cancelled, and
archived, while excluding `state=deleted` and non-null `deleted_at`. Active
counts are exactly `idea`, `draft`, `open`, `in_progress`, and `blocked` with
clear archive/delete markers. Archived containers are omitted as rows by
default but remain part of ancestor subtrees; `includeArchived` returns their
rows. Project roots, nested containers, and empty containers are always
represented when selected.

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

### Causal lineage (`caused_by`)

`caused_by` is a first-class, multi-valued task field recording the existing
task(s) whose delivered work caused this task to exist as defect/rework. It is
DISTINCT from task relations: it is causal/outcome-quality lineage, stored in a
dedicated normalized edge table (`task_causes`), and does NOT participate in
blocker checks, `relation` output, parent/subtask nesting, moves, archives,
restores, recursive tree behavior, or residency.

- Accepted input is comma-separated friendly task IDs matching `^T-[0-9]{5}$`
  (e.g. `--caused-by T-00012,T-00034`). Each must reference an existing task.
  Bad format, missing reference, and self-causation are validation errors. The
  resolved set is ordered (first-seen) and de-duplicated.
- `wrkq touch <path> --caused-by ...` sets initial lineage on creation.
- `wrkq set <task> --caused-by ...` replaces the full set; `--caused-by ""`
  clears it; omitting the flag leaves it unchanged.
- `wrkq cat` exposes `caused_by` (legacy CLI/compat surfaces) / `causedBy`
  (stable `WrkqTask` DTO) as an array of friendly IDs.
- `wrkq find --caused-by T-XXXXX` returns tasks whose `caused_by` set contains
  that ID, composing with existing path/state/type/kind/sort/pagination.
- `task.created` / `task.updated` event payloads include `caused_by`, so it
  appears in `wrkq log --patch` like other field edits.
- Hard-purging a task referenced by surviving `caused_by` edges is rejected
  unless the dependents are purged together or their lineage is cleared first.

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
- `--as <principal-ref|compat-slug>`
- `--project <container>`
- `--output table|human|json|ndjson|porcelain|yaml|tsv|raw`

Command-local `--json`, `--ndjson`, `--human`, and similar flags override
global output defaults when present.

### Output Defaults

Non-TTY defaults:

| Shape | Default |
| --- | --- |
| list/search/history stream | NDJSON |
| singleton/detail/mutation/content | JSON |

TTY defaults vary by command. Porcelain is a stability modifier: compact,
machine-oriented output and no width/ANSI formatting where applicable.

### Core `wrkq` Commands

Primary task/container surface:

`projects`, `ls`, `tree`, `find`, `search`, `index`, `stat`, `cat`, `touch`,
`set`, `apply`, `diff`, `log`, `watch`, `mkdir`, `rmdir`, `mv`, `cp`, `rm`,
`restore`, `container`, `rename-container`, `comment`, `attach`, `relation`,
`check`, `ack`, `handoff`, `agent-context`, `whoami`, `webhook`, `server`,
`usage`, `agent-info`, `version`, `completion`.

Important behavior:

- `touch` creates tasks. Default state is `open`; default priority is `3`.
- `set` updates task fields and supports bulk operation over refs or stdin.
  `wrkq set <project> --root <path>` is the dedicated project exception: it
  registers a checkout root on one top-level project, normalizing paths beneath
  `$HOME` to `~/...`; `--root ""` clears it. `wrkq projects` returns the stored
  string verbatim, and consumers are responsible for expanding `~` locally.
- `apply` updates description/specification from markdown, YAML, JSON, or stdin.
  Metadata in input is ignored unless `--with-metadata` is passed.
- `cat` prints markdown with YAML front matter and comments on a TTY; when
  piped, it defaults to JSON. Use `--output raw` for markdown in pipelines.
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

`init`, `migrate`, `db snapshot`, `actors ls`, `actors add`, `state export`,
`state import`, `state verify`, `patch create`, `patch validate`, `patch apply`,
`patch rebase`, `patch summarize`, `merge`, `attach path`, `doctor`,
`config doctor`, `version`.

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

Both task discovery surfaces accept a repeatable singular label filter:

```bash
wrkq find --label refactor --label urgent --state all --type t
wrkq search "prompt shaping" --label refactor --state all
```

Each `--label VALUE` requires exact, case-sensitive membership in the canonical
`tasks.labels` JSON array. Repeated distinct values are logical AND; duplicate
values are idempotent. Filtering is server-side and happens before paging, so
search-sidecar text/metadata and substring matches cannot satisfy it. Search
still requires its text query. The existing `--labels` flag remains the
JSON-array write surface on task mutations.

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
| `/v1/actors/list` | List legacy actor display-cache rows. |
| `/v1/actors/create` | Explicit legacy/admin actor-cache creation; not required for writes. |
| `/v1/actors/update` | Update legacy actor display-cache rows. |

There are no handoff HTTP routes in the current daemon.

## 10. Persistence and Concurrency

Canonical state lives in one SQLite database. Connections are opened with WAL
mode and a busy timeout. Migrations live under `internal/db/migrations` and are
embedded into the binaries.

Mutable rows carry `etag INTEGER`. Mutating commands that expose `--if-match`
must reject stale writes when the current etag differs from the expected etag.

Events are appended to `event_log` with resource type, UUID, event type, etag,
`principal_ref`, optional `scope_ref`, legacy actor UUID when available,
timestamp, and JSON payload. The event log is an audit trail, not a hash-chain
ledger.

`state export`, `state import`, and `state verify` provide canonical snapshot
operations. The former Git-ops bundle workflow is not part of the production
CLI, admin, daemon, or workrpc contract.

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

- Role/principal authorization is still shallow compared with a real authorization
  system.
- Evidence facts are validated as small decision surfaces; they do not prove the
  truth of referenced artifacts.
- Hook/effect handler catalogs are runtime filesystem dependencies, not
  content-pinned workflow dependencies.
- Production action-handler and agent-work manifests for scheduler-driven agent
  work are a separate proposed contract: ASP owns the agent/space handler source
  and prompt bundles; ASPC compiles per-agent work manifests; HRC exposes stable
  agent-manifest list/match endpoints; wrkf imports handler contract and
  assignability snapshots tied to workflow template action contract refs before
  unattended scheduler claims are enabled. Lance explicitly rejected adding a
  scheduler-owned hash/provenance layer for handler manifests, prompts, schemas,
  capabilities, or delivery refs. This does not make low-level hook/effect
  catalogs durable by implication.

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
| `internal/rpccli/embedded/WRKQ-USAGE.md` | Embedded full agent usage block. |
| `internal/rpccli/embedded/AGENT-WRKQ-USAGE.md` | Embedded compact quick reference. |
| `mcp-server/README.md` | Local package README for the MCP stdio wrapper. |
| `pbc/templates/` | Workflow artifact templates used by the PBC sample preset. |
| `vendor/` | Third-party vendored documentation; not a wrkq spec surface. |

Historical scratch/proposal markdown should live in git history or wrkq tasks,
not as top-level repo docs.
