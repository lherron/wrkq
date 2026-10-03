# wrkq

wrkq is a local-first work ledger shared by humans and coding agents. Tasks,
conversations, workflows and project events live in one SQLite database,
served by a small daemon and driven through filesystem-flavored CLIs with
stable machine-readable output. Every mutation is attributed to a principal
and recorded in an append-only event log.

The easiest way to put an agent on wrkq is to have it run `wrkq info` (and
`wrkc info`, `wrkp info`) at session start; each prints an embedded agent
guide.

## Binaries

`just build` and `just install` build six binaries:

| Binary | Role |
| --- | --- |
| `wrkq` | Day-to-day task surface: containers, tasks, comments, attachments, relations, claims, search, handoffs, promises, campaigns, monitoring |
| `wrkc` | Collaboration: rooms, addressed envelopes, reply obligations, inbox |
| `wrkp` | Project events: post foreign facts (git, CI, `just` runs) and read the merged project timeline |
| `wrkf` | Workflow engine: templates, instances, evidence, obligations, effects and transitions layered on wrkq tasks |
| `wrkqd` | Daemon: token-authenticated HTTP + JSON-RPC API over the database |
| `wrkqadm` | Administration: init, migrations, snapshots, state export/import, patches, doctor. Not for agents |

The repository also ships `@wrkq/client` (`packages/client`), a TypeScript
client for the same JSON-RPC surface.

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap lherron/wrkq
brew install wrkq
```

### From source

Requires the Go toolchain named in `go.mod` (currently Go 1.25) and
[`just`](https://github.com/casey/just). SQLite is bundled.

```bash
git clone https://github.com/lherron/wrkq.git
cd wrkq
just build      # binaries in ./bin
just install    # install to ~/.local/bin
```

### Agent startup hook

```bash
echo "=== This project uses wrkq ==="
wrkq info 2>/dev/null || echo "(wrkq info failed or not available, notify user)"
```

## Quick start

```bash
# Point at a database: a local file, or rpc://host[:port] for a wrkqd
export WRKQ_DB=$PWD/.wrkq/wrkq.db
wrkqadm init

# Create a project and work inside it (a leading / is root-absolute)
wrkq mkdir /myproject --kind project
export WRKQ_PROJECT_ROOT=myproject

# Tasks
wrkq touch implement-feature -t "Implement new feature" -d "Description here"
wrkq tree
wrkq cat T-00001

# Work it
wrkq set T-00001 --state in_progress
wrkq comment add T-00001 -m "Started implementation"
wrkq set T-00001 --state completed

# Structured output for scripts and agents
wrkq find --state all --json
wrkq cat T-00001 --json --one
```

## Core concepts

| Concept | Summary |
| --- | --- |
| **Container** | Hierarchical unit of organization. Kinds: `project`, `directory`, `feature`, `area`. Top-level projects can register checkout roots. |
| **Task** | The work item. Kinds `task`, `spike`, `bug`, `chore`; priority `1` (highest) to `4`; labels, due dates, assignee, metadata, description and specification. |
| **Named subtask** | An assignment owned by a task, addressed `T-00001.render`. Created with `wrkq touch T-00001.render --subtask`; moves with its owner and is hidden from default listings (`--subtasks` includes it). |
| **Child task** | An independent task linked with `--parent-task`; it keeps its own ID and project. |
| **Comment** | Append-only task or container notes, optionally a judgment kind (`blocker`, `decision`, `postmortem`, `digest`). |
| **Attachment** | File bytes stored outside the database, keyed by task UUID. |
| **Relation** | `blocks`, `relates_to`, `duplicates` edges between tasks; `caused_by` records rework lineage. |
| **Claim** | Atomic, cross-node holdership of a task by a session scope (`wrkq claim`, `wrkq release`). |
| **Campaign** | A container adorned with a brief, specification and lifecycle; tasks from other projects can enroll in it. |
| **Promise** | A recorded intent to revisit a subject at a future time, attached to a task or container. |
| **Handoff** | A durable note one agent session leaves for the next. |
| **Principal** | The caller identity (`agent:<id>`) every mutation is attributed to. Runtime/session scope is recorded separately. |

### Task states

`idea`, `draft`, `open`, `in_progress`, `blocked`, `completed`, `cancelled`,
`archived`, `deleted`. The common path is
`idea -> draft -> open -> in_progress -> completed`. `find` and `tree` show the
actionable set (`draft`, `open`, `in_progress`, `blocked`) unless given
`--state <state>` or `--state all`.

### Addressing

- **Path**: `myproject/subproject/task-slug`, relative to the current project;
  a leading `/` is root-absolute.
- **Friendly ID**: `T-00123`, `P-00007`, `T-00123.render` (named subtask).
  IDs are global and work from any project.
- **UUID**: the full database UUID.

## Common workflows

```bash
# Find and read work
wrkq tree
wrkq find --label urgent --state all --type t
wrkq index update && wrkq search 'flaky migration' --state all --limit 10
wrkq log T-00001 --oneline

# Relations and readiness
wrkq relation add T-00002 blocks T-00003
wrkq check blocked T-00003

# Named subtasks
wrkq touch T-00002.render --subtask
wrkq ls T-00002 --subtasks

# Cross-node work: claim at the canonical wrkqd home (requires a
# node-identified daemon connection). The claim token and generation it returns
# travel as WRKQ_CLAIM_TOKEN / WRKQ_CLAIM_GENERATION for completion and release.
wrkq claim T-00001 --scope agent:cody:project:myproject:task:T-00001
wrkq release T-00001

# Campaigns, promises, handoffs
wrkq mkdir launch
wrkq campaign convert launch --state active
wrkq promise add --task T-00001 --in 7d --subject "Revisit perf numbers"
wrkq handoff list --json
```

## Collaboration (`wrkc`)

`wrkc` keeps conversations and reply obligations in the same ledger. A room
holds a conversation (a task's room, a campaign's room, a project room or an
ad-hoc pair room); an envelope is one message in it. Only `--to` presents a
message to someone; replying to a sender discharges their outstanding
requests.

```bash
wrkc inbox
wrkc show EN-00001
wrkc log T-00001 --limit 20

wrkc say T-00001 --to cody@wrkq:T-00001 - <<'TEXT'
State the objective, scope and required completion evidence.
TEXT

wrkc defer EN-00002 --reason 'Waiting on installed validation' --retry-after 10m
```

See [docs/wrkc-reference.md](docs/wrkc-reference.md) for routing, obligations
and identity.

## Watching work

```bash
wrkq monitor watch --raw --since 100                              # raw event-log tail
wrkq monitor watch T-00001 --until state=completed --timeout 30m  # per-task stream
wrkq monitor wait EN-00001 --until terminal --timeout 10m         # block on a reply
wrkq timeline myproject                                          # composite timeline
wrkq webhook add http://127.0.0.1:18451/api/webhooks/wrkq        # global webhook

wrkp post myproject --type ci.passed -m "CI green" --attr sha=abc123
wrkp log myproject --since 24h                                   # project events
```

`wrkp git` and `wrkp just` post facts from Git hooks and `just` runs. Monitor
exit codes: `0` condition met, `1` unmet at timeout or stall, `2` selector
error, `3` stream error.

## Workflows (`wrkf`)

wrkf attaches workflow instances to wrkq tasks without replacing the task
lifecycle. Templates are validated and installed by id, version and hash
(`wrkf workflow`); agents act through runs (`wrkf action`, `wrkf next`), record
evidence and resolve obligations, and supervisors recover stuck instances.
`wrkf rpc` serves the unified JSON-RPC stdio contract in
[docs/wrkq-wrkf-rpc.md](docs/wrkq-wrkf-rpc.md); the
[wrkf recovery guide](docs/wrkf-rpc.md) covers clients and recovery.

## Running the daemon

A single canonical `wrkqd` owns the database; other processes and nodes reach
it with `WRKQ_DB=rpc://host[:port]` (default port `7171`).

```bash
wrkq server start              # or: wrkqd -addr 127.0.0.1:7171 -token <token>
wrkq server status
wrkq server health
wrkqd -node-tokens-file ~/.config/wrkq/node-tokens   # per-node bearer tokens
```

When an upgrade carries schema migrations, run `wrkqadm migrate` against the
daemon's database before restarting it, and upgrade the daemon before its
clients. Backups (`wrkqadm db snapshot`, `wrkqadm state`), health checks and
the HTTP route surface are covered in
[docs/wrkq-operations.md](docs/wrkq-operations.md).

## Configuration

The database locator resolves in this order:

1. `--db`
2. `WRKQ_DB`: a local path or `rpc://host[:port]`
3. `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` (local paths only; refused rather than
   ignored when they disagree with a higher locator)
4. Nearest `.env.local`, walking upward from the current directory
5. `$PRAESIDIUM_HOME/.env.local` (or `~/praesidium/.env.local`)
6. `~/.config/wrkq/config.yaml`
7. `.wrkq/wrkq.db` in the current directory, if it exists

`wrkqadm config doctor` reports which source won. `--project` (or
`WRKQ_PROJECT_ROOT`) selects the project that relative paths resolve under.

Caller authority is principal-only: `--as agent:<id>` / `--principal-ref`, or
`WRKQ_PRINCIPAL_REF=agent:<id>` (`WRKF_PRINCIPAL_REF` for wrkf). Legacy actor
values are not authority. Attribution is not authentication: wrkq records who
claims to act, and `wrkqd` tokens gate who may connect.

## Output formats

`--output` accepts `table`, `human`, `json`, `ndjson`, `porcelain`, `yaml`,
`tsv` and `raw`; most read commands also take `--json`, `--ndjson` and
`--porcelain`. `wrkq cat` prints human detail on a TTY and JSON when piped;
JSON `cat` is always an array unless `--one` is given. List commands return a
cursor when more results exist.

## Development

```bash
just test              # unit and integration tests
just verify-full       # full gate, including smoke and wrkf adoption checks
scripts/agent-check.sh # no-network / sandboxed validation
just doc-links         # documentation link check
```

Guard recipes (`just layer-boundary`, `just surface-guard`,
`just suppression-lint`, `just rot-sensor`) enforce architecture and surface
contracts. See [AGENTS.md](AGENTS.md) for agent-specific working rules.

## Documentation

- [docs/SPEC.md](docs/SPEC.md): canonical product, domain, CLI and daemon
  contract. It wins when docs disagree.
- [docs/wrkq-overview.md](docs/wrkq-overview.md): architecture and data model
- [docs/wrkq-cli-reference.md](docs/wrkq-cli-reference.md): command reference
- [docs/wrkq-concepts.md](docs/wrkq-concepts.md): handoffs, search, monitoring,
  attribution
- [docs/wrkq-operations.md](docs/wrkq-operations.md): database location,
  backups, daemon deployment
- [docs/wrkc-reference.md](docs/wrkc-reference.md): rooms and obligations
- [docs/wrkf-rpc.md](docs/wrkf-rpc.md): wrkf RPC recovery and client guide

## License

MIT License. See [LICENSE](LICENSE).
