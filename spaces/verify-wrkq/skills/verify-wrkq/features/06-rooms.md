# 6. Rooms (wrkc)

wrkc is durable agent collaboration. Rooms hold envelopes. An addressed say (`--to`) creates one envelope per
addressee, with an obligation (`reply_required` by default, `fyi`, or `note`). A reply with `--to` acks the replier's
standing obligations from that counterparty in the room. `defer` pauses an obligation, and `withdraw` takes
back unpresented mail. HRC delivers envelopes to live seats by tailing the ledger. That delivery side lives in
hrc-runtime, not here. Code: `cmd/wrkc/`, `internal/rpccli/wrkc*.go` (`wrkc_say.go`, `wrkc_room.go`,
`wrkc_envelope.go`, `wrkc_identity.go`) and `internal/store/` (rooms, envelopes). Guide:
`internal/rpccli/embedded/WRKC-USAGE.md` (`wrkc info`) and `docs/wrkc-reference.md`.

## Sub-features

- **Routing.** `wrkc say <ref>` routes by ref. `R-`/`EN-` names the room (an envelope resolves to its room).
  `T-` names the task's campaign room when the task is in a campaign, and otherwise the task room. A container
  routes to its campaign or project room. `agent@project[:task]` with no ref uses the work context of both
  parties. A bare number is a task ref by feature 2's suffix rule: once T-00017 exists, `wrkc say 7` and
  `wrkc log 7` name T-00017's room and `07` names T-00007's (driven 2026-10-26, T-11873).
- **Body source.** The ref is the one required positional. The body is exactly one of `-m <literal|@file>` or a
  positional `-` with the body on stdin (`- <<'EOF'` or a pipe); wrkc reads stdin only after `-` (ab8aed4,
  T-11007). Usage refusals, exit 2: a second positional (`say takes one positional, the ref`), no body source
  (`say needs a body source`), `-m -`, both `-m` and `-`, no ref (also `say - --to ...`: the old no-ref form is
  gone), a ref containing whitespace (`"hello there cody" is not a room ref`, refused before stdin is read,
  T-11005), and `-` when stdin is a TTY or a socket (`body comes from stdin: use a heredoc (<<'EOF') or -m`). An
  empty stdin is refused `stdin is empty` (exit 1). Driven 2026-10-20, T-11374.
- **Addressing.** Only `--to` presents. A say without `--to` is a log entry: `to: null`, `obligation: none`,
  nobody presented, and nothing acked. `--to a,b` fans out into one envelope per addressee under a shared
  `groupId`.
- **Reply is the ack.** A say with `--to` acks the sender's own pending or presented obligations in that room from
  the same counterparty (`acked: [EN-...]`). Each envelope carries `replyTo`. Only `reply_required` envelopes that
  are pending or presented are acked. A **deferred** envelope is not (`acked: []`, it stays `deferred`). Finish it
  with `--discharges EN-<n>` on the reply. `--fyi` without `--to` is refused (`a say without an addressee is a
  log entry`).
- **Notes.** `--note` (with `--to`) records addressed mail that is never presented: the envelope is written
  `obligation: note`, `state: acked`, `terminal: true`, `presentedTo: []`, with `envelope.created` then
  `envelope.acked` (reason `note_recorded`) in the event log. It never wakes, injects or births a seat. A `--note`
  reply is still the ack: it discharges the replier's pending `reply_required` from that counterparty
  (`acked: [EN-...]`). `--note` without `--to`, or with `--fyi`, `--wait`, `--ttl` or `--preempt`, is a usage
  refusal (exit 2), and the server refuses the same combinations `WRKQ_VALIDATION` with `data.field` (95b3340,
  T-11305; driven 2026-10-20, T-11374).
- **wrkc notes.** `wrkc notes [--since 36h|7d|<timestamp>] --json` returns `{principalRef, scopeRef, items[]}`,
  newest first, over the last 24h by default. There is no read state. A bad `--since` is refused
  `invalid notesSince` (`WRKQ_VALIDATION`). The human form says `no notes in <window>` when empty.
- **Inbox.** `wrkc inbox --as <principal> --scope-ref <scope> --output json` returns
  `{groups[].room, groups[].items[], deferred[], failed, sentExpired, sentFailed, sentWithdrawn, recentNotes}`.
  `recentNotes` counts the notes addressed to you in the last 24h. Notes never appear in `groups`. The human
  inbox ends with `1 note in the last 24h: wrkc notes` when the count is non-zero.
- **Defer and withdraw.** `wrkc defer EN-<n> --reason ... [--retry-after ...]` pauses an obligation (paused,
  never terminal). `wrkc withdraw EN-<n> --reason ...` withdraws unpresented mail and returns
  `{withdrawn[], refused[]}`. Only the sender (or a scope-less operator) may withdraw. The addressee gets
  `WRKQ_FORBIDDEN`.
- **Reads.** `wrkc show <EN|R>`, `wrkc log <room>`, `wrkc members <room>` (`items[].memberRef`, `source`),
  `wrkc ls` (a bare JSON array of rooms), and `hide`/`unhide`, `join`/`leave`/`invite`. `wrkc ack` is labelled
  operator-only, but the daemon doesn't enforce it: an agent scope acked an envelope (`terminalActor: agent:cody`).
  Product task T-10358 (2026-10-05, T-10349 `06-rooms/drive.txt`; still reproduces at 7350485, 2026-10-26).
  `wrkc ack --output json` returns an array.

## How to get to it

On a scratch, run `eval "$(wv env <name>)"` and `unset HRC_SESSION_REF`. A seat's own session ref would
otherwise become every say's scope. Act as each party with
`--as agent:<id> --scope-ref agent:<id>:project:wv-<name>:task:primary`.

Live, read only: `wrkc inbox --output json` as your own seat lists what you owe right now.

## Driving it

```bash
CL=agent:clod:project:wv-<name>:task:primary; CO=agent:cody:project:wv-<name>:task:primary
wrkq touch inbox/wv-room -t "WV room task"     # T-00004 after the baseline blocks alone; use the id it prints
printf 'WV rooms drive marker WV-R-1.\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL --output json -
wrkc inbox --as agent:cody --scope-ref $CO --output json | jq -c '[.groups[] | {room: .room.key, items: [.items[] | {id,obligation,state,replyTo}]}]'
printf 'WV reply.\n' | wrkc say EN-00001 --to clod@wv-<name>:primary --fyi --as agent:cody --scope-ref $CO --output json - | jq -c '{acked, sent: [.envelopes[] | .id]}'
wrkc show EN-00001 --as agent:clod --scope-ref $CL --output json | jq -c '{id,state,terminal}'   # acked, terminal
printf 'WV defer probe\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL -
wrkc defer EN-<that id> --as agent:cody --scope-ref $CO --reason 'WV defer probe' --output json
printf 'WV withdraw probe\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL -
wrkc withdraw EN-<that id> --as agent:clod --scope-ref $CL --reason 'WV withdraw probe' --output json
wrkc log T-00004 --output json; wrkc members T-00004 --output json; wrkc ls --output json
# notes
wrkc say T-00004 --to cody@wv-<name>:primary --note -m 'WV note' --as agent:clod --scope-ref $CL --output json   # acked, terminal
wrkc inbox --as agent:cody --scope-ref $CO --output json | jq -c '{items: [.groups[].items[].id], recentNotes}'  # recentNotes 1
wrkc notes --as agent:cody --scope-ref $CO --json | jq -c '[.items[] | {id,obligation,state}]'
wrkc say T-00004 --to cody@wv-<name>:primary -m 'needs a reply' --as agent:clod --scope-ref $CL --output json
wrkc say EN-<that id> --to clod@wv-<name>:primary --note -m 'quiet answer' --as agent:cody --scope-ref $CO --output json | jq -c .acked
wrkc say T-00004 --note -m x --as agent:clod --scope-ref $CL; echo $?                                          # 2
```

**Never send test mail on the canonical daemon or to a real seat.**

## Gotchas

- **HRC delivery can't be driven on a scratch.** HRC's kicker tails the canonical ledger, so a scratch envelope
  is never presented, steered or woken: `presentedTo` stays `[]` and `delivery: queue`. On a scratch you prove
  the ledger (obligations, acks, defer, withdraw, routing). Delivery is hrc-runtime's to verify. A scratch
  reply with `--to` still acks a pending envelope that was never presented.
- **A reply without `--to` doesn't ack.** `wrkc say EN-00001 -` as the addressee wrote an unaddressed log entry
  (`to: null`, `obligation: none`) and left EN-00001 `pending`. The same reply with
  `--to clod@...:primary` acked it (2026-10-05, T-10298 `06-rooms/drive.txt`). Reply with the envelope's exact
  `replyTo` as `--to`.
- Inbox groups carry `.items`, not `.envelopes`. Say results carry `.envelopes` and `.acked`. Members carry
  `.items[].memberRef`.
- The default `reply_required` makes the addressee owe a reply. Use `--fyi` for probes you don't want answered.
- **A bare-number ref moves as tasks are created.** `wrkc say 7` wrote EN-00009 into T-00017's new room once
  T-00017 existed, although the conversation was in T-00007's room. Reply by `EN-` id or a full `T-<n>`
  (2026-10-26, T-11873 `06-rooms/drive.txt`).
- An enrolled task's key coalesces to its campaign room at say time. Reply by `EN-` id so that reply-is-ack
  fires in the right room.

## Proven when

On the scratch, the addressed say produces one `reply_required` envelope with `replyTo` set, and it shows in
the addressee's inbox. The addressee's `--to` reply returns `acked: [that id]` and turns the original
`acked`/`terminal`. Defer moves the next one to `deferred[]`, and withdraw marks one `withdrawn`. A `--note` is
written acked and terminal with `note_recorded`, shows only in the addressee's `recentNotes` and `wrkc notes`, and a
`--note` reply acks the envelope it answers. `wrkc log`
shows every envelope with its sender and addressee. Live, read only: your own `wrkc inbox` matches what you
know you owe.

Driven 2026-10-26 on wv `t-11873` (T-11873 upkeep) with installed 7350485: `var/wrkq-artifacts/T-11873/06-rooms/drive.txt`
(the T-11374 replay with its notes, body-source and T-10358 drives, then the bare-ref drives). T-10358 still
reproduces at 7350485. Live inbox read: `live/reads.txt`.
