---
id: wrkq/operations
title: wrkq operations - database, backup, daemon, attribution
kind: runbook
authority: descriptive
status: active
visibility: internal
provenance: authored
---

# wrkq operations

Operational reference for locating, backing up, and running wrkq's canonical
database and daemon, plus how principal attribution actually resolves at
runtime. Derived from `docs/SPEC.md`, `wrkqadm`/`wrkq` `--help` output, and
live configuration in this environment.

## Database location

Resolution order:

1. `--db` CLI flag.
2. `WRKQ_DB` — a local path, or `rpc://host[:port]` (default port `7171`) to
   use a remote canonical `wrkqd`. This is the isolation lever: point it at a
   scratch file to keep a smoke test off the shared ledger.
3. `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` — local-path-only compatibility
   inputs; reject `rpc://`. They never override a locator from `WRKQ_DB`
   (process env or `.env.local`) or `config.yaml` `db_locator`: when one of
   those names a different database, commands refuse with an error naming
   `WRKQ_DB` instead of silently using it. An explicit `--db` moots the
   conflict; a matching path is honoured.
4. Nearest `.env.local`, walking upward from the current directory.
5. Platform `.env.local` at `$PRAESIDIUM_HOME/.env.local` (falls back to
   `~/praesidium/.env.local` when `PRAESIDIUM_HOME` is unset).
6. `~/.config/wrkq/config.yaml`.
7. Built-in default: if `.wrkq/wrkq.db` exists in the current directory it is
   used; otherwise there is no implicit path, and DB-needing commands fail
   with a message naming `WRKQ_DB` and `--db`. `wrkqadm init` without
   `--db` initializes the configured local locator and refuses an `rpc://`
   one rather than silently creating `.wrkq/wrkq.db`.

Admin/daemon path-owning surfaces are local-path-only by design and reject
`rpc://`: `wrkqadm --db`, `wrkqd --db`, `wrkq server --db-path`.

`wrkq whoami` shows the locator the production CLI actually uses (see the
live RPC example below). `wrkqadm config doctor` reports the **local**
configuration surface:

```bash
wrkqadm config doctor
wrkqadm config doctor --json
```

Example output (an agent seat in the `wrkq` repo root, with `WRKQ_DB` set to
the remote daemon and no local `.wrkq/wrkq.db`):

```text
Database:
  WRKQ_DB_PATH:
    Source: environment variable WRKQ_DB
    Status: ✗ File does not exist

Attachments:
  WRKQ_ATTACH_DIR:
    Source: config file
    Status: ✗ Directory does not exist

Project Root:
  WRKQ_PROJECT_ROOT: wrkq
    Source: environment variable WRKQ_PROJECT_ROOT
    Status: ✓ Loaded

Actor:
  Principal: (not set)
    Source: default_principal_ref setting
    Status: ✗ Not configured
```

Read it with three caveats. `wrkqadm` never follows `rpc://`, so a shell
whose `WRKQ_DB` names a remote daemon (where `wrkq whoami` resolves fine)
still shows no local DB. The `Source` labels name the highest-precedence input
that is set, not the one that won: the built-in `.wrkq/wrkq.db` default
reads `config file`. And `Principal` ignores the seat scope, so a seat that
attributes writes correctly still reads `(not set)` (T-11431).

### Live RPC example

This environment routes production `wrkq` calls to a remote daemon rather
than a local file, through the platform `~/praesidium/.env.local`:

```text
WRKQ_DB=rpc://mini
WRKQD_TOKEN_FILE=/Users/lherron/.config/wrkq/node-token
```

```bash
$ wrkq whoami
{
  "db_locator": "rpc://mini",
  "db_mode": "remote",
  "principal_ref": "agent:mable",
  "remote_endpoint": "mini:7171",
  "scope_ref": "agent:mable:project:taskboard:task:primary"
}
```

The platform-wide canonical store lives on mini at
`/Users/lherron/.praesidium-data/wrkq/wrkq.db`, served by `wrkqd` (see
[Daemon deployment](#daemon-deployment-wrkqd)); other machines reach it as
clients via `WRKQ_DB=rpc://<host>:7171`.

## Backup and snapshotting

Two distinct mechanisms exist; pick based on what you need.

### 1. WAL-safe binary snapshot (`wrkqadm db snapshot`)

Creates a consistent point-in-time copy of the SQLite file using SQLite's
online backup API — the result is immediately usable without accompanying
WAL/SHM files. Intended for ephemeral working copies (agents, CI), not as the
canonical backup format of record.

```bash
wrkqadm db snapshot --out ./wrkq-snapshot-$(date +%Y%m%d).db
wrkqadm db snapshot --out ./snap.db --json    # emit a JSON manifest
```

### 2. Canonical JSON state export (`wrkqadm state`)

**A state snapshot is NOT a disaster-recovery artifact.** For DR use a
file-level copy: `wrkqadm db snapshot` (above) or a copy of the database file
taken with the daemon stopped.

A snapshot is the complete *task-ledger domain* (T-07498):

- every container, task, comment, promise and task relation (`links`) —
  archived containers and tasks, deleted-state tasks and soft-deleted comments
  included, because live rows reference them;
- every domain column of those rows, written back verbatim on import (ids,
  etags, timestamps, attribution, claims);
- friendly-id high-water marks (`meta.sequences`), so a restore never reissues
  the id of a purged row.

Outside the snapshot model, and therefore NOT restored by import: rooms,
room members, envelopes and presentations, handoffs, wrkf workflow runtime
(`workflow_*`), attachments and their blobs, task causes / role assignments /
transitions / evidence items, the event log and project events
(`--include-events` adds those two for reading; import does not replay them),
legacy actors and `*_actor_uuid` columns (attribution is principal-only), and
idempotency caches. Normalised on the way through: `tasks.start_at`/`due_at`
`''` restore as NULL; label sets (`''`, `[]`, element order) restore as a
sorted JSON array or NULL.

Output is deterministic JSON — top-level sections in a fixed order, every
object key below them sorted, no insignificant whitespace — byte-for-byte
identical for identical ledger state, designed for diffing and
version-controlled/patch-first workflows.

```bash
wrkqadm state export --out .wrkq/state.json
wrkqadm state export --out full.json --include-events
wrkqadm state verify .wrkq/state.json     # canonical-bytes determinism check
wrkqadm --db /path/fresh.db init          # root + seeded inbox counts as empty
wrkqadm --db /path/fresh.db state import --from .wrkq/state.json
```

Import requires an essentially empty database (root plus at most the inbox
`init` seeds). `--force` truncates the modelled tables first; deleting those
rows cascade-deletes or nulls the out-of-model rows that reference them
(rooms, envelopes, workflow instances, attachments, ...), so `--force` refuses
when any exist unless `--allow-cascade` is also given. `wrkqadm patch apply`
writes through the same forced import and takes the same flag. Never run
either against a live ledger.

RFC 6902 JSON patches can be layered on top of state snapshots for
patch-first Git workflows:

```bash
wrkqadm patch create ...
wrkqadm patch validate ...
wrkqadm patch apply ...
wrkqadm patch rebase ...
wrkqadm patch summarize ...
```

`docs/SPEC.md` §10 records that "the former Git-ops bundle workflow is not
part of the production CLI, admin, daemon, or workrpc contract" —
`state export`/`import`/`verify` are the current canonical snapshot
operations.

## Database health

```bash
wrkqadm doctor                 # schema/config/attachment health checks
wrkqadm doctor --verbose --json
wrkqadm doctor --fix           # auto-repair issues where supported
```

`wrkqadm migrate` applies any pending migrations (embedded from
`internal/db/migrations`); `wrkqadm init` initializes a fresh database and
config. Applying migrations (including init) refuses a serving database before
opening SQLite. `migrate --dry-run` and `--status` use `mode=ro`/`query_only`
connections and remain available while the daemon serves. The typed
`MIGRATION_DATABASE_SERVING` error names the daemon PID or loaded launchd job
and prints the bootout → wait unloaded/process exited → migrate → bootstrap
sequence. No live-migration override is provided.

The guard uses the job's resolved database and `lsof` to detect existing daemons,
plus a persistent `<db>.migration-lock` lease to exclude startup during
migration. Do not remove that sidecar while a process holds it. Symlink and
relative paths share the resolved lease. Applying migrations and daemon startup
refuse hardlinked databases and URI/query DSNs, which can split SQLite WAL or
lease identity. Inspection failures fail closed. Disable legacy standalone launchers before migrating:
a future pre-guard binary cannot participate in the new lease protocol.

## Actor attribution

wrkq attributes every mutation to a principal ref (`agent:<id>`) but performs
no authentication. Resolution precedence for mutating commands:

1. `--principal-ref <ref>` / `--as <ref>` flag — accepts `agent:<id>` or a
   full agent ScopeRef (e.g. `agent:<id>:project:<projectId>`), reduced to
   `agent:<id>`. If both flags are given, they must resolve to the same
   agent.
2. `WRKQ_PRINCIPAL_REF` env var — same accepted forms.
3. A validated runtime scope: `AGENT_SCOPE_REF`, or `ASP_SCOPE_REF`, or
   `ASP_HANDLE`, or `ASP_AGENT_ID` + `ASP_PROJECT`, in that order — reduced
   to `agent:<agentId>`.
4. `default_principal_ref` in `~/.config/wrkq/config.yaml`.

Explicitly **not** attribution sources: bare slugs, actor UUIDs, `A-*` actor
IDs, `system:*`, `WRKQ_ACTOR`, `WRKQ_ACTOR_ID` — these are
legacy/display-cache inputs only. Passing a full ScopeRef as a principal
input keeps only the reduced agent identity; the fuller runtime scope
provenance is recorded separately via `scope_ref`.

Inspect what will actually be used:

```bash
wrkq whoami                 # resolved principal_ref + scope_ref
wrkq agent-context           # scope resolution debug view, works without a DB connection
wrkq agent-context --scope agent:cody:project:wrkq
```

## Daemon deployment (`wrkqd`)

`wrkqd` serves token-auth HTTP (REST `/v1/*` + JSON-RPC `/v1/rpc`) over a
configured DB, on TCP or a Unix socket.

```bash
# Run directly
wrkqd -addr 127.0.0.1:7171 -token dev -db /path/to/wrkq.db
wrkqd -unix /tmp/wrkqd.sock -token dev -db /path/to/wrkq.db

# Per-node bearer tokens (nodeId=token pairs), supersedes -token
wrkqd -node-tokens "mini=abc123,max3=def456" -db /path/to/wrkq.db
wrkqd -node-tokens-file /path/to/tokens.txt -db /path/to/wrkq.db

# Inspect build and RPC compatibility metadata without starting the daemon
wrkqd version
wrkqd version --json

# Via the wrkq CLI wrapper
wrkq server serve --addr 127.0.0.1:7171 --token dev   # foreground
wrkq server start                                      # background
wrkq server status
wrkq server health
wrkq server stop
wrkq server restart
```

`-unsafe-no-token` allows a non-loopback listener without a bearer token —
dev-only, never for a reachable deployment.

On macOS, `wrkq server restart` reloads the launchd job (`bootout`, wait for the
job to disappear, `bootstrap`) instead of `launchctl kickstart -k`, and reports
success only after the daemon answers. launchd pins a code requirement to the
cdhash of the binary present when the job was bootstrapped; `go build` re-signs
adhoc on every build, so a rebuilt `wrkqd` fails the pinned requirement and
every respawn inside the existing job is SIGKILLed with `OS_REASON_CODESIGNING`,
writing nothing to the log. Only a bootout/bootstrap cycle re-derives it.

That also means an install without a restart leaves a healthy-looking daemon
armed to die on its next respawn — keepalive, a crash, a reboot, or anyone's
restart, unbounded time later. When `just install` replaces a `wrkqd` that a
running job still holds, its read-only migration probe permits an automatic
restart when there are no pending migrations. It resolves the database from the
job's own `--db` or launchd environment, not the caller's shell
(`scripts/resolve-job-db.sh`). If resolution or the probe fails, or migrations
are pending, it leaves the daemon armed and exits non-zero after installing.
For a migration release, take a backup, boot out the job and wait for it to
unload and its process to exit, then run `just install`, `wrkqadm --db <path>
migrate`, bootstrap the plist, and check `wrkq server health`. Never apply
migrations under a serving daemon: that preceded canonical-store corruption on
2026-10-04 (T-10158). `wrkq server stop` refuses under launchd. `wrkq server
status` reports `binaryStale`, and `wrkq server health` fails on it. Only the
authorized service operator activates the canonical daemon.

`wrkq server health` resolves the daemon's token the way the CLI transport does
(`WRKQD_TOKEN`, else `WRKQD_TOKEN_FILE`). On a node running per-node tokens an
unauthenticated caller gets HTTP 401, which is reported as `auth:
unauthorized` and is not a failure — the daemon answered, so liveness is proven
and only the token is missing. Exit status stays reserved for a daemon that is
down or one running an image the installed binary no longer matches.

`wrkq server status` and `wrkq server health` probe the address the launchd job
actually binds (its `--addr` argument or `WRKQD_ADDR`), not the `127.0.0.1:7171`
default.

The canonical daemon runs on mini as the launchd job
`com.praesidium.wrkq-server`: `wrkqd --addr 0.0.0.0:7171 --db
/Users/lherron/.praesidium-data/wrkq/wrkq.db --node-tokens-file
~/.config/wrkq/node-tokens`. Its stdout, `var/logs/wrkq/wrkqd.log`, is the
request log: one JSON `http.request` line per request (logging L3), with the
JSON-RPC method as `op`. Successful follow-loop polls, listed in
`sampledPollOps`, log at most once a minute per caller, with a `suppressed`
count. Its stderr, `var/logs/wrkq/wrkqd.err.log`, holds the diagnostics. Both
sit under `var/logs/*/`, which is where the collective's log-rotation job
looks. The repo's
`launchd/com.praesidium.wrkq-server.plist` is a different, dev variant
(`wrkq server serve` on `127.0.0.1:7171` with token `dev`); installing it over
mini's job would take the ledger away from every remote node.
`just launchd-install` therefore refuses to replace an installed plist that
differs from the repo copy and prints the diff (T-08044); never pass
`--force` on mini. The claim-authority node identity is derived exclusively
from the per-node bearer token presented to `wrkqd` — a claiming caller
cannot supply or spoof which node it's claiming from.

### Upgrading other nodes from max3

All nodes are reachable by `ssh <host>`. Check first with `wrkq version --json`
there and compare `commit` to max3's.

- **mini** (canonical wrkqd, producer): `ssh mini` is a non-login shell, so
  export `PATH=$HOME/.local/bin:$HOME/.bun/bin:$HOME/go/bin:/opt/homebrew/bin:$PATH`
  first, then `cd ~/praesidium/wrkq && git pull --ff-only && just install`.
  A migration release follows the offline sequence above. Upgrade mini first
  and the other nodes after it: a new client refuses an old server.
- **hrcdev**: same PATH export, then `git pull --ff-only && just install no-sync=1`.
  It runs no wrkqd; its clients point at mini (`HRC_WRKQ_DB=rpc://…:7171`).
- **thestill** (Lance's laptop): a client-only node with no wrkqd, no HRC and
  just `~/.local/bin/wrkq` plus `~/.config/wrkq/node-token`. Don't build there.
  From max3, run `just install-thestill`. It copies max3's installed `wrkq`,
  keeps the old one as `wrkq.bak`, and proves the new one reaches wrkqd. A stale
  copy fails with `rpc protocol mismatch`.

### HTTP route surface

`wrkqd` exposes a deliberately narrower REST surface than the full JSON-RPC
method catalog — no handoff HTTP routes exist, for example:

```text
/v1/health
/v1/containers/tree
/v1/tasks/{list,get,create,update,archive,restore}
/v1/comments/{list,create}
/v1/relations/{list,create,delete}
```

The full JSON-RPC method catalog (`wrkq.task.*`, `wrkq.comment.*`,
`wrkq.attachment.*`, `wrkq.relation.*`, `wrkq.container.*`, `wrkq.project.*`,
`wrkq.promise.*`, `wrkq.handoff.*`, `wrkq.room.*`, `wrkq.envelope.*`,
`wrkq.projectEvent.*`, `wrkq.webhook.*`, `wrkq.search.listView`,
`wrkq.index.*`, `wrkq.history.*`, `wrkq.monitor.*`, `wrkq.workflow.*`, and
the `wrkf.*` workflow engine) is reachable only via `POST /v1/rpc`, not the
narrower REST routes — this is why handoffs, rooms, search, and monitoring
require an RPC-capable client (production `wrkq` CLI, `@wrkq/client`, or the
MCP server) rather than raw REST calls.

## Attachment storage

- If the DB is the default `.wrkq/wrkq.db`, attachments default to
  `.wrkq/attachments`.
- Otherwise `WRKQ_ATTACH_DIR` (or `attach_dir` in config) must be set
  explicitly.
- Bytes live at `<attach_dir>/tasks/<task_uuid>/...`, keyed by task UUID so
  moving/renaming a task never moves its attachment bytes. Purging a task
  removes its attachment directory.

`wrkqadm attach path` exposes filesystem paths and is explicitly documented
as administrative-only — not for agent use.
