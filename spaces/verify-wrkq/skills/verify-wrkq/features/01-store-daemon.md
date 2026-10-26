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
  answers 401 to an unauthenticated health read. With a wrong token, `server health` still exits 0 and prints
  `{"auth":"unauthorized","status":"ok"}` plus a stderr notice that the daemon rejected the caller's token, so
  check `auth`, not the exit code. `wrkq server status` reports this node's launchd job and pid
  file, plus `binaryStale`.
- **Request log.** `wrkqd` writes one JSON line per HTTP request on **stdout**
  (`internal/wrkqd/request_log.go`, a01fa92..e36728f): `{level, component: "http", event: "http.request",
  data: {method, path, route, listener, client, status, durationMs, remote, op?, suppressed?, code?, message?}, ts}`,
  plus one `http.listen` line per listener at startup. `client` is the User-Agent's leading `<name>/<version>`
  (`unknown` without one). Every wrkq binary, and the stdio bridge that `@wrkq/client` spawns, sends
  `wrkq/<version>`, so the caller's `rpc.initialize` `clientInfo` never reaches the log. A `/v1/rpc` line names
  its JSON-RPC method as `op`. A frame that fails inside HTTP 200 logs `warn` with the JSON-RPC `code` and
  `message`, and a non-2xx logs `warn` (`error` at 5xx) with the body's message (`unauthorized` for a 401).
  Successful polls (`rpc.initialize`, `wrkq.envelope.pendingView`, monitor, timeline, history and `wrkf.watch.*`
  views) log at most once per `-poll-sample-window` (default 1m) per client, remote and op, and the next admitted
  line carries `suppressed: N`. Diagnostics stay on stderr. On mini, launchd sends stdout to
  `var/logs/wrkq/wrkqd.log` and stderr to `var/logs/wrkq/wrkqd.err.log`. Driven 2026-10-26, T-11873.
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
- **Profile.** `WRKQ_PROFILE=1 <any client verb>` adds a wall-time breakdown on stderr after the verb's own output:
  one row per wrkqd round trip (`connect`, `send`, `wait`, `read`, `total`, response bytes), then process start,
  round-trip total and client-side time. Stdout is unchanged. Against a local `--db` path it reports 0 RPC calls.
- **Offline admin.** These run against the store **file** (`--db <path>`), never over `rpc://`, and are safe on a
  serving scratch store. `wrkqadm config doctor` prints the effective DB, attachment dir, project root and
  principal, with the source of each. `wrkqadm db snapshot --out F [--json]` takes an online-backup copy.
  `wrkqadm state export --out F` writes the canonical task-ledger JSON snapshot (containers, tasks, comments,
  promises, links; not rooms, handoffs, workflows or events), and `state verify F` checks it round-trips.
  `wrkqadm patch create --from S0 --to S1 --out P`, `patch summarize --patch P` and
  `patch validate --base S0 --patch P` diff two snapshots as RFC 6902. `patch apply`/`rebase` and `state import`
  write the store, so a verification drive doesn't run them.

## How to get to it

- Scratch: `wv up --name <task>` (it runs `wrkqadm init` and `wrkqd` for you), then `eval "$(wv env <name>)"`.
  The state root is `$WV_STATE`: `wrkq.db`, `wrkqd.log`, `hooks.json`, `node-tokens`, `token`, `token-wv2`.
  `wv up` sends the daemon's stdout and stderr both to `wrkqd.log`, so the request log's JSON lines are
  interleaved with plain stderr diagnostics. Read it with `jq -R -c 'fromjson? | ...'`.
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

Then the additions (offline admin reads run on the scratch store file):

```bash
WRKQD_TOKEN=wv-bogus WRKQD_TOKEN_FILE= wrkq server health --addr "127.0.0.1:${WRKQ_DB##*:}"   # auth: unauthorized, exit 0
WRKQ_PROFILE=1 wrkq projects --json >/dev/null                                           # breakdown on stderr
D="--db $WV_STATE/wrkq.db"
wrkqadm $D config doctor
wrkqadm $D db snapshot --out "$WV_STATE/snap0.db" --json
wrkqadm $D state export --out "$WV_STATE/s0.json" && wrkqadm $D state verify "$WV_STATE/s0.json"
# ...after feature 2 has written tasks:
wrkqadm $D state export --out "$WV_STATE/s1.json"
wrkqadm $D patch create --from "$WV_STATE/s0.json" --to "$WV_STATE/s1.json" --out "$WV_STATE/p01.json" --json
wrkqadm $D patch summarize --patch "$WV_STATE/p01.json" && wrkqadm $D patch validate --base "$WV_STATE/s0.json" --patch "$WV_STATE/p01.json"
```

The request log, after the other features have driven traffic:

```bash
L="$WV_STATE/wrkqd.log"
jq -R -c 'fromjson? | select(.event=="http.listen")' "$L"
jq -R -c 'fromjson? | select(.event=="http.request") | .data.client' "$L" | sort | uniq -c      # wrkq/<version>, curl/<v>
jq -R -c 'fromjson? | select(.level!="info") | .data | {route,status,op,code,message}' "$L"     # rpc errors as warn
jq -R -c 'fromjson? | select(.data.suppressed) | {op: .data.op, suppressed: .data.suppressed}' "$L"
curl -sS -o /dev/null -A "" -H "Authorization: Bearer $(cat "$WRKQD_TOKEN_FILE")" "http://127.0.0.1:${WRKQ_DB##*:}/v1/nope"
tail -1 "$L" | jq -c '.data | {client, route, status}'                                          # unknown, unmatched, 404
```

Never migrate, restart or bootout the canonical daemon in a verification drive.

## Gotchas

- **The wrong-token health probe needs a one-line token.** `WRKQD_TOKEN_FILE=$WV_STATE/node-tokens` sends the
  whole two-line file and fails client-side (`invalid header field value for "Authorization"`, exit 1) before the
  daemon sees it. Use `WRKQD_TOKEN=wv-bogus WRKQD_TOKEN_FILE=` (2026-10-20, T-11374 `01-store-daemon/drive.txt`).
- **`wrkqadm` file verbs refuse the `wv env` locator.** With `WRKQ_DB=rpc://...` exported, even
  `wrkqadm state verify F` (a file check) refuses `remote database locator ... is not valid for this local command`.
  Pass `--db "$WV_STATE/wrkq.db"` to every `wrkqadm` verb (2026-10-20, T-11374).
- **`wrkqadm config doctor` misreports the principal.** In a seat it says `Principal: (not set) ... ✗ Not
  configured` although `wrkq whoami` resolves one from the seat scope, and `WRKQ_PRINCIPAL_REF` beats `--as`.
  Read the principal from `wrkq whoami`. Product task T-11431 (2026-10-20, T-11374 `01-store-daemon/drive.txt`; still
  reproduces at 7350485, 2026-10-26).
- Doctor's `sequence_drift` checks `event_log` against its own AUTOINCREMENT row in `sqlite_sequence`
  (named `event_log`). Before T-10327 it read the dead `event_seq` row and flagged every store with an event.
- Doctor's `attach_dir_exists` needs `WRKQ_ATTACH_DIR` (or `attach_dir` in config) on the **caller's** side.
  `wv env` exports it. Without it, a correct store reports an error row, and `doctor --json` still exits 0, so
  read the rows (2026-10-05, T-10349 `01-store-daemon/drive.txt`).
- A malformed `X-Wrkq-Principal-Ref` header on `/v1/rpc` is refused `WRKQ_VALIDATION` ("principal attribution
  only supports agent identities"). The CLI validates the flag first, so prove the server side with `curl`.
- In an agent seat an unattributed write doesn't reach the "principalRef is required" refusal: the CLI resolves
  the principal from `ASP_AGENT_ID`/`AGENT_ACTOR` even with `WRKQ_PRINCIPAL_REF`, `HRC_SESSION_REF` and
  `AGENT_SCOPE_REF` unset (T-10349).
- `wrkqd` won't start without a hook catalog:
  `failed to initialize workrpc registry: hook catalog configuration is required`. The canonical job sets
  `WRKF_HOOK_CATALOG`. A hand-started daemon has to set it too.
- `wrkq server status` describes **this node's** launchd job and pid file, not the daemon your locator names.
  On a client node (max3) it says `running: false` while mini serves normally. Use `server health --addr`.
- A copy of the canonical store carries its webhook URLs. A daemon serving that copy posts to live ACP/Discord.
  Use `wv up` (a fresh store), or clear `webhook_urls` before you serve a copy.

## Proven when

On a fresh `wv up`, health is ok with the token and 401 without it, a tokenless client gets the token refusal,
a dead locator gets the "retrying is safe" refusal, `whoami` names the scratch locator, doctor reports every row
ok, `migrate --status` lists every migration as applied (000070 last at 7350485), and `migrate` refuses
the serving store. `WRKQ_PROFILE=1` prints the breakdown, and the snapshot, `state verify` and `patch validate`
reads succeed on the scratch file. The daemon log has an `http.listen` line and one `http.request` line per request
with `client: wrkq/<installed version>` and an `op`, the refusals as `warn` lines with their code and message, and
sampled polls with a `suppressed` count. On the canonical daemon, read only: health is ok and `rpc.initialize`
names a revision.

Driven 2026-10-26 on wv `t-11873` (T-11873 upkeep) with installed 7350485: `var/wrkq-artifacts/T-11873/01-store-daemon/drive.txt`,
`evidence/t-11873/`, live reads in `live/reads.txt` (canonical serves 7350485). Every feature's drive.txt starts with
a replay of T-11374's drives (each entry after a `NOTE replay T-11374 ...` line, diffed in `replay/compare.txt`),
then its drift drives after a `NOTE drift drives` line.
