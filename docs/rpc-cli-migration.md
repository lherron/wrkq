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

**catView contract green ≠ cat command parity green.** Every exposed `cat`
render mode is now byte-parity proven on the SAME catView projection: `--json`
(indented array + non-TTY default), `--json --porcelain` (compact array),
`--ndjson` (one compact object per line), and `raw` markdown front-matter
(`--output raw` / `--porcelain` / TTY default), including `--no-frontmatter`,
comments, and relation/blocker front-matter. The TTY-only styled view is NOT
reproduced (never byte-tested; never reached when stdout is piped). No catView
DTO change was needed — every scalar the raw renderer prints was already on the
projection (T-05090), so its fingerprint pin is unchanged.

## Coverage

| Command path | Status | Notes |
|---|---|---|
| `ack` | `rpc-backed` | **Proven parity** (TestParity, 6 cases): RPC-backed via composition — `wrkq.task.show` to classify already-acked skips + not-found, then `wrkq.task.acknowledge` for the durable mutation (server stays authoritative for the force/terminal gate, attribution, etag). Output + error wording reproduced byte-for-byte; durable snapshot verified. |
| `stat` | `rpc-backed` | **Proven parity** (TestParity, 6 cases): first RPC-backed READ command. Re-projects `wrkq.task.show` (falling back to `wrkq.container.show`) into the legacy stat metadata shape. Byte-identical stdout incl. UUIDs (copy-fixture), plus error parity (`path not found`). |
| `cat` | `rpc-backed` | **All exposed render modes parity proven** (TestParity, 16 cases): `--json` (single/multi/comments/relations/blockers/exclude-comments/unknown), `--ndjson` (single/multi), `--json --porcelain` (compact array), `raw` markdown (`--output raw`, `--porcelain`, multi, `--no-frontmatter`, comments, relation/blocker front-matter), and the `--output table` "not supported" byte-parity error. All modes render the SAME server-owned `wrkq.task.catView` compat projection (one read snapshot per task; T-05090 ruling) — CLI-side byte rendering only; no DTO change. TTY-only styled markdown is intentionally not reproduced (never byte-tested). |
| `touch` | `rpc-backed (partial)` | **Core-flag parity proven** (TestParity: basic/flags/default-title) via `wrkq.task.create`, re-projected to the legacy touchResult array. Flags with no create param (`due-at`/`start-at`/`requested-by`/`assigned-project`/`resolution`/`meta-file`/`force-uuid`) hard-error as gaps (no silent divergence). Artifact-dir disk creation is a side-effect not yet replicated (output string matches). |
| `set` | `rpc-backed (partial)` | **Common field-update parity proven** (state/multi-field) via `wrkq.task.update` patch. Patch-less flags (slug/parent-task/cp-*/resolution/run-status/…) hard-error as gaps; partial-failure exit-code taxonomy not yet covered. |
| `mv` | `rpc-backed (partial)` | **Single-source task→container move parity proven** via `wrkq.task.move`. Rename, container sources, multi-source, `--dry-run` not yet (hard-error). |
| `rm` | `rpc-backed (partial)` | **TASK-target parity proven** (Tranche B0, caller-owned-confirmation seam; daedalus hrcchat#10185) via `wrkq.task.delete` with an EXPLICIT `mode` (the mirror ALWAYS passes `archive` for the legacy default → `archived_at`, `purge` for `--purge` → hard-delete + attachment-file/row cleanup; absent-mode reversible-delete behavior PRESERVED server-side). The mirror owns ALL human interaction: the legacy purge prompt (warning block + `Type 'yes' to confirm:`, exact `yes`), `--yes`, abort, dry-run (TTY text + non-TTY JSON), and project-root scoping — the RPC method is non-interactive. Cases (TestParity/rm/*): archive default, multi-target, `--purge` (+ prompt accept/abort/--yes/empty-stdin), dry-run archive/purge, `--ndjson`/`--porcelain`, nullglob/mixed, `-` stdin refs, not-found taxonomy, project-root. PTY prompt-ownership proven separately (TestRmPurgePromptOwnership). **CONTAINER targets are HARD-GATED** (clean non-zero, abort-before-mutation): `wrkq.container.delete` has no archive mode, so container `rm` cannot be byte-proven on this seam — that is a future container-archive slice, deliberately NOT pulled into B0 (TestRmContainerHardGate). `--recursive`/`--jobs` parallel path not yet covered. |
| `restore` | `rpc-backed (partial)` | **TASK-target parity proven** (Tranche B, caller-owned-confirmation seam; daedalus hrcchat#10185 item 4) via the EXTENDED `wrkq.task.restore`: the WHOLE legacy semantic op is carried SERVER-side (NOT composed client-side, which would expose intermediate states + drift). New params: `toPath` (move-on-restore — parent + final slug resolved + slug-conflict-checked inside the op), `title`/`description`/`priority`/`labels`/`assignee` field updates, `comment`, `ifMatch`. Error precedence mirrors legacy: state/priority/labels/assignee validation → not-deleted-or-archived → `ifMatch` mismatch (WRKQ_CONFLICT) → `toPath` resolve + slug-conflict (WRKQ_CONFLICT). The restore UPDATE does NOT bump etag (legacy parity, snapshot-checked). restore NEVER prompts, so no confirmation flow — the mirror owns only caller-side scoping of the task ref + `toPath` and the legacy output. **WEBHOOK parity now proven** (daedalus hrcchat#10201 gap 1): the RPC restore dispatches the `updated` webhook for the ROOT task AND each cascade-restored subtask — archived/deleted→target transition, per-field Changed/Changes (move carries `project_uuid`+`slug`), `Origin.Via="rpc"` (the RPC-mutation convention vs legacy `cli`) — via `webhooks.DispatchTaskEvent` (TestTaskRestore_DispatchesWebhook / _CascadeDispatchesSubtaskWebhook / _MoveWebhookChangedPayload). **VALIDATION-PRECEDENCE parity now proven** (gap 2): the mirror calls `wrkq.task.restore` FIRST (no speculative `task.show` pre-resolve) and only rewrites a genuine NOT_FOUND to the container-gate / legacy not-found, so a bad `--state`/`--priority`/`--labels`/`--assignee` on a MISSING ref surfaces the VALIDATION error, not not-found (TestParity/restore/precedence-bad-{state,priority,labels,assignee}-missing-ref); output uses the RETURNED DTO. Cases (TestParity/restore/*): basic-archived, by-id, state, comment, title, description, priority, labels, assignee, to-move, to-slug-conflict, if-match-ok, if-match-mismatch, cascade-subtasks, reject-archived-state, reject-active, not-found, precedence-bad-{state,priority,labels,assignee}-missing-ref, project-root. Server protocol tests (TestWrkqTaskRestore_ExtendedFields / _IfMatchMismatch / _InvalidPriority). **CONTAINER targets are HARD-GATED** (clean non-zero, no mutation): there is no `wrkq.container.restore` method — a future container archive/restore slice (TestRestoreContainerHardGate). |
| `comment add` | `rpc-backed` | **Proven parity** via wrkq.comment.add → legacy {id,uuid,task_id,principal_ref,created_at,etag}. |
| `comment ls` | `rpc-backed` | **Full render-mode + multi-task surface proven** via server-owned wrkq.comment.listView. Rendering: --json, NDJSON/non-TTY, --porcelain first-page next_cursor (token byte-identical), --yaml, --tsv, table/--human (TTY default), all byte-matched. Paging: limit+1, empty, cursor-replay no-dup/no-miss across pages (TestCommentLsCursorReplay). Sorting: --sort id/created_at/updated_at + --reverse/desc (server applies cursor.Apply Descending; reverse-json + reverse-sort-id parity). MULTI-TASK: `tasks` array param — server applies the same cursor predicate + limit+1 to EACH task and accumulates in task order, truncating the combined set at limit (legacy-exact); multi-task json/ndjson/yaml/tsv + porcelain-limit parity + a distinct multi-task cursor-replay no-dup/no-miss test (TestCommentLsMultiTaskCursorReplay). Project-root scoping is caller-side (each task arg run through the scoper before RPC). malformed-cursor + include-deleted parity retained. DELIBERATE DIVERGENCE (pinned, do not chase): invalid --sort returns a clean WRKQ_VALIDATION error (legacy leaks a raw SQL "no such column" string). |
| `comment cat` | `rpc-backed` | **All render modes parity proven** via server-owned wrkq.comment.catView compat projection (alphabetical-key struct matching legacy map; conditional actor/scope/deletion fields). Modes: `--json` + non-TTY-default-json (indented array), `--ndjson` (compact server DTO bytes == legacy map marshal, single + multi), `--raw` (body-only, `---`-joined, single + multi), and error parity: not-found (`comment not found: <arg>`) + invalid-ref (`invalid comment reference: <arg> (expected friendly ID like C-00001 or UUID)`). Error messages preserve the ORIGINAL arg incl. any `c:` prefix (mirror reconstructs from the WRKQ_NOT_FOUND/WRKQ_VALIDATION domain code, not the stripped ref). CLI-side rendering only; no new RPC method/DTO. TTY header mode (`[id] [ts] slug (role) - Task: id` + body) implemented but not parity-reachable (parity runs non-TTY). |
| `comment rm` | `rpc-backed` | **Parity proven** via `wrkq.comment.delete` with an EXPLICIT `mode` (`soft` default / `purge` for `--purge`) + `ifMatch` precondition, on the caller-owned-confirmation seam. The mirror owns the legacy `[y/N]` prompt (accepts `y`/`Y` — NOT rm's "yes"; prompts EVEN for soft-delete), abort/`--yes`, the `--if-match` warn-and-skip (`Warning: etag mismatch …, skipping`), unknown-ref warn-and-continue, dry-run (`[DRY RUN] Would <action> …` TTY / `{id,task_id,action,dry_run}` non-TTY), the bad-ref-shape hard error (aborts loop), and the non-TTY JSON-array results. TestParity/comment-rm (12 cases): soft-yes, purge-yes, prompt-accept-y, prompt-abort-n, prompt-empty-stdin-skip, unknown-warn-continue, invalid-ref-errors, if-match-mismatch-skip, if-match-match, dry-run-json, multi-yes, c-prefix-yes. PTY prompt-ownership: TestCommentRmPromptOwnership (accept-y/Y, abort, purge-prompts-too, --yes-skips). Server: TestCommentDelete_* (soft/purge/invalid→VALIDATION/ifMatch→CONFLICT/non-interactive). The snapshot oracle now captures comment (id,etag,deleted) so mutation parity is durable, not just rendered. Comment refs are not project-scoped (legacy doesn't scope them). |
| `attach ls` | `rpc-backed` | **Empty-set + POPULATED-row + pagination + transport parity proven** via server-owned wrkq.attachment.listView (DB-only cursor-paginated). Populated rows seeded with the legacy binary's `attach put` (source files materialized into the fixture dir before seeding via the additive `parityCase.files` map). Modes: `--json`, NDJSON/non-TTY default, `--porcelain --limit` first-page next_cursor→stderr. DB-only projection; relative_path is dir-independent so the seed `attach put` may write to the default attach dir. |
| `attach put` | `rpc-backed (full)` | **Real-FILE put parity proven** via the existing wrkq.attachment.add (host-path fast path; the mirror sends the HOST FILE PATH, server reads the bytes). Output re-projected from the camelCase WrkqAttachment DTO to legacy's alphabetical snake_case map {filename,id,mime_type,relative_path,size_bytes,task_id,task_uuid,uuid}; task_id resolved via wrkq.task.show. Cases: basic, --name/--mime override, unknown-task error. WRKQ_ATTACH_DIR set relative per-case so each binary writes into its own cmd.Dir. **STDIN put (`attach put <task> -`) is NOW RPC-backed via wrkq.attachment.addBytes** (T-05103, daedalus OPTION 1): the mirror reads stdin and uploads the bytes as base64 PROTOCOL DATA in 1 MiB chunks (never a host path); the server stages each chunk into a temp file, enforces the size limit incrementally, and on the final chunk atomically renames into the attach dir + inserts metadata/event (temp+atomic-rename cleanup parity with AttachmentAdd). Parity (TestParity/attach-put-stdin/*): basic text, binary stdin (NUL/newlines) with --mime, --name-required error, duplicate-filename conflict (`attachment with filename "<f>" already exists for task <T-id>`), unknown-task error. Error precedence matches legacy (server resolves task BEFORE the --name check). |
| `attach get` | `rpc-backed (full)` | **Byte-transfer get parity proven** via NEW wrkq.attachment.getBytes (T-05103, daedalus OPTION 1). Attachment CONTENT crosses the RPC boundary as base64 PROTOCOL DATA (chunked: server returns up to 1 MiB raw per frame + whole-file size/checksum); the MIRROR decodes each frame and writes RAW bytes to stdout (`--as -`) or a local file (`--as <path>`, CLI-owned write). The reassembled bytes are checksum/size-verified against the server metadata. **Stdout purity:** raw bytes are emitted ONLY by wrkq-rpccli post-decode — the JSON-RPC server stdout stays pure (guarded by TestStdoutPurity_AttachmentGetBytes: every server stdout line is valid JSON, no raw NUL). Parity (TestParity/attach-get/*): stdout text, stdout binary NUL payload, `--as <path>` JSON `{copied,path,...}` result, missing-attachment error (`attachment not found: <ref>`). The global persistent `--as` actor flag is shadowed by `attach get`'s local `--as` output flag (legacy collision reproduced; `--as -` forces stdout). Transport-equivalence across in-proc + subprocess stdio (TestTransportEquivalence_AttachmentBytes). |
| `attach rm` | `rpc-backed` | **Parity proven** via the existing wrkq.attachment.remove (metadata DELETE + server-side file unlink; NO new method). Multi-arg; `--yes` skips the interactive confirm (the prompt path resolves via wrkq.attachment.show first to print id+filename, then removes — empty-stdin harness uses --yes for determinism). Unknown id → `Warning: attachment not found: <ref>` on stderr + continue (non-fatal), legacy-exact. Non-TTY emits the indented JSON results array {id,uuid,filename,deleted}. Cases: rm --yes, unknown-warns-continues. |
| `relation add`/`rm` | `rpc-backed` | **Proven parity** via wrkq.relation.add/.remove, composing task.show for each endpoint id+uuid. |
| `container cat` | `rpc-backed` | **All render modes parity proven** via server-owned wrkq.container.catView compat projection (CLI-side rendering on the one projection). TestParity (9 cases): `--json` + non-TTY default (indented), `--ndjson` + `--porcelain` (compact single-line, identical objects), `--no-frontmatter` ("raw" body-only), webhook_urls array round-trip (json/ndjson), and `container not found: <ref>` error parity (mirror strips the RPC domain prefix and matches legacy's raw `selectors.ResolveContainer` message — NOT the generic `path not found`). The front-matter **markdown** branch only renders on a TTY (the pipe harness cannot drive one), so it is pinned by TestContainerCatMarkdownRender, which renders the REAL catView projection through the same `renderContainerMarkdown` the CLI uses and asserts the field set/order/YAML framing byte-for-byte (parent_id/parent_uuid emitted, parent_path omitted when path has no `/`, created_by/updated_by are actor slugs). No new RPC method/DTO (catView already proven across transports). Project-root scoping applies via the scoper. |
| `relation ls` | `rpc-backed` | **--json + NDJSON/non-TTY + TTY table parity proven** (incl. empty→null and the empty "No relations found" line) via server-owned wrkq.relation.listView (no cursor; bounded by task). All four exposed render modes byte-proven. The TTY table (Direction/Kind/Task ID/Slug/Title) is legacy-only output, unreachable through the bytes.Buffer harness; it is byte-compared old-vs-new by running BOTH binaries with stdout attached to a real pseudo-terminal — TestRelationLsTTYTableParity (populated multi-kind/multi-direction + empty). The pty's identical \n→\r\n (ONLCR) translation is applied to both sides, so byte parity holds. PTY allocation lives in internal/rpccli/pty_darwin_test.go (no third-party dep; darwin /dev/ptmx ioctls; skips cleanly if a pty cannot be allocated). |
| `find` | `rpc-backed (partial)` | **Recursive/filtered search parity proven** via server-owned wrkq.task.findListView (server owns recursive path-prefix matching, metadata filters, cursor.Apply + limit+1 + sort-validation + BuildNextCursor over the filtered set, and the mixed-type in-memory merge-sort): all-default, path-prefix recursion, --type t\|p, --state (open/all/default-excludes-archived/deleted/idea), --kind, --slug-glob, --json (empty→`[]`, NOT null — legacy inits `[]findResult{}`), NDJSON/non-TTY, --porcelain limit+cursor (next_cursor→stderr), all accepted --sort (updated_at/created_at/id/path), --reverse, unknown-path→empty (find does NOT resolve search paths; a non-matching path is a no-op filter, not an error), invalid --sort error, unknown --parent-task error. **Mixed-type (no --type) IGNORES the cursor** — legacy's searchBoth path calls findTasks/findContainers with skipPagination so cursor.Apply never runs; single-type (--type t\|p) applies the cursor SQL-side (TestParity/find/mixed-cursor-ignored vs find/type-t-malformed-cursor-errors). Error wrapping replicated: findTasks/findContainers errors carry legacy's `finding tasks:`/`finding containers:` prefix (server prefixFindError preserves the domain code). **`--output raw` is byte-parity UNSUPPORTED** (legacy excludes raw from find's allow-set; identical `output mode "raw" is not supported for this command`). Cursor replay/no-dup-no-miss across the FILTERED/RECURSIVE single-type set proven (TestFindCursorReplay, per daedalus). HARD-GATED mirror-only (proven by TestFindHardGates, no silent degradation; legacy RENDERS these): --print0, table/human/yaml/tsv, conflicting modes. Project-root scoping applies to search paths + --parent-task selector (TestParity/pr/find-default-root; see architecture/records/invariants/wrkq.project-root.caller-semantics.yaml). |
| `ls` | `rpc-backed (full read surface)` | **Mixed task/container parity proven** via server-owned wrkq.task.lsView (rollup counts, in-memory merge-sort over the merged set, cursor): top-level rollups, container mixed ordering, single-task path, --json (empty→`null`), NDJSON/non-TTY, --porcelain limit+cursor (next_cursor→stderr), all accepted --sort (slug/id/created_at/updated_at), --reverse, --type p\|t, --all (incl. default-hides-completed), empty→null, unknown-path error, malformed-cursor. **NOW UNGATED with REAL byte parity** (moved out of the former TestLsHardGates into TestParity): `--output table`/`human` (both render through internal/render.FormatTable — legacy's runLs switch has no human case → table fall-through), `--output yaml` (render decodes the compat projection back into the legacy `lsEntry` struct so yaml.v3's untagged-field-name keys match exactly), `--output tsv`, `--one` (-1) / `--nul` (-0) path emission, multi-path (server-owned per-path query + combined merge-sort + combined limit+1/next-cursor via the new `paths` param), `--recursive`/`-R` (a NO-OP in legacy — rollups already recurse — accepted-and-ignored to byte-match), and conflicting `--json --ndjson` (identical "choose only one output mode"). **`--output raw` stays byte-parity UNSUPPORTED** (legacy excludes raw from ls's allow-set; both emit the identical `output mode "raw" is not supported for this command` — TestParity/ls/output-raw-unsupported). **Invalid `--type` stays parity-pinned to empty/`null`**. Cursor replay/no-dup-no-miss across the MIXED set proven (TestLsCursorReplay). TestLsHardGates RETIRED — no ls surface remains hard-gated. DELIBERATE DIVERGENCE: invalid --sort returns a clean validation error (legacy leaks a raw SQL "no such column" string). lsSingleTask not-found emits legacy's exact "path not found: <path>". Project-root scoping applies (see architecture/records/invariants/wrkq.project-root.caller-semantics.yaml). |
| `tree` | `rpc-backed (partial)` | **Recursive-hierarchy parity proven** via server-owned `wrkq.task.treeView` compat projection (the SERVER owns the entire walk: container pruning, "all done" rollups, in-set subtask nesting, hidden-container counting; the CLI owns ONLY byte rendering). Proven (non-TTY): bare-default NDJSON, `--json` (full nested `treeOutput`), `--ndjson` (flat depth/path stream), `--porcelain` (tab-separated walk), `-L/--level` depth limit, `--open`, `--all` (incl. default-hides-completed + `(All done)` collapse), single subtree path, empty container, nested subtasks, unknown-path error (legacy's exact `failed to resolve path "<p>": container not found: <p>`), and project-root scoping (`WRKQ_PROJECT_ROOT` default-root + relative path). **`--output raw` is byte-parity UNSUPPORTED** (legacy excludes raw from tree's allow-set; both emit the identical `output mode "raw" is not supported for this command` — TestParity/tree/output-raw-unsupported). HARD-GATED mirror-only (TestTreeHardGates, no silent degradation): the interactive PRETTY/human renderer (TTY-only + embeds wall-clock-relative `opened N ago` strings and ANSI color → not byte-reproducible in the hermetic non-TTY harness; legacy gates the non-TTY default to NDJSON for outputShapeList so it is never exercised), `--output table/yaml/tsv`, multi-path, `--fields`, conflicting modes. The tree DTO carries two `wire_*` carriers (`wire_created_at`, `wire_parent_task_uuid`) + `wire_raw_path` — **real fingerprinted RPC protocol fields** (non-canonical compat carriers); the CLI uses them to rebuild the NDJSON stream + nesting and strips them from the user-facing `tree --json` projection (legacy never exposed them — hidden from `tree --json`, NOT from the RPC response; pinned by TestTreeViewDTOFingerprint). Project-root scoping applies (invariant wrkq.project-root.caller-semantics). |
| `apply` | `rpc-backed` | **Full parity proven** (Tranche B, T-05100) via `wrkq.task.update`. The mirror owns ALL caller-side work — reading the file/`-` stdin, format detection + parse (md / md+YAML-frontmatter / yaml / json via the pure `internal/parse` package), the `--with-metadata` gate (drops title/state/priority/due_at + emits the legacy stderr warning when absent), empty/size validation, the legacy etag precheck (its distinct "task was modified" message stays the source of truth; the patch additionally sends `expectEtag` when `--if-match`>0), dry-run rendering (TTY text + non-TTY JSON plan), and project-root scoping — the RPC method only receives the resolved patch. Output shape (`uuid`/`updated`/`fields` with snake_case `due_at`) is mirror-owned. Cases (TestParity/apply/*): md-file + stdin description, frontmatter spec, metadata-gated warn, `--with-metadata`, dry-run JSON, `--if-match` mismatch, empty input, unknown-task (`failed to resolve task: task not found: <id>`), project-root. The RPC `task not found: <ref>` message byte-matches legacy `selectors.ResolveTask`, so the wrapped resolve error is identical. |
| `cp` | `rpc-backed (partial)` | **Single-task deep-copy parity proven** (Tranche B, T-05111) via the NEW server method `wrkq.task.copy` (daedalus hrcchat#10196). The SERVER owns ONE source-task copy envelope: source resolution, destination-container resolution, source `expectEtag` CAS, the create-or-overwrite decision, the task-row write, the attachment-metadata cascade, the optional SAME-STORE attachment-file copy, the `task.copied` event, and the post-commit `created` webhook carrying a synthetic `source_uuid` change. The CLI owns multi-source fan-out, stdin sources (`cp - <dest>`), the caller-owned `>5`-source `Copy <n> tasks? [y/N]` prompt (PTY-tested) / abort / `--yes`, the dry-run plan, `--nullglob` / `--continue-on-error` / `--jobs`, output rendering (machine-JSON default returns exit 0 even on a single-source failure — legacy's early-return shape), and project-root scoping. DTO `WrkqTaskCopyParams{source,destination,overwrite,withAttachments,shallow,expectEtag,actor,idempotencyKey}` → `WrkqTaskCopyResult{source_id,source_uuid,dest_id,dest_uuid,dest_path,attachments_copied?,with_files?}`. FILE-COPY SAFETY: not claimed fully transactional — every attachment file is staged into a temp `.copy-*` under the dest task dir BEFORE commit; a staging failure rolls the DB tx back + removes the temps (no RPC-visible partial durable state), and only after commit are the staged temps atomically renamed into place. `idempotencyKey` makes a retried source copy non-duplicating under fan-out. Cases (TestParity/cp/*): new-into-container, `--overwrite`, slug-conflict-no-overwrite, `--shallow`, with/shallow validation, unknown source/dest, nullglob, stdin sources, dry-run JSON, with-attachments (real shared store), project-root; plus TestCpPromptOwnership (PTY `>5` prompt) and TestCpRecursiveHardGate. Server-side durable/event/idempotency coverage in internal/workrpc/taskcopy_test.go. **PARTIAL:** container/recursive copy (`cp -r`) is NOT in this tranche → clean hard-gate on the mirror (legacy would recurse). |
| `mkdir` | `rpc-backed` | **Proven parity** (TestParity): RPC-backed via `wrkq.container.create` with legacy top-level→project / nested→directory kind inference. |
| `rmdir` | `rpc-backed (full)` | **Empty-container parity proven** via `wrkq.container.delete`. **`--force` (recursive) NOW proven** via the TWO-PHASE `wrkq.container.deleteRecursive` on the caller-owned-confirmation seam: a `dryRun:true` preflight returns the impact `{containers,tasks,attachments,bytes}` (recursive; `containers` includes the target), the mirror renders the legacy WARNING block + prompts `Are you sure? (yes/no):` (requiring EXACTLY `yes`) — only when non-empty — then commits echoing `expected:{…}` with the exact preflight numbers (the CAS race guard; stale impact → `WRKQ_CONFLICT`). The mirror also reproduces legacy's per-container `✓ Removed: <id> (<path>)` TTY line vs the non-TTY `[{path,removed,forced}]` JSON. **PROMPT-COUNT SEMANTICS (daedalus-accepted divergence, hrcchat#10198):** the prompt counts come from the dry-run impact, which is RECURSIVE and ARCHIVED-INCLUSIVE (`Tasks` = all descendant tasks; child-container line = `Containers-1` descendant containers). Legacy's prompt rendered IMMEDIATE, active-only (archived-excluded) counts. For a single-level container holding only ACTIVE contents these coincide and are byte-proven; for nested or archived contents the mirror's counts intentionally reflect the TRUE recursive destructive impact, NOT legacy's immediate active-only count. The committed delete is byte-equivalent either way (deleteRecursive is recursive in legacy too). TestParity/rmdir (6 cases): empty, force-yes, force-prompt-accept, force-prompt-abort, force-prompt-empty-stdin-abort, force-empty-no-prompt. PTY prompt-ownership: TestRmdirForcePromptOwnership (accept/abort-no/abort-y-not-yes/--yes-skips). Server `deleteRecursive` dryRun/expected/CAS already existed (no new method). |
| `rename-container` | `rpc-backed` | **Full parity proven** (Tranche B, T-05112) via the NEW method `wrkq.container.update` (daedalus hrcchat#10196). The FIRST patch surface is NARROW — `patch{slug?, title?}` only; any other key → `WRKQ_VALIDATION` (no overbroad mutation sink; kind/parent/webhook/archive need explicit future review). Identity-preserving: the server does an in-place `UpdateFieldsWithAttribution` (same uuid/friendly-id/children/history, etag bump, `container.updated` event), normalizes/validates the slug server-side, and maps a slug collision / stale `expectEtag` to typed `WRKQ_CONFLICT` (not a raw store leak); `v_container_paths` rebuilds so the path + descendant paths reflect the new slug. The mirror owns ONLY the project-root scoping of the container selector + the legacy `--dry-run` / TTY / non-TTY output rendering (it normalizes the slug client-side purely to reproduce the dry-run display + invalid-slug wording; the server re-validates authoritatively). NON-destructive (no prompt). Cases (TestParity/rename-container/*): default-title, custom-title, dry-run, dry-run-custom-title, if-match-ok, if-match-stale, unknown-container, invalid-slug, slug-conflict, project-root-scoping. Server protocol/behavior: container_update_test.go (empty/missing patch, slug normalize/validate, title patch, unknown-field rejection, stale-etag conflict, slug-conflict typed, identity retained, children attached, path views, event payload/etag/attribution, unknown→not-found). |
| `handoff` | `not-started` | `rpc-gap` candidate |
| `search` | `not-started` | `rpc-gap` / index-owner question |
| `index` | `not-started` | `local-only` / admin candidate |
| `monitor` | `not-started` | streaming/watch as protocol behavior |
| `watch` | `not-started` | streaming |
| `diff` | `rpc-backed (partial)` | **Two-task field comparison parity proven** (TestParity: two-tasks-json/default-json/same-title/single-arg-not-implemented/unknown-ref-A/unknown-ref-B) via **two `wrkq.task.catView` reads + CLI-local comparison & rendering** (no new RPC method/DTO; the `{fields_changed, changes}` diff object is a pure presentation projection, not durable state). Compares slug/title/description/specification/state/priority/due_at/etag in that order, due_at coalesced to "". Default (non-TTY) output is JSON; explicit `--json` matches. Single-arg form returns the legacy `comparing with working copy not yet implemented` error AFTER fetching A. Unknown ref → `failed to resolve task <A\|B>: task not found: <ref>` (mirror restores legacy's wrapping prefix over catView's NOT_FOUND). PENDING: TTY human/colorized rendering not parity-proven (harness is non-TTY); `--unified` is accepted but a no-op in legacy too. DELIBERATE-LIKE NARROWING: catView's selector resolver collapses malformed-selector errors (e.g. wrong-type selector, invalid-slug) into `task not found: <ref>`, so legacy's *specific* resolve-error text for malformed selectors is not reproduced — only plain unknown-ref is in the proven contract (same bar as `cat`). |
| `log` | `rpc-backed (partial)` | **Deterministic-mode + history parity proven** via server-owned `wrkq.history.listView` — a CLI **compatibility history read model over the generic `event_log` table** (NOT `wrkf.event.query`, which reads `workflow_events`). The SERVER owns resource resolution (the already-caller-scoped target → exactly one `(resource_type, resource_uuid)` among task/container/actor, by friendly ID `T-*`/`P-*`/`A-*` AND UUID — actor history included), `since`/`until` filtering (legacy time-format error text), and `cursor.Apply` + `limit+1` + `BuildNextCursor` over `event_log.id DESC`. Server default `limit=50` (`0`=unlimited), applied in the registry handler only when the caller OMITS `limit`; the mirror always sends the flag value so legacy parity holds. The CLI owns presentation. DTO is LEGACY STRUCT ORDER (not alphabetical): `WrkqHistoryListView{items,next_cursor}` wrapping `[]WrkqLogEvent{id,timestamp,actor_uuid,actor_slug,actor_id,principal_ref,scope_ref,resource_type,resource_uuid,event_type,etag,payload}` — `payload` stays a raw STRING; pointer/omitempty match legacy. **Byte-proven (TestParity/log*):** `--json` (incl. empty history → `null`, NOT `[]` — legacy `var events []logEvent`), NDJSON (non-TTY default + empty→no-output), `--porcelain --limit` next_cursor→stderr, task-ID / container-ID / actor-ID / UUID resolution, all unknown-`T`/`P`/`A`/UUID errors, `--since`/`--until` (include/exclude + invalid-format errors), malformed cursor; cursor replay no-dup/no-miss over id DESC (TestLogCursorReplay). Error wrapping replicated: the server returns legacy's full `failed to resolve resource: …` / `failed to query event log: …` text (prefixLogError preserves the domain code; mirror strips the code prefix). **PINNED DIVERGENCE:** path-target resolution is a legacy TODO — path args advertised in help but error today (`path resolution not yet implemented: <scoped>`); the mirror reproduces this incl. project-root scoping (TestParity/log/pr-relative-path-scoped-error). Project-root is CALLER-scoped: an ID is NOT prefixed (TestParity/log/pr-id-not-prefixed), a relative path IS scoped before the call. **DOCUMENTED NON-PARITY (NOT hard-gated):** `--oneline`, the detailed default, and `--patch` render their Summary/Changes lines by iterating the decoded payload **map**, which Go randomizes — legacy itself is non-byte-stable for any multi-key payload (two legacy `--patch` runs differ). The mirror copies legacy's render code VERBATIM (faithful, not narrower); a hard gate would make the mirror fail where legacy succeeds (worse parity), so these modes are implemented + documented rather than gated. `--oneline`/`--patch` are guarded by an order-insensitive SEMANTIC test (TestLogHumanModesSemanticParity: identical token multiset old-vs-new over a multi-key payload — same event ids/types/actors/payload pairs; NOT byte equality). The TTY detailed default is an exposed legacy renderer with a semantic smoke PENDING. Project-root scoping applies (invariant wrkq.project-root.caller-semantics). |
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

## attach get / stdin-put byte-transfer boundary (RESOLVED — daedalus OPTION 1, T-05103)

`attach put <task> <file>` (real file) and `attach rm` are mirrored through the
EXISTING `wrkq.attachment.add` / `wrkq.attachment.remove` methods: for a
co-located real file the mirror sends a host **path string** plus metadata (the
server reads the bytes). This stays the **local fast path**.

The two byte-transfer surfaces — **`attach get <id>`** (server → client bytes) and
**`attach put <task> -`** (client stdin → server bytes) — are now implemented per
**daedalus OPTION 1 (hrcchat#10132)**: attachment CONTENT crosses the RPC boundary
as **explicit protocol data** (base64 + declared size + checksum), NEVER as a
server-local host path. A server-local path is a hint/impl detail, never required
for client-visible `attach get` output or stdin-upload correctness.

**Chunked, not single-frame.** The JSON-RPC frame cap is 8 MiB
(`workrpc.DefaultMaxFrameBytes`); the default attach limit is 50 MB
(`attachments_max_mb`). A single base64 frame for a max-size attachment would
exceed the frame cap, so a bounded single-base64 DTO **cannot** cover the
configured limit — transfers are CHUNKED (1 MiB raw per frame → ~1.34 MiB base64,
comfortably under the cap).

- **`wrkq.attachment.getBytes`** `{id, offset, limit}` → `WrkqAttachmentBytes`
  `{uuid,id,taskUuid,filename,mimeType,sizeBytes,checksum,offset,contentBase64,eof}`.
  The mirror loops with an increasing `offset` until `eof`, decoding each frame
  and writing RAW bytes to stdout (`--as -`) or a local file (`--as <path>`,
  CLI-owned write). The reassembled bytes are checksum/size-verified against the
  server's whole-file metadata. A missing row → `WRKQ_NOT_FOUND` (`attachment not
  found: <ref>`); a missing file-on-disk → `WRKQ_NOT_FOUND` (kind `attachment
  file`).
- **`wrkq.attachment.addBytes`** chunked upload: the first call (no `uploadId`)
  supplies `task/filename/mimeType/actor` and receives a server-generated
  `uploadId`; subsequent calls echo it with a monotonic `seq`. The server stages
  each chunk into a **temp file** under the task's attach dir, enforces the size
  limit **incrementally** (cleanup on reject), and on the `final` chunk validates
  the total, **atomically renames** the temp file into place, and inserts the
  metadata row + `attachment.created` event — temp+atomic-rename cleanup parity
  with `AttachmentAdd`. Duplicate filename → `WRKQ_CONFLICT` with legacy's exact
  wording. Idempotency: `idempotencyKey` on the begin call replays the committed
  DTO.

**Stdout purity (sacred).** Raw bytes are emitted ONLY by `wrkq-rpccli` AFTER
decoding the RPC frame — the JSON-RPC server stdout carries only JSON-RPC frames
(guarded by `TestStdoutPurity_AttachmentGetBytes`; invariant
`architecture/records/invariants/wrkq.wrkf-rpc.attachment-byte-transfer.yaml`).

**Capability/contract.** Absence of byte capability HARD-GATES: when no attach dir
is explicitly configured the byte methods return `WRKQ_VALIDATION` ("attachment
storage is not configured") — they never silently fall back to a host path. There
is NO host-path return/staged-path path; if one is added later it must be a
distinctly-named local capability (`sharedFilesystemPaths`), never the
correctness/remote path.

**Byte-proven coverage:** `attach get` stdout text, stdout binary (NUL/newlines),
`--as <path>` JSON result, missing-attachment, checksum/size match; `attach put -`
basic text, binary stdin + `--mime`, `--name` required, duplicate filename, unknown
task. Transport-equivalence across in-proc + subprocess stdio. (TestParity/attach-*,
TestTransportEquivalence_AttachmentBytes, TestStdoutPurity_AttachmentGetBytes.)

## Open gaps — Tranche B mutations awaiting new RPC surface (T-05100)

Two Tranche-B mutation commands cannot be byte-proven on the existing RPC surface
and are STOP-AND-GAPped (no mirror code landed; faking them would violate
identity/event invariants):

### `cp` → proposed `wrkq.task.copy`

**Legacy behavior** (`internal/cli/cp.go`): a deep, per-source-task copy into a
destination container. For each source task it (1) copies the mutable task fields
into a NEW task in the destination (or upserts an existing same-slug task when
`--overwrite`); (2) copies attachment **metadata** rows, and with
`--with-attachments` copies the underlying **files** (skipped entirely with
`--shallow`); (3) writes a `task.copied` event with a `{source_id, source_uuid,
attachment_count, with_files}` payload; (4) dispatches a `created` webhook with a
synthetic `source_uuid` change. Output: `copyResult{source_id, source_uuid,
dest_id, dest_uuid, dest_path, attachments_copied, with_files}`. Flags: `--dry-run`,
`-j/--jobs`, `--continue-on-error`, `--with-attachments`/`--shallow`,
`-r/--recursive`, `--overwrite`, `--yes` (prompt only when >5 sources),
`--nullglob`, `--if-match`, output modes.

**Why no existing method maps**: composing `wrkq.task.show` + `wrkq.task.create`
client-side would (a) NOT reproduce the `task.copied` event type or the
source-uuid webhook change, (b) expose intermediate states + drift the event
shape, and (c) leave the attachment cascade (metadata + optional file copy) with
no RPC home. There is no copy method on the surface today.

**Proposed**: `wrkq.task.copy` (server-owned deep copy + cascade + event +
webhook), DTO `WrkqTaskCopyParams{ source, destination, overwrite, withAttachments,
shallow, expectEtag, actor }` → `WrkqTaskCopyResult{ sourceId, sourceUuid, destId,
destUuid, destPath, attachmentsCopied, withFiles }`. The CLI owns the >5-source
prompt, `--jobs`/`--continue-on-error` fan-out, dry-run, nullglob, and output
rendering; the server owns one atomic single-task copy + cascade + event + webhook.

**Daedalus question**: *Approve a new server-owned `wrkq.task.copy` method (full
catalog/DTO/fingerprint checklist) that performs the deep single-task copy
(fields + attachment-metadata + optional attachment-file copy + `task.copied`
event + `created` webhook with the synthetic `source_uuid` change), with the CLI
retaining the multi-source fan-out / prompt / dry-run / output rendering? Or
should the attachment-file copy be a separate explicit mode/boundary (cf. the
attach byte-transfer ruling, T-05103)?*

### `rename-container` → `wrkq.container.update` ✅ RESOLVED (T-05112)

**Daedalus APPROVED** (hrcchat#10196): the NEW method `wrkq.container.update` (NOT
`.rename`), DTO `WrkqContainerUpdateParams{ container, patch{slug?,title?},
expectEtag, actor, idempotencyKey }` → `WrkqContainer` (the updated record). The
FIRST patch surface is deliberately **NARROW** — only `slug`/`title`; any other
key (`kind`/`parentUuid`/`webhookUrls`/`archived`/…) → `WRKQ_VALIDATION`, and
widening it requires explicit separate review (no overbroad mutation sink).

Implemented identity-preserving via the server-side in-place
`store.Containers.UpdateFieldsWithAttribution` (same uuid/friendly-id/children/
history, etag bump, `container.updated` event + attribution); the slug is
normalized + validated server-side; a slug collision (unique-in-parent) and a
stale `expectEtag` map to typed `WRKQ_CONFLICT` (not a raw store leak), and
`v_container_paths` rebuilds so the path + descendant paths reflect the new slug.
The mirror (`wrkq rename-container`) owns the project-root scoping + `--dry-run` /
TTY / non-TTY rendering; it normalizes the slug client-side purely to reproduce
the dry-run display + invalid-slug wording (the server re-validates). The command
is **non-destructive** (no prompt).

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
