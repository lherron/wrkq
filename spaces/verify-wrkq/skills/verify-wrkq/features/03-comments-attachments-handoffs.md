# 3. Comments, attachments and handoffs

These are the records hung on tasks and seats: comments on a task, attachment files stored by the daemon, and
session handoffs that a seat creates for its successor and the successor acknowledges. Code:
`internal/rpccli/` (`comment.go`, `comment_rm.go`, `attach.go`, `handoff*.go`), `internal/attach/`,
`internal/store/handoffs.go` and `internal/admincli/attachadm.go`.

## Sub-features

- **Comments.** `wrkq comment add <task> -m ...` and `wrkq comment ls <task> --json` give
  `C-<n>` ids attributed to the caller. `comment add --json` is inert: the output mode follows only whether
  stdout is a TTY (on a TTY it prints `Comment created: C-<n>` even with `--json`).
- **Attachments.** `wrkq attach put <task> <file>`, `wrkq attach put <task> - --name x` (stdin),
  `wrkq attach ls <task> --json`, `wrkq attach get ATT-<n>` (to stdout by default) and
  `wrkq attach rm ATT-<n> --yes`. The daemon stores files under its own attachment dir at
  `tasks/<task uuid>/<name>`. A second put of the same filename on a task is refused, a stdin put without
  `--name` is refused, and a missing source path is refused client-side (`cannot read attachment source`).
- **Handoffs.** `wrkq handoff create --scope <agent>@<project> -t ... --body-file -`,
  `wrkq handoff list --scope ... --json` (`{handoffs[], next_cursor, truncated}`), `wrkq handoff get H-<n>`,
  `wrkq handoff search`, and `wrkq handoff acknowledge H-<n> --if-match <etag> --note ...`. Acknowledging is the
  only way a handoff retires. An acknowledged handoff leaves the default pending list and stays readable with
  `get`. `handoff list` filters by `--status pending|acknowledged|all` (there is no `--state`).
- **Successor handoffs.** `wrkq handoff create --to <agent>@<project> --as agent:<you> ...` (d43d2f9) files the
  row in the **successor's** scope (`scope_ref`, `agent_id` = successor) with you as author (`created_by_agent_id`),
  so the successor lists and acknowledges it as itself (`acknowledged_by_agent_id`). A human `list` adds a `From`
  column only on a page holding a handoff someone else wrote, and human `get` prints `From: <author>`. `--to` with
  `--scope` is refused (`validation_error`, exit 2), and `--scope` naming another agent still is
  (`self_scope_violation`, exit 1). Driven 2026-11-02, T-12309.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon's attachment dir is `$WV_STATE/attachments`. Handoff scopes
are `<agent>@wv-<name>`.

## Driving it

```bash
~/.local/bin/wrkq comment add T-00002 -m "WV comment marker WV-C-1" && ~/.local/bin/wrkq comment ls T-00002 --json
echo "wv attachment payload" > "$WV_STATE/note.txt" && ~/.local/bin/wrkq attach put T-00002 "$WV_STATE/note.txt"
echo "piped payload" | ~/.local/bin/wrkq attach put T-00002 - --name piped.txt
~/.local/bin/wrkq attach ls T-00002 --json && ~/.local/bin/wrkq attach get ATT-00001
find "$WV_STATE/attachments" -type f
printf 'Objective: wv drive.\nNext: acknowledge.\n' | ~/.local/bin/wrkq handoff create --scope clod@wv-<name> -t "WV handoff" --body-file - --json
~/.local/bin/wrkq handoff acknowledge H-00001 --as agent:clod:project:wv-<name> --if-match 2 --note "stale" --json   # etag_mismatch, exit 6
~/.local/bin/wrkq handoff acknowledge H-00001 --as agent:clod:project:wv-<name> --if-match 1 --note "Consumed; wv drive" --json
~/.local/bin/wrkq handoff list --scope clod@wv-<name> --json | jq -c '.handoffs'      # []
~/.local/bin/wrkq handoff get H-00001 --json | jq -c '{id,status,etag,acknowledgement_note}'
H=$(printf 'Next: cody acks.\n' | ~/.local/bin/wrkq handoff create --to cody@wv-<name> --as agent:clod -t "WV successor handoff" --body-file - --json | jq -r .handoff.id)
~/.local/bin/wrkq handoff list --scope cody@wv-<name> --as agent:cody --human        # ID Scope From Status ...: From = clod
~/.local/bin/wrkq handoff get "$H" --human | grep -E '^(Agent|From):'                 # Agent: cody / From: clod
~/.local/bin/wrkq handoff create --to cody@wv-<name> --scope cody@wv-<name> --as agent:clod -t x --body y   # mutually exclusive, exit 2
~/.local/bin/wrkq handoff create --scope cody@wv-<name> --as agent:clod -t x --body y                       # self_scope_violation, exit 1
~/.local/bin/wrkq handoff acknowledge "$H" --as agent:cody --if-match 1 --note "Consumed by successor" --json | jq -c '.handoff | {status,acknowledged_by_agent_id,created_by_agent_id}'
```

## Gotchas

- **Over `rpc://` the CLI reads the file and streams it** (source read, since 6245bea: `attach.go`), so
  `attach put <task> <path>` should work from any node against any daemon. A scratch can't tell the two apart,
  because its daemon is local. The client-side `cannot read attachment source` refusal for a missing path is
  consistent with streaming.
- `attach put --json` (or a non-TTY stdout) prints JSON. Both `put` and `attach ls --json` carry the size as
  `size_bytes`. There is no `size` key, so `.size` reads null (2026-10-05, T-10349).
- `attach get --output-file`/`-o <path>` writes to a file (`-`, the default, is stdout). `--as` on `attach get` is
  the global principal alias, like every other verb.
- **A handoff acknowledge resolves the actor from the runtime env when `--as` is absent.** In an HRC seat whose
  project (`HRC_SESSION_REF`/`ASP_PROJECT`) differs from the handoff's, it refuses with
  `ambiguous project: runtime scope resolved "<seat project>" but handoff row project_id is "<handoff project>"`.
  Pass `--as agent:<id>`. The bare form is enough (2026-10-05, T-10349
  `03-comments-attachments-handoffs/drive.txt`). Source read, not driven: an explicit `--as` keeps only the agent
  (`handoff_ack.go`), so a `:project:` suffix on it is never checked.
- Acknowledge exit codes: 6 `etag_mismatch` (stale `--if-match`), 5 `already_acknowledged`,
  4 `handoff_not_found`, 1 for validation.
- **Handoff JSON shapes differ by verb.** `handoff create --json` and `acknowledge --json` wrap the row as
  `{handoff: {...}, idempotent_replay}`. `handoff get --json` returns the row flat. The row's lifecycle field is
  `status` (`pending`/`acknowledged`), not `state`, and the note is `acknowledgement_note`. `handoff list`
  filters with `--status pending|acknowledged|all`; there is no `--state` (2026-10-20, T-11374
  `03-comments-attachments-handoffs/drive.txt`).
- `comment add` keeps its own `--json`, so the every-verb `--json` (feature 2) doesn't reach it. On a TTY,
  `script -q /dev/null wrkq comment add T-2 -m x --json` still prints `Comment created: C-<n>` (2026-10-20, T-11374).

## Proven when

The comment comes back attributed to you, both attachments are listed and `get` returns the bytes you put, the
files exist under the daemon's attachment dir, a stale-etag acknowledge is refused, the right one moves the
handoff to `acknowledged` with your note, and the pending list is empty afterwards. A `--to` handoff lands in the
successor's scope with you as author, shows `From`, and the successor acknowledges it as itself.

Driven 2026-11-02 on wv `t-12309` (T-12309 upkeep) with installed 5fbf10c: `var/wrkq-artifacts/T-12309/03-comments-attachments-handoffs/drive.txt`
(the `--to` drives after the NOTE line; the successor handoff is H-00003 after the replay).
