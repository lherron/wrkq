# 3. Comments, attachments and handoffs

These are the records hung on tasks and seats: comments on a task, attachment files stored by the daemon, and
session handoffs that a seat creates for its successor and the successor acknowledges. Code:
`internal/rpccli/` (`comment.go`, `comment_rm.go`, `attach.go`, `handoff*.go`), `internal/attach/`,
`internal/store/handoffs.go` and `internal/admincli/attachadm.go`.

## Sub-features

- **Comments.** `wrkq comment add <task> -m ...` and `wrkq comment ls <task> --json` give
  `C-<n>` ids attributed to the caller.
- **Attachments.** `wrkq attach put <task> <file>`, `wrkq attach put <task> - --name x` (stdin),
  `wrkq attach ls <task> --json` and `wrkq attach get ATT-<n>` (to stdout by default). The daemon stores
  files under its own attachment dir at `tasks/<task uuid>/<name>`.
- **Handoffs.** `wrkq handoff create --scope <agent>@<project> -t ... --body-file -`,
  `wrkq handoff list --scope ... --json` (`{handoffs[], next_cursor, truncated}`), `wrkq handoff get H-<n>`,
  `wrkq handoff search`, and `wrkq handoff acknowledge H-<n> --if-match <etag> --note ...`. Acknowledging is the
  only way a handoff retires. An acknowledged handoff leaves the default pending list and stays readable with
  `get`.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch daemon's attachment dir is `$WV_STATE/attachments`. Handoff scopes
are `<agent>@wv-<name>`.

## Driving it

```bash
wrkq comment add T-00002 -m "WV comment marker WV-C-1" && wrkq comment ls T-00002 --json
echo "wv attachment payload" > "$WV_STATE/note.txt" && wrkq attach put T-00002 "$WV_STATE/note.txt"
echo "piped payload" | wrkq attach put T-00002 - --name piped.txt
wrkq attach ls T-00002 --json && wrkq attach get ATT-00001
find "$WV_STATE/attachments" -type f
printf 'Objective: wv drive.\nNext: acknowledge.\n' | wrkq handoff create --scope clod@wv-<name> -t "WV handoff" --body-file - --json
wrkq handoff acknowledge H-00001 --as agent:clod:project:wv-<name> --if-match 2 --note "stale" --json   # etag_mismatch, exit 6
wrkq handoff acknowledge H-00001 --as agent:clod:project:wv-<name> --if-match 1 --note "Consumed; wv drive" --json
wrkq handoff list --scope clod@wv-<name> --json | jq -c '.handoffs'      # []
```

## Gotchas

- **The attachment path is server-local.** `attach put <task> <path>` makes the **daemon** stat the path. On a
  scratch the daemon is local, so it works. Against the canonical daemon from any node other than mini, pipe the
  file: `put <task> - --name x`.
- `attach put --json` (or a non-TTY stdout) prints JSON. `attach ls --json` reports `size` as null. The size is in `put`'s `size_bytes`.
- `attach get --output-file`/`-o <path>` writes to a file (`-`, the default, is stdout). `--as` on `attach get` is
  the global principal alias, like every other verb.
- **A handoff acknowledge resolves the actor from the runtime env first.** In an HRC seat whose project
  (`HRC_SESSION_REF`/`ASP_PROJECT`) differs from the handoff's, it refuses with
  `ambiguous project: runtime scope resolved "<seat project>" but handoff row project_id is "<handoff project>"`.
  Pass the full ScopeRef: `--as agent:<id>:project:<project>` (2026-10-05, T-10298
  `03-comments-attachments-handoffs/drive.txt`).
- A stale `--if-match` on acknowledge fails `etag_mismatch` with exit 6, not 1.

## Proven when

The comment comes back attributed to you, both attachments are listed and `get` returns the bytes you put, the
files exist under the daemon's attachment dir, a stale-etag acknowledge is refused, the right one moves the
handoff to `acknowledged` with your note, and the pending list is empty afterwards.

Driven 2026-10-05 on wv `t-10298` (T-10298): `var/wrkq-artifacts/T-10298/03-comments-attachments-handoffs/drive.txt`.
