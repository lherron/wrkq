# RPC-backed wrkq CLI — Coverage Matrix

Tracking doc for the RPC-backed mirror CLI (`wrkq-rpccli`). Design and rationale
live in `rpcwrkqcli.md`. This file is the command-by-command coverage matrix and
the standing record of what is and is not migrated.

## Status legend

- `rpc-backed` — implemented through JSON-RPC; parity proven against the oracle.
- `seam-smoke` — a transport/protocol smoke only; **not** CLI parity. Compared
  against an RPC resource DTO, not against legacy `wrkq` output.
- `rpc-gap` — should be RPC-backed, but the RPC method/DTO is missing.
- `local-only` — should remain local (process/runtime control).
- `not-started` — mirrored as a stub; no behavior yet.

## Architecture (as built — seam smoke slice)

- **Transport seam** — `internal/rpccli`'s `Transport` interface is the only path
  to durable wrkq behavior. Command adapters never import `store`, `wrkqapi`,
  `db`/SQL, or `internal/cli`; this is enforced by `TestCoreRuleImportGuard`.
- **In-process transport** drives the *real* `workrpc.Server.Serve` loop over
  pipes (same codec, `rpc.initialize` gating, dispatch, `MapError`, stdout-purity
  redirection as `wrkq rpc --stdio`).
- **Subprocess transport** speaks the identical `conn` protocol to a spawned
  `wrkq rpc --stdio`; `TestTransportEquivalence_InProcVsSubprocess` proves they
  agree. This is the on-ramp for a future `wrkqd` daemon transport.
- **Neutral construction** — `internal/workrpc/bootstrap` builds the
  `*wrkfapi.API` + `workrpc.RegistryOptions` for both the stdio entrypoint and
  the mirror, so they cannot drift. `internal/cli/rpc.go` was refactored to use
  it (no behavior change).
- **Project-root scoping is CLI caller semantics** — the neutral
  `internal/projectroot` package holds the ONE project-root transform
  (`ApplyToPath`/`ApplyToSelector`/`ApplyToPaths` + `ResolveProjectFlag`). Both
  the legacy CLI (`internal/cli/project_root.go` + appctx delegate to it) and the
  mirror's `scoper` (`internal/rpccli/scope.go`, built in `openMirror` from the
  bootstrap handle config + `--project` override) apply it to raw path/selector
  args BEFORE any RPC param is sent. RPC methods receive already-scoped selectors
  and never read `WRKQ_PROJECT_ROOT` / `ASP_PROJECT` / `--project`. One
  implementation, one test suite (`projectroot.TestTransform`); parity proven with
  a project root set (`TestParity/pr/*`: ls + cat/stat + touch/set/mv +
  `ASP_PROJECT` + `--project` override) and an installed smoke under real config.

## `cat` via `wrkq.task.catView` (T-05090 ruling: option A)

Daedalus ruled (T-05090) that `cat`'s rich legacy projection is reproduced by a
dedicated **server-owned compatibility projection** method, `wrkq.task.catView`
— NOT by expanding the canonical `wrkq.task.show` DTO and NOT by client-side
multi-call composition (the legacy projection needs scalars — `artifact_dir`,
friendly `project_id`, actor display slugs/roles, `created_by_scope_ref`, derived
`blocked_by` — that no existing RPC read exposes, so composition could not avoid
new RPC surface anyway).

- `wrkq.task.catView({task, includeComments?=true})` returns the **exact** legacy
  per-task `cat --json` object (snake_case, legacy time formats), assembled under
  **one read transaction** (task scalars + project/friendly ids + parent display
  + actor/principal/scope + comments + incoming/outgoing relations + incomplete
  blockers). It is a compatibility read model, not the canonical resource.
- `wrkq.task.show` stays the camelCase domain DTO — no back-propagation.
- `artifact_dir` is documented as a **server-local host path hint**, not a remote
  filesystem guarantee.
- The CLI owns byte rendering: the mirror wraps per-task objects into the legacy
  indented JSON array.

**catView contract green ≠ cat command parity green.** `cat` stays `partial`
until *every* exposed mode (`--json` done; `ndjson`/`porcelain`/`raw` pending) is
byte-parity proven and covered by installed smoke.

## Coverage

| Command path | Status | Notes |
|---|---|---|
| `ack` | `rpc-backed` | **Proven parity** (TestParity, 6 cases): RPC-backed via composition — `wrkq.task.show` to classify already-acked skips + not-found, then `wrkq.task.acknowledge` for the durable mutation (server stays authoritative for the force/terminal gate, attribution, etag). Output + error wording reproduced byte-for-byte; durable snapshot verified. |
| `stat` | `rpc-backed` | **Proven parity** (TestParity, 6 cases): first RPC-backed READ command. Re-projects `wrkq.task.show` (falling back to `wrkq.container.show`) into the legacy stat metadata shape. Byte-identical stdout incl. UUIDs (copy-fixture), plus error parity (`path not found`). |
| `cat` | `rpc-backed (partial)` | **`--json` parity proven** (TestParity: single/multi/comments/relations/blockers/exclude-comments/unknown). RPC-backed via the server-owned `wrkq.task.catView` compatibility projection (one read snapshot per task; T-05090 ruling). `ndjson`/`porcelain`/`raw` modes not yet implemented → **partial, not fully `rpc-backed`** until every exposed mode is byte-proven (daedalus guard). |
| `touch` | `rpc-backed (partial)` | **Core-flag parity proven** (TestParity: basic/flags/default-title) via `wrkq.task.create`, re-projected to the legacy touchResult array. Flags with no create param (`due-at`/`start-at`/`requested-by`/`assigned-project`/`resolution`/`meta-file`/`force-uuid`) hard-error as gaps (no silent divergence). Artifact-dir disk creation is a side-effect not yet replicated (output string matches). |
| `set` | `rpc-backed (partial)` | **Common field-update parity proven** (state/multi-field) via `wrkq.task.update` patch. Patch-less flags (slug/parent-task/cp-*/resolution/run-status/…) hard-error as gaps; partial-failure exit-code taxonomy not yet covered. |
| `mv` | `rpc-backed (partial)` | **Single-source task→container move parity proven** via `wrkq.task.move`. Rename, container sources, multi-source, `--dry-run` not yet (hard-error). |
| `rm` | `rpc-gap` | wrkq.task.delete sets `state=deleted`; legacy `rm` default archives (`archived_at`) + has `--purge/--recursive/--ndjson/--porcelain`. Needs reconciliation/RPC gap fill. |
| `restore` | `rpc-gap` | wrkq.task.restore lacks `--to` (move-on-restore) and `--comment`. Basic restore covered; advanced surface is an RPC gap. |
| `rm` | `not-started` | likely `rpc-backed` (wrkq.task.delete) |
| `restore` | `not-started` | likely `rpc-backed` (wrkq.task.restore) |
| `comment add` | `rpc-backed` | **Proven parity** via wrkq.comment.add → legacy {id,uuid,task_id,principal_ref,created_at,etag}. |
| `comment cat` | `rpc-backed (partial)` | **--json parity proven** via server-owned wrkq.comment.catView compat projection (alphabetical-key struct matching legacy map; conditional actor/scope/deletion fields). ndjson/raw/error modes pending. |
| `comment ls` | `rpc-backed` | **Full render-mode + multi-task surface proven** via server-owned wrkq.comment.listView. Rendering: --json, NDJSON/non-TTY, --porcelain first-page next_cursor (token byte-identical), --yaml, --tsv, table/--human (TTY default), all byte-matched. Paging: limit+1, empty, cursor-replay no-dup/no-miss across pages (TestCommentLsCursorReplay). Sorting: --sort id/created_at/updated_at + --reverse/desc (server applies cursor.Apply Descending; reverse-json + reverse-sort-id parity). MULTI-TASK: `tasks` array param — server applies the same cursor predicate + limit+1 to EACH task and accumulates in task order, truncating the combined set at limit (legacy-exact); multi-task json/ndjson/yaml/tsv + porcelain-limit parity + a distinct multi-task cursor-replay no-dup/no-miss test (TestCommentLsMultiTaskCursorReplay). Project-root scoping is caller-side (each task arg run through the scoper before RPC). malformed-cursor + include-deleted parity retained. DELIBERATE DIVERGENCE (pinned, do not chase): invalid --sort returns a clean WRKQ_VALIDATION error (legacy leaks a raw SQL "no such column" string). |
| `comment rm` | `not-started` | confirmation-flow. |
| `attach ls` | `rpc-backed (partial)` | **Empty-set + transport parity proven** via server-owned wrkq.attachment.listView (DB-only cursor-paginated; DB-only cursor projection). EMPTY-SET/transport/catalog/fingerprint-green ONLY — NOT populated-row or pagination proven (needs attach put fs fixtures). put/get/rm pending. |
| `relation add`/`rm` | `rpc-backed` | **Proven parity** via wrkq.relation.add/.remove, composing task.show for each endpoint id+uuid. |
| `relation ls` | `rpc-backed (partial)` | **--json + NDJSON/non-TTY parity proven** (incl. empty→null) via server-owned wrkq.relation.listView (no cursor; bounded by task). TTY table pending. |
| `container cat` | `rpc-backed (partial)` | **--json parity proven** via server-owned wrkq.container.catView compat projection. ndjson/porcelain/raw/markdown pending. |
| `ls` | `rpc-backed (partial)` | **Mixed task/container parity proven** via server-owned wrkq.task.lsView (rollup counts, in-memory merge-sort over the merged set, cursor): top-level rollups, container mixed ordering, single-task path, --json (empty→`null`), NDJSON/non-TTY, --porcelain limit+cursor (next_cursor→stderr), all accepted --sort (slug/id/created_at/updated_at), --reverse, --type p\|t, --all (incl. default-hides-completed), empty→null, unknown-path error, malformed-cursor. **`--output raw` is byte-parity UNSUPPORTED** (legacy excludes raw from ls's allow-set; both emit the identical `output mode "raw" is not supported for this command` — TestParity/ls/output-raw-unsupported). **Invalid `--type` is parity-pinned to empty/`null`** (legacy passes an unknown type through both type blocks → empty set → `null`; server lsView matches — TestParity/ls/invalid-type-null). Cursor replay/no-dup-no-miss across the MIXED set proven (TestLsCursorReplay). HARD-GATED mirror-only (proven by TestLsHardGates, no silent degradation; legacy RENDERS these): multi-path, --recursive, --one, --nul, table/human/yaml/tsv, conflicting modes. DELIBERATE DIVERGENCE: invalid --sort returns a clean validation error (legacy leaks a raw SQL "no such column" string). Server fix: lsSingleTask not-found now emits legacy's exact "path not found: <path>". Project-root scoping applies (see architecture/records/invariants/wrkq.project-root.caller-semantics.yaml). |
| `find` | `rpc-backed (partial)` | **Recursive/filtered search parity proven** via server-owned wrkq.task.findListView (server owns recursive path-prefix matching, metadata filters, cursor.Apply + limit+1 + sort-validation + BuildNextCursor over the filtered set, and the mixed-type in-memory merge-sort): all-default, path-prefix recursion, --type t\|p, --state (open/all/default-excludes-archived/deleted/idea), --kind, --slug-glob, --json (empty→`[]`, NOT null — legacy inits `[]findResult{}`), NDJSON/non-TTY, --porcelain limit+cursor (next_cursor→stderr), all accepted --sort (updated_at/created_at/id/path), --reverse, unknown-path→empty (find does NOT resolve search paths; a non-matching path is a no-op filter, not an error), invalid --sort error, unknown --parent-task error. **Mixed-type (no --type) IGNORES the cursor** — legacy's searchBoth path calls findTasks/findContainers with skipPagination so cursor.Apply never runs; single-type (--type t\|p) applies the cursor SQL-side (TestParity/find/mixed-cursor-ignored vs find/type-t-malformed-cursor-errors). Error wrapping replicated: findTasks/findContainers errors carry legacy's `finding tasks:`/`finding containers:` prefix (server prefixFindError preserves the domain code). **`--output raw` is byte-parity UNSUPPORTED** (legacy excludes raw from find's allow-set; identical `output mode "raw" is not supported for this command`). Cursor replay/no-dup-no-miss across the FILTERED/RECURSIVE single-type set proven (TestFindCursorReplay, per daedalus). HARD-GATED mirror-only (proven by TestFindHardGates, no silent degradation; legacy RENDERS these): --print0, table/human/yaml/tsv, conflicting modes. Project-root scoping applies to search paths + --parent-task selector (TestParity/pr/find-default-root; see architecture/records/invariants/wrkq.project-root.caller-semantics.yaml). |
| `ls` | `rpc-backed (full read surface)` | **Mixed task/container parity proven** via server-owned wrkq.task.lsView (rollup counts, in-memory merge-sort over the merged set, cursor): top-level rollups, container mixed ordering, single-task path, --json (empty→`null`), NDJSON/non-TTY, --porcelain limit+cursor (next_cursor→stderr), all accepted --sort (slug/id/created_at/updated_at), --reverse, --type p\|t, --all (incl. default-hides-completed), empty→null, unknown-path error, malformed-cursor. **NOW UNGATED with REAL byte parity** (moved out of the former TestLsHardGates into TestParity): `--output table`/`human` (both render through internal/render.FormatTable — legacy's runLs switch has no human case → table fall-through), `--output yaml` (render decodes the compat projection back into the legacy `lsEntry` struct so yaml.v3's untagged-field-name keys match exactly), `--output tsv`, `--one` (-1) / `--nul` (-0) path emission, multi-path (server-owned per-path query + combined merge-sort + combined limit+1/next-cursor via the new `paths` param), `--recursive`/`-R` (a NO-OP in legacy — rollups already recurse — accepted-and-ignored to byte-match), and conflicting `--json --ndjson` (identical "choose only one output mode"). **`--output raw` stays byte-parity UNSUPPORTED** (legacy excludes raw from ls's allow-set; both emit the identical `output mode "raw" is not supported for this command` — TestParity/ls/output-raw-unsupported). **Invalid `--type` stays parity-pinned to empty/`null`**. Cursor replay/no-dup-no-miss across the MIXED set proven (TestLsCursorReplay). TestLsHardGates RETIRED — no ls surface remains hard-gated. DELIBERATE DIVERGENCE: invalid --sort returns a clean validation error (legacy leaks a raw SQL "no such column" string). lsSingleTask not-found emits legacy's exact "path not found: <path>". Project-root scoping applies (see architecture/records/invariants/wrkq.project-root.caller-semantics.yaml). |
| `find` | `not-started` | |
| `tree` | `not-started` | |
| `find` | `not-started` | |
| `tree` | `rpc-backed (partial)` | **Recursive-hierarchy parity proven** via server-owned `wrkq.task.treeView` compat projection (the SERVER owns the entire walk: container pruning, "all done" rollups, in-set subtask nesting, hidden-container counting; the CLI owns ONLY byte rendering). Proven (non-TTY): bare-default NDJSON, `--json` (full nested `treeOutput`), `--ndjson` (flat depth/path stream), `--porcelain` (tab-separated walk), `-L/--level` depth limit, `--open`, `--all` (incl. default-hides-completed + `(All done)` collapse), single subtree path, empty container, nested subtasks, unknown-path error (legacy's exact `failed to resolve path "<p>": container not found: <p>`), and project-root scoping (`WRKQ_PROJECT_ROOT` default-root + relative path). **`--output raw` is byte-parity UNSUPPORTED** (legacy excludes raw from tree's allow-set; both emit the identical `output mode "raw" is not supported for this command` — TestParity/tree/output-raw-unsupported). HARD-GATED mirror-only (TestTreeHardGates, no silent degradation): the interactive PRETTY/human renderer (TTY-only + embeds wall-clock-relative `opened N ago` strings and ANSI color → not byte-reproducible in the hermetic non-TTY harness; legacy gates the non-TTY default to NDJSON for outputShapeList so it is never exercised), `--output table/yaml/tsv`, multi-path, `--fields`, conflicting modes. The tree DTO carries two `wire_*` carriers (`wire_created_at`, `wire_parent_task_uuid`) + `wire_raw_path` the JSON projection hides; the CLI uses them to rebuild the NDJSON stream + nesting and strips them for `--json` (pinned by TestTreeViewDTOFingerprint). Project-root scoping applies (invariant wrkq.project-root.caller-semantics). |
| `stat` | `not-started` | |
| `apply` | `not-started` | |
| `cp` | `not-started` | |
| `mkdir` | `rpc-backed` | **Proven parity** (TestParity): RPC-backed via `wrkq.container.create` with legacy top-level→project / nested→directory kind inference. |
| `rmdir` | `rpc-backed (partial)` | **Empty-container parity proven** via `wrkq.container.delete`. `--force` (recursive) is a gap: legacy uses interactive confirmation; RPC `deleteRecursive` needs an expected-impact param. |
| `rename-container` | `not-started` | |
| `handoff` | `not-started` | `rpc-gap` candidate |
| `search` | `not-started` | `rpc-gap` / index-owner question |
| `index` | `not-started` | `local-only` / admin candidate |
| `monitor` | `not-started` | streaming/watch as protocol behavior |
| `watch` | `not-started` | streaming |
| `diff` | `not-started` | |
| `log` | `not-started` | event/history |
| `check blocked` | `rpc-backed (partial)` | **Non-TTY parity proven** via server-owned `wrkq.task.blockedView` compat projection (reproduces legacy `BlockedResult` over `store.Tasks.BlockedBy`: incomplete `blocks`-relation sources, state filter `NOT IN (completed,archived,deleted,cancelled,idea)`): default/`--json` indented JSON to stdout; **blocked → exit 1 with the JSON body on stdout AND `Error: task is blocked by N incomplete task(s)` on stderr**; `--quiet` (exit-code only; blocked → `Error: task is blocked`); unknown-ref → `Error: failed to resolve task: <raw resolve err>` (mirror re-wraps the server's raw resolve message exactly). TTY human-readable table HARD-GATED (mirror-only error, no silent degradation). Project-root scoping applies (selector via `sc.selector(arg,false)`). |
| `check-inbox` | `rpc-backed (partial)` | **Non-TTY parity proven** via server-owned `wrkq.task.inboxView` compat list projection (reproduces legacy `inboxTask` rows + the two queries: open tasks under the inbox container path, then ack-pending `completed/cancelled` tasks requested by the configured project): default ndjson, `--json` (indented array), `--ndjson`/`--porcelain` (ndjson), empty→`[]`, excludes non-open. **Project-root is CALLER-scoped**: the mirror passes the scoped inbox path (`ApplyToPath("inbox",false)`) and project id (`Normalize(cfg)`) as RPC params; the view never reads project-root env/flags (TestParity/check-inbox/project-root-scoped). TTY table HARD-GATED. |
| `bundle` | `not-started` | `rpc-gap` candidate |
| `webhook` | `not-started` | `rpc-gap` candidate |
| `agent` / `agent-context` / `agent-info` | `not-started` | |
| `projects` | `not-started` | |
| `usage` | `not-started` | aliases: info; `local-only` candidate |
| `version` | `not-started` | `local-only` |
| `whoami` | `not-started` | `local-only` |
| `server` | `not-started` | `daemon-control` (start/stop/restart/status/health) |
| `rpc` | `not-started` | `local-only` (the mirror is itself the client) |

## Parity harness (data-driven)

The equivalence oracle is `internal/rpccli/parity_test.go` (`TestParity`), a
table-driven harness. Each row in `parityCases` declares `{setup, args, mutates}`;
the generic driver seeds two identical hermetic fixtures, runs the **old** binary
on one and the **new** binary on the other, and asserts byte-equal **exit code +
stdout + stderr**, plus an identical durable **task-table snapshot** for mutating
commands. Timestamps are normalized; provenance columns (`via`) are excluded
because the RPC path correctly records `via='rpc'`.

**Adding a command = appending rows to `parityCases`.** No shell edits, no new
script per command. `test/compare-rpccli.sh` is a thin convenience runner over
this Go harness. The harness is self-verifying — a negative-control check
(deliberately breaking a command) confirms it catches divergence rather than
passing vacuously.
