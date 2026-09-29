# Named subtasks in wrkq

September 28, 2026 · Proposal, revision 2 · Not approved for implementation

Authors: Astra (revision 1, **T-09873**); Mable (revision 2, from Lance's review).
Design context: `architecture-assessment/graph-session-handoff.md` (Latent Work
Graph). Where this document and the handoff differ on subtask mechanics, this
document is the concrete proposal; the handoff remains the conceptual direction.

## Decision summary

Add **named subtasks**: assignments inside an existing wrkq task. Each has a
parent-local slug, its own state and claim, its own agent scope, and the parent's
effective wrkc room. A subtask never receives a global `T-XXXXX` ID.

```text
mvp/                                  container
  offline-editing/                    task T-12345: Add offline editing
    decompose                         subtask
    architecture-diagram              subtask
    render-preview                    subtask
```

Revision 2 commits to these choices:

1. **Storage is task-backed.** Subtasks are rows in `tasks` with a structural
   discriminator, a NULL global ID and an owning-parent reference.
2. **Role removal ships first, as its own slice.** The subtask handle syntax is
   introduced only after the `role` dimension is gone. No handle ever means both.
3. **Paths are unambiguous by construction.** A container and a task cannot share a
   slug under the same container, so `a/b/c` never needs a guess.
4. **`AGENT_TASK` names the claimed work record.** For a subtask session it is
   `T-12345/architecture-diagram`, never the bare parent ID.
5. **No required-subtask completion gate in the first release.** Parent completion
   stays a judgement; it reports open subtasks and never refuses because of them.
6. **Destructive operations stay conservative.** Owner delete/archive never mutates
   its subtasks; purge is refused for a subtask or any task that owns one.

The proposal needs ordinary wrkq records, wrkc rooms, claims and identity changes.
It needs no graph interface, reconciler, executor catalog, attempt engine or wrkf
redesign, and it must not preclude them (see *Forward compatibility*). Wrkq remains
usable with HRC unavailable.

## Why subtasks and not campaigns plus child tasks

Most of this experience is available today: make the parent a campaign, enroll
child tasks, and dispatch each to `agent@project:T-child`. Tasks get distinct
scopes and claims, share the campaign room, and are filterable with
`wrkc log <campaign> --task T-x` (`docs/wrkc-reference.md`, routing rule 2).

The Latent Work Graph direction needs what that arrangement cannot provide:

- **A task-owned shared room.** A code task that grows a diagram, a render and a
  decomposition should not have to become a campaign container to share its
  conversation. Campaigns organize deliverables; subtasks live inside one.
- **Readable, local names.** Lance rejected numeric local addresses. A message
  naming `mvp/offline-editing/architecture-diagram` identifies the work; a fourth
  `T-` number does not.
- **No global-ID inflation for contributions.** Specialist contributions (diagram,
  render, review, decomposition) are frequent and small in management terms.
  Revision 1 of the graph concept modelled each as a child task; the handoff
  explicitly supersedes that.
- **A distinct node class in the combined graph.** The graph's five MVP node types
  include Subtask separately from Task, with a `has subtask` relation distinct from
  `has child task`.
- **Lifecycle within the parent's context.** Subtasks are non-blocking by default
  and may finish after the parent closes; they inherit residency and room rather
  than choosing their own.

Child tasks remain the tool for independently managed deliverables, including
cross-project ones. The distinction follows how work is managed, not its size.

## Task, child task and subtask

| Property | Task / child task | Named subtask |
| --- | --- | --- |
| Work boundary | Independently managed deliverable | Assignment in an existing task's context |
| Stable identity | UUID | UUID |
| Human address | Global ID and container/task path | Parent address plus slug |
| Global task ID | Allocated | Never allocated |
| Parent relationship | Optional child-task edge; may cross residency | Required owning task; inherits residency |
| Discussion | Own effective room, including campaign coalescing | Parent's effective room |
| Agent scope | Agent + project + task | Agent + project + parent task + subtask |
| Completion | Existing task contract | Independent state; never gates the parent (v1) |

For the first release a subtask cannot own subtasks or child tasks. An ordinary
child task can own named subtasks. A decomposition subtask creates child tasks
whose parent is the owning task and records `created` relations to them.

## Current baseline

Source inspection on September 28, 2026: wrkq `f28700a`, agent-spaces `46a1e4e8`,
hrc-runtime `ca0e3dd6`, plus HRC `state.sqlite` readback. These are source and
data observations, not claims of installed subtask behavior.

- Task creation requires only a title; the CLI derives it from the path. No
  executor or acceptance schema is required.
- `tasks.parent_task_uuid` is a bounded child-task edge. The CLI calls such tasks
  "subtasks" and often sets `kind=subtask`; they have global IDs and independent
  residency. This terminology must be retired in favour of "child task".
- A migration-000031 trigger allocates a global ID for every task lacking one.
- Paths are container segments plus a final task slug
  (`internal/selectors/selectors_local.go`). Container slugs are unique per parent
  container and task slugs per resident container, but **nothing prevents a task
  and a container sharing a slug** under the same container.
- `ResolveTaskByPath` with a single segment searches every project and takes
  `LIMIT 1`. It also scans `id` into a Go `string`, which fails on NULL: a concrete
  example of the nullable-ID audit below.
- Wrkc separates room anchor from envelope task tag. `routeToTaskUUID` coalesces
  by campaign, not by parent task. `roomOrDerivedForTask` keeps a pre-enrollment
  task room readable while new sends go to the campaign room.
- Reply obligations match sender scope, recipient scope and room.
- Claims are generation-fenced with no lease or TTL, and assume a global task ID.
- ASP handles use `/` for role (`contracts/agent-scope/src/scope-handle.ts`:
  `alice@demo:t1/reviewer`). `resolveQualifiedScopeInput` applies a
  `defaultRoleName` when a task is present and the role omitted; HRC feeds it from
  observed identity (`hrc-sdk/src/resolve-scope.ts`).
- **Role is effectively dormant.** 1,903 of 11,137 HRC sessions are role-scoped.
  The newest was created 2026-09-11. Nearly all are retired wrkf process roles
  (`verify`, `red`, `triage`, `implementer`, `tester`, `coordinator`, `worker`).
- `AGENT_TASK` has two code consumers: the agent-spaces env contract and
  `hrc-server/src/agent-spaces-adapter/cli-adapter.ts`.

Active architecture records this work must preserve or deliberately revise:

| Record | Treatment |
| --- | --- |
| `wrkq.task-hierarchy.cross-project-parents` | Child-task residency, depth and cross-project deletion unchanged; containment is a separate relation. |
| `wrkq.collaboration-ledger.authority` | Wrkq ownership, campaign coalescing, per-scope obligations, post-completion replies preserved. |
| `wrkq.task-claim.authority` | Exact claim identity extends to subtasks; node derivation, generation fencing, no TTL preserved. |
| `wrkq.task-view.caller-state-selection` | Subtask selection and display specified without changing task rollups. |
| `wrkq.attribution.caller-principal-exact` | Full scope, including subtask, retained in attribution. |
| `wrkq.rpc.remote-transport-locator` | Resolution and storage stay on the canonical server; contracts and consumers update together. |

Scope strings are coordination identities, not authentication.

## Slice 0: remove role from agent scope

This slice lands and activates before any subtask syntax exists.

- ASP: remove `roleName`, the `project-role` and `project-task-role` kinds,
  `:role:` construction, `/role` handle parsing and `defaultRoleName`. Handles
  containing `/` are refused until slice 3 assigns them a meaning.
- Wrkq Go scope parser, HRC selectors/monitoring/claim-start, ACP and wrkc
  consumers: drop role in the same coordinated release.
- History: canonical `:role:` ScopeRefs remain decodable **read-only** so old
  sessions, envelopes and attribution render. They are refused for new dispatch,
  birth and claim.
- Migration: enumerate live role-scoped sessions, pending envelopes, handoffs and
  profile defaults. Given the dormancy above, the expected action is to let them
  lapse; any pending obligation to a role scope is finished or explicitly
  withdrawn before cutover, never string-rewritten to another scope.
- Wrkf process roles, task role assignments and agent capabilities are domain data
  and are not removed. Inventory consumers so a role string is not dropped where it
  is still data.
- Role removal must not broaden primary-seat or service-restart authority.

Because canonical `:role:` and `:subtask:` segments differ and the handle `/` is
unassigned between slices, no input ever resolves ambiguously.

## Addresses and identity

Three selectors identify a subtask (syntax proposed, not installed):

```text
wrkq cat mvp/offline-editing/architecture-diagram    # full path
wrkq cat T-12345/architecture-diagram                # task-relative
wrkq cat <subtask-uuid>
```

Rules:

1. A subtask slug matches the task-slug character rule, 1–64 characters, so it fits
   the ASP token contract.
2. Slugs are unique under the owning task, including deleted records. Different
   tasks can each own `architecture-diagram`.
3. Slug and owner are immutable in the first release. Titles are editable. Retry
   and reopen reuse the same record.
4. A subtask is only ever the final segment, directly after a task. Resolution walks
   containers, reaches a task, then optionally one subtask segment.
5. **Container/task slug disjointness.** Creating, renaming or moving a task or
   container is refused when a sibling of the other kind has the same slug. Slice 1
   includes a one-time audit of existing collisions; each is fixed by an explicit
   rename before the constraint is enabled. Resolution never needs to guess, and a
   later container cannot break an address already written in mail.
6. A bare slug never resolves to a subtask. The single-segment cross-project
   fallback in `ResolveTaskByPath` applies to ordinary tasks only.
7. Moving or renaming the parent within its project changes the displayed full path
   and preserves the UUID and `T-12345/slug` selector. Full paths get the same
   staleness treatment as task paths; no permanent aliases.
8. Moving the parent across projects is refused while any task or subtask claim is
   held. After release, new dispatch uses the new project. Old-scope mail remains
   visible and is resolved explicitly, never rebound to a new seat.
9. Promotion to a task, reparenting and slug renaming are deferred. If added they
   preserve UUID and history; never delete-and-recreate.

Human output always renders a descriptive path, never a bare UUID or an empty ID.

## Storage

Subtasks reuse task content, states, comments, attachments, events, relations,
search, attribution and claims. The rationale: the graph handoff calls for reuse
of task facilities rather than an activity subsystem, and a future attempt
foundation benefits from one UUID space for work records.

| Field | Ordinary task | Named subtask |
| --- | --- | --- |
| `record_class` | `task` | `subtask` |
| `uuid` | Generated | Generated |
| `id` | Global task ID | NULL |
| `subtask_parent_uuid` | NULL | Required; references an ordinary task |
| `parent_task_uuid` | Optional child-task edge | NULL |
| `slug` | Unique in resident container | Unique under owner |
| resident container (`project_uuid`) | Authoritative | Materialized from owner |
| `campaign_uuid` | Existing enrollment | NULL; membership follows owner |

- `kind=subtask` is never authoritative. All existing rows migrate to
  `record_class=task`, including legacy `kind=subtask` rows, keeping IDs, edges,
  scopes, rooms, comments and attachments. New `--parent-task` creation defaults to
  `kind=task`.
- The ID trigger runs only for `record_class=task`. Subtasks consume no sequence
  values.
- Separate unique indexes cover container task slugs and owner subtask slugs.
  Constraints prevent orphans, subtask owners that are subtasks, and cycles.
- The server updates materialized residency in the same transaction as an owner
  move. No API writes a subtask's residency directly.

**Nullable-ID audit.** Slice 1 starts with an inventory of every SQL scan, DTO,
error message, claim path, event payload, search, bundle, import/export and client
that assumes `tasks.id` is non-NULL (for example `ResolveTaskByPath`). Every
response carries `recordClass`, `uuid`, readable path and parent references, and
`id: null` for subtasks. If the inventory shows the audit is disproportionate,
bring that evidence back as a storage decision before building; do not paper over
NULL with a synthetic or empty ID.

## Lifecycle and completion

Subtasks use the existing task states and permissive transitions. Claims and
optimistic concurrency keep their current rules.

- Completing a subtask changes nothing else.
- Completing a parent does not complete, cancel, hide or release its subtasks.
  **The completion response and CLI output list the parent's open subtasks** as a
  notice. They never refuse the transition.
- Open subtasks under a terminal parent stay claimable, replyable and findable. A
  find selector (spelling to be settled, e.g.
  `wrkq find --subtasks --parent-state terminal --state open`) surfaces them, and
  room discovery shows a count of unfinished subtasks, so they cannot quietly rot.
- Parent `blocked`/`cancelled` is context, not a mutation of subtasks. Cancelling a
  set of subtasks is an explicit operation that lists what it changes. No state
  change stops an HRC process.
- No subtask creation or claim under a deleted or archived owner. Existing
  conversations remain readable and replyable.
- Reopening a subtask does not reopen its parent.

**Required contributions are deferred.** The graph direction wants explicit rules
for required contributions eventually. In v1 they are expressed by a workflow on
the parent, or by the parent's owner checking before completion. A later
`required_for_parent` flag can be added without schema conflict. When it is, it
must be enforced in every writer, including bulk/import and wrkf's task-state
projection.

Typed relations can reference either record class. `render-preview` may depend on
`architecture-diagram`; existing blocker semantics are unchanged, so cancellation
still counts as non-blocking. No dependency scheduler is added.

Results are recorded as comments, outcome and attachments on the subtask. Process
exit or a wrkc reply never sets `completed`. If review matters, the subtask stays
unfinished until accepted.

## Commands and API

```sh
# Explicit flag distinguishes a subtask from a task/container typo.
wrkq touch mvp/offline-editing/architecture-diagram --subtask \
  -t 'Explain the offline editing architecture' -d 'Editable diagram and preview.'

wrkq cat mvp/offline-editing/architecture-diagram
wrkq set T-12345/architecture-diagram --state in_progress
wrkq comment add T-12345/architecture-diagram -m 'Draft ready for review.'

wrkq cat mvp/offline-editing              # parent detail lists its subtasks
wrkq ls mvp/offline-editing --subtasks
wrkq tree mvp                             # subtasks shown with a distinct marker
```

- `--parent-task` keeps meaning "child task"; docs and output say so. It never
  creates a named subtask.
- RPC selectors resolve both classes. Creation takes `recordClass`, owner, slug and
  title. Responses separate `parentTask` (child edge) from `subtaskParent`
  (ownership). The schema catalog, Go/TypeScript clients and CLI change together,
  and the new request fields follow the unknown-param refusal rule.
- Parent detail shows subtask summaries separately from its own content. Subtask
  detail shows its own brief plus parent reference and room locator, and never
  concatenates parent comments or transcripts.
- Existing task-only machine queries keep their scope and count meaning; including
  subtasks requires an explicit record-class selector. `tree` never folds open
  subtasks into an "all done" parent; it shows "parent complete; 2 subtasks open".
- Monitoring a parent can include its subtasks' events as typed related events;
  filtering happens before pagination. History and exports carry UUIDs, not only
  paths.

## ScopeRef, sessions and environment

Introduced in slice 3, after slice 0 has freed `/`. ASP owns the grammar; wrkq
owns whether the referenced record exists.

```text
ScopeRef:       agent:arris:project:demo:task:T-12345:subtask:architecture-diagram
ScopeHandle:    arris@demo:T-12345/architecture-diagram
SessionHandle:  arris@demo:T-12345/architecture-diagram~planning
```

- Add `subtaskId` and a `project-task-subtask` scope kind. Subtask requires a
  project and an ordinary parent task. Nested subtask segments are refused.
- ASP's grammar package stays pure. Wrkc/HRC resolve the subtask record before
  birth or claim, and runtime bindings keep the resolved UUID with the scope.
- SessionRef stays scope plus lane. Different subtasks give different scopes and
  sessions, even for one agent. A lane is conversation isolation within a scope,
  not work identity. Two agents on one subtask have different scopes, share the
  room, and one claim holder applies.
- The wrkq Go scope parser matches ASP against shared contract fixtures, and full
  subtask identity survives into FullRef and attribution. Project-only context
  lookups may normalize for configuration but never discard the qualifier from
  durable attribution or routing.

Environment for a subtask session:

| Variable | Value |
| --- | --- |
| `AGENT_TASK` | Claimed work selector: `T-12345/architecture-diagram` |
| `AGENT_PARENT_TASK` | `T-12345` (set only for subtask sessions) |
| `AGENT_SCOPE_REF` | Full canonical scope including subtask |
| `AGENT_SESSION_REF` | Scope plus lane |
| Claim token/generation | The subtask's claim |

`AGENT_TASK` always names the record the session works on. A skill that runs
`wrkq set $AGENT_TASK --state completed` then completes the subtask, not the
parent. Consumers that parse `AGENT_TASK` as a bare `T-` ID update in slice 3.

## Claims and execution

Existing `wrkq claim`, `release`, claim validation and holder-guarded completion
extend to subtasks. No new run or attempt system.

- The server resolves parent and slug, verifies that the scope names exactly that
  UUID and project, and records the holder on the subtask row.
- Parent and sibling claims are independent. A subtask token cannot claim or
  complete the parent. An ordinary task claim requires a scope with no subtask
  segment.
- Node identity, monotonic generations, explicit takeover, stale-holder fencing and
  no automatic expiry are unchanged.
- HRC claim/start passes the subtask claim receipt and routes the workspace by the
  owning task and project. Separate sessions do not imply separate worktrees or
  permission to edit the same files concurrently.
- Dispatch is an addressed `wrkc say` to the subtask handle. Creating a record does
  not start an agent.
- Named subtasks do not get a wrkf instance. Attaching one directly to a subtask is
  refused in v1.

## Wrkc rooms, subjects and recipients

| Identity | Example | Purpose |
| --- | --- | --- |
| Room anchor | Parent task, or its campaign | Shared durable conversation |
| Message subject | `architecture-diagram` subtask UUID | Exact work discussed; filtering and `--record` |
| Recipient scope | `arris@demo:T-12345/architecture-diagram` | Working session and reply obligation |

- A subtask has no room of its own and cannot enroll in a campaign. Sends resolve
  through the owner with exactly the owner's routing, including campaign
  coalescing. No subtask room rows. Child-task routing is unchanged.
- The envelope task tag holds the subtask UUID; it is never overwritten with the
  parent's. Read models add parent, record class and path.
- Reading a subtask's history mirrors the owner's read rules, including a
  pre-enrollment task room. Old rooms are not merged and envelopes not rewritten.

```sh
wrkc say T-12345/architecture-diagram \
  --to arris@demo:T-12345/architecture-diagram - <<'MESSAGE'
Create an editable architecture diagram from the parent brief and decisions.
MESSAGE

wrkc log mvp/offline-editing                                    # whole shared room
wrkc log mvp/offline-editing --task T-12345/architecture-diagram # one subject
```

- An explicit work selector sets the subject. A subtask handle with no selector
  derives subject and room from the handle. Replying through `EN-XXXXX` keeps the
  original room and subject. `--record` follows the subject, not the recipient.
- Bare-addressee fallback uses the subtask subject when one was selected. If
  membership or obligation lookup finds several scopes for the same agent, a full
  handle is required.
- Obligations still match sender scope, recipient scope and room, so a reply from
  the `architecture-diagram` scope does not answer a request to
  `deployment-diagram`. No new reply state machine.
- Say never hard-fails because of subtask state; parent completion, staleness and
  hidden labels continue to permit messages.

## Deletion, movement and preservation

Subtasks are contained records; child tasks keep residency rules, including
cross-project detachment. The two relations never share one rule just because both
have a parent.

- **Owner delete/archive does not mutate subtasks.** Their states are untouched;
  new claims and creation under the owner are refused; they appear in the
  open-under-terminal-parent find. Owner restore therefore needs no bookkeeping.
- **Purge is refused** for a subtask and for any task that owns one. Archive
  instead. Conversation evidence is never lost to a cascade.
- A held claim on the owner or any subtask refuses cross-project moves.
- Same-project owner moves carry materialized residency atomically.
- Subtask deletion keeps its slug reserved, so a stale scope never addresses
  unrelated replacement work; restore the record or reopen it.

## Decomposition example

1. Create `mvp/offline-editing/decompose` asking for a breakdown, dependencies and
   acceptance criteria. Dispatch it to its own scope.
2. The agent reads the parent brief and room, then creates child tasks
   (persistence, synchronization, conflict resolution). Each has a global ID and a
   resident path.
3. Each creation is recorded as a `created` relation from the subtask as it
   happens. On resume the agent reads those relations before creating more. Crash
   safety beyond that belongs to the future attempt foundation, not this proposal.
4. A reviewer checks coverage and boundaries; `decompose` completes when accepted.
5. The child tasks stay open; their dispatch and completion are separate acts.

## Forward compatibility with the Latent Work Graph

This proposal delivers the work-record, addressing, room and scope half of the
graph design. It deliberately leaves the rest open without blocking it:

| Graph concept | Status here | Why it is not blocked |
| --- | --- | --- |
| Subtask node, `has subtask` relation | Provided | `record_class` + `subtask_parent_uuid`, stable UUID |
| Standalone sessions, combined projection | Out of scope | Subtasks add stable source identities; no graph store |
| Reconciliation activation | Out of scope | Push dispatch is unchanged; subtasks are explicit intent a reconciler can read |
| Shared attempt foundation | Out of scope | Claims give one holder per record now; attempts can key on the same UUID |
| Deterministic executors | Out of scope | No executor field is required; a handler can update the record like any caller |
| Return policy, durable result before notify | Partial | Results live on the subtask; notification remains explicit wrkc |
| Required contributions | Deferred | Workflow or later `required_for_parent`; schema has room |
| Keep-current / awaiting-evidence | Out of scope | Subtask state stays open; no auto-completion |

## Delivery slices

Each slice lands, installs and passes installed acceptance before the next begins.
Producer before consumers: the canonical wrkqd on mini migrates first (install,
`wrkqadm migrate`, restart), then clients install. Protocol-hash pins mean an
older client refuses a newer server, so client publication is coordinated.

0. **Role removal** (agent-spaces, wrkq Go scope, hrc-runtime, ACP, wrkc
   consumers). Inventory, lapse or withdraw live role obligations, cut over,
   verify historical decoding.
1. **Wrkq storage, selectors, claims.** Nullable-ID inventory, slug-collision audit
   and fixes, migration, `record_class`, path resolution, CRUD/search/export,
   completion notice and find selector, subtask claims, delete/purge rules, RPC
   schema and Go/TS clients.
2. **Wrkc rooms.** Owner-room routing, subtask subject tags, log filtering,
   `--record`, history mirroring, subtask counts in discovery.
3. **Subtask scopes.** ASP grammar and handles, wrkq Go parser parity, HRC
   resolution before birth/claim, session keys, monitoring, env (`AGENT_TASK`,
   `AGENT_PARENT_TASK`), ACP display and destinations.
4. **Docs and records.** SPEC, CLI guides, identity contract and the active records
   above, updated to delivered behavior.

Slices 1–2 are usable on their own: subtasks can be created, tracked and discussed,
with dispatch to the parent-task scope. Slice 3 adds per-subtask seats.

## Acceptance scenarios

Each installed run keeps command transcripts, JSON responses, database/event
readback and session references in the task artifact directory.

1. **Role removal.** Role handles and new `:role:` ScopeRefs are refused for birth,
   claim and dispatch; historical role sessions and envelopes still render; no
   pending obligation is silently reassigned.
2. **Identity.** `architecture-diagram` created under two tasks: distinct UUIDs, no
   global IDs, task sequence unchanged. Duplicate under one owner refused.
3. **Paths.** Every selector round-trips through create/show/set/comment/attach/
   search/export/import. Creating a container with a sibling task's slug, and the
   reverse, is refused. A bare subtask slug does not resolve.
4. **Child tasks unchanged.** Existing child tasks, including cross-project ones,
   keep IDs, paths, rooms and delete behavior through migration.
5. **Two subtasks, one agent.** Two scopes and sessions, one room, two subject tags,
   independent obligations: replying from one leaves the other owed.
6. **Author and reviewer on one subtask.** Different scopes, shared room, one claim
   holder.
7. **Room history.** Ordinary parent, campaign parent and parent with a
   pre-enrollment room: current sends match parent routing; old-envelope replies
   keep their room and subject.
8. **Claims.** Sibling subtasks and the parent claimed concurrently; no token
   completes another record; takeover fences the old holder; subtask scopes grant
   no primary-seat authority.
9. **Completion.** Completing a parent with open subtasks succeeds and lists them;
   they stay claimable, replyable and appear in the terminal-parent find.
   Completing one later leaves the parent unchanged.
10. **Environment.** In a subtask session `AGENT_TASK` is the subtask selector;
    `wrkq set $AGENT_TASK --state completed` completes the subtask, not the parent.
11. **Movement.** Same-project parent move/rename keeps UUIDs, task-relative
    selectors, scopes, attachments and room history. Subtask rename, reparent and
    nesting are refused with an actionable message.
12. **Deletion.** Owner delete/archive leaves subtask states untouched and refuses
    new claims; purge of an owner or subtask is refused; nothing is lost.
13. **Decomposition resume.** Interrupted after two child creations, resumed from
    recorded `created` relations with no duplicates; completing `decompose` leaves
    the children open.
14. **HRC down.** Subtask CRUD and shared-room messages work through wrkq/wrkc.

## Open items for Daedalus review

- Exact flag, RPC field and find-selector spellings.
- The nullable-ID inventory result, if it argues against task-backed storage.
- Whether any active record needs revision beyond the treatments listed above.

## Source map

Repository-root-relative unless prefixed with a sibling checkout.

- `internal/wrkqapi/tasks.go`, `internal/wrkqapi/types.go`, `internal/store/tasks.go`,
  `internal/db/migrations/000031_cross_project_parent_edges.sql` — creation, ID
  allocation, residency, parent constraints.
- `internal/selectors/selectors_local.go` — path resolution, bare-slug fallback.
- `internal/wrkqapi/rooms.go`, `internal/store/rooms.go`, `internal/domain/rooms.go`
  — routing, subjects, scopes, obligations.
- `internal/wrkqapi/claims.go`, `internal/db/migrations/000048_task_claim_authority.sql`.
- `internal/scope/`, `internal/attribution/` — Go scope parser and attribution.
- agent-spaces: `contracts/agent-scope/src/{scope-ref,scope-handle,input}.ts`,
  `docs/identity-scope-and-env-contract.md`, `compiler/agent-spaces/docs/env-contract.md`.
- hrc-runtime: `packages/hrc-sdk/src/resolve-scope.ts`,
  `packages/hrc-core/src/selectors.ts`, `packages/hrc-core/src/monitor/index.ts`,
  `packages/hrc-server/src/scope-claim-core.ts`,
  `packages/hrc-server/src/server-lifecycle-authority.ts`,
  `packages/hrc-server/src/agent-spaces-adapter/cli-adapter.ts`.
- architecture-assessment: `graph-session-handoff.md`,
  `graph-detailed-artifact-reference.md` — design context.
