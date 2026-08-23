# wrkq Promises: Design Handoff

Status: design handoff, not yet an implementation specification

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

  review_at TEXT NOT NULL,

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'resolved', 'abandoned')),
  closed_at TEXT,

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
promise timeline. Do not add `last_reviewed_at` or `review_count` to the domain
row unless a measured query justifies denormalizing them.

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

### Assignment boundary

The MVP should support owner-authorized creation, not arbitrary assignment.

| Situation | Expected result |
| --- | --- |
| Lance records a promise for Lance | Accepted |
| Cody records Lance's promise at Lance's request | Accepted |
| Cody records Cody's own promise | Accepted |
| Cody unsolicitedly assigns Lance a promise | Rejected initially |

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

The current local-trust CLI also cannot cryptographically distinguish “Cody ran
`--for lance` because Lance asked” from an unsolicited use of that flag. The
agreed product behavior is still owner-authorized auto-acceptance. Stronger
proof later requires the runtime to provide delegation or request provenance;
the promise row should not invent a false `accepted_by` event.

## Proposed CLI

The product noun should be singular in the command tree, following existing
`wrkq campaign` style:

```text
wrkq promise add
wrkq promise list
wrkq promise ready
wrkq promise show
wrkq promise edit
wrkq promise renew
wrkq promise resolve
wrkq promise abandon
wrkq promise attach
wrkq promise detach
wrkq promise history
```

Exact flags remain provisional, but the intended flows are:

### Standalone capture

```bash
wrkq promise add \
  --for lance \
  --in 7d \
  --subject "Check the HRC envelope rollout" \
  --question "Has hrcchat migration progressed? What is the next rollout boundary?"
```

Expected result: an accepted `PR-xxxxx` owned by `agent:lance`, created by the
acting Cody principal.

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

The final CLI should use wrkq's existing timestamp parsing conventions and
machine-output defaults. Relative input such as `7d` is syntactic sugar; storage
remains an absolute UTC timestamp.

## Attention surfaces

A storage-only implementation will repeat the original failure. At least one
prominent consumer must make ready promises hard to overlook.

Potential consumers:

- `wrkq promise ready` for explicit review;
- a promise section in `wrkq check` or another habitual CLI surface;
- the Taskboard home/portfolio view;
- a bounded daily digest;
- agent-session priming that shows promises owned by the active principal.

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
7. CLI create/show/list/ready/renew/resolve/abandon/attach behavior.
8. RPC/API/client contracts required to keep the current remote-first CLI
   architecture intact.
9. At least one prominent ready-attention surface.
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

## Open decisions for the next session

The design is coherent enough to shape into a specification, but these details
remain intentionally unsettled:

1. What runtime evidence authorizes an agent's `--for lance` creation, beyond
   the current local-trust model?
2. Should the broader wrkq principal grammar be generalized beyond `agent:<id>`
   as part of this work or handled separately?
3. Which ready-attention surface is mandatory for MVP completion: CLI only,
   Taskboard, session priming, or a digest?
4. Should `turn into work` be a transactional convenience command or remain a
   manual create/attach/renew sequence?
5. Is `subject` the best persisted field name, or should the public API use
   `title` while retaining subject terminology?
6. Should terminal vocabulary remain `resolved`/`abandoned`, or align with
   another wrkq lifecycle vocabulary?
7. Should standalone promises optionally capture a project/context container?
8. What exact event payload snapshot is required for durable audit and webhook
   consumers?
9. Should likely duplicate promises warn, error, or remain entirely permitted?
10. Should promise events participate in existing webhook subscriptions in the
    first release?

## Guiding invariant

The core product contract should survive implementation choices:

> A wrkq promise is a principal-owned commitment to revisit a subject. Before
> `review_at`, wrkq may keep it out of active attention. At and after
> `review_at`, wrkq must return it to the principal's ready queue until the
> principal renews, resolves, or abandons it. A promise is not a task deadline,
> campaign schedule, or implicit authorization to execute work.
