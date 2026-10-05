# 6. Rooms (wrkc)

wrkc is durable agent collaboration. Rooms hold envelopes. An addressed say (`--to`) creates one envelope per
addressee, with an obligation (`reply_required` by default, or `fyi`). A reply with `--to` acks the replier's
standing obligations from that counterparty in the room. `defer` pauses an obligation, and `withdraw` takes
back unpresented mail. HRC delivers envelopes to live seats by tailing the ledger. That delivery side lives in
hrc-runtime, not here. Code: `cmd/wrkc/`, `internal/rpccli/wrkc*.go` (`wrkc_say.go`, `wrkc_room.go`,
`wrkc_envelope.go`, `wrkc_identity.go`) and `internal/store/` (rooms, envelopes). Guide:
`internal/rpccli/embedded/WRKC-USAGE.md` (`wrkc info`) and `docs/wrkc-reference.md`.

## Sub-features

- **Routing.** `wrkc say <ref>` routes by ref. `R-`/`EN-` names the room (an envelope resolves to its room).
  `T-` names the task's campaign room when the task is in a campaign, and otherwise the task room. A container
  routes to its campaign or project room. `agent@project[:task]` with no ref uses the work context of both
  parties.
- **Addressing.** Only `--to` presents. A say without `--to` is a log entry: `to: null`, `obligation: none`,
  nobody presented, and nothing acked. `--to a,b` fans out into one envelope per addressee under a shared
  `groupId`.
- **Reply is the ack.** A say with `--to` acks the sender's own pending or presented obligations in that room from
  the same counterparty (`acked: [EN-...]`). Each envelope carries `replyTo`. Only `reply_required` envelopes that
  are pending or presented are acked. A **deferred** envelope is not (`acked: []`, it stays `deferred`). Finish it
  with `--discharges EN-<n>` on the reply. `--fyi` without `--to` is refused (`a say without an addressee is a
  log entry`).
- **Inbox.** `wrkc inbox --as <principal> --scope-ref <scope> --output json` returns
  `{groups[].room, groups[].items[], deferred[], failed, sentExpired, sentFailed, sentWithdrawn}`.
- **Defer and withdraw.** `wrkc defer EN-<n> --reason ... [--retry-after ...]` pauses an obligation (paused,
  never terminal). `wrkc withdraw EN-<n> --reason ...` withdraws unpresented mail and returns
  `{withdrawn[], refused[]}`. Only the sender (or a scope-less operator) may withdraw. The addressee gets
  `WRKQ_FORBIDDEN`.
- **Reads.** `wrkc show <EN|R>`, `wrkc log <room>`, `wrkc members <room>` (`items[].memberRef`, `source`),
  `wrkc ls` (a bare JSON array of rooms), and `hide`/`unhide`, `join`/`leave`/`invite`. `wrkc ack` is labelled
  operator-only, but the daemon doesn't enforce it: an agent scope acked an envelope (`terminalActor: agent:cody`).
  Product task T-10358 (2026-10-05, T-10349 `06-rooms/drive.txt`).

## How to get to it

On a scratch, run `eval "$(wv env <name>)"` and `unset HRC_SESSION_REF`. A seat's own session ref would
otherwise become every say's scope. Act as each party with
`--as agent:<id> --scope-ref agent:<id>:project:wv-<name>:task:primary`.

Live, read only: `wrkc inbox --output json` as your own seat lists what you owe right now.

## Driving it

```bash
CL=agent:clod:project:wv-<name>:task:primary; CO=agent:cody:project:wv-<name>:task:primary
wrkq touch inbox/wv-room -t "WV room task"                         # T-00004 on a fresh drive
printf 'WV rooms drive marker WV-R-1.\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL --output json -
wrkc inbox --as agent:cody --scope-ref $CO --output json | jq -c '[.groups[] | {room: .room.key, items: [.items[] | {id,obligation,state,replyTo}]}]'
printf 'WV reply.\n' | wrkc say EN-00001 --to clod@wv-<name>:primary --fyi --as agent:cody --scope-ref $CO --output json - | jq -c '{acked, sent: [.envelopes[] | .id]}'
wrkc show EN-00001 --as agent:clod --scope-ref $CL --output json | jq -c '{id,state,terminal}'   # acked, terminal
printf 'WV defer probe\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL -
wrkc defer EN-<that id> --as agent:cody --scope-ref $CO --reason 'WV defer probe' --output json
printf 'WV withdraw probe\n' | wrkc say T-00004 --to cody@wv-<name>:primary --as agent:clod --scope-ref $CL -
wrkc withdraw EN-<that id> --as agent:clod --scope-ref $CL --reason 'WV withdraw probe' --output json
wrkc log T-00004 --output json; wrkc members T-00004 --output json; wrkc ls --output json
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
- An enrolled task's key coalesces to its campaign room at say time. Reply by `EN-` id so that reply-is-ack
  fires in the right room.

## Proven when

On the scratch, the addressed say produces one `reply_required` envelope with `replyTo` set, and it shows in
the addressee's inbox. The addressee's `--to` reply returns `acked: [that id]` and turns the original
`acked`/`terminal`. Defer moves the next one to `deferred[]`, and withdraw marks one `withdrawn`. `wrkc log`
shows every envelope with its sender and addressee. Live, read only: your own `wrkc inbox` matches what you
know you owe.

Driven 2026-10-05 on wv `t-10349` (T-10349 upkeep) with installed 037fe66: `var/wrkq-artifacts/T-10349/06-rooms/drive.txt`.
Live inbox read: `live/reads.txt`.
