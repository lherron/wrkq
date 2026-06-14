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

CLI flags → env vars → `./.env.local` → `~/.config/wrkq/config.yaml`.

Key vars: `WRKQ_DB_PATH`, `WRKQ_ATTACH_DIR`, `WRKQ_LOG_LEVEL`, `WRKQ_OUTPUT`, `WRKQ_PAGER`, `WRKQ_ACTOR` (slug), `WRKQ_ACTOR_ID` (friendly ID like `A-00001`).

Secrets: env vars or `_FILE` variants. Never commit SQLite files or attachment contents.

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

- [Search index operations](internal/search/README.md) — FTS5, dense vectors, llama-server operating notes, and canonical launch args.
- [Domain model operations](internal/domain/README.md) — resources, addressing, optimistic concurrency, attachments, comments, and migrations.
- [wrkq product/domain/CLI/daemon spec](docs/SPEC.md) — canonical product and command contract.
- [wrkf JSON-RPC stdio contract](docs/wrkf-rpc.md) — frozen machine contract for wrkf RPC.
- [Embedded agent usage block](internal/cli/embedded/WRKQ-USAGE.md) — task lifecycle and wrkq command quick reference.
