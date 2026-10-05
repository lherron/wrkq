# 2. Tasks and containers

This is the core ledger: projects, containers and tasks addressed by `T-<n>`/`P-<n>`, uuid or path. It covers
create, read, update with etags, list, tree, find, search, move, soft delete and restore, container archive with
its cascade, named subtasks, relations, the edit round trip (`cat --output raw` and `apply`), and history
(`log`). Code: `internal/rpccli/` (`touch.go`, `set.go`, `ls.go`, `tree.go`, `mv.go`, `rm.go`, `search.go`,
`apply.go`, `relation.go`, `log.go`), `internal/store/` and `internal/workrpc/`. Guide:
`internal/rpccli/embedded/WRKQ-USAGE.md` (`wrkq info`).

## Sub-features

- **Create.** `wrkq mkdir <path> [--kind project|directory|...]` and `wrkq touch <container>/<slug> -t ... -d ...`.
  A leading `/` is root-absolute, which is how you reach another project or make a new one. Mutations are
  attributed to `--as`/`WRKQ_PRINCIPAL_REF` (`created_by_principal_ref`).
- **Read.** `wrkq cat <sel> --json --one` (one object; without `--one`, JSON `cat` returns an array),
  `--output raw` (the task as Markdown), and `wrkq stat`.
- **Update.** `wrkq set <sel> --state ... --priority ... [--if-match <etag>]`. A stale etag fails with
  `task etag precondition failed` (exit 1).
- **List.** `wrkq ls <container> --type t`, `wrkq tree` (JSON lines when stdout is not a TTY), and
  `wrkq find --state open|all --type t [--campaign P]`. Completed, archived and deleted tasks are hidden by
  default.
- **Search.** `wrkq search '<words>' --state all --json` returns
  `{query, stale, status, results[].resource_id}`. Dense search uses the local embedding model, so a word can
  match a neighbouring task.
- **Move.** `wrkq mv <task> <container>/`.
- **Delete and restore.** `wrkq rm <task>` soft-deletes (state `archived`, `archived_at` set) and
  `wrkq restore <task>` returns it to `open`. `--purge` hard-deletes and cascades away the task's room and its
  envelopes. Never purge a task that has a room.
- **Container archive.** `wrkq archive <container> --yes` cancels the live tasks below it, and `wrkq unarchive`
  restores exactly the task states that the archive changed.
- **Named subtasks.** `wrkq touch T-<n>.<name> --subtask` creates id `T-<n>.<name>` and bumps the owner's
  `open_subtask_count`.
- **Relations.** `wrkq relation add <a> blocks <b>` and `wrkq relation ls <a>`.
- **Edit round trip.** `wrkq cat <task> --output raw > f.md`, edit the file, then `wrkq apply <task> f.md`.
- **History.** `wrkq log <ID|path> [--oneline|--patch|--json]`. A path names a task first, then a container.

## How to get to it

Run `eval "$(wv env <name>)"`. `WRKQ_PROJECT_ROOT=wv-<name>`, so relative paths land in the scratch project.
The scratch project starts with no containers, so `wrkq mkdir inbox` comes first.

## Driving it

```bash
export WRKQ_PRINCIPAL_REF=agent:<you>
wrkq mkdir inbox
wrkq touch inbox/wv-alpha -t "WV alpha" -d "body one" --json | jq -c '.[0] | {id,path,state}'
wrkq set inbox/wv-alpha --state in_progress --priority 1 && wrkq cat inbox/wv-alpha --json --one | jq -c '{id,state,priority,etag}'
wrkq set inbox/wv-alpha --if-match 1 --priority 3          # refused: etag precondition failed
wrkq mkdir /wv-<name>/area2 && wrkq touch inbox/wv-beta -t "WV beta" && wrkq mv inbox/wv-beta area2/
wrkq log T-00001 --oneline
wrkq set inbox/wv-alpha --state completed && wrkq find --state open --type t --json | jq -c '[.[].id]'
wrkq rm T-00002 && wrkq restore T-00002
wrkq archive area2 --yes && wrkq cat T-00002 --json --one | jq -r .state   # cancelled
wrkq unarchive area2 && wrkq cat T-00002 --json --one | jq -r .state       # open
wrkq touch T-00002.check --subtask -t "WV named subtask"
wrkq relation add T-00002 blocks T-00001 && wrkq relation ls T-00002
wrkq index update && wrkq search alpha --state all --json | jq -c '[.results[].resource_id]'
```

## Gotchas

- **`wrkq archive` is for containers only.** `wrkq archive T-00002` fails with `container not found: T-00002`.
  Archive a task with `wrkq rm`.
- `touch --json` returns an array (`.[0].id`), `cat --json` returns an array unless you pass `--one`, and
  `mkdir` has no `--json` flag (`unknown flag: --json`, exit 2). It prints JSON when stdout is not a TTY.
- `wrkq set` prints `Processing 1/1...` on stderr and an `errors/failed/succeeded/total` summary when an item
  fails. Look at the exit code, not only the stdout JSON.
- Closing a task prints a hint about reconciling worktrees under `~/praesidium/under-construction/`. On a scratch
  it means nothing.
- `wrkq tree` without a TTY prints JSON lines, and an empty project prints nothing at all.

## Proven when

On the scratch, the created task carries your principal, an etag-guarded set refuses a stale etag, `find` hides
the completed task until `--state all`, `rm`/`restore` moves the task between `archived` and `open`, the
container archive cascades to `cancelled` and `unarchive` reverses exactly that change, a named subtask raises
`open_subtask_count`, and `log <ID>` and `log <path>` show `task.created` and `task.updated` attributed to you.

Driven 2026-10-05 on wv `t-10298` (T-10298) with installed f43c308: `var/wrkq-artifacts/T-10298/02-tasks-containers/drive.txt`.
`drive-attempt1-bad-commands.txt` beside it keeps the first, malformed attempt.
