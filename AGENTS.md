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

## Sample workflows

`pbc/` is a sample wrkf workflow, not part of the core wrkq/wrkf runtime. Use it as a concrete example of a workflow template, evidence contracts, descriptions, and generated explanatory artifacts.

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

## Domain model essentials

**Resources:** Actor, Container (project/subproject, hierarchical via `parent_uuid`), Task, Comment (append-only, soft-deletable), Attachment, Event (append-only audit log).

**Addressing** — any of:
- Path: `project/subproject/task`
- Friendly ID: `T-00123`, `P-00007`, `A-00001`, `C-00012`
- UUID
- Typed selector: `t:T-00123`, `c:C-00012`
- Globs: `*`, `?`, `**` (always quote: `wrkq ls 'portal/**/login-*'`)

**Task states:** `open`, `in_progress`, `completed`, `blocked`, `cancelled`, `archived` (soft-delete via `archived_at`).

**Slug rules:** lowercase `[a-z0-9-]`, must start with `[a-z0-9]`, max 255 bytes, unique among siblings. Source: `internal/domain/validation.go`.

**Attribution, not auth:** every mutating command requires an actor (`--as`, `WRKQ_ACTOR_ID`, `WRKQ_ACTOR`, or config default). Resolution order is in `internal/actors/`.

## Concurrency

Every mutable row carries `etag INTEGER` (increments on write). All mutating commands accept `--if-match <etag>` for optimistic concurrency. Conflicts exit `4`. SQLite is opened in WAL + 5000ms busy_timeout, so concurrent CLI/agent access is expected.

## Attachments

Metadata in DB, bytes on disk at `<attach_dir>/tasks/<task_uuid>/...`. Paths are keyed by task UUID, so task moves don't rewrite paths. Soft delete preserves files; `--purge` removes the directory. Default `attach_dir` is `~/.local/share/wrkq/attachments` — user-specific, untracked.

## Comments

Append-only. Soft delete via `deleted_at`; `--purge` for hard delete. The comments block emitted by `wrkq cat --include-comments` is **read-only** — `wrkq apply` does not parse it back.

## Exit codes

- `0` success
- `1` generic error (db, io)
- `2` usage error
- `3` not found
- `4` conflict (etag mismatch, merge conflict)
- `5` partial success (with `--continue-on-error`)

## Output formats

`--json`, `--ndjson` (best for piping), `--yaml`, `--tsv`, `--porcelain` (stable machine-readable, no ANSI).

## Migrations

SQL files in `internal/db/migrations/000NNN_description.sql`, embedded via `//go:embed`. Applied filenames tracked in `schema_migrations`. `wrkqadm migrate` (idempotent), `--status`, `--dry-run`. `wrkqadm init` runs migrations automatically.

## Search index (FTS5 + dense vectors)

- Sidecar SQLite at `<canonical>.search.sqlite`. Built and queried independently from the canonical DB. Drop and rebuild is non-destructive.
- Indexes three resource types: `task`, `comment`, `handoff`. `wrkq search` queries tasks+comments; `wrkq handoff search` queries handoffs through the same engine. Both go through `internal/search/search.go`.
- `search_chunks` schema is at version `2` (`internal/search/indexdb/db.go`). On version mismatch the chunk/FTS tables are auto-dropped and the indexer resets `last_indexed_event_id` to `0` so the next `wrkq index update` or `wrkq handoff search` lazily rebuilds.
- **FTS5 requires the `sqlite_fts5` build tag.** `just build` already passes it. Without the tag, queries silently degrade to a LIKE-based fallback on `search_fts_plain` — works, but no bm25 ranking.
- Lifecycle commands: `wrkq index status`, `wrkq index rebuild [--foreground]`, `wrkq index update`, `wrkq index vacuum`, `wrkq index pause`, `wrkq index resume`.
- `wrkq handoff search` calls `IndexPending` automatically before each query so freshly created/acked handoffs show up without a manual rebuild.

### Dense embeddings — llama-server (`internal/search/embed/embed.go`)

The dense provider defaults to `llama-cpp` at `http://127.0.0.1:8080` (see `cfg.Search.Dense*`). The configured model is `Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M`, dim 4096. Operating notes from this codebase's experience:

- **`--pooling last` reliably crashes llama.cpp 9260 with this model** (`EXC_BREAKPOINT` in `llama_context::decode`, malloc heap-protection trap). Use **`--pooling mean`** instead. Crash reports land in `~/Library/Logs/DiagnosticReports/llama-server-*.ips`.
- **`--parallel N>1` also crashes** under the same model+quant. Single slot is stable.
- **`--ubatch-size 512` (the default) is too small.** wrkq chunks truncate at 4000 chars (~1000 tokens). llama-server returns 500 `input (N tokens) is too large to process. increase the physical batch size` if input exceeds the physical batch. Set `--batch-size 4096 --ubatch-size 4096` to cover the largest realistic chunk.
- Set **`WRKQ_SEARCH_INDEX_BATCH_SIZE=1`** when running a full rebuild against this model+quant to send one document per HTTP call. Multi-doc batches risk exceeding `--ctx-size`.
- **`--cache-ram 0`** disables prompt cache. Helpful for memory-constrained or batch-only workloads; the cache doesn't help indexing throughput.
- **`WRKQ_SEARCH_DENSE_PROVIDER=none`** disables dense indexing entirely (FTS-only). Use in tests and on hosts without a llama-server.
- **Full rebuild rate** on this model+quant (Apple Silicon, single slot, batch=1): ~20 dense vectors/min. ~4000 chunks ≈ 3.5 hours.
- Embeddings index lives at `<canonical>.search.sqlite` in the `search_dense_vec` table (sqlite-vec virtual table, dim=4096). The shell `sqlite3` binary cannot read this table (no `vec0` module); use `wrkq index status` to count vectors.

### llama-server canonical args (see `launchd/com.praesidium.llama-server.plist`)

```
llama-server \
  --hf-repo Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M \
  --host 127.0.0.1 \
  --port <PORT> \
  --embedding \
  --pooling mean \
  --ctx-size 4096 \
  --batch-size 4096 --ubatch-size 4096 \
  --parallel 1
```

`--port 8080` conflicts with other things on Lance's hosts — wrkq's default `DenseBaseURL` and the launchd plist both use the wrkq-block port assigned in `internal/config/config.go`. Don't co-locate llama-server on 8080.

## Commits & PRs

Conventional Commits (`feat`, `fix`, `chore`, `test`, optional scopes like `fix(mcp): ...`). Run `just verify` before opening PRs; mention affected commands or migration IDs.

## Talking to the user

If you have questions, number them for easier responses.

## Specs / further reading

- `docs/SPEC.md` — product spec (source of truth for behavior)
- `docs/ARCHITECTURE.md` — package layout and internals
- `docs/DOMAIN-MODEL.md` — entity relationships
- `docs/CLI-REFERENCE.md` — full CLI reference
- `internal/cli/embedded/WRKQ-USAGE.md` — agent quick reference (embedded in binary)
