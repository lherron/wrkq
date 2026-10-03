# wrkf RPC — recovery and client guide

The maintained machine contract is [the unified wrkq/wrkf RPC protocol](docs/wrkq-wrkf-rpc.md).
Both `wrkq rpc --stdio` and `wrkf rpc --stdio` expose that registry; workflow
binding and task-record operations use `wrkq.workflow.*`, while workflow
execution uses `wrkf.*`. This companion explains recovery and client usage.
Link paths on this page are repo-root-relative.

The active architecture contract
[wrkq.contract.wrkf-rpc](architecture/contracts/wrkf-rpc.yaml) owns the
`WRKF_*` error/retryability contract and its producer/client obligations.
Its [ADR](architecture/adr/0001-wrkf-rpc-recovery-contract.md) records provenance.

## Transport and initialization

Stdio uses JSON-RPC 2.0 as NDJSON: one compact frame per stdout line, with
diagnostics and hook output on stderr. The default inbound frame limit is
8 MiB. The shared registry is also available through authenticated wrkqd
HTTP at `/v1/rpc`.

Call `rpc.initialize` first with protocol version `2026-06-30`. Its result
includes `protocolSchemaHash`, `database.migrationHash`, `capabilities`, and
`methods`. Check compatibility before business dispatch. Lifecycle methods
are `rpc.shutdown` and the `rpc.exit` notification; stdin EOF also ends stdio.
`$/cancelRequest` is accepted on a best-effort basis. Remote hook execution
honors request-context cancellation and refuses post-execution persistence
after cancellation.

Use `WRKQ_DB` or `--db` for a local path or `rpc://host[:port]` locator.
Hook catalogs are explicit canonical-node configuration: local mode accepts
`--hook-catalog` or `WRKF_HOOK_CATALOG`, while remote mode refuses a caller
catalog override. No catalog is discovered from cwd or workspace ancestry.

Mutation attribution is principal-only: use `--principal-ref agent:<id>`,
`WRKF_PRINCIPAL_REF`, a valid runtime scope, or `default_principal_ref`.
Legacy actor names/IDs are not authority inputs. Transport authentication
and caller attribution are separate concerns.

## Recovering from errors

Read `error.data.code`, not human message text. Domain errors carry a boolean
`data.retryable`; standard JSON-RPC parse/method/params errors may omit
`data.code`. Unknown non-domain errors map to `WORKRPC_INTERNAL`.
The code mappings live in [workrpc/errors.go](internal/workrpc/errors.go)
and typed workflow errors in [wrkfapi/errors.go](internal/wrkfapi/errors.go).

| Code | Recovery |
| --- | --- |
| `WRKF_STALE_REVISION` | Reload instance state and re-evaluate the intended transition before retrying with its revision. |
| `WRKF_TRANSITION_BLOCKED` | Inspect `blocksOn`; satisfy evidence/check/obligation requirements before retrying. |
| `WRKF_IDEMPOTENCY_MISMATCH` | Read the original operation; do not reuse its key for different params. |
| `WRKF_LEASE_CONFLICT` | Read current ownership and predecessor history; never reuse superseded authority. |
| `WRKF_SUSPENDED` | Read the suspension and use its explicit resolution operation. |
| `WRKF_KIND_ROLE_DENIED`, `WRKF_LINKAGE_UNRESOLVED`, `WRKF_LINKAGE_STALE` | Use structured `field`, `expected`, `allowed`, and `fix` details to correct evidence role/linkage. |

Transition revision CAS, evidence writes, events, effects, and internal task
projections commit atomically. An identical idempotent replay returns the
stored result; a changed payload is refused. Run authority is fenced by owner
token and generation. Expiry makes an action claim contestable; it does not
revoke the current holder. A successor claim must acknowledge the predecessor
through `priorRun` (explicit `null` for the first claim).

`wrkf.transition.apply` rejects `runChecks: true`. Run `wrkf.check.run`, then
pass persisted `checkIds`; transactional input-hash revalidation catches drift.
Hooks never run inside the transition writer transaction. Effect claim/ack/fail
use lease tokens; wrong or expired tokens are refused.

## Clients and validation

The TypeScript package is `@wrkq/client` in `packages/client`, with sibling
`client.wrkq` and `client.wrkf` facades. The shared Go transport is
`github.com/lherron/wrkq/pkg/client`. Both preserve initialize/schema/auth/error
behavior across local and remote transports. Template operations carry bounded
content, and watch operations return bounded snapshots/event pages; clients own
polling loops and caller-local files.

Run `just verify-rpc` for client typechecking, unit coverage, and integration
against real repo-local binaries and isolated databases. `just verify` includes
that gate. The [change-validation guide](docs/change-validation.md) explains
the full gate and installed-surface smoke requirements.
