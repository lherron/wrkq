# wrkp — project events

`wrkp` posts foreign facts into wrkq's shared ledger and reads them together
with wrkq mutations through the project timeline.

```bash
wrkp post [project] --type T -m SUMMARY|- --attr key=value [--attr key=value ...]
          [--task T-x] [--key K] [--occurred-at TS]
wrkp git commit
wrkp git push <remote> <url>   # pre-push ref lines on stdin
wrkp log [project] [--after CURSOR] [--since 4h|TS] [--type a,b,session.*]
         [--task T-x] [--limit N] [--follow] [--json|--ndjson]
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

`wrkp git commit` and `wrkp git push` are best-effort Git-hook producers. They
resolve the current checkout through the registered project roots, attribute
facts to the current principal when there is one, and always exit zero so
observability can never block a commit or push. Diagnostics are one-line
`wrkp git:` messages on stderr.
