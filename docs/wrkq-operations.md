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

Resolution order (`wrkqadm config doctor` reports the winning source):

1. `--db` CLI flag.
2. `WRKQ_DB` (production `wrkq` only) — a local path, or `rpc://host[:port]`
   (default port `7171`) to use a remote canonical `wrkqd`.
3. `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` — local-path-only compatibility
   inputs; reject `rpc://`.
4. Nearest `.env.local`, walking upward from the current directory.
5. Platform `.env.local` at `$PRAESIDIUM_HOME/.env.local` (falls back to
   `~/praesidium/.env.local` when `PRAESIDIUM_HOME` is unset).
6. `~/.config/wrkq/config.yaml`.
7. Built-in default: if `.wrkq/wrkq.db` exists in the current directory it is
   used; otherwise there is no implicit path, and DB-needing commands fail
   with a message naming `WRKQ_DB_PATH` and `--db`.

Admin/daemon path-owning surfaces are local-path-only by design and reject
`rpc://`: `wrkqadm --db`, `wrkqd --db`, `wrkq server --db-path`.

Check the effective, resolved configuration at any time:

```bash
wrkqadm config doctor
wrkqadm config doctor --json
```

Example output (this environment, run from the `wrkq` repo root without a
local `.wrkq/wrkq.db`):

```text
Database:
  WRKQ_DB_PATH:
    Source: config file
    Status: ✗ File does not exist
Project Root:
  WRKQ_PROJECT_ROOT: taskboard
    Source: environment variable WRKQ_PROJECT_ROOT
    Status: ✓ Loaded
Actor:
  Principal: (not set)
    Source: default_principal_ref setting
    Status: ✗ Not configured
```

Note `wrkqadm config doctor` reports the **local** configuration surface
(`WRKQ_DB_PATH`), which is intentionally separate from the production `wrkq`
CLI's `WRKQ_DB` locator resolution — a shell can have `WRKQ_DB` pointed at a
remote `rpc://` daemon (so `wrkq whoami` resolves fine) while
`wrkqadm config doctor` still reports no local DB, because `wrkqadm` never
follows `rpc://`.

### Live RPC example

This environment routes production `wrkq` calls to a remote daemon rather
than a local file:

```text
HRC_WRKQ_DB=rpc://100.117.215.92:7171
HRC_WRKQD_TOKEN_FILE=/Users/lherron/.config/wrkq/node-token
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

The platform-wide canonical store is deployed at
`WRKQ_DB_PATH=/Users/lherron/praesidium/var/db/wrkq.db` on its host node;
other machines reach it as clients via `WRKQ_DB=rpc://<host>:7171`.

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

Produces a deterministic, canonicalized JSON representation of the whole
database (actors, containers, tasks, comments — optionally the full event
log) — sorted keys, no insignificant whitespace, sorted arrays, byte-for-byte
identical output for identical DB state. Designed for diffing and
version-controlled/patch-first workflows.

```bash
wrkqadm state export --out .wrkq/state.json
wrkqadm state export --out full.json --include-events
wrkqadm state verify .wrkq/state.json     # round-trip determinism check
wrkqadm state import .wrkq/state.json
```

RFC 6902 JSON patches can be layered on top of state snapshots for
patch-first Git workflows:

```bash
wrkqadm patch create ...
wrkqadm patch validate ...
wrkqadm patch apply ...
wrkqadm patch rebase ...
wrkqadm patch summarize ...
```

Note the README's mention of a "git-native, bundle diffs for PRs" workflow is
aspirational framing; `docs/SPEC.md` §12 records explicitly that "the former
Git-ops bundle workflow is not part of the production CLI, admin, daemon, or
workrpc contract" — `state export`/`import`/`verify` are the current
canonical snapshot operations.

## Database health

```bash
wrkqadm doctor                 # schema/config/attachment health checks
wrkqadm doctor --verbose --json
wrkqadm doctor --fix           # auto-repair issues where supported
```

`wrkqadm migrate` applies any pending migrations (embedded from
`internal/db/migrations`); `wrkqadm init` initializes a fresh database and
config.

## Actor attribution

wrkq attributes every mutation to a principal ref (`agent:<id>`) but performs
no authentication. Resolution precedence for mutating commands:

1. `--principal-ref <ref>` / `--as <ref>` flag — accepts `agent:<id>` or a
   full agent ScopeRef (e.g. `agent:<id>:project:<projectId>`), reduced to
   `agent:<id>`. If both flags are given, they must resolve to the same
   agent.
2. `WRKQ_PRINCIPAL_REF` env var — same accepted forms.
3. A validated ASP scope: `ASP_SCOPE_REF`, or `ASP_HANDLE`, or
   `ASP_AGENT_ID` + `ASP_PROJECT` — reduced to `agent:<agentId>`.
4. `default_principal_ref` in `~/.config/wrkq/config.yaml`.

Explicitly **not** attribution sources: bare slugs, actor UUIDs, `A-*` actor
IDs, `system:*`, `WRKQ_ACTOR`, `WRKQ_ACTOR_ID`, `default_actor` — these are
legacy/display-cache inputs only. Passing a full ScopeRef as a principal
input keeps only the reduced agent identity; the fuller runtime scope
provenance is recorded separately via `scope_ref`.

Inspect what will actually be used:

```bash
wrkq whoami                 # resolved principal_ref + scope_ref
wrkq agent-context           # scope resolution debug view, works without a DB connection
wrkq agent-context --scope agent:cody:project:wrkq
```

`wrkqadm actors ls` / `wrkqadm actors add` manage the legacy actor
display-cache table — not required for ordinary writes to succeed.

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
restart, unbounded time later. `just install` warns when it replaces a `wrkqd`
that a running job still holds, `wrkq server status` reports `binaryStale`, and
`wrkq server health` fails on it. On the canonical node, install and restart
together.

`wrkq server status` and `wrkq server health` probe the address the launchd job
actually binds (its `--addr` argument or `WRKQD_ADDR`), not the `127.0.0.1:7171`
default.

This environment runs `wrkqd` as a launchd service:
`launchd/com.praesidium.wrkq-server.plist`, invoking `wrkq server serve`
bound to `127.0.0.1:7171` with token `dev`. The claim-authority node identity
is derived exclusively from the per-node bearer token presented to `wrkqd` —
a claiming caller cannot supply or spoof which node it's claiming from.

### HTTP route surface

`wrkqd` exposes a deliberately narrower REST surface than the full JSON-RPC
method catalog — no handoff HTTP routes exist, for example:

```text
/v1/health
/v1/containers/tree
/v1/tasks/{list,get,create,update,archive,restore}
/v1/comments/{list,create}
/v1/relations/{list,create,delete}
/v1/actors/{list,create,update}
```

The full JSON-RPC method catalog (`wrkq.task.*`, `wrkq.comment.*`,
`wrkq.attachment.*`, `wrkq.relation.*`, `wrkq.container.*`, `wrkq.project.*`,
`wrkq.handoff.*`, `wrkq.webhook.*`, `wrkq.search.listView`, `wrkq.index.*`,
`wrkq.history.*`, `wrkq.monitor.*`, `wrkq.admin.legacyActor.*`) is reachable
only via `POST /v1/rpc`, not the narrower REST routes — this is why handoffs,
search, and monitoring require an RPC-capable client (production `wrkq`
CLI, `@wrkq/client`, or the MCP server) rather than raw REST calls.

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
