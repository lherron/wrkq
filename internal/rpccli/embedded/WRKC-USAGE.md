# wrkc agent guide

`wrkc` stores conversations and reply obligations in the shared wrkq ledger. A room holds a conversation; an envelope is one addressed message. Read durable room history when resuming work.

## Read and answer mail

```bash
wrkc inbox
wrkc show EN-00001
wrkc log T-00001 --limit 20
```

Read the envelope body and any unfamiliar room history before answering. A mail hint is only a notification: use `inbox` and `show` to read the actual mail. A fresh runtime must pull its outstanding obligations; earlier messages are not automatically replayed into it.

Reply in the same room, using the envelope's exact `replyTo` as `--to`. Replace the example handle below with that value.

```bash
wrkc say EN-00001 --to cody@wrkq:T-00001 - <<'TEXT'
Answer the request and cite the durable result or remaining blocker.
TEXT
```

A reply discharges every pending or presented reply-required envelope from that sender scope to your scope in this room. Another session of the same agent is a different counterparty. A final assistant response does not send a reply. Agents reply or defer; `wrkc ack` is operator-only.

When answering only selected messages, add `--discharges EN-00001,EN-00002`. Deferred messages stay deferred even when you reply to other mail.

## Defer or finish a conversation

```bash
wrkc defer EN-00001 --reason 'Waiting for installed validation' --retry-after 10m
wrkc say EN-00002 --to cody@wrkq:T-00001 --fyi - <<'TEXT'
Readback accepted. No further action needed.
TEXT
```

Defer records why you are not answering now and survives runtime rotation. With `--retry-after`, the message returns to pending when the retry time arrives. Without a retry time it remains paused.

An ordinary addressed reply creates a new reply obligation. Use `--fyi` when no answer is needed: it can reach an existing seat but does not birth one or create an obligation. It can still discharge the mail you are answering.

## Send new work

```bash
wrkc say T-00001 --to cody@wrkq:T-00001 - <<'TEXT'
State the objective, scope, prerequisites, and required completion evidence.
TEXT
```

Use explicit full handles for new work. Principal identity normally comes from the runtime; `--as agent:<id>` overrides attribution. Caller scope comes from `HRC_SESSION_REF`, or `--scope-ref`; changing attribution does not change your seat. Address a human with a scope-less principal such as `--to agent:lance`.

Always use quoted heredocs for multiline bodies. Write task-related messages against the task ID. Tasks in campaigns share the campaign room; use `wrkc log <campaign> --task T-00001` to narrow history. `R-` selects an ad-hoc room; `EN-` selects its envelope's room.

With room/task references, omitting `--to` records a log entry without addressing anyone. Handle references can imply an addressee; prefer the explicit form above. Addressed requests can create the recipient's session. Completed work remains reachable; room visibility and activity do not close communication.

Room messages are conversation. Use `wrkq comment add` for task evidence, or `say --record` when deliberately recording the message there too.

## Observe delivery and recover

```bash
wrkq monitor wait EN-00001 --until terminal --timeout 30s
wrkc show EN-00001
wrkc inbox --failed
```

A wait timeout is not a delivery failure or a completed request. Read the returned state. Sender-side failures appear in `inbox`; `--failed` also includes failed mail addressed to you. Failed waits exit non-zero. A fan-out group wait covers every recipient.

If a presenting runtime terminates before replying or deferring, its obligation can fail as `runtime_terminated`; resend in the same room when the request is still needed. For `ignored`, escalate rather than repeatedly sending the same request. Delivery timing depends on the active harness; silence during a busy turn does not prove failure.

Use `wrkc <command> --help` for additional options, and `docs/wrkc-reference.md` in the wrkq repository for room kinds, the full routing table, obligation lifecycle, identity resolution, and operator verbs. Task records and session handoffs are covered by `wrkq info`; runtime lifecycle is covered by `hrc info`.
