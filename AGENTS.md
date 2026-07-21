# AGENTS.md

Guide for agents working on **wrkq** — a filesystem-flavored CLI for projects, subprojects, tasks, comments, and attachments on a SQLite backend.

## Dogfooding

**Use wrkq to track your own work in this repo.** The wrkq database tracks active tasks; agents working here should `wrkq ls`, `wrkq cat`, `wrkq comment add`, and `wrkq set --state` as part of normal flow.

@internal/cli/embedded/WRKQ-USAGE.md

For a deeper command reference, run `wrkq info`.

## Binaries

Four binaries (built by `just build`):

- **`wrkq`** — agent + human collab surface (task/container/comment CRUD, content editing, history).
- **`wrkqadm`** — admin surface (DB lifecycle, actor management, bundle apply, health).
- **`wrkqd`** — local daemon (TCP/unix socket, token auth) for shared-DB access.
- **`wrkf`** — workflow CLI (workflow engine surface).

The wrkq/wrkqadm split exists so agents get a focused, safe API while admins retain DB lifecycle control.

`pbc/` is a sample wrkf workflow, not part of the core wrkq/wrkf runtime; use it as a concrete example of workflow templates, evidence contracts, descriptions, and generated explanatory artifacts.

## Justfile is the lifecycle

`just --list` for the full menu. Common: `just build`, `just test`, `just verify` (lint+test), `just install` (canonical install to `~/.local/bin` — Lance's validation flow), `just smoke` (build + wrkqd + wrkf smokes), `just db-migrate-local`, `just db-reset` (destructive, prompts).

Main-checkout `just install` also publishes one timestamped immutable
`@wrkq/client` snapshot to the current node's Verdaccio and, unless
`no-sync=1` is passed, synchronizes local downstream consumers. Fleet consumer
nodes must receive that exact package version in their own loopback registry;
do not rerun a timestamp-generating producer install merely to seed another
node.

If a project lacks `just install`, add it.

## Deterministic local validation

For no-network or sandboxed environments, use:

```bash
scripts/agent-check.sh
```

This assumes vendored Go dependencies and disables toolchain/dependency downloads:

- `GOPROXY=off`
- `GOSUMDB=off`
- `GOTOOLCHAIN=local`
- `GOFLAGS=-mod=vendor -p=1`
- `CGO_CFLAGS=-O0 -g0`

Expected local Go version: see `go.mod`.

Keep `go.mod`, `vendor/`, and `vendor/modules.txt` in sync:

```bash
go mod tidy
go mod vendor
go list -mod=vendor ./... >/dev/null
```

## Config precedence

CLI flags → environment variables → nearest `./.env.local` → platform
`~/praesidium/.env.local` → `~/.config/wrkq/config.yaml` → built-in defaults.

Key authority and transport inputs:

- `WRKQ_DB` selects a local SQLite path or `rpc://host[:port]`; remote locators
  default to port `7171`.
- `WRKQ_DB_PATH` / `WRKQ_DB_PATH_FILE` are local-path compatibility inputs and
  reject `rpc://` values.
- `WRKQD_TOKEN` / `WRKQD_TOKEN_FILE` authenticate remote calls. An explicitly
  supplied token file wins over a dotenv-loaded token.
- `WRKQ_CLAIM_TOKEN` / `WRKQ_CLAIM_GENERATION` carry the active task claim into
  a runtime and are forwarded by holder-guarded completion.
- `WRKQ_PRINCIPAL_REF` supplies mutation attribution; legacy `WRKQ_ACTOR` and
  `WRKQ_ACTOR_ID` are not caller authority.
- `WRKQ_ATTACH_DIR`, `WRKQ_OUTPUT`, and `WRKQ_PROJECT_ROOT` retain their normal
  storage/output/project roles.

Secrets belong in environment variables or `_FILE` inputs. Never commit
tokens, SQLite files, or attachment contents. Bun autoloads `.env.local` before
application code; Bun operator bridges that require explicit transport
authority should start with `bun --env-file=/dev/null` so ambient dotenv values
cannot replace the intended locator or credential.

## Federated Task Claims

`wrkqd --node-tokens` / `--node-tokens-file` maps each bearer credential to one
authenticated logical `nodeId`. The daemon derives `claimed_node` from that
credential; clients must never infer or assert node identity from hostname or
IP address.

`wrkq claim <task>` establishes one holder and generation. `--take-over`
supersedes the current holder and monotonically bumps the generation. Claimed
runtimes receive the opaque claim token and generation, and completion is
accepted only from the current holder. Preserve remote HTTP/auth errors (for
example HTTP 401) rather than collapsing them into task-not-found.

## Exit codes

- `0` success
- `1` generic error (db, io)
- `2` usage error
- `3` not found
- `4` conflict (etag mismatch, merge conflict)
- `5` partial success (with `--continue-on-error`)

## Output formats

`--json`, `--ndjson` (best for piping), `--yaml`, `--tsv`, `--porcelain` (stable machine-readable, no ANSI).

## Commits & PRs

Conventional Commits (`feat`, `fix`, `chore`, `test`, optional scopes like `fix(mcp): ...`). Run `just verify` before opening PRs; mention affected commands or migration IDs.

## Talking to the user

If you have questions, number them for easier responses.

## Territory docs & further reading

- [Durable architecture law](architecture/README.md) — canonical invariant/risk records, producer contracts, and their generated projections; consult and cite record ids before changing a recorded surface.
- [Search index operations](internal/search/README.md) — FTS5, dense vectors, llama-server operating notes, and canonical launch args.
- [Domain model operations](internal/domain/README.md) — resources, addressing, optimistic concurrency, attachments, comments, and migrations.
- [wrkq product/domain/CLI/daemon spec](docs/SPEC.md) — canonical product and command contract.
- [wrkf JSON-RPC stdio contract](docs/wrkf-rpc.md) — frozen machine contract for wrkf RPC.
- [wrkq change validation](docs/change-validation.md) — when to run verify / verify-full / install+smoke and where the wrkf template fits.
- [Agent-enablement changelog](docs/enablement-changelog.md) — target-local retro carrier for sensor/workflow changes.
- [Rule-authoring template](docs/rule-template.md) — author any new build-failing rule deliberately with a 7-field candidate and when-to-use policy.
- [Embedded agent usage block](internal/cli/embedded/WRKQ-USAGE.md) — task lifecycle and wrkq command quick reference.
