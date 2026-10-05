# 10. RPC and @wrkq/client

wrkq and wrkf share one JSON-RPC protocol (`protocolVersion` `2026-06-30`, pinned by `protocolSchemaHash`).
`wrkq rpc --stdio` and `wrkf rpc --stdio` serve it over stdin and stdout, either against a local store
(`--db path`) or bridged to a daemon (`rpc://`). `@wrkq/client` is the Bun TypeScript client. It spawns that
stdio server and exposes `client.wrkq.*` and `client.wrkf.*`. Every CLI also rides the same protocol. Code:
`internal/workrpc/` (catalog, codec, errors), `internal/rpccli/rpc.go`, and `packages/client/`
(`src/client.ts`, `src/stdio-transport.ts`, `src/wrkq/`, `src/wrkf/`). Contract: `docs/wrkq-wrkf-rpc.md`, plus
`docs/wrkq-wrkf-rpc-client-forward-spec.md` and `packages/client/README.md`.

## Sub-features

- **Initialize.** `rpc.initialize {protocolVersion, client}` returns `protocolVersion`, `protocolSchemaHash`,
  `capabilities`, `database`, `methods` and `server {name, version, revision, pid, entrypoint}`. A missing or
  wrong `protocolVersion` is refused `-32602 invalid protocolVersion` (`data.code` reads `WRKF_VALIDATION` even
  on `wrkq rpc`), and `error.data` carries `expected`, `serverProtocolSchemaHash` and `serverRevision`.
- **Methods.** `wrkq.task.create|show|update|list`, `wrkq.workflow.*`, `wrkq.projectEvent.post`, `wrkf.*` and
  more. An unknown method fails with JSON-RPC `-32601 method not found`. Domain errors carry
  `error.data.code` (`WRKQ_VALIDATION`, `WRKQ_CONFLICT`, ...) and `retryable`. An unknown param key is refused
  `-32602 invalid params: unknown field "tsak" for wrkq.task.show`, with `data.field`, so a misspelled selector
  no longer slips through. `wrkq rpc` without `--stdio` is refused.
- **Idempotency.** `idempotencyKey` on create replays the same task. The replay returns the original response,
  so its `etag` can be stale. Read the current etag (`wrkq.task.show`) before a CAS update.
- **CAS.** `expectEtag` on update. A stale etag fails `WRKQ_CONFLICT`.
- **The client.** `createClient({command: "wrkq"|"wrkf", dbLocator, principalRef, role, clientInfo})` runs
  `rpc.initialize` before it resolves. An `rpc://` locator goes through `WRKQ_DB`, so the client never parses
  CLI output and never calls wrkqd HTTP itself. Errors are `WorkRpcError` with `domainCode` and `retryable`.
- **Publishing.** A main-checkout `just install` on a producer node with a clean tracked tree (source read: `justfile`) publishes a timestamped `@wrkq/client` to the node's
  Verdaccio (`npm view @wrkq/client dist-tags`). Consumers pin it. A new client refuses an old server on a
  protocol-hash mismatch.

## How to get to it

Run `eval "$(wv env <name>)"`. For raw frames, pipe JSON lines into `wrkq rpc --stdio`. For the client, install
the **published** package into a scratch dir **that has its own `package.json`**:
`mkdir "$WV_STATE/client" && cd "$WV_STATE/client" && echo '{"name":"wv-client","private":true}' > package.json && bun add @wrkq/client@latest`.
Copy `~/praesidium/wrkq/spaces/verify-wrkq/fixtures/client-drive.ts` there and run it with `bun --env-file=/dev/null`. Bun autoloads
`.env.local`, and that would swap the locator back to canonical.

Live, read only: one `rpc.initialize` frame against canonical names the serving revision.

## Driving it

```bash
INIT='{"jsonrpc":"2.0","id":1,"method":"rpc.initialize","params":{"protocolVersion":"2026-06-30","client":{"name":"wv","version":"0"}}}'
printf '%s\n' "$INIT" '{"jsonrpc":"2.0","id":2,"method":"wrkq.nope"}' \
  '{"jsonrpc":"2.0","id":3,"method":"wrkq.task.show","params":{"task":"T-00001"}}' \
  | wrkq rpc --stdio | jq -c '{id, hash: .result.protocolSchemaHash, task: (.result.id // null), err: .error.code}'
printf '%s\n' "$INIT" '{"jsonrpc":"2.0","id":2,"method":"wrkq.task.create","params":{"title":"x","path":"wv-<name>/inbox/x"}}' \
  | wrkq --principal-ref agent:clod rpc --stdio | jq -c '{id, by: .result.createdByPrincipalRef, err: .error.data.code}' # see Gotchas
cd "$WV_STATE/client" && bun --env-file=/dev/null run client-drive.ts
# {"created":{...,"by":"agent:clod"}} {"replay_same_id":true} {"stale_update_refused":"WRKQ_CONFLICT"} {"cas_update":{"priority":1,...}}
```

## Gotchas

- **`bun add` without a local `package.json` writes the `~/praesidium` workspace.** Bun walks up to the nearest
  `package.json`, which is the monorepo root. A bare `bun add @wrkq/client@latest` in `$WV_STATE/client` rewrote
  `~/praesidium/package.json` (`"latest"` became a pinned dev version), its `bun.lock` and the root `node_modules`.
  Create the scratch `package.json` first. If it already happened, put back only the `@wrkq/client` line
  (2026-10-05, T-10349 `10-rpc-client/drive.txt`).

- **The session principal is a default, not an override.** `wrkq --principal-ref agent:X rpc --stdio` (or
  `--as`) attributes a write that carries no `principalRef`/`actor` to `agent:X`, locally and over `rpc://`: the
  proxy sends it as the `X-Wrkq-Principal-Ref` header and wrkqd defaults from it (T-10328). A per-frame principal
  still wins. `--principal-ref` and `--as` naming different agents are refused before serving. The published
  client's session `principalRef` now attributes over `rpc://` too, with no per-call principal (T-10349).
  Against a wrkqd older than T-10328 the header is ignored and the write is refused
  `WRKQ_VALIDATION: principalRef is required`; if you see that over `rpc://`, check the serving revision
  before calling it a defect. A malformed header value is refused `WRKQ_VALIDATION`, never ignored.
- `dbPath` and `dbLocator` must not disagree, and the client refuses `actor` as a session option. Pass
  `principalRef`.
- The repo's `packages/client/dist` may not be what consumers run. Drive the published version and record it
  (`jq -r .version node_modules/@wrkq/client/package.json`).
- On a stale `expectEtag`, the update is refused `WRKQ_CONFLICT` before the principal check runs, so a missing
  principal can hide behind a conflict.

## Proven when

On the scratch, raw frames return the protocol hash, `-32601` for an unknown method and the task for
`task.show`. The published client creates a task attributed to your principal, replays the same id on the same
`idempotencyKey`, refuses a stale `expectEtag` with `WRKQ_CONFLICT`, and applies a correct CAS update (etag +1).
Live: `rpc.initialize` names the canonical revision.

Driven 2026-10-05 on wv `t-10349` (T-10349 upkeep), installed 037fe66, published `@wrkq/client@0.1.0-dev.20261005155132`:
`var/wrkq-artifacts/T-10349/10-rpc-client/drive.txt` with `spaces/verify-wrkq/fixtures/client-drive.ts` (typed, re-driven in `fixup/drive.txt`). Live: `live/reads.txt` (canonical serves 037fe66).
