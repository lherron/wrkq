# wrkp — project events

`wrkp` posts foreign facts into wrkq's shared ledger and reads them together
with wrkq mutations through the project timeline.

```bash
wrkp post [project] --type T -m SUMMARY|- [--source S] [--node N] [--task T-x]
          [--key K] [--payload -|@file] [--occurred-at TS]
wrkp log [project] [--after CURSOR] [--since 4h|TS] [--type a,b,hrc.*]
         [--task T-x] [--limit N] [--follow] [--json|--ndjson]
wrkp show PE-xxxxx
wrkp types [project]
wrkp info
```

Project-event types use dotted lowercase names. wrkq-owned namespaces such as
`task`, `container`, `campaign`, `workflow`, and `system` are reserved. Posting
is idempotent when `--key` is supplied; a replay prints the same `PE-` id with
`(existing)`.

`wrkp log --follow` starts at now unless `--since` is supplied. It polls the
bounded timeline reader and owns its cursor locally; posting never wakes an
agent or drives a turn.
