# 1. Store and daemon

wrkq keeps one SQLite store and serves it through `wrkqd`. Every client (`wrkq`, `wrkc`, `wrkf`, `wrkp`,
`@wrkq/client`) reaches it over `rpc://host[:port]` (port 7171 by default) with a bearer token. `wrkqadm` owns
the store's lifecycle: `init`, `migrate`, `doctor`, `db`, `state`, `attach`. Code: `cmd/wrkqd/main.go`,
`internal/wrkqd/`, `internal/admincli/` (`initadm.go`, `migrateadm.go`, `doctoradm.go`), `internal/db/`
(`sequences.go`) and `internal/rpccli/server.go`. Operations: AGENTS.md "Justfile is the lifecycle" and
`docs/wrkq-operations.md`.

## Sub-features

- **Init.** `wrkqadm --db P init` creates and migrates a store, creates `attachments/` next to it, and seeds the
  principal-owned and plain `inbox` projects.
- **Serve.** `wrkqd -addr H:P -db P` takes either `-token` or `-node-tokens`/`-node-tokens-file`. With node
  tokens, each bearer credential maps to a `nodeId`, which task claims require (feature 4). It needs a wrkf hook
  catalog (`WRKF_HOOK_CATALOG`) and refuses to start without one. It also refuses a non-loopback listen without
  a token, unless it gets `-unsafe-no-token`.
- **Health.** `wrkq server health --addr H:P` reads `/v1/health` with the caller's token. A node-token daemon
  answers 401 to an unauthenticated health read. `wrkq server status` reports this node's launchd job and pid
  file, plus `binaryStale`.
- **Locator and auth precedence.** CLI flags, then env, then `./.env.local`, then `~/praesidium/.env.local`,
  then `~/.config/wrkq/config.yaml`. A client with no token is refused with a message naming both token inputs.
  An unreachable locator is refused with "nothing reached the daemon, so retrying is safe" and the
  `server health` command to run next.
- **Migrate.** `wrkqadm migrate` refuses a serving store (`MIGRATION_DATABASE_SERVING`, naming the pid and the
  offline procedure). `migrate --status` and `--dry-run` use read-only connections and work while the daemon
  serves.
- **Doctor.** `wrkqadm doctor [--json] [--fix]` checks the file, WAL, foreign keys, integrity, schema, root,
  orphans, duplicate slugs, sequence drift, the attachment dir and the counts.
- **whoami.** `wrkq whoami --json` shows the locator, the mode, the principal and the scope the caller resolves.

## How to get to it

- Scratch: `wv up --name <task>` (it runs `wrkqadm init` and `wrkqd` for you), then `eval "$(wv env <name>)"`.
  The state root is `$WV_STATE`: `wrkq.db`, `wrkqd.log`, `hooks.json`, `node-tokens`, `token`, `token-wv2`.
- Live, read only: `wrkq whoami`, `wrkq server health --addr mini:7171`, and an `rpc.initialize` frame through
  `wrkq rpc --stdio` (feature 10). Its `server.revision` is the canonical daemon's build.

## Driving it

```bash
wv up --name <task> && eval "$(wv env <task>)"
wrkq version --json | jq -c '{version,commit,build_date}'
curl -sS -o /dev/null -w '%{http_code}\n' "http://127.0.0.1:${WRKQ_DB##*:}/v1/health"   # 401
wrkq server health --addr "127.0.0.1:${WRKQ_DB##*:}"                                    # {"status":"ok"}
WRKQD_TOKEN_FILE=/dev/null wrkq projects                                                # 401 refusal naming the token inputs
WRKQ_DB=rpc://127.0.0.1:1 wrkq projects                                                 # unreachable, "retrying is safe"
wrkq whoami --json
wrkqadm --db "$WV_STATE/wrkq.db" doctor --json | jq -c '[.checks[] | select(.status!="ok")]'
wrkqadm --db "$WV_STATE/wrkq.db" migrate --status | tail -3
wrkqadm --db "$WV_STATE/wrkq.db" migrate        # refused: MIGRATION_DATABASE_SERVING
```

Never migrate, restart or bootout the canonical daemon in a verification drive.

## Gotchas

- **Doctor's `sequence_drift` on `event_seq` is a false positive.** `event_log` takes its ids from SQLite
  AUTOINCREMENT, which `sqlite_sequence` records under the name `event_log`. Doctor reads a sequence named
  `event_seq` (`internal/db/sequences.go:41`), and nothing advances that one. So every store that has at least
  one event reports `event_seq (table event_log): sqlite_sequence=0, max_id=N` as an error. A bare `init`
  doesn't show it, but the first write does (2026-10-05, T-10298 `01-store-daemon/drive.txt`). This is a
  product defect. Don't run `--fix` to make the row go away.
- Doctor's `attach_dir_exists` needs `WRKQ_ATTACH_DIR` (or `attach_dir` in config) on the **caller's** side.
  `wv env` exports it. Without it, a correct store reports an error.
- `wrkqd` won't start without a hook catalog:
  `failed to initialize workrpc registry: hook catalog configuration is required`. The canonical job sets
  `WRKF_HOOK_CATALOG`. A hand-started daemon has to set it too.
- `wrkq server status` describes **this node's** launchd job and pid file, not the daemon your locator names.
  On a client node (max3) it says `running: false` while mini serves normally. Use `server health --addr`.
- A copy of the canonical store carries its webhook URLs. A daemon serving that copy posts to live ACP/Discord.
  Use `wv up` (a fresh store), or clear `webhook_urls` before you serve a copy.

## Proven when

On a fresh `wv up`, health is ok with the token and 401 without it, a tokenless client gets the token refusal,
a dead locator gets the "retrying is safe" refusal, `whoami` names the scratch locator, doctor's only non-ok row
is the `event_seq` false positive, `migrate --status` lists every migration as applied, and `migrate` refuses
the serving store. On the canonical daemon, read only: health is ok and `rpc.initialize` names a revision.

Driven 2026-10-05 on wv `t-10298` (T-10298) with installed f43c308: `var/wrkq-artifacts/T-10298/01-store-daemon/drive.txt`,
`evidence/t-10298/`, live reads in `live/canonical.txt`.
