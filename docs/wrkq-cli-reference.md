---
id: wrkq/cli-reference
title: wrkq CLI reference
kind: reference
authority: descriptive
status: active
visibility: internal
provenance: authored
---

# wrkq CLI reference

Command surface and examples captured from `wrkq --help` and per-subcommand
`--help` output in this repo. Flags shown are the ones most commonly used;
run `wrkq <command> --help` for the exhaustive, current list — this page is
descriptive, not the contract of record (`docs/SPEC.md` is).

## Global flags

Every subcommand accepts:

```text
--db string              Path to database file (overrides WRKQ_DB_PATH)
--as string              Alias for --principal-ref: agent:<id> or full agent ScopeRef
--principal-ref string   Caller principal for write attribution
--project string         Project to operate under (overrides WRKQ_PROJECT_ROOT)
--output string          table, human, json, ndjson, porcelain, yaml, tsv, raw
```

Non-TTY output defaults to NDJSON for list/search/history streams and JSON for
singleton/detail/mutation/content responses. `--json`/`--ndjson`/`--human`
flags on individual commands override the global `--output` default.

## Command index

```text
ack            agent          agent-context  agent-info
apply          attach         cat            check
check-inbox    claim          comment        completion
container      cp             diff           find
handoff        help           index          log
ls             mkdir          monitor        mv
projects       relation       release        rename-container
restore        rm             rmdir          rpc
search         server         set            stat
touch          tree           usage          version
watch          webhook        whoami
```

## Containers and projects

```bash
# Create a top-level project container
wrkq mkdir myproject

# Create a nested container
wrkq mkdir myproject/backend

# List top-level projects
wrkq projects

# Tree view: containers + draft/open tasks by default
wrkq tree myproject
wrkq tree myproject --open        # narrow to open tasks
wrkq tree myproject -a            # include archived, empty containers
wrkq tree myproject -L 2          # max depth 2

# Container detail / update
wrkq container cat myproject
wrkq container set myproject --webhook-url http://127.0.0.1:18451/api/webhooks/wrkq

# Register a checkout root on a top-level project (paths under $HOME stored as ~/...)
wrkq set myproject --root ~/praesidium/myproject
```

## Task CRUD

```bash
# Create a task (default state: open, default priority: 3)
wrkq touch myproject/implement-feature \
  -t "Implement new feature" \
  -d "Description here" \
  --priority 2 --kind bug

# Create a subtask
wrkq touch myproject/sub-piece --parent-task T-00001

# Read a task (TTY: markdown + front matter + comments; piped: JSON)
wrkq cat myproject/implement-feature
wrkq cat T-00001 --output raw       # markdown in pipelines
wrkq cat T-00001 --exclude-comments

# Update fields (set is aliased as edit; supports bulk over refs/stdin)
wrkq set T-00001 --state in_progress
wrkq set T-00001 --priority 1 --labels '["needs_smoketest"]'
wrkq set T-00001 --caused-by T-00012,T-00034   # replace causal lineage
wrkq set T-00001 --caused-by ""                # clear causal lineage

# Update description/specification from a file, YAML/JSON, or stdin
wrkq apply T-00001 spec.md
wrkq apply T-00001 - --format yaml <<< "..."
wrkq apply T-00001 spec.md --with-metadata     # also update title/state/priority/due_at

# Archive (default) vs. permanent purge
wrkq rm T-00001
wrkq rm T-00001 --purge --yes

# Restore an archived/deleted task (defaults to open)
wrkq restore T-00001

# Move / copy across containers
wrkq mv T-00001 otherproject/new-slug
wrkq cp T-00001 otherproject/copy-slug
```

## Discovery: find, search, stat, diff, log

```bash
# find: defaults to active items, excludes archived/deleted/idea
wrkq find myproject --state open --kind bug
wrkq find --claimed-by agent:cody
wrkq find --caused-by T-00012
wrkq find --ack-pending             # completed/cancelled, not yet acknowledged

# search: FTS over task+comment text, defaults to state=open
wrkq search "rate limiting" myproject
wrkq search "auth flow" --state all --sort updated_at --limit 5
wrkq search "config" --explain      # include ranking diagnostics

# stat: quick task/container status
wrkq stat T-00001

# diff: compare two tasks or task versions
wrkq diff T-00001 T-00002
wrkq diff T-00001 --unified 5

# log: change history for a task or container
wrkq log T-00001 --oneline
wrkq log T-00001 --patch            # detailed payload changes
wrkq log T-00001 --since 2026-07-01 --limit 100
```

## Comments and attachments

```bash
wrkq comment add T-00001 -m "Started implementation"
wrkq comment cat T-00001
wrkq comment rm T-00001 <comment-id>

wrkq attach put T-00001 ./notes.pdf
wrkq attach ls T-00001
wrkq attach get T-00001 <attachment-id> -o ./out.pdf
wrkq attach rm T-00001 <attachment-id>
```

## Relations and lineage

```bash
wrkq relation add T-00001 T-00002 --kind blocks
wrkq relation ls T-00001
wrkq relation rm T-00001 T-00002 --kind blocks

# Pre-flight blocker check
wrkq check blocked T-00001
```

## Task claims (cross-node work)

```bash
# Atomically claim an open/in_progress/blocked task at its canonical wrkqd home
wrkq claim T-00001 --as agent:cody \
  --scope agent:cody:project:wrkq:task:T-00001

# Explicit takeover of an existing holder (increments generation)
wrkq claim T-00001 --as agent:cody --take-over --yes

# Release holdership without changing task state
wrkq release T-00001
```

Completing a claimed task (`wrkq set --state completed`) requires the exact
current principal/scope/node/token/generation tuple in the same transaction
as the state mutation; the claim token/generation travel via
`WRKQ_CLAIM_TOKEN`/`WRKQ_CLAIM_GENERATION` in the claimed runtime.

## Monitoring and streaming

```bash
# Raw event-log tail
wrkq watch                          # all events
wrkq watch --since 100
wrkq watch --ndjson

# Structured per-task monitor stream (built for the Claude Monitor tool)
wrkq monitor watch T-04466 --state-only --until state=completed --timeout 30m
wrkq monitor wait T-1 T-2 T-3 --until all-terminal --stall-after 30m
```

`wrkq monitor` exit codes: `0` condition met, `1` timeout/stall, `2` selector
error, `3` stream error.

## Search index

```bash
wrkq index status
wrkq index rebuild
wrkq index update      # index pending canonical changes
wrkq index vacuum
wrkq index pause
wrkq index resume
```

## Handoffs

```bash
wrkq handoff create --title "Continue T-00001" -m "Left off after step 3..."
wrkq handoff list                       # defaults to status=pending
wrkq handoff get H-00001
wrkq handoff acknowledge H-00001
wrkq handoff search "auth flow"
```

## Agent ergonomics

```bash
wrkq agent-info                # embedded usage doc, for startup hooks
wrkq agent-context             # resolve/print the active agent scope
wrkq agent-context --scope agent:cody:project:wrkq
wrkq whoami                    # resolved principal + runtime scope
wrkq whoami --json
```

## Daemon lifecycle (`wrkq server`)

```bash
wrkq server start
wrkq server status
wrkq server health
wrkq server stop
wrkq server restart
wrkq server serve --addr 127.0.0.1:7171 --token dev   # foreground
```

Or run the daemon binary directly:

```bash
wrkqd -addr 127.0.0.1:7171 -token dev -db /path/to/wrkq.db
```

## Webhooks

```bash
wrkq webhook add http://127.0.0.1:18451/api/webhooks/wrkq
wrkq webhook list
wrkq webhook rm http://127.0.0.1:18451/api/webhooks/wrkq
```

Global webhooks (via `wrkq webhook`) live on the internal root container and
fire for every project. Per-container webhooks use
`wrkq container set --webhook-url` and are inherited down that container's
subtree.

## Administrative surface (`wrkqadm`)

`wrkqadm` is local-path-only (rejects `rpc://`) and is not meant to be exposed
to agents. See `/docs/wrkq/operations` for the operational flows.

```text
init         Initialize the wrkq database and configuration
migrate      Run any pending database migrations
db snapshot  Create a WAL-safe database snapshot
actors ls/add
state export/import/verify
patch create/validate/apply/rebase/summarize
merge        Merge a project database into a canonical database (disabled)
attach path
doctor       Check database health and configuration
config doctor
```

## Exit codes

| Code | General meaning |
| --- | --- |
| `0` | Success. |
| `1` | Generic runtime, IO, DB, or validation error unless a command defines a narrower code. |
| `2` | Usage error, invalid selector context, or unresolvable handoff scope. |
| `3` | Not found in the general contract; handoff create uses it for idempotency payload mismatch. |
| `4` | Conflict, or not-found for command-specific handoff `get` behavior. |
| `5` | Partial success for bulk operations; already-acknowledged for handoff acknowledge. |
| `6` | Handoff acknowledge `etag` mismatch. |

Command-specific structured errors may refine these; prefer machine output
plus the command's own `--help` for automation.
