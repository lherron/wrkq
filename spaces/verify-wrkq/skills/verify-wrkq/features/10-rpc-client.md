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
  `capabilities` and `server {name, version, revision, pid, entrypoint}`.
- **Methods.** `wrkq.task.create|show|update|list`, `wrkq.workflow.*`, `wrkq.projectEvent.post`, `wrkf.*` and
  more. An unknown method fails with JSON-RPC `-32601 method not found`. Domain errors carry
  `error.data.code` (`WRKQ_VALIDATION`, `WRKQ_CONFLICT`, ...) and `retryable`.
- **Idempotency.** `idempotencyKey` on create replays the same task.
- **CAS.** `expectEtag` on update. A stale etag fails `WRKQ_CONFLICT`.
- **The client.** `createClient({command: "wrkq"|"wrkf", dbLocator, principalRef, role, clientInfo})` runs
  `rpc.initialize` before it resolves. An `rpc://` locator goes through `WRKQ_DB`, so the client never parses
  CLI output and never calls wrkqd HTTP itself. Errors are `WorkRpcError` with `domainCode` and `retryable`.
- **Publishing.** Every main-checkout `just install` publishes a timestamped `@wrkq/client` to the node's
  Verdaccio (`npm view @wrkq/client dist-tags`). Consumers pin it. A new client refuses an old server on a
  protocol-hash mismatch.

## How to get to it

Run `eval "$(wv env <name>)"`. For raw frames, pipe JSON lines into `wrkq rpc --stdio`. For the client, install
the **published** package into a scratch dir (`cd $WV_STATE && mkdir client && cd client && bun add @wrkq/client@latest`),
copy the drive script `client-drive.ts` from the evidence dir, and run it with `bun --env-file=/dev/null`. Bun
autoloads `.env.local`, and that would swap the locator back to canonical.

Live, read only: one `rpc.initialize` frame against canonical names the serving revision.

## Driving it

```bash
INIT='{"jsonrpc":"2.0","id":1,"method":"rpc.initialize","params":{"protocolVersion":"2026-06-30","client":{"name":"wv","version":"0"}}}'
printf '%s\n' "$INIT" '{"jsonrpc":"2.0","id":2,"method":"wrkq.nope"}' \
  '{"jsonrpc":"2.0","id":3,"method":"wrkq.task.show","params":{"task":"T-00001"}}' \
  | wrkq rpc --stdio | jq -c '{id, hash: .result.protocolSchemaHash, task: (.result.id // null), err: .error.code}'
printf '%s\n' "$INIT" '{"jsonrpc":"2.0","id":2,"method":"wrkq.task.create","params":{"title":"x","path":"wv-<name>/inbox/x"}}' \
  | wrkq --principal-ref agent:clod rpc --stdio | jq -c '{id, err: .error.data.code}'     # see Gotchas
cd "$WV_STATE/client" && bun --env-file=/dev/null run client-drive.ts
# {"created":{...}} {"replay_same_id":true} {"stale_update_refused":"WRKQ_CONFLICT"} {"cas_update":{"priority":1,...}}
```

## Gotchas

- **Over `rpc://`, the session `--principal-ref` is not applied to writes.**
  `wrkq --principal-ref agent:clod rpc --stdio` with an `rpc://` locator refuses `wrkq.task.create` with
  `WRKQ_VALIDATION: principalRef is required ... or launch with --principal-ref`. The same frames against a local
  `--db` path succeed. So `createClient({principalRef})` has no effect in remote mode, and every write has to
  carry its principal: `principalRef` on create, `actor` on update (2026-10-05, T-10298
  `10-rpc-client/drive.txt`). This is a product defect.
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

Driven 2026-10-05 on wv `t-10298` (T-10298) with published `@wrkq/client@0.1.0-dev.20261005134223`:
`var/wrkq-artifacts/T-10298/10-rpc-client/drive.txt` and `client-drive.ts`. Live: `live/canonical.txt`.
