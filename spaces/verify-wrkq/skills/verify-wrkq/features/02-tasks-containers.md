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
- **Bare numbers.** A bare 1-4 digit number names the numerically **highest task whose id ends in exactly those
  digits**, and leading zeros count (7350485, T-11807). On a store holding T-00002 and T-00012, `2` and `12` select
  T-00012, while `02`, `002` and `0002` select T-00002. On canonical, `498` is T-11498 and `0498` is T-10498. Five
  digits is the literal id. With no match the verbs report the raw token, `task not found: 4242`. `cat`, `set`,
  `stat`, `comment ls`, `diff`, `monitor wait`, `wrkp log --task`, `wrkf` task selectors, `wrkc` room refs
  (feature 6) and a raw `wrkq.task.show` all follow this rule. `wrkq log` doesn't (see Gotchas). Driven 2026-10-26,
  T-11873.
- **Update.** `wrkq set <sel> --state ... --priority ... [--if-match <etag>]`. A stale etag fails with
  `task etag precondition failed` (exit 1).
- **List.** `wrkq ls <container> --type t`, `wrkq tree` (JSON lines when stdout is not a TTY), and
  `wrkq find --state open|all --type t [--campaign P]`. Without `--state`, completed, archived and deleted
  tasks are hidden, and so are `cancelled` (driven 2026-10-20, T-11374) and `idea` (source read, `find.go`).
- **Search.** `wrkq search '<words>' --state all --json` returns
  `{query, stale, status, results[].resource_id}`. Dense search uses the local embedding model, so a word can
  match a neighbouring task.
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
  `mkdir --json` returns an array of `{path, created}`.
- `wrkq set` prints `Processing 1/1...` on stderr and an `errors/failed/succeeded/total` summary when an item
  fails. Look at the exit code, not only the stdout JSON.
- Closing a task prints a hint about reconciling worktrees under `~/praesidium/under-construction/`. On a scratch
  it means nothing.
- `wrkq tree` without a TTY prints JSON lines, and an empty project prints nothing at all.
- **A one-character slug fails with a raw store error.** `wrkq touch inbox/x -t x` returns `WRKQ_ERROR`
  `CHECK constraint failed: slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' ...`, because that GLOB needs
  two characters. Use slugs of two or more characters. Product task T-10355 (2026-10-05, T-10349
  `02-tasks-containers/drive.txt`; still reproduces at 7350485, 2026-10-26, T-11873).

- **A bare number names the newest task with that suffix, not the T-1xxxx one.** On canonical (2026-10-26),
  `wrkq cat 7` is T-11867, not T-10007, and `07` is T-11807. An agent's "7 means T-10007" shorthand has to pass
  `T-10007` to the CLI (2026-10-26, T-11873 `live/reads.txt`).
- **`wrkq log` doesn't expand bare numbers.** `wrkq log 2` fails `failed to resolve resource: resource not found: 2`
  (exit 1), while `wrkq cat 2` works. Pass `T-<n>` or a path. SPEC.md also promises a padded `task not found: T-04242`
  that no verb prints. Product task T-11874 (2026-10-26, T-11873 `02-tasks-containers/drive.txt`).

## Proven when

On the scratch, the created task carries your principal, an etag-guarded set refuses a stale etag, `find` hides
the completed task until `--state all`, `rm`/`restore` moves the task between `archived` and `open`, the
container archive cascades to `cancelled` and `unarchive` reverses exactly that change, a named subtask raises
`open_subtask_count`, `log <ID>` and `log <path>` show `task.created` and `task.updated` attributed to you, and a
bare `2` selects T-00012 once it exists, while `02` selects T-00002.

Driven 2026-10-26 on wv `t-11873` (T-11873 upkeep) with installed 7350485: `var/wrkq-artifacts/T-11873/02-tasks-containers/drive.txt`
(the T-11374 replay, then the bare-number drives after the NOTE line). T-10355 still reproduces at 7350485.
