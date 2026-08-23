# wrkq Promises: Design Handoff

Status: design handoff with decisions closed 2026-08-23; ready to shape into an implementation specification

Discussion date: 2026-08-23

Tracking task: `T-07485`

## Purpose

This document captures the full context and provisional design for a wrkq
promise mechanism. It is intended to let a new session resume the work without
needing the conversation that produced it.

The central problem is not task scheduling. It is loss of intentional,
long-running threads from a principal's active memory. wrkq can represent work
and group it into campaigns, but it cannot currently let a human or agent say:

> I am allowed to forget this for now, but wrkq must put it back in front of me
> at a specified time and require me to decide what happens next.

The proposed product noun and CLI surface are **promises**. The explanatory
mental model is an **attention lease**.

## Canonical motivating example: HRC envelopes

HRC recently gained a durable embedded-envelope communication mechanism,
exposed through `hrcmail`. The current HRC contracts model durable envelopes
with states including `pending`, `presented`, `acked`, `deferred`, and `dead`.
They distinguish request and conversational payloads, target a session, and can
carry a reply schema. The mailbox is deliberately separate from existing
`hrcchat` history today.

The larger intent was to move agent communications onto the envelope system,
including migrating `hrcchat`. The substrate landed, but the broader rollout
stagnated. Roughly a week or two later Lance realized that the thread had
fallen out of active memory.

This is the canonical failure that promises should prevent.

It was not naturally represented by a task due date:

- There was no honest assertion that the rollout must be completed on a
  particular date.
- The next required action was to review progress and decide what to do, not to
  declare the whole migration late.
- The concern spans multiple possible tasks and potentially multiple projects.
- A campaign could organize the rollout, but merely existing in a campaign
  portfolio does not guarantee that it will re-enter anyone's attention.
- The failure was not missing storage. It was missing resurfacing and a review
  decision loop.

A representative promise would be:

```text
Owner: Lance
Subject: HRC envelope rollout
Review at: seven days from creation
Question: Has hrcchat migration progressed? If not, what is the next rollout boundary?
```

At review time the correct output is not a red overdue badge. The promise
should become ready and require a disposition: renew it, turn the concern into
concrete work, resolve it, or deliberately abandon it.

## Why existing wrkq concepts are insufficient

### Task due dates

Tasks already have `start_at` and `due_at`. `due_at` is indexed with task state
and is described as a planning and sorting timestamp. It carries deadline-like
semantics: this work should be done by this point.

A promise instead says: this subject should return to my attention at this
point. Missing the time does not make the underlying work late. It means the
review is waiting.

### Campaigns and the Epics view

wrkq campaigns are the canonical execution-grouping substrate behind the
Taskboard Epics product view. A campaign is a container adornment with lifecycle
`draft -> active -> completed|cancelled`; tasks can be resident in or explicitly
enrolled into a campaign.

Campaigns were deliberately designed without canonical owner, schedule,
priority, or rank fields. Their purpose is to describe and aggregate a body of
work, not to hold personal attention state. Adding `review_at` directly to a
campaign would mix those concerns and would not support two principals wanting
different review cadences for the same campaign.

The distinction is:

| Concept | Question answered |
| --- | --- |
| Task | What executable work exists? |
| `due_at` | By when should this task be finished? |
| Campaign | What body of work belongs together? |
| Promise | When must this subject re-enter this principal's attention? |

Promises should often point at campaigns, but they should not be campaign
fields or campaign children.

## Decisions reached in the design discussion

The following points had agreement:

1. The product and CLI noun is **promise**.
2. An attention lease is the conceptual explanation, not the command name.
3. A promise is a commitment to revisit a subject, not a commitment to finish
   the subject by a deadline.
4. Promises are first-class standalone entities with their own identity and
   lifecycle.
5. A promise may optionally reference a task, campaign, or other container.
6. Unattached promises are allowed so capture does not require first creating a
   task or campaign.
7. Promises belong to principals, not specifically to humans. Humans, agents,
   and future principal types should be able to own them.
8. The executing principal and owning principal are distinct. An agent can
   record a promise on a human's behalf.
9. A promise created by an agent at the owner's explicit request is immediately
   accepted.
10. An unsolicited promise assignment is a different concept and is not needed
    in the first version.
11. `review_at` is the right timestamp name. `due_at` and `expires_at` carry the
    wrong meanings.
12. Ready state should be derived from time rather than materialized by a
    scheduler.
13. Merely storing promises is insufficient. wrkq needs a prominent ready
    queue and a required review disposition.

## Terminology

### Promise

A principal-owned commitment to revisit a subject at or after `review_at` and
make a review decision.

### Attention lease

The mental model behind a promise. Until `review_at`, wrkq holds the subject out
of active attention. At `review_at`, that lease ends and the subject returns to
the owner's ready queue.

This should remain explanatory language. “Lease” is poor CLI language because
it suggests locks, resource ownership, or loss of access, and Praesidium already
uses claims and leases in runtime coordination.

### Subject

The concern to be revisited. It always has a durable text snapshot and may also
reference one task or one container. Campaigns are containers, so a container
reference covers projects, directories, features, areas, and campaigns.

### Owner

The principal whose future attention is being committed. The owner determines
which ready queue receives the promise.

### Creator

The principal that executed the write. The creator may differ from the owner
when an agent records a promise on the owner's behalf.

### Ready

A derived condition, not a durable lifecycle state:

```text
state = open AND review_at <= now
```

### Sleeping

Another derived presentation condition:

```text
state = open AND review_at > now
```

## Entity and relationship model

Promises are standalone in storage and lifecycle but attached by reference in
use:

```text
human or agent principal
          |
          | owns
          v
       promise -------- watches --------> task
          |
          +-----------------------------> container/campaign
          |
          +-----------------------------> standalone text subject
```

Consequences:

- A task or campaign can exist without a promise.
- A promise can exist before formal work has been created.
- Attaching a promise later does not replace its identity or history.
- Completing a task or campaign does not silently resolve its promises.
- Purging a referenced resource must not erase the promise's text or history.
- Different principals may hold independent promises against the same subject.
- Multiple promises by one principal against the same subject are not forbidden
  initially; actual usage should determine whether that needs a constraint.

## Proposed lifecycle

Only terminal lifecycle state should be stored:

```text
                    renew
             +------------------+
             |                  |
             v                  |
           open ----------------+
             |
             +---- resolve ----> resolved
             |
             +---- abandon ----> abandoned
```

`ready` and `sleeping` are views of `open`, based on `review_at`.

### Review dispositions

When a promise is ready, the owner should make one of these decisions:

- **Renew**: the review was performed, the subject still matters, and a new
  `review_at` is chosen.
- **Turn into work**: create or identify a task/campaign, attach it, and then
  either renew or resolve the promise. “Activate” arose as conversational
  shorthand, but should not become a stored promise state without further
  design.
- **Resolve**: the subject no longer requires future attention because its
  concern is satisfied or superseded.
- **Abandon**: deliberately stop carrying the concern without claiming it was
  satisfied.

Ignoring a ready promise leaves it ready. It does not become a distinct
`overdue` state. Interfaces may show “ready for 4 days” as neutral evidence of
neglected attention.

## Proposed SQLite layout

The initial implementation should add one domain table and reuse the existing
append-only event log. A separate `promise_reviews` table is not justified until
a query requires it.

```sql
CREATE TABLE promise_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);

CREATE TABLE promises (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  id TEXT UNIQUE,

  owner_principal_ref TEXT NOT NULL,

  subject TEXT NOT NULL CHECK (length(trim(subject)) > 0),
  review_question TEXT,

  subject_task_uuid TEXT
    REFERENCES tasks(uuid) ON DELETE SET NULL,
  subject_container_uuid TEXT
    REFERENCES containers(uuid) ON DELETE SET NULL,

  review_at TEXT NOT NULL
    CHECK (review_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z'),

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'resolved', 'abandoned')),
  closed_at TEXT,

  last_reviewed_at TEXT,
  last_review_note TEXT,

  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),

  created_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  updated_by_principal_ref TEXT NOT NULL,
  updated_by_scope_ref TEXT,

  CHECK (
    subject_task_uuid IS NULL
    OR subject_container_uuid IS NULL
  ),

  CHECK (
    (state = 'open' AND closed_at IS NULL)
    OR
    (state IN ('resolved', 'abandoned') AND closed_at IS NOT NULL)
  )
);

CREATE INDEX promises_owner_ready_idx
  ON promises(owner_principal_ref, review_at)
  WHERE state = 'open';

CREATE INDEX promises_task_idx
  ON promises(subject_task_uuid)
  WHERE subject_task_uuid IS NOT NULL;

CREATE INDEX promises_container_idx
  ON promises(subject_container_uuid)
  WHERE subject_container_uuid IS NOT NULL;
```

A friendly-ID trigger should assign `PR-00001`, `PR-00002`, and so on using
`promise_seq`.

### Why text plus typed references

The `subject` text is always required. It provides:

- a meaningful standalone promise;
- a readable snapshot if the target is later purged;
- stable history even if the target is renamed;
- concise ready-queue presentation without another lookup.

Typed nullable foreign keys are preferable to `subject_type + subject_uuid` for
a live domain entity because SQLite can enforce them. At most one target may be
populated. Both may be null for standalone promises.

### Why there is no project column yet

The primary home of a promise is a principal's attention queue, not a project
hierarchy. Project context can be derived from an attached task or container.
Standalone promises are global to the owner in this proposal.

An optional context/project reference may become useful for filtering unattached
promises, but actual use should prove that need before it becomes canonical.

### Temporal normalization (binding)

The ready predicate is a lexical text comparison, so it is only correct if
every stored `review_at` and the comparison instant share one canonical form.
This is NOT how `due_at`/`start_at` work today: `rpccli/touch.go` and
`wrkqapi/tasks.go` forward those strings unchanged and `domain.ValidateTimestamp`
is never called on that path. Promises must not inherit that behavior.

Contract:

- The API layer (`wrkqapi`), not the CLI, is the normalization authority.
  `promise.add`, `promise.renew`, and `promise.edit` accept `reviewAt` as any
  RFC3339 string with any offset, or `reviewIn` as a duration; the API parses
  via `domain.ValidateTimestamp` (offset-aware), converts to UTC, and stores
  exactly `YYYY-MM-DDTHH:MM:SSZ`. Invalid input fails with
  `WRKQ_VALIDATION` before any write.
- The SQL `CHECK` on `review_at` above rejects any non-canonical form at the
  storage boundary, so a bypassing writer cannot corrupt ordering.
- The ready predicate's `now` is produced by the same server-side formatter
  (`strftime('%Y-%m-%dT%H:%M:%SZ','now')` or the Go equivalent), never a
  client-supplied string.
- Relative `--in` is resolved server-side against server `now`, so CLI clock
  skew cannot shift `review_at`.
- Acceptance test: `--review-at 2026-08-24T00:30:00+01:00` stores
  `2026-08-23T23:30:00Z` and is ready at `23:30Z`, not at `00:30Z`.

### Why readiness is not stored

The ready query is:

```sql
SELECT *
  FROM promises
 WHERE owner_principal_ref = ?
   AND state = 'open'
   AND review_at <= ?
 ORDER BY review_at, id;
```

No daemon or cron job must transition `sleeping -> ready`. Querying with the
current time is sufficient. Notification delivery, if added later, is a
consumer of this state rather than its authority.

### No uniqueness constraint yet

Do not initially enforce one open promise per owner and target. Separate review
questions or cadences may justify multiple promises. The CLI can detect and warn
about likely duplicates while practice determines the durable rule.

## Event and audit model

The existing `event_log` should remain the append-only audit and review history.
Add `promise` to its `resource_type` constraint and emit at least:

```text
promise.created
promise.updated
promise.renewed
promise.resolved
promise.abandoned
promise.retargeted
```

A renewal event should record the review itself, for example:

```json
{
  "owner_principal_ref": "agent:lance",
  "previous_review_at": "2026-08-30T15:00:00Z",
  "next_review_at": "2026-09-06T15:00:00Z",
  "note": "Envelope transport landed; hrcchat conversion is still pending."
}
```

The event-log index `(resource_type, resource_uuid, id DESC)` already supports a
promise timeline and remains the full review history. The row additionally
carries `last_reviewed_at` and `last_review_note` (set by renew, resolve, and
abandon) so `show` and the ready queue can print "last time you said: ..."
without walking the event log. Do not add `review_count` or further
denormalization unless a measured query justifies it.

Event-log payloads follow the task-event shape: a changed-fields map, with
`promise.renewed`, `promise.resolved`, and `promise.abandoned` additionally
carrying `previous_review_at`, `next_review_at` (renew only), and `note`.

### Webhook producer contract (binding)

Webhook bodies are typed projections built separately from event-log rows;
the existing code has no promise-compatible routing, family, or payload, so
all three are specified here:

- **Family**: a new `promise` event family. `subscriptionMatchesEvent` gains
  `case "promise", "promise.*"`, and `isTaskWebhookEvent` is changed to
  exclude `promise.*` so subscriptions narrowed to `task` never receive
  promise events. Subscriptions with no event filter, or `*`, receive them.
- **Routing**: subscriptions are container-scoped (ancestor-chain walk from a
  container UUID). An attached promise routes through its subject's container
  chain — the task's `project_uuid`/parent container, or the container itself.
  A standalone promise has no container and therefore no subscription can be
  in scope for it: it emits event-log rows only and no webhook. This is a
  consequence of the subscription model, not an exclusion; it narrows the
  earlier "flows through existing subscriptions" ruling to attached promises.
  Attaching a standalone promise emits `promise.retargeted` into the new
  subject's chain.
- **Payload**: a `promise` typed projection — `event`, `promise` (id, uuid,
  owner_principal_ref, subject, review_question, review_at, state,
  last_reviewed_at, last_review_note, etag), `subject_ref` (`task`/`container`
  id+path or null), `changes` (delta), `actor` (creator/updater principal),
  `occurred_at`. It does not reuse the task payload struct and does not
  require ticket/project fields.

Purging an attached task or container sets the typed reference to null via the
foreign key, but the purge path must also emit `promise.retargeted` carrying the
lost reference (uuid, id, slug). A silent `SET NULL` would leave the history
lying about when and why the link disappeared.

The `event_log.resource_type` SQL `CHECK` currently excludes promises, so a
migration will have to rebuild or otherwise widen that table consistently.

## Principal ownership and on-behalf creation

Promises should be principal-generic at the domain boundary. Do not add a
promise-specific distinction between humans and agents.

Agents benefit directly because promises preserve intent across ephemeral
sessions. Examples include:

- rechecking a rollout after a settling period;
- revisiting a compatibility path after a dependency lands;
- verifying that temporary migration scaffolding was removed;
- returning to an architectural decision after gathering evidence.

### Canonical on-behalf flow

Lance says:

> Cody, create a promise for me to check on the envelope rollout in seven days.

Cody executes the command. The resulting accepted row should be conceptually:

```text
owner_principal_ref      = agent:lance
created_by_principal_ref = agent:cody
created_by_scope_ref     = agent:cody:project:wrkq:task:...
state                    = open
review_at                = now + 7 days
```

The promise belongs to Lance and appears in Lance's ready queue. Audit surfaces
may say “recorded by Cody.” It is not a proposal awaiting acceptance because the
owner explicitly authorized its creation.

### Mutation authority (binding)

Ownership is an authority boundary, not only attribution. For every mutation
after creation — `edit`, `attach`, `detach`, `renew`, `resolve`, `abandon`,
and `rm`/`rm --purge` — the API checks `caller principal == owner_principal_ref`
before the store is touched and fails with `WRKQ_FORBIDDEN` otherwise. The
creator of an on-behalf promise holds no standing authority over it after
creation. Reads (`cat`, `log`, `list`, `ready --for`, `tree`) are not
restricted. There is no delegated-mutation grant in the MVP; if an owner wants
an agent to renew on their behalf, the agent must act as the owner's principal.
Existing records `wrkq.attribution.caller-principal-exact` and
`wrkq.mutation.caller-owned-confirmation` prove attribution and explicit
intent; this rule adds the owner-permission check they do not provide.

### Assignment boundary

The MVP should support owner-authorized creation, not arbitrary assignment.

| Situation | Expected result |
| --- | --- |
| Lance records a promise for Lance | Accepted |
| Cody records Lance's promise at Lance's request | Accepted |
| Cody records Cody's own promise | Accepted |
| Cody unsolicitedly assigns Lance a promise | Rejected initially |
| Cody (creator, not owner) renews/resolves/purges Lance's promise | Rejected (`WRKQ_FORBIDDEN`) |

If cross-principal delegation is later needed, the honest concept is a
**promise request**: another principal proposes a future attention commitment,
and the owner accepts or declines it. That would add states such as `proposed`,
`accepted`, and `declined`, plus inbox and anti-spam rules. It is deliberately
outside the MVP.

### Current principal-model caveat

The current wrkq attribution implementation is only partially principal-generic:

- The legacy `actors` table supports roles `human`, `agent`, and `system`.
- Canonical resource attribution moved away from actor foreign keys to external
  `principal_ref` plus optional `scope_ref`.
- The current validator and SQL checks accept only normalized `agent:<id>`
  principal references.
- Mini's live data currently represents Lance as `agent:lance`; even the legacy
  local-human identity appears as `agent:local-human` in canonical attribution.

Promises should use `owner_principal_ref`, not regress to `owner_actor_uuid`.
However, the new table should avoid hard-coding a new `agent:`-only SQL check if
possible. Application validation can enforce today's supported grammar while
leaving the column capable of accepting a future genuinely generic principal
grammar.

The current local-trust CLI cannot cryptographically distinguish “Cody ran
`--for lance` because Lance asked” from an unsolicited use of that flag.
Ruling (2026-08-23): `--on-behalf` is the **auto-accept assertion**, not a
general gate on `--for`. It is required only when a promise for a different
owner is to be created already accepted (`state = open`): the creator asserts
the owner requested it, and that assertion is recorded in the `promise.created`
payload (`on_behalf_asserted_by: <creator principal>`). `--for <other>` without
`--on-behalf` is an unsolicited assignment — the deferred promise-request path
that would create a `proposed` promise. Because `proposed` is outside the MVP,
that form is rejected before insertion today (acceptance scenario 7) and will
become a promise request when that concept lands; the flag's meaning does not
change. Self-owned promises never need the flag. This keeps the audit honest
about who claimed authority and is the hook a future runtime delegation/request
provenance slots into; the promise row must not invent a false `accepted_by`
event.

The default owner is the caller principal (`WRKQ_PRINCIPAL_REF` /
`--principal-ref`). `--for lance` is sugar for `agent:lance` under today's
grammar. Generalizing the principal grammar beyond `agent:<id>` is separate
work and not part of this campaign; the column stays grammar-agnostic with no
new `agent:`-only SQL check.

## Proposed CLI

The product noun should be singular in the command tree, following existing
`wrkq campaign` style:

```text
wrkq promise add
wrkq promise list
wrkq promise ready
wrkq promise edit
wrkq promise renew
wrkq promise resolve
wrkq promise abandon
wrkq promise attach
wrkq promise detach
```

There is no `promise show` or `promise history`. The root verbs already cover
them and must accept `PR-xxxxx` selectors: `wrkq cat PR-00101` renders the
promise (subject, question, owner, creator, review_at, ready-for duration,
attachment, last review), and `wrkq log PR-00101` renders its event timeline
with the existing `--oneline` / `--patch` modes.

### Timestamp flags

- `--review-at <timestamp>`: absolute. The flag *name* follows the
  `--due-at` / `--start-at` naming convention only; the CLI forwards the raw
  string and the API normalizes it per *Temporal normalization*. It must NOT
  use the `due_at`/`start_at` passthrough path.
- `--in <duration>`: relative sugar (`7d`, `36h`), resolved at write time.
- Exactly one of the two is required on `add` and `renew`.
- Not `--by`: "by" is deadline language, and `review_at` is explicitly not a
  deadline. Not `--at`: it collides with nothing but matches no existing flag.

### Attachment flags

`add` and `attach` accept at most one of `--task T-xxxxx` or
`--container <path>`; `--campaign <path>` is an alias for `--container`.
`detach` takes no target. Attaching to a task is as first-class as attaching
to a campaign.

### Standard CLI invariants

Promise commands are ordinary wrkq commands and inherit every existing
contract; none of these are promise-specific decisions:

- **Remote-first**: every verb is an RPC-CLI command over `workrpc`; no
  direct store access from the CLI. New methods register in the method
  registry with JSON schemas and move the fail-closed protocol hash.
- **Output modes**: `--output table|human|json|ndjson|porcelain|yaml|tsv|raw`,
  `--json`, `--ndjson`, `--porcelain` via the shared render/encode helpers
  (`encodeJSONIndent`, `isStdoutTTY`, the `render*` family) — no hand-rolled
  printing. Non-TTY defaults: `list`/`ready` emit NDJSON; `add`/`renew`/
  `resolve`/`abandon`/`attach`/`detach`/`edit` emit singleton JSON;
  `cat PR-xxxxx --json` is array-shaped with `--one` for a bare object.
- **Stable fingerprints**: JSON/porcelain shapes are recorded in the output
  fingerprint tests and `cli_surface_manifest.json` is regenerated
  (`gen-rpccli-surface-manifest`); surfaceguard baselines updated in the same
  change.
- **Errors**: RPC domain errors surface through `rpcMessage` with domain IDs
  (e.g. `WRKQ_WRONG_STATE` for renew on a closed promise,
  `WRKQ_FORBIDDEN` for `--for` without `--on-behalf`); exit codes follow the
  existing table.
- **Stdin conventions**: `-` and `@file` for `--subject`, `--question`,
  `--note`; one stdin consumer per invocation.
- **Principal flags**: `--principal-ref` / `--as` / `WRKQ_PRINCIPAL_REF`
  supply the creator; owner defaults to creator.
- **Concurrency**: mutations take `--etag` / `--if-match` like task `set`;
  every mutation increments `etag`.
- **Help and info**: usage text lives in the embedded `WRKQ-USAGE.md` and
  `AGENT-WRKQ-USAGE.md` (served by `wrkq info` / `wrkq agent-info`), plus
  cobra `--help`; the reference docs are generated from the same source, not
  written twice.
- **Selectors**: `PR-xxxxx` friendly IDs and UUIDs resolve through the
  existing selector package; `cat`, `log`, `rm --purge` accept them.
- **Snapshot**: promises participate in export/import with canonical ordering.

Intended flows:

### Standalone capture

```bash
wrkq promise add \
  --for lance --on-behalf \
  --in 7d \
  --subject "Check the HRC envelope rollout" \
  --question "Has hrcchat migration progressed? What is the next rollout boundary?"
```

Expected result: an accepted `PR-xxxxx` owned by `agent:lance`, created by the
acting Cody principal with the auto-accept assertion recorded. `--in <duration>`
and `--review-at <timestamp>` are alternatives; storage is absolute UTC. Omitting `--for` makes the caller the owner and
`--on-behalf` is then not required. `--for lance` without `--on-behalf` is a
request, not an accepted promise (deferred; rejected in MVP).

### Attach at creation

```bash
wrkq promise add \
  --for lance \
  --campaign hrc/envelopes \
  --in 7d \
  --question "Is hrcchat now using durable envelopes?"
```

`--campaign` may be syntactic sugar for resolving a container and setting
`subject_container_uuid`.

### Attach later

```bash
wrkq promise attach PR-00123 --campaign hrc/envelopes
wrkq promise attach PR-00124 --task T-07412
```

This preserves the promise ID, original subject text, and event history.

### Review ready promises

```bash
wrkq promise ready
wrkq promise ready --for lance
```

The default owner should come from caller/runtime context. Explicit `--for`
requires the on-behalf authority described above.

### Renew after reviewing

```bash
wrkq promise renew PR-00123 --in 7d \
  --note "Envelope storage is live; hrcchat cutover still needs a rollout slice."
```

### Resolve or abandon

```bash
wrkq promise resolve PR-00123 \
  --note "hrcchat has completed the envelope cutover."

wrkq promise abandon PR-00123 \
  --note "The migration direction was superseded."
```

The final CLI uses wrkq's machine-output defaults. Timestamp handling is
governed solely by *Temporal normalization*: the CLI never parses or formats
`review_at`; relative `--in` is forwarded as a duration and resolved
server-side; storage is canonical UTC `…Z`.

## Attention surfaces

A storage-only implementation will repeat the original failure. At least one
prominent consumer must make ready promises hard to overlook.

Ruling (2026-08-23) — mandatory for MVP:

- `wrkq promise ready` for explicit review;
- a promise section in `wrkq check`;
- the **session context-template** (the injected session-start context block,
  not the priming prompt) shows ready promises for the session's principal:
  compact lines with ID, subject, ready-for duration, and review question.
  Sleeping promises and an empty block are omitted.
- subject-side visibility: `wrkq cat T-xxxxx` and campaign/container show list
  attached promises (owner, review_at, state) so an agent working a subject
  learns someone is revisiting it.
- `wrkq tree` renders attached open promises as leaf rows under their task or
  container node (`PR-xxxxx  owner  review_at  ready 2d`), in all four tree
  renderers (human, json, ndjson, porcelain) with a `promises` array on the
  node in the wire view. Standalone promises have no project and do not appear
  in `tree`; closed promises are omitted unless `--state all`.

Session-surface scoping: a standalone promise has no project, so it is
owner-global and appears in every session of that principal. An attached
promise derives its project from its task or container and appears only in
sessions scoped to that project. Mable started in `hrc-runtime` sees her global
promises plus those attached to hrc-runtime subjects, never those attached to
wrkq tasks.

Deferred consumers: the Taskboard home/portfolio view and a bounded daily
digest.

For agents, a ready promise must initially be observable state, not automatic
execution. A later runtime policy may choose to:

- show it at the start of the agent's next session;
- add it to an agent inbox;
- wake an existing session;
- create a new dispatched turn.

Those are delivery and scheduling policies outside wrkq's initial authority.
wrkq should first answer which promises are ready and preserve the review
decision.

## Recommended MVP boundary

The smallest useful release includes:

1. `promises` storage with friendly IDs and optimistic concurrency.
2. Standalone subjects plus optional task/container attachment.
3. Principal ownership separate from creator attribution.
4. Absolute `review_at` storage and derived ready queries.
5. Lifecycle actions: renew, resolve, and abandon.
6. Append-only promise events.
7. CLI add/list/ready/renew/resolve/abandon/attach/detach/edit behavior, plus
   `cat`, `log`, and `tree` surfacing promises.
8. RPC/API/client contracts required to keep the current remote-first CLI
   architecture intact.
9. The ready-attention surfaces ruled mandatory above (`ready`, `check`,
    session context-template, subject-side listing).
10. Snapshot/export/import handling so promises are not lost in administrative
    lifecycle operations.

Explicitly defer:

- recurring/cadence rules;
- event-triggered waking such as “when task X completes”;
- inactivity-triggered waking such as “after seven days without campaign
  activity”;
- email, push, or chat notifications;
- automatic agent dispatch;
- cross-principal promise requests and acceptance state;
- a uniqueness rule per owner and subject;
- project placement for standalone promises;
- arbitrary external URL subjects beyond text stored in `meta`;
- a separate normalized review-history table.

## Acceptance scenarios for a first implementation

### 1. Human asks an agent to record a promise

Given Cody is acting in a session explicitly initiated by Lance, when Cody
creates a promise owned by Lance for seven days in the future, then the promise
is immediately open, audit shows Cody as creator, and it is absent from the
ready query until `review_at`.

### 2. Promise becomes ready without a scheduler

Given an open promise with `review_at <= now`, the owner's ready query returns
it even though no background process mutated the row.

### 3. Renewal records a completed review

Given a ready promise, when the owner renews it for seven days, then the row
remains open with a later `review_at`, leaves the ready query, increments its
etag, and appends a `promise.renewed` event containing the old and new times.

### 4. Standalone promise later attaches to a campaign

Given a standalone envelope-rollout promise, when an HRC envelopes campaign is
created and the promise is attached, then its ID and prior history remain
unchanged and the campaign reference becomes available to readers.

### 5. Subject lifecycle does not erase attention state

Given a promise attached to a task or campaign, completing or cancelling that
resource does not automatically close the promise. Purging the resource sets
the typed reference to null while preserving the subject snapshot and events.

### 6. Agent owns a promise

Given Cody creates a promise owned by Cody to verify a rollout later, then the
same data model and ready query work without a human-specific branch.

### 8. Offset input normalizes to a correct instant

Given `--review-at 2026-08-24T00:30:00+01:00`, the stored value is
`2026-08-23T23:30:00Z`; the promise is absent from the ready query at
`2026-08-23T23:29:59Z` and present at `2026-08-23T23:30:00Z`.

### 9. Non-owner mutation is rejected

Given Cody created a promise owned by Lance, when Cody attempts to renew,
resolve, abandon, detach, or purge it, the call fails with `WRKQ_FORBIDDEN`
before any write and the row and etag are unchanged.

### 10. Webhook delivery follows the subject

Given a promise attached to a task under a container with a webhook
subscription filtered to `promise`, renewing it delivers a `promise`-family
payload to that URL; a subscription filtered to `task` receives nothing; a
standalone promise's renewal delivers to no URL.

### 7. Unauthorized assignment is rejected

Given the acting principal lacks authority to create a promise for another
principal, the mutation fails before insertion and does not manufacture a
`proposed` promise.

## Existing wrkq schema findings

This design was checked against:

- ordered migrations `000001` through `000050` in `internal/db/migrations/`;
- a fresh database created from the current working-tree migration runner;
- mini's canonical database at
  `/Users/lherron/praesidium/var/db/wrkq.db` via read-only SSH inspection;
- current Go domain types and principal validation.

The fresh database and mini both report migration `000050_campaign_portfolio`
as current. Relevant existing shapes are:

- tasks contain `start_at`, `due_at`, principal/scope attribution, outcome, and
  optional `campaign_uuid`;
- containers carry optional campaign lifecycle, specification, and labels;
- event rows carry resource type/UUID plus principal/scope attribution;
- event-log resource type is constrained and must be widened for promises;
- timestamps are stored as sortable UTC text;
- principal references are external text identities rather than actor foreign
  keys.

`schema_dump.sql` was stale before this discussion: it stopped reflecting later
principal, claim, outcome, campaign, and workflow migrations. `T-07485` refreshes
it from a fresh database fully migrated through `000050`.

The refresh also exposed an unrelated existing schema defect present both in a
fresh database and on mini: `sections.project_uuid` references the migration-era
name `containers_old`. The dump should faithfully show the canonical schema;
fixing that foreign-key migration is separate work and must not be silently
bundled into promise implementation.

Mini's existing `wrkqadm doctor` output also reported sqlite sequence drift and
an unset attachment directory. Those findings are unrelated to promises and
were not changed during this design handoff.

## Likely implementation surfaces

A future implementation session should inspect and update at least:

- the next available SQL migration under `internal/db/migrations/`;
- migration tests and a fresh-schema assertion;
- `internal/domain` promise types;
- store/API methods and transaction-scoped event emission;
- workrpc method registry and JSON schemas;
- RPC CLI commands and output fingerprints;
- TypeScript client types/facade if promise access is public there;
- snapshot export/import and canonical ordering;
- search/index behavior if promises should be searchable;
- embedded usage text and CLI reference documentation;
- surfaceguard/catalog baselines;
- Taskboard or another selected ready-attention consumer.

Because new RPC methods or schema changes alter the fail-closed protocol hash,
do not install a promise-capable CLI onto the shared host independently of the
mini `wrkqd` deployment. Validate protocol changes against an isolated installed
instance, then cut the daemon and clients forward in one coordinated window.

## Decisions closed 2026-08-23

Lance ruled on the previously open decisions in review with mable:

1. **On-behalf authority**: `--on-behalf` is the auto-accept assertion,
   required only to create an already-accepted promise for another owner;
   recorded in the `promise.created` payload. `--for` alone is an unsolicited
   assignment (future promise request; rejected in MVP only because `proposed`
   is deferred). See *Principal ownership*.
2. **Principal grammar**: separate work. Owner defaults from the caller
   principal; `--for lance` is sugar for `agent:lance`.
3. **Mandatory attention surface**: `wrkq promise ready`, `wrkq check`
   section, session context-template (not priming prompt), and subject-side
   listing on task/container show. See *Attention surfaces*.
4. **Turn into work**: manual `attach` + `renew`/`resolve` sequence; no
   transactional verb in MVP.
5. **Field name**: `subject` everywhere, storage and API.
6. **Terminal vocabulary**: `resolved` / `abandoned`; deliberately not aligned
   with task states.
7. **Project context for standalone promises**: none; owner-global until usage
   proves a need.
8. **Event payload**: event-log rows carry a changed-fields map exactly as
   task events do (`store/tasks.go` shape); review verbs add
   `previous_review_at`, `next_review_at` (renew), and `note`. The full typed
   projection exists only in the webhook body (see *Webhook producer
   contract*), not in `event_log`. (Revised from "full row snapshot" after
   daedalus flaw 3.)
9. **Duplicates**: fully permitted, no detection or warning.
10. **Webhooks**: attached promises deliver via the subject's container
    subscription chain under a new `promise` family with a typed payload;
    standalone promises have no subscription scope and emit event-log only
    (narrowed after daedalus flaw 3).
11. **Last review on the row**: add nullable `last_reviewed_at` and
    `last_review_note`.
12. **Purge detach**: purge path emits `promise.retargeted` with the lost
    reference; FK `SET NULL` alone is insufficient.
13. **Subject-side visibility**: in MVP, including `wrkq tree` rows for
    attached open promises.

14. **Flags**: `--review-at` absolute / `--in` relative (not `--by`, not
    `--at`); attach targets `--task` or `--container` (`--campaign` alias).
15. **No `show`/`history` subcommands**: root `cat` and `log` accept `PR-`
    selectors.

16. **Temporal normalization** (daedalus flaw 1): API normalizes `review_at`
    to canonical UTC `…Z`; SQL CHECK enforces it; ready `now` is server-side.
17. **Mutation authority** (daedalus flaw 2): only the owner principal may
    mutate or purge a promise; `WRKQ_FORBIDDEN` otherwise.

Operational note (daedalus, non-binding): snapshot export includes
`event_log` but import does not replay it, so a state round-trip preserves
the row's last-review fields but not the full review timeline. Accepted for
MVP; timeline round-trip is not a promise-specific obligation.

Remaining for the implementer: output fingerprints and the
context-template integration point (which lives outside wrkq; wrkq supplies the
scoped ready query).

## Guiding invariant

The core product contract should survive implementation choices:

> A wrkq promise is a principal-owned commitment to revisit a subject. Before
> `review_at`, wrkq may keep it out of active attention. At and after
> `review_at`, wrkq must return it to the principal's ready queue until the
> principal renews, resolves, or abandons it. A promise is not a task deadline,
> campaign schedule, or implicit authorization to execute work.
