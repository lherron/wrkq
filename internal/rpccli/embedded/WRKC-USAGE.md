# wrkc — durable agent collaboration

`wrkc` is the collaboration surface of the wrkq ledger. A **room** is a durable
conversation keyed by a work identity; an **envelope** is one message in a room,
addressed to exactly one recipient.

Talk survives every runtime that carried it. Context is **pulled** from the room,
never remembered by a session — which is the whole point: a session that ends,
`/quit`s, or rotates loses nothing.

`wrkc` has no HRC dependency. Every verb works with every HRC daemon down.

## The three rules

1. **Only `--to` fires.** A say without `--to` is a log entry; nobody is
   presented. There is no mute and no subscription.
2. **Rooms are talk; comments are record.** `--record` is the only bridge, and
   it is explicit.
3. **A say is never refused for what a room IS.** There is no close and no
   reopen. A room you can resolve always accepts talk, and its obligations
   always gate your turn and wake you.

## Rooms

| kind | key | anchored on |
| --- | --- | --- |
| campaign | the campaign container (`P-xxxxx` or path) | the campaign |
| task | `T-xxxxx`, only for a task **not** in a campaign | the task |
| project | the project container id/path | the project |
| ad-hoc | `R-xxxxx` | nothing |

Rooms are created lazily on the first `say`. They are readable by any principal:
membership is identity and attendance, never an ACL.
An ad-hoc room's identity is its members; a topic is a task — say into it.

### Projections: `work` and `activity`

A room has **no lifecycle state**. `wrkc show` prints two values computed at read
time, and neither can refuse anything:

- **`work`** — `open` or `terminal`, from the task or campaign it is keyed by.
  An ad-hoc room anchors on nothing and is always `open`.
- **`activity`** — first match wins over `last_activity`
  (`max(opened, newest message, newest join)`):
  `stale` if work is terminal and last activity is older than 4h, else `active`
  under 24h, else `quiet`.

Everything reads that one value. `wrkc ls` omits `stale`; `--all` shows every
room. A say into a `stale` room writes and prints a one-line **notice** on
stderr naming the terminal transition and the age — never an error, and there is
no override flag, because the say already happened.

`wrkc hide|unhide <room>` sets a `hidden` label. It is a label, not a state: it
removes the room from the default `wrkc ls` and changes nothing else. Any
principal may set it.

Terminal work stays reachable on purpose. Messaging the seat on a completed task
— a grading follow-up, a late question — is intended, and that seat is summoned
for it exactly as it would be on live work.

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

`--to X` → reply required: births X's seat if unborn, injects, gates X's turn end
until X replies. `--to X --fyi` → no obligation: still INJECTED into X's seat if
one is live (it drives a turn there), but never births an unborn seat and never
gates. No `--to` → a log entry, nobody is presented.

**Reply is the ack.** Saying `--to X` acks every presented reply-required
envelope in that room addressed to your own scope and sent from X's scope. The
match is seat-to-seat: the principal a say was attributed to never enters it, so
two seats of the same agent are two counterparties, and only a scope-less party
(a human) matches on its principal. Sibling envelopes of a fan-out addressed to
*other* scopes are untouched. To hold one back, `defer` it first.
`--discharges EN-a,EN-b` narrows a reply to exactly the presented envelopes in
the broker turn manifest. The whole set is validated before the reply and acks
commit together; omitting it keeps the wide rule above.

An obligation belongs to the runtime that receives it. Its body is pushed once
on the common path; later attention is a pointer to `wrkc show`. It is never
presented to a different runtime. If the presenting runtime ends undisposed,
the envelope fails as `runtime_terminated`. Inside a live runtime the kicker may
send one pointer reminder; ending that reminder turn undisposed fails it as
`ignored`. If you are not answering now, `defer` with a reason (and optionally a
retry time): the deferral survives rotation and returns as a pointer.

`defer` is paused, never terminal — a later reply still acks it.
`--retry-after` arms a wrkq promise; at expiry the envelope returns to pending.

`say --ttl 30s` gives addressed mail a server-normalized expiry. If it has no
presentation receipt by then, the next authoritative read materializes terminal
`expired`; a deferral does not extend the TTL. `say --preempt` asks HRC to
interrupt the addressee's active turn and inject the message now; it is honored
only under operator authority and otherwise queued with a refusal receipt. wrkq
stores it as immutable delivery intent `hold` and never routes or authorizes
preemption itself.

`withdraw EN-xxxxx` lets the sender cancel pending or deferred mail before any
presentation receipt. `--group` attempts every fan-out sibling atomically and
reports siblings already presented. Presentation-first returns the typed
`already_presented` refusal; withdrawal-first prevents any later receipt.

`--to a,b` fans out to one envelope per addressee sharing a group id. Every
lifecycle field is per envelope, so one recipient's reply, defer, or failure never
disposes another's obligation.

A reply returned by `say --wait` is acked as `consumed_by_wait`; nothing is owed
on it. The ack requires the waiter's exact principal and scope, so another
same-principal seat and every other fan-out sibling remain untouched. During the
HRC companion rollout, a reply already queued in a busy seat's broker may still
replay once even though the wrkq ledger now records it as consumed.

`ack` is operator-only, for a human clearing failed mail
(`wrkc ack EN-00042 --as agent:lance`). Agents do not ack; they reply or defer.

Obligations are **uniform**: one addressed to you gates your turn and wakes you
whatever its room's `work`, `activity`, or `hidden` label says. `wrkc inbox`
marks a group whose work has gone terminal — the seat that asked may have moved
on — and answering it is an ordinary say.

## Verbs

```bash
wrkc say <ref> [body|-|-m body] [--to a,b] [--fyi] [--new]
                        [--ttl d] [--preempt] [--discharges EN-a,EN-b]
                        [--wait [--timeout d]] [--respond-to p]
                        [--record] [--idempotency-key k] [--as p]
wrkc log <room> [--task T-x] [--limit n]
wrkc show <EN-xxxxx|room>
wrkc ls [--all] [--failed] [--scope me] [--kind k]
wrkc inbox [--failed]
wrkc defer <EN-xxxxx> --reason <t> [--retry-after d]
wrkc withdraw <EN-xxxxx> [--group] [--reason t]
wrkc hide|unhide <room>
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
obligation to fail unanswered, which costs far more than a retry with a
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

`--until acked` and `--until terminal` (= acked | failed) take `EN-` selectors. An
`EN-` id that is a fan-out group head covers every envelope of that group, which
is exactly what `wrkc say --wait` blocks on. A failed member prints
`failed:<reason>` and makes `--wait` exit non-zero. `--state-only` still emits
only task lifecycle changes.

`wrkc inbox` always includes sender-side failures under `sent, failed`.
`--failed` additionally includes failed obligations addressed to you, with the
failure reason. The ordinary `wrkc ls` human/table view prints the sender-side
failure count before the room table.
