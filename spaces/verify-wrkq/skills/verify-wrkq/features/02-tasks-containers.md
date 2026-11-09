# 2. Tasks and containers

This is the core ledger: projects, containers and tasks addressed by `T-<n>`/`P-<n>`, uuid or path. It covers
create, read, update with etags, list, tree, find, search, move, soft delete and restore, container archive with
its cascade, named subtasks, relations, subject extensions (`wrkq ext`), workstreams, the edit round trip (`cat --output raw` and `apply`), and history
(`log`). Code: `internal/rpccli/` (`touch.go`, `set.go`, `ls.go`, `tree.go`, `mv.go`, `rm.go`, `search.go`,
`apply.go`, `relation.go`, `log.go`, `ext.go`, `containers.go`), `internal/wrkqapi/` (`ext.go`, `workstream.go`,
`task_update.go`), `internal/store/` and `internal/workrpc/`. Guide:
`internal/rpccli/embedded/WRKQ-USAGE.md` (`wrkq info`).

## Sub-features

- **Create.** `wrkq mkdir <path> [--kind project|directory|...]` and `wrkq touch <container>/<slug> -t ... -d ...`.
  A leading `/` is root-absolute, which is how you reach another project or make a new one. Mutations are
  attributed to `--as`/`WRKQ_PRINCIPAL_REF` (`created_by_principal_ref`). `mkdir -p a/b/c` (988b172, T-12731)
  creates the missing directories (`created:true` for the leaf), reports an existing one `created:false`, and
  won't create a missing top-level project (`container not found: no-such-proj`, exit 1); without `-p` an existing
  path is `WRKQ_CONFLICT` "container slug already exists in parent". `mkdir --campaign[=draft|active]` creates a
  campaign in one write and `-p --campaign` converts an existing plain container (feature 5). A one-character
  container slug is `WRKQ_VALIDATION` "container slug must be at least 2 characters ..." (e9c5054). Driven
  2026-11-09, T-12762.
- **Task metadata.** Create/update accepts only the wrkq-owned top-level keys
  `workflow` and `_wrkq_archive_cascade`. Foreign keys, including null-valued or
  unchanged keys, refuse `meta_key_reserved` with `wrkq ext put` guidance. This
  applies to structured RPC metadata and `--meta`/`--meta-file`; empty/null clears
  and carrying wrkq keys through a read-modify-write remain admitted (T-12376). Migration 000073 strips foreign
  keys from existing rows. Re-driven 2026-11-09, T-12762: `--meta '{"acp":{"x":1}}'` and `{"foo":null}` refused,
  `--meta null` and `{}` admitted, a workflow-only round trip admitted (feature 8).
- **Extensions.** `wrkq ext put|get|list|delete <subject> <ns> <kind> <id>` (000071) keeps foreign data off the
  task with its own etag. The subject is a public `T-`, `P-` or `R-` id (a bare number or path is a usage error,
  exit 2). `put --body <json object|@file|->` with `--create` refuses an existing record (`WRKQ_CONFLICT`) and
  `--if-match N` a stale etag; `--create --if-match` together is a usage error. A non-object body, or an `ns`/`kind`
  that isn't 1-4 lowercase-led alphanumeric dot segments (`Bad`), is `WRKQ_EXT_INVALID`. `put` returns
  `{created, ext}`, `get` the record, `list [<subject>] [--ns] [--kind] [--states] [--limit] [--cursor]`
  `{items, nextCursor}` (`--states` filters by the subject task's state), `delete --if-match` `{deleted:true}`.
  The task's etag never moves; the task log gets `task.ext.put`/`task.ext.deleted`, and no webhook fires
  (feature 9). An `ext put` carries the seat scope (`updatedByScopeRef`). Driven 2026-11-09, T-12762.
- **Workstreams.** `touch|set --kind workstream --covers '{"tasks":[...],"container":"P-.."}'` (000072) records
  what a workstream covers; `cat --output raw` shows `kind: workstream` and `covers: {...}`. Covers on any other
  kind, or changing a covering workstream's kind, refuses `covers_requires_workstream`; a missing covered task is
  `task not found`; `--covers null` then `--kind task` is admitted. A workstream can't be claimed
  (`WRKQ_WORKSTREAM_NOT_CLAIMABLE`, feature 4), lists in `find --type t --kind workstream` (the `--kind` help
  text doesn't name `workstream` yet), and its `covers` is lost by
  `state export` (T-12717, feature 1). Driven 2026-11-09, T-12762.
- **Read.** `wrkq cat <sel> --json --one` (one object; without `--one`, JSON `cat` returns an array),
  `--output raw` (the task as Markdown), and `wrkq stat`.
- **Bare numbers.** A bare 1-4 digit number names the numerically **highest task whose id ends in exactly those
  digits**, and leading zeros count (7350485, T-11807). On a store holding T-00002 and T-00012, `2` and `12` select
  T-00012, while `02`, `002` and `0002` select T-00002. On canonical, `498` is T-11498 and `0498` is T-10498. Five
  digits is the literal id. With no match the verbs report the raw token, `task not found: 4242`. `cat`, `set`,
  `stat`, `comment ls`, `diff`, `monitor wait`, `wrkp log --task`, `wrkf` task selectors, `wrkc` room refs
  (feature 6), a raw `wrkq.task.show` and, since 3f33dec (T-11874), `wrkq log` all follow this rule: `log 2` reads
  T-00012's history, and `log 4242` fails `failed to resolve resource: task not found: 4242` plus a hint (exit 1).
  Driven 2026-10-26, T-11873; `log` driven 2026-11-02, T-12309.
- **Update.** `wrkq set <sel> --state ... --priority ... [--if-match <etag>]`. A stale etag fails with
  `task etag precondition failed` (exit 1). A raw `wrkq.task.update` with an `idempotencyKey` (a30b776) replays:
  the same frame again returns the original DTO even after a later edit made its `expectEtag` stale, with no second
  `task.updated`, and the same key with a changed patch is `WRKQ_CONFLICT` "idempotency key reused with a different
  request". There is no `wrkq set` flag for it. Driven 2026-11-09, T-12762.
- **List.** `wrkq ls <container> --type t`, `wrkq tree` (JSON lines when stdout is not a TTY), and
  `wrkq find --state open|all --type t [--campaign P]`. Without `--state`, completed, archived and deleted
  tasks are hidden, and so are `cancelled` (driven 2026-10-20, T-11374) and `idea` (source read, `find.go`).
- **Search.** `wrkq search '<words>' --state all --json` returns
  `{query, stale, status, results[].resource_id}`. Dense search uses the local embedding model, so a word can
  match a neighbouring task. The indexer wakes on every commit (876f20a): a task touched 3 s earlier is the top
  hit without `wrkq index update` (2026-11-09, T-12762).
- **Move.** `wrkq mv <task> <container>/`.
- **Delete and restore.** `wrkq rm <task>` soft-deletes (state `archived`, `archived_at` set) and
  `wrkq restore <task>` returns it to `open` (a task that isn't archived or deleted is refused,
  `task is not deleted or archived (current state: completed)`). `wrkq rm <container> --yes` archives the
  container and cancels its live tasks, like `archive`. `--purge` hard-deletes and cascades away the task's room and its
  envelopes. Never purge a task that has a room. Without `--yes` on a non-TTY, `--purge` prints the warning,
  reads EOF at `Type 'yes' to confirm:` and aborts (`Error: aborted`, exit 1), leaving the task as it was
  (driven 2026-10-20, T-11374, on a roomless task).
- **Container archive.** `wrkq archive <container> --yes` cancels the live tasks below it, and `wrkq unarchive`
  restores exactly the task states that the archive changed. Without `--yes` on a non-TTY it prompts, reads EOF
  and aborts (`Error: aborted`, exit 1), changing nothing.
- **Named subtasks.** `wrkq touch T-<n>.<name> --subtask` creates id `T-<n>.<name>` and bumps the owner's
  `open_subtask_count`.
- **Relations.** `wrkq relation add <a> blocks|relates_to|duplicates <b>` and `wrkq relation ls <a>`. Any other
  kind is refused, naming the three.
- **Edit round trip.** `wrkq cat <task> --output raw > f.md`, edit the file, then `wrkq apply <task> f.md`.
  `apply` writes only the body. Front-matter edits (title, state) need `--with-metadata`, and without it `apply`
  warns `Metadata ignored without --with-metadata`.
- **The other verbs.** `wrkq cp <task> <container>/` copies a task (a new `T-<n>`, so it shifts later ids;
  its JSON row reports `dest_path: ""`). `wrkq diff <a> <b>` lists `fields_changed`. `wrkq stat <sel> --json`
  returns an array. `wrkq rename-container <c> <new-slug>` (prints JSON off a TTY). `wrkq rmdir <c> [--force]`
  hard-deletes, and `--force` cascades with no undo. Never use it on shared data. `wrkq ack <task>` acknowledges completed
  tasks (`--force` for others). `wrkq check` is a group (`wrkq check T-1` is an unknown command, exit 2).
  `wrkq check-inbox --json` lists open inbox tasks. `wrkq timeline <container> --json` is the composite timeline
  (one object: `campaign`, `container`, `entries`, `members`, `rollup`, ...).
- **`--json` on every verb.** Since c720cb5 every runnable leaf accepts `--json`, hidden from `--help` where
  the verb has none of its own. It forces the non-TTY rendering, which is JSON for these verbs. Before it, `set`,
  `mv`, `relation add`, `archive`, `restore`, `rename-container`, `timeline` and others refused `--json` with
  exit 2 before writing. Exempt: `agent`, `rpc`, `server serve` and the `watch` shim (driven 2026-10-20,
  T-11374: `set --json`, `rename-container --json`, `timeline --json`).
- **History.** `wrkq log <ID|path> [--oneline|--patch|--json]`. A path names a task first, then a container.
  `--oneline` and the detailed `Summary:` print the payload as `key=value` pairs cut at 60 characters, in a
  different order on every run (see Gotchas).

## How to get to it

Run `eval "$(wv env <name>)"`. `WRKQ_PROJECT_ROOT=wv-<name>`, so relative paths land in the scratch project.
The scratch project starts with no containers, so `wrkq mkdir inbox` comes first.

## Driving it

```bash
export WRKQ_PRINCIPAL_REF=agent:<you>
~/.local/bin/wrkq mkdir inbox
~/.local/bin/wrkq touch inbox/wv-alpha -t "WV alpha" -d "body one" --json | jq -c '.[0] | {id,path,state}'
~/.local/bin/wrkq set inbox/wv-alpha --state in_progress --priority 1 && ~/.local/bin/wrkq cat inbox/wv-alpha --json --one | jq -c '{id,state,priority,etag}'
~/.local/bin/wrkq set inbox/wv-alpha --if-match 1 --priority 3          # refused: etag precondition failed
~/.local/bin/wrkq mkdir /wv-<name>/area2 && ~/.local/bin/wrkq touch inbox/wv-beta -t "WV beta" && ~/.local/bin/wrkq mv inbox/wv-beta area2/
~/.local/bin/wrkq log T-00001 --oneline
~/.local/bin/wrkq set inbox/wv-alpha --state completed && ~/.local/bin/wrkq find --state open --type t --json | jq -c '[.[].id]'
~/.local/bin/wrkq rm T-00002 && ~/.local/bin/wrkq restore T-00002
~/.local/bin/wrkq archive area2 --yes && ~/.local/bin/wrkq cat T-00002 --json --one | jq -r .state   # cancelled
~/.local/bin/wrkq unarchive area2 && ~/.local/bin/wrkq cat T-00002 --json --one | jq -r .state       # open
~/.local/bin/wrkq touch T-00002.check --subtask -t "WV named subtask"
~/.local/bin/wrkq relation add T-00002 blocks T-00001 && ~/.local/bin/wrkq relation ls T-00002
~/.local/bin/wrkq search alpha --state all --json | jq -c '[.results[].resource_id]'
~/.local/bin/wrkq mkdir -p area3/deep/er --json; ~/.local/bin/wrkq mkdir x --json        # created:true; WRKQ_VALIDATION
~/.local/bin/wrkq ext put T-00001 acp request main --body '{"rev":1}' --create --output json | jq -c '{created,etag:.ext.etag}'
~/.local/bin/wrkq ext put T-00001 acp request main --body '{"rev":2}' --if-match 1 --output json | jq -c .ext.etag   # 2
~/.local/bin/wrkq ext list T-00001 --ns acp --output json | jq -c '{n:(.items|length)}'; ~/.local/bin/wrkq ext delete T-00001 acp request main --if-match 2
~/.local/bin/wrkq touch inbox/wv-ws -t "WV workstream" --kind workstream --covers '{"tasks":["T-00001"]}' --json | jq -r '.[0].id'
~/.local/bin/wrkq touch inbox/wv-nows -t n --covers '{"tasks":["T-00001"]}'             # covers_requires_workstream
```

## Gotchas

- **`wrkq archive` is for containers only.** `wrkq archive T-00002` fails with `container not found: T-00002`.
  Archive a task with `wrkq rm`.
- `touch --json` returns an array (`.[0].id`), `cat --json` returns an array unless you pass `--one`, and
  `mkdir --json` returns an array of `{path, created}` (plus `converted:true` when `-p --campaign` converts), with no
  container id: read it with `wrkq stat <path> --json`.
- `wrkq set` prints an `errors/failed/succeeded/total` summary when an item fails. Look at the exit code, not only
  the stdout JSON. It also writes the progress line `\rProcessing 1/1...\r\033[K` to stderr whenever stdout is a
  character device, which includes `>/dev/null` (a file gets none). Product task T-12318 (2026-11-02, T-12309
  `02-tasks-containers/drive.txt`; still at 401c525, 2026-11-09).
- **Some `--json` refusals lose their typed code.** `wrkq set --meta '{"acp":1}' --json` prints
  `{"code":"WRKQ_ERROR","message":"meta_key_reserved: ..."}` though the server sends `WRKQ_META_KEY_RESERVED`; so do
  `release` and `campaign close` (features 4, 5). `ext`, `mkdir` and `promise` keep the code. Branch on the message or
  use `wrkq rpc --stdio`. Product task T-12766 (2026-11-09, T-12762).
- Closing a task prints a hint about reconciling worktrees under `~/praesidium/under-construction/`. On a scratch
  it means nothing.
- `wrkq tree` without a TTY prints JSON lines, and an empty project prints nothing at all.
- **A one-character task slug fails with a raw store error.** `wrkq touch inbox/x -t x` returns `WRKQ_ERROR`
  `CHECK constraint failed: slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' ...`, because that GLOB needs
  two characters. Use slugs of two or more characters. Product task T-10355 (2026-10-05, T-10349
  `02-tasks-containers/drive.txt`; task half still reproduces at 401c525, 2026-11-09, T-12762; the container half
  is a clean `WRKQ_VALIDATION` since e9c5054).

- **A bare number names the newest task with that suffix, not the T-1xxxx one.** On canonical (2026-10-26),
  `wrkq cat 7` is T-11867, not T-10007, and `07` is T-11807. An agent's "7 means T-10007" shorthand has to pass
  `T-10007` to the CLI (2026-10-26, T-11873 `live/reads.txt`).
- **`log --oneline` summaries are in random order.** The payload is a Go map, so `wrkq log PR-00001 --oneline` showed
  `subject=...`, then `state=open, subject=...`, then `review_question=...` on successive runs, and the 60-character
  cut picks which fields you see. Read `--json` when it matters, and expect these lines to differ in a replay
  diff. Product task T-12319 (2026-11-02, T-12309 `02-tasks-containers/drive.txt`; still at 401c525, 2026-11-09).

## Proven when

On the scratch, the created task carries your principal, an etag-guarded set refuses a stale etag, `find` hides
the completed task until `--state all`, `rm`/`restore` moves the task between `archived` and `open`, the
container archive cascades to `cancelled` and `unarchive` reverses exactly that change, a named subtask raises
`open_subtask_count`, `log <ID>` and `log <path>` show `task.created` and `task.updated` attributed to you, and a
bare `2` selects T-00012 once it exists (in `cat`, `set` and `log`), while `02` selects T-00002. `mkdir -p` makes a
nested path, an `ext` record round-trips put/get/list/delete with its own etag while the task's etag holds, a
workstream records `covers` and refuses covers on another kind, and a replayed `task.update` key returns the first
result.

Driven 2026-11-09 on wv `t-12762` (T-12762 upkeep) with installed 401c525: `var/wrkq-artifacts/T-12762/02-tasks-containers/drive.txt`
(the T-12309 replay, then the mkdir, meta, ext, workstream, update-replay and search drives after the NOTE drift line).
T-10355 (task half), T-12318 and T-12319 still reproduce at 401c525.

T-12376 candidate drive, 2026-11-04Z: `var/wrkq-artifacts/T-12376/rehearsal-drive.txt`
uses the built CLI against a migrated canonical-snapshot copy. It proves ACP/HCS/
unknown-key refusals and allowed workflow metadata carry. Canonical installed
rollout evidence is recorded separately in that task's VALIDATION.md.
