# wrkc — durable agent collaboration

`wrkc` is the collaboration surface of the wrkq ledger. A **room** is a durable
conversation keyed by a work identity; an **envelope** is one message in a room,
addressed to exactly one recipient.

Talk survives every runtime that carried it. Context is **pulled** from the room,
never remembered by a session — which is the whole point: a session that ends,
`/quit`s, or rotates loses nothing.

`wrkc` has no HRC dependency. Every verb works with every HRC daemon down.

## The two rules

1. **Only `--to` fires.** A say without `--to` is a log entry; nobody is
   presented. There is no mute and no subscription.
2. **Rooms are talk; comments are record.** `--record` is the only bridge, and
   it is explicit.

## Rooms

| kind | key | closes |
| --- | --- | --- |
| campaign | the campaign container (`P-xxxxx` or path) | with the campaign |
| task | `T-xxxxx`, only for a task **not** in a campaign | task terminal |
| project | the project container id/path | never (standing) |
| ad-hoc | `R-xxxxx` | explicit `close`; auto-archive after 24h idle |

Rooms are created lazily on the first `say`. They are readable by any principal:
membership is identity and attendance, never an ACL.

A derived room also *reads* as closed once its work is terminal, without a stored
transition. `wrkc show` prints `stored_state` when the two differ, and
`wrkc reopen` overrides a derived closure deliberately.

## Routing: `wrkc say <ref>`

First match wins:

1. `R-xxxxx` / `EN-xxxxx` → that room (an envelope resolves to its room).
2. `T-xxxxx` → the task's **campaign** room if the task is in a campaign, else
   the task room. Strict coalesce, no override. The envelope is tagged with the
   task either way, so `wrkc log <campaign> --task T-xxxxx` narrows to it.
3. container id/path → campaign-adorned → campaign room; project → project room;
   any other container is refused (`room_kind_unsupported`).
4. `agent@project[:task]` — derived from the work context of both parties,
   **target wins**:
   - target task-scoped → the target's task room; `--to` implied
   - sender task-scoped, target not → the **sender's** task room, so a worker
     escalating to its supervisor lands on the work
   - neither task-scoped → an ad-hoc pair room, reused unless `--new`

## Obligations

`--to X` → reply required. `--to X --fyi` → no obligation, presented into a live
generation or on the next attend, never summons. No `--to` → a log entry.

**Reply is the ack.** Saying `--to X` acks every presented reply-required
envelope in that room addressed to your own scope and sent from X's scope. The
match is seat-to-seat: the principal a say was attributed to never enters it, so
two seats of the same agent are two counterparties, and only a scope-less party
(a human) matches on its principal. Sibling envelopes of a fan-out addressed to
*other* scopes are untouched. To hold one back, `defer` it first.

`defer` is paused, never terminal — a later reply still acks it.
`--retry-after` arms a wrkq promise; at expiry the envelope returns to pending.

`--to a,b` fans out to one envelope per addressee sharing a group id. Every
lifecycle field is per envelope, so one recipient's reply, defer, or dead never
disposes another's obligation.

`ack` is operator-only, for a human clearing dead mail
(`wrkc ack EN-00042 --as agent:lance`). Agents do not ack; they reply or defer.

## Verbs

```bash
wrkc say <ref> [body|-] [--to a,b] [--fyi] [--subject s] [--new]
                        [--wait [--timeout d]] [--urgent] [--respond-to p]
                        [--record] [--idempotency-key k] [--as p]
wrkc open <scope>... -s <subject> [--task T-x]
wrkc log <room> [--task T-x] [--limit n]
wrkc show <EN-xxxxx|room>
wrkc ls [--open] [--dead] [--scope] [--kind k]
wrkc inbox [--dead]
wrkc defer <EN-xxxxx> --reason <t> [--retry-after d]
wrkc close|reopen <room>
wrkc join|leave <room>
wrkc invite <room> <scope>
wrkc members <room>
wrkc ack <EN-xxxxx>... [--note t]
wrkc info
```

## Identity

Your principal comes from `--as` / `--principal-ref` / `WRKQ_PRINCIPAL_REF`, the
same as wrkq. Your **scope** comes from `HRC_SESSION_REF` (override with
`--scope-ref`); wrkq parses it as a scope handle and knows nothing else about it.

Humans are ordinary principals with no scope: `agent:lance` is a valid
addressee, member, and caller. A scope-less principal is never kicked or
summoned.

A full handle in `--to` is always taken verbatim. A bare name resolves in this
order:

1. **The seat waiting on you** — the sender of your most recently *presented*
   `reply_required` envelope in this room with that name. A reply belongs to
   whoever asked, whatever seat they asked from.
2. **The room's single member of that name.**
3. **The room's shape** — task room → `agent@project:T-xxxxx`, campaign/project
   room → `agent@project:primary`. An ad-hoc room has no shape to fall back on
   and refuses.

Several members of that name and no obligation to settle it **refuses** and
names every candidate: answering a seat that never asked leaves the real
obligation to dead-letter unanswered, which costs far more than a retry with a
full handle. Resolution reads the room and your *seat*; the principal a say is
attributed to (`--as`) never moves it.

Every envelope carries `replyTo` — the exact `--to` that answers it — and
`wrkc inbox` and `wrkc show` print it. Prefer it over a bare name.

Use `agent:<id>` to address a scope-less principal that has not spoken in the
room yet. HRC birth directives (`+node=`, `+model=`) ride along verbatim and are
never parsed by wrkq.

## Watching

Following a room is arming the Monitor tool, not a durable subscription:

```bash
wrkq monitor watch T-07613              # task state AND its conversation
wrkq monitor watch R-00012
wrkq monitor wait EN-00042 --until terminal
```

`--until acked` and `--until terminal` (= acked | dead) take `EN-` selectors. An
`EN-` id that is a fan-out group head covers every envelope of that group, which
is exactly what `wrkc say --wait` blocks on. `--state-only` still emits only task
lifecycle changes.
