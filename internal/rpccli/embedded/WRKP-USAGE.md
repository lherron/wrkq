# wrkp — project events

`wrkp` posts foreign facts into wrkq's shared ledger and reads them together
with wrkq mutations through the project timeline.

```bash
wrkp post [project] --type T -m SUMMARY|- --attr key=value [--attr key=value ...]
          [--task T-x] [--key K] [--occurred-at TS]
wrkp git commit
wrkp git push <remote> <url>   # pre-push ref lines on stdin
wrkp just [-- just-args...]    # via the `just` shim; see below
wrkp cursor [project]          # forward cursor at the timeline head
wrkp log [project] [--after CURSOR] [--since 4h|TS] [--type a,b,session.*]
         [--task T-x] [--limit N] [--follow] [--json|--ndjson] [--porcelain]
wrkp show <uuid>
wrkp types [project]
wrkp info
```

## The envelope is wrkq's; the vocabulary is the producer's

wrkq validates shape and bounds only. It never reads an attribute value, keeps
no registry of types, and carries no producer's vocabulary. A producer declares
its types and attribute keys in its own repo and owns their stability.

- `--type`: dotted lowercase. The FIRST segment names the SUBJECT of the fact,
  never the producer: a session manager writes `session.born`, a git hook
  writes `git.commit`, a daemon writes `server.started`. wrkq-owned namespaces
  (`task`, `container`, `campaign`, `workflow`, `promise`, `comment`, `room`,
  `envelope`, `member`, `handoff`, `attachment`, `actor`, `config`, `system`)
  are refused. The subject rule is a convention the server cannot check.
- `-m`: one line, at most 512 characters. What a reader sees on the timeline.
- `--attr key=value`: REQUIRED, repeatable. A flat map of string values: at
  least one key, at most 32, keys `^[a-z][a-z0-9_]*$`, values at most 1024
  characters. Stored and rendered in the order given, so lead with what a
  reader scans for. `source` and `node` are conventional keys, not rules.
- `--task` or `[project]`: exactly one. A task-attached fact threads under the
  task on the timeline; a project fact sits at the top level.
- `--key`: idempotency, scoped per project. A replay prints the same uuid with
  `(existing)` and never overwrites the stored row.
- `--occurred-at`: the producer's clock; server time when omitted.
- Principal and scope are recorded when the runtime supplies them and null
  otherwise. Nothing is minted for a caller that has none.

Identity is a uuid. Every event renders by one rule: type and principal, the
summary, then `key=value` pairs in the producer's order.

`wrkp log --follow` starts at now unless `--since` is supplied. It polls the
bounded timeline reader and owns its cursor locally; posting never wakes an
agent or drives a turn.

The merged timeline also includes ad-hoc room messages when an endpoint was in
this project at send time. Those entries are `message` records with
`membership: participant` and no task path. The affiliation is an immutable
project UUID stamp, so renaming a project or reusing an old slug cannot move
history; messages written before stamps existed remain excluded.

`wrkp git commit` and `wrkp git push` are best-effort Git-hook producers. They
resolve the current checkout through the registered project roots, attribute
facts to the current principal when there is one, and always exit zero so
observability can never block a commit or push. Diagnostics are one-line
`wrkp git:` messages on stderr.

## Reading forward from a cursor

A plain `wrkp log` reads newest-first, and the `next_cursor` that
`--porcelain` writes to stderr pages further back into history. To read what
changed SINCE a point, start from a forward cursor:

```bash
C=$(wrkp cursor foundry)                         # capture first
...list current state...                          # e.g. wrkq find, git log
wrkp log foundry --after "$C" --ndjson --porcelain  # everything after C, then stop
```

A forward `--after` read delivers entries oldest-first up to now, stops, and
with `--porcelain` always writes the next forward cursor. Capturing the cursor
before listing gives a consistent cut: a change made during the listing is
delivered again rather than lost. `--limit N` bounds one read; continue from
the printed cursor.

## Quiet task entries

`task.created`, `task.edited` (a task update that sets no state: title,
priority, labels, description, ...) and `task.moved` are delivered only to a
read whose `--type` filter names them, exactly or by glob (`task.*`). An
unfiltered read never shows them. `task.created` carries the initial state in
`taskState`; `task.edited` and `task.moved` carry only the task identity, and a
mirror re-reads the task. A move appears on the timeline of the container it
left as well as the one it entered. Events written before the affiliation stamp
existed are not placed on any timeline.

## `just` runs: `run.settled`

`just` keeps no history, so its runs reach the timeline as facts. wrkq's
`just install` puts a `just` shim ahead of the real binary on PATH; the shim
hands each invocation to `wrkp just`. A justfile opts in with one line:

```just
# wrkp: run.settled
```

Then each top-level recipe run posts `run.settled` to the project whose
registered root holds the justfile, threaded under the caller's task when the
seat's scope names a task in that project. Attributes, in order: `source`
(`wrkp-just`), `node`, `recipe`, `repo`, `status` (just's exit code, 128+N for
a signal), `duration_ms`, `signal` (when signalled), `argv`, `justfile`,
`started_at`. Listings and other non-run modes (`--list`, `--summary`,
`--dry-run`, ...), recipes that just runs from inside an observed run, and
justfiles without the marker are passed straight to the real `just` with exec.
The exit status is always just's; a failed post is one `wrkp just:` line on
stderr.
