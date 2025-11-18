Below is a pragmatic, **MVP‑first milestone plan** that turns your spec into shippable slices. Each milestone lists concrete deliverables (CLI surface, schema, internals), acceptance criteria, and a tiny demo script. Timelines are intentionally omitted; sequence is optimized for risk‑reduction and preserving stable machine contracts early.

---

## Milestone Overview (high level)

| Milestone | Theme | What You Get |
|---|---|---|
| **M0 — Core CLI + DB** | “Hello, durable world.” | WAL SQLite DB, migrations, actors, containers, tasks, slugs, friendly IDs, pathspecs & globs, ETag concurrency, event log (create/update/delete), core CRUD commands, machine outputs (`--json`, `--ndjson`, `--porcelain`). |
| **M1 — Editing, History & Search** | “Work with content safely.” | `edit`/`apply` with 3‑way merge + `--if-match`, `log`, `watch`, `diff`, `tree`, `find` filters, sorting/field selection everywhere, perf indices, p95 checks. |
| **M2 — Attachments, Scale & Packaging** | “Operate at scale; ship binaries.” | Attachment put/get/ls/rm + purge semantics, pagination cursors, bulk‑ops knobs (`--jobs`, `--continue-on-error`), `cp`, `rm --purge`, doctor tools, completions, install scripts, GoReleaser artifacts & SBOM. |
| **M3 — API‑ready Contracts + Comments** | “Browser‑ready.” | Machine interface v1 freeze, `version --json` contract, comments table + CLI, minimal HTTP/JSON façade spec (Go or Node) aligned to CLI porcelain, doc set for integrators. |
| **M4 — Stretch (optional)** | “Nice to have.” | SQLite FTS seed for future `rg`, advanced diffs/patches, sample Node server stub & Postman collection, additional diagnostics. |

---

## M0 — Core CLI + DB (MVP)

### Deliverables

**Schema & migrations**
- `0001_init.sql` (SQLite dialect)
  - Tables: `actors`, `containers`, `tasks`, `events`.
  - Columns per spec incl. `etag INTEGER`, timestamps, FKs.
  - Indices:  
    - `containers(parent_id, slug)`,  
    - `tasks(project_id, slug)`,  
    - `tasks(state, due_at)`,  
    - `tasks(updated_at)`,  
    - `events(resource_type, resource_id, id DESC)`.
  - Sequences (or counters) table for friendly IDs (`next_actor`, `next_project`, `next_task`).
- WAL on open; `busy_timeout` + retry policy in code.

**Internals**
- `internal/db`: handle + migrations runner; pragmas.
- `internal/id`: friendly IDs (`A-xxxxx`, `P-xxxxx`, `T-xxxxx`) + UUIDs; zero‑padded stable width.
- `internal/paths`: slug normalization + pathspec/glob resolver (`*`, `?`, `**`) with sibling uniqueness guarantees.
- `internal/domain`: validation + ETag helpers.
- `internal/events`: append helpers for `created|updated|deleted|moved`.
- `internal/actors`: resolution precedence (`--as`, env, config, default seeded).
- `internal/render`: table, JSON, NDJSON, YAML, TSV; `--columns`, `--porcelain` (stable keys, no ANSI), `-1`/`-0`.

**CLI surface**
- `todo init` (creates DB, WAL, runs migrations, seeds `inbox`, default actors; respects `--db`, `--actor-slug`, `--actor-name`, `--attach-dir`).
- `todo whoami`, `todo actors ls`, `todo actor add`.
- Navigation/metadata:
  - `todo ls`, `todo stat`, `todo ids`, `todo resolve`.
- Structure & lifecycle:
  - `todo mkdir`, `todo touch -t`, `todo mv`, `todo rm` (soft delete), `todo restore`.
- Content read:
  - `todo cat` (task only; container → usage error `2`).
- Mutations:
  - `todo set key=value [...]` (state, priority, title, slug, labels, dates).
- Plumbing:
  - `todo version` (basic), `todo completion`, minimal `todo config doctor` stub that prints effective config.
- Exit codes: `0,1,2,3,4,5` implemented and covered by tests where applicable.

**Concurrency & audit**
- ETag check on all writes; `--if-match` honored by `set`, `mv`, `rm`, `restore`, `touch` metadata updates, `mkdir` metadata updates.
- Events appended with resulting `etag`.

**Docs & tests**
- Golden tests for outputs (`--porcelain`, `--json`, `-0`).
- Pathspec & slug property tests.
- Concurrency smoke: two writers, ETag conflict returns `4`.

### Acceptance criteria
- Fresh repo: `todo init` → DB file exists, WAL active, `inbox` project seeded, default actors present.
- `ls 'portal/**' -type t -1 | xargs -n1 todo cat` yields deterministic Markdown with front matter.
- `cat` on a container exits `2` with message “cat only supports tasks; got container …”.
- ETag mismatch reliably returns `4` without partial writes.
- `--json`, `--ndjson`, `--porcelain`, `-0` behave across `ls`, `stat`, `ids`, `resolve`.
- Basic perf: list **5k** tasks under **200 ms p95** locally (single run, warm cache).

### Demo script (happy path)
```sh
todo init --db ~/.local/share/todo/todo.db --actor-slug lance --actor-name "Lance (human)"
todo whoami
todo mkdir portal/auth -p
todo touch 'portal/auth/login-ux' -t "Login UX"
todo ls 'portal/**' -type t --columns=id,path,slug,title -1
todo set 'portal/auth/login-ux' priority=1 labels='["backend"]'
todo cat 'portal/auth/login-ux'
todo mv 'portal/auth/login-ux' 'portal/auth/login-experience' -type t
todo rm 'portal/auth/login-experience' --yes
todo restore 'portal/auth/login-experience'
```

---

## M1 — Editing, History & Search

### Deliverables

**Schema**
- `0002_event_payloads.sql` (if needed to widen event `payload`/indexes).

**Internals**
- `internal/edit`: 3‑way merge (base/current/edited), conflict markers; integrates `--if-match`.
- `internal/render` enhancements: `--sort`, richer `--fields` across commands.

**CLI surface**
- `todo edit <task>`: opens `$EDITOR`, applies on save via 3‑way merge; writes event with patch summary.
- `todo apply [<task>] [-]`: accepts md/yaml/json; `--body-only`, `--format`, `--dry-run`, `--if-match`.
- `todo log <path|id>`: oneline & detailed; `--since`, `--until`, `--oneline`, `--patch`.
- `todo watch [PATH...] --since <cursor> --ndjson`: streams events.
- `todo diff <A> [B]`: unified or JSON diff; supports task vs DB.
- `todo tree [PATH...] -L <depth>`.
- `todo find [PATH...]` with `-type`, `--slug-glob`, `--state`, `--due-before/after`, `--limit`, `--cursor`.

**Performance**
- Verify and document indices; micro-benchmarks for `find` and `ls` sorts.

### Acceptance criteria
- Editing a task body with concurrent update produces merge conflict and exits `4`.
- `apply --dry-run` prints plan, does not mutate.
- `watch` prints NDJSON events with cursor continuity.
- `find` filters by due date & state correctly and supports `-0` for piping.

### Demo script
```sh
todo find 'portal/**' -type t --state open --slug-glob 'login-*' --json
todo edit 'portal/auth/login-ux'   # make a change
todo log 'portal/auth/login-ux' --oneline
todo diff 'portal/auth/login-ux' --unified=3
todo watch --since 0 --ndjson | head -n5
```

---

## M2 — Attachments, Scale & Packaging

### Deliverables

**Schema**
- `0003_attachments.sql`: `attachments` table as spec; FKs to tasks and actors; indexes on `(task_id)` and `(created_at DESC)`.

**Internals**
- `internal/attach`: IO moving files to `attach_dir/tasks/<task_uuid>/...`; checksum optional; size/mime capture.
- Pagination/cursors: Opaque `--cursor` support on `ls`, `find`, `log`, `attachments ls`; `--limit`.

**CLI surface**
- `todo attach ls <task>`
- `todo attach get <ATT-ID> [--as PATH|-]`
- `todo attach put <task> <FILE|-> --mime <type> [--name <filename>]`
- `todo attach rm <ATT-ID...>` (metadata + delete file; respects `--yes`).
- `todo rm --purge` (hard delete rows + delete `attach_dir/tasks/<task_uuid>`).
- `todo cp <SRC...> <DST>` with `--with-attachments|--shallow`.
- Bulk & ops:
  - `--jobs N`, `--batch-size`, `--ordered`, `--continue-on-error`.
- Housekeeping & packaging:
  - `todo doctor` (pragmas, WAL, indices, attach_dir checks, attachment size limits).
  - `todo version --json` (add `commit`, `build_date`, `machine_interface_version`, supported formats/commands).
  - `install.sh`, completions for bash/zsh/fish.
  - GoReleaser config: darwin/linux/windows, amd64/arm64, checksums + SBOM.

### Acceptance criteria
- Attachments survive task moves/slug changes (path is `<task_uuid>` anchored).
- Soft delete of task leaves files; `--purge` removes files and metadata.
- Cursor pagination: deterministic `--sort` + `--cursor` round‑trips; `--porcelain` prints `next_cursor` on stderr.
- Binaries install via `install.sh` and completions load.

### Demo script
```sh
todo attach put 'portal/auth/login-ux' ./specs/login-flow.pdf --mime application/pdf --name "Login Flow Spec"
todo attach ls 'portal/auth/login-ux' --json
todo attach get ATT-00001 --as ./out/login-flow.pdf
todo rm 'portal/auth/login-ux' --yes
todo rm 'portal/auth/login-ux' --purge --yes
```

---

## M3 — API‑ready Contracts + Comments

### Deliverables

**Schema**
- `0004_comments.sql`: `comments(id, task_id, actor_id, body, created_at)`.

**Contracts & docs**
- **Machine Interface v1 freeze**:
  - `--porcelain` columns/keys stable and enumerated.
  - Field dictionaries per command documented (names, types, nullability).
  - Exit codes documented and unchanged across minors.
- **HTTP/JSON façade spec** (not the server itself, but ready to consume):
  - Endpoints mirroring CLI porcelain (e.g., `/v1/tasks/:id`, `/v1/containers/:id`, `/v1/find`, `/v1/events/stream`).
  - Auth: none (local only), but **actor attribution required** via header `X-Todo-Actor` (slug or friendly ID).
  - Request/response examples (JSON + NDJSON for streams), pagination cursors, ETag via `If-Match`.

**CLI surface**
- `todo comment add <task> -` (stdin) or `--body`
- `todo comment ls <task> --json|--ndjson`
- `todo comment rm <comment-id> --yes`

**Docs**
- “Browser UI enablers” guide: mapping CLI porcelain ↔ HTTP JSON; streaming (`watch`) over SSE or chunked NDJSON.
- Postman/Insomnia collection with sample calls (optional if no server stub).

### Acceptance criteria
- Comments append‑only; appear in `cat --include-comments` (from spec).
- HTTP/JSON spec aligns 1:1 with CLI fields and supports ETag semantics & cursors.

### Demo script
```sh
echo "We should add MFA to login." | todo comment add 'portal/auth/login-ux' -
todo comment ls 'portal/auth/login-ux' --ndjson
todo cat 'portal/auth/login-ux' --include-comments
todo version --json | jq .machine_interface_version
```

---

## M4 — Stretch (optional)

- SQLite FTS virtual table for tasks/comments bodies; scaffold `todo rg` (hidden/experimental).
- `todo diff --json` emits structured hunks suitable for UI patch views.
- Minimal Node or Go HTTP server stub implementing the spec (single‑binary dev server).
- Additional `todo doctor` checks (VACUUM/ANALYZE guidance, WAL checkpoints).

---

## Cross‑cutting Implementation Notes

- **Slug normalization**: single function used by `mkdir`, `touch`, `mv`, `set slug=...`; rejects invalid per global regex; max 255 bytes enforced at insert/update.
- **Friendly IDs**: allocate inside transaction to avoid gaps; render fixed width (5 digits) to match examples.
- **ETag**: numerically increments per row write; all mutating commands accept `--if-match`; `edit`/`apply` use 3‑way merge on body + field‑wise merge for metadata (no silent drops).
- **Event payloads**: store structured deltas (changed fields, previous→next); keep small to avoid bloat.
- **Globs**: implement internally (do not rely on shell); patterns must be quoted in docs.
- **Zero matches**: default error exit `3`; `--nullglob` converts to no‑op for bulk ops.
- **Perf guardrails**: NDJSON for bulk listings; avoid loading large bodies in `ls`/`find`.
- **Testing strategy**: CLI e2e (golden), unit tests for domain, property tests on resolver, race/concurrency tests (WAL+busy_timeout), Windows path handling in CI.

---

## Cut‑Scope Levers (if needed)

- Defer `tree` to M1 (kept).
- Defer `cp` to M2 (kept).
- Start M1 `log/watch` with minimal payloads before `--patch`.
- Start M2 without checksum for attachments; add in patch release.
- Limit `find` to simple predicates in M1; add more fields later without breaking porcelain.

---

## “Definition of Done” (overall)

- `todo version --json` exposes `machine_interface_version: 1`.
- All commands listed for a milestone have:
  - Stable `--porcelain` shape + documented fields.
  - `--json`/`--ndjson` parity where applicable.
  - Exit codes implemented and tested.
- CLI passes golden tests across macOS/Linux/Windows in CI.
- GoReleaser publishes signed checksums + SBOM; install script works; completions install.
- Docs: quickstart, piping examples, machine contracts, HTTP façade spec, migration/backup notes.

---

### Why this order?
- **M0** lays foundational contracts (IDs, slugs, ETag, events) and core UX so you can actually use the tool.
- **M1** makes content editing safe and auditable—the biggest day‑2 necessity.
- **M2** completes operational needs (attachments, pagination, packaging) to scale real usage.
- **M3** freezes machine interfaces and readies the ecosystem for a browser UI without locking you into a server implementation yet.