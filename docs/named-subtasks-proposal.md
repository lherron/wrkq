# Named subtasks in wrkq

September 28, 2026 · Proposal, revision 3 · Not approved for implementation

Authors: Astra (revision 1, **T-09873**); Mable (revisions 2–3, from Lance's review).
Design context: `architecture-assessment/graph-session-handoff.md` (Latent Work
Graph). Where this document and the handoff differ on subtask mechanics, this
document is the concrete proposal; the handoff remains the conceptual direction.

## Decision summary

Add **named subtasks**: assignments inside an existing wrkq task. A subtask is an
ordinary task row whose ID is its parent's ID plus a local slug:

```text
mvp/                                  container
  offline-editing/                    T-12345   Add offline editing
    decompose                         T-12345/decompose
    architecture-diagram              T-12345/architecture-diagram
    render-preview                    T-12345/render-preview
```

**The composite ID is the design.** `T-12345/architecture-diagram` is at once the
stored ID, the selector, the scope's task token and the handle suffix. Because it
is a real, non-empty, stable, unique task ID, existing task logic keeps working:
ID lookup, claims, scope matching, session keys, attribution, obligations,
comments, attachments, events and `AGENT_TASK`. No global sequence number is
allocated, so Lance's "no new `T-XXXXX`" requirement holds.

The remaining changes are the places where a subtask must behave differently
from a task, plus consumers that assume an ID shape:

1. **Room routing** sends a subtask's traffic through its owner's effective room.
2. **Container membership**: subtasks are excluded from container slug uniqueness,
   listings and container-path resolution. Paths reach them only through their
   owner.
3. **ID shape**: consumers that match `T-\d{5}` accept the `/slug` suffix. Three
   unanchored matchers would otherwise quietly act on the parent.
4. **Scope tokens**: a task token may carry one `/slug`. This requires removing the
   `role` dimension first, because `/` means role in handles today.
5. **Parent completion** reports open subtasks as a notice; it never refuses.
6. **Purge** is refused for a subtask or any task that owns one.

The proposal needs no graph interface, reconciler, executor catalog, attempt
engine or wrkf redesign, and does not preclude them (see *Forward compatibility*).
Wrkq remains usable with HRC unavailable.

## Why subtasks and not campaigns plus child tasks

Most of this experience is available today: make the parent a campaign, enroll
child tasks, and dispatch each to `agent@project:T-child`. Tasks get distinct
scopes and claims, share the campaign room, and are filterable with
`wrkc log <campaign> --task T-x` (`docs/wrkc-reference.md`, routing rule 2).

The Latent Work Graph direction needs what that arrangement cannot provide:

- **A task-owned shared room.** A code task that grows a diagram, a render and a
  decomposition should not have to become a campaign container to share its
  conversation. Campaigns organize deliverables; subtasks live inside one.
- **Readable, local names.** Lance rejected numeric local addresses.
  `T-12345/architecture-diagram` says what the work is; a fourth `T-` number does
  not.
- **No global-ID inflation for contributions.** Specialist contributions (diagram,
  render, review, decomposition) are frequent and small in management terms.
  Revision 1 of the graph concept modelled each as a child task; the handoff
  explicitly supersedes that.
- **A distinct node class in the combined graph.** The graph's MVP node types
  include Subtask separately from Task, with `has subtask` distinct from
  `has child task`.
- **Lifecycle within the parent's context.** Subtasks are non-blocking and may
  finish after the parent closes; they inherit residency and room.

Child tasks remain the tool for independently managed deliverables, including
cross-project ones. The distinction follows how work is managed, not its size.

## Task, child task and subtask

| Property | Task / child task | Named subtask |
| --- | --- | --- |
| Work boundary | Independently managed deliverable | Assignment in an existing task's context |
| ID | `T-12345` from the global sequence | `T-12345/slug`; no sequence number |
| Parent relationship | Optional child-task edge; may cross residency | Required owner, fixed by the ID; inherits residency |
| Discussion | Own effective room, including campaign coalescing | Owner's effective room |
| Agent scope | `agent@project:T-12345` | `agent@project:T-12345/slug` |
| Completion | Existing task contract | Independent; never gates the parent (v1) |

For the first release a subtask cannot own subtasks or child tasks, so an ID has
at most one `/`. An ordinary child task can own named subtasks
(`T-12400/render-preview`). A decomposition subtask creates child tasks whose
parent is the owning task and records `created` relations to them.

## Current baseline

Source inspection on September 28, 2026: wrkq `f28700a`, agent-spaces `46a1e4e8`,
hrc-runtime `ca0e3dd6`, plus HRC `state.sqlite` readback. These are source and
data observations, not claims of installed subtask behavior.

- Task creation requires only a title. A migration-000031 trigger allocates a
  global ID only when `id` is NULL, so an insert that supplies the composite ID
  consumes no sequence value.
- `tasks.parent_task_uuid` is a bounded child-task edge. The CLI calls such tasks
  "subtasks" and often sets `kind=subtask`; they have global IDs and independent
  residency. This terminology is retired in favour of "child task".
- `tasks_unique_slug_in_container` is `UNIQUE(project_uuid, slug)`, where
  `project_uuid` is the resident container. Container slugs are unique per parent
  container, but **nothing prevents a task and a container sharing a slug**.
- Paths are container segments plus a final task slug
  (`internal/selectors/selectors_local.go`). A single segment searches every
  project with `LIMIT 1`.
- `routeToTaskUUID` coalesces by effective campaign (residency first, then
  enrollment), otherwise it uses the task's own room, and always tags the envelope
  with the task. `roomOrDerivedForTask` keeps a pre-enrollment task room readable.
- Reply obligations match sender scope, recipient scope and room. Claims are
  generation-fenced with no TTL and match the scope's task token to the task ID.
- ASP tokens match `[A-Za-z0-9._-]+`, 1–64 characters. Handles use `/` for role
  (`alice@demo:t1/reviewer`), and `defaultRoleName` fills in a role when a task is
  present (`hrc-sdk/src/resolve-scope.ts`).
- **Role is effectively dormant.** 1,903 of 11,137 HRC sessions are role-scoped;
  the newest was created 2026-09-11. Nearly all are retired wrkf process roles.
- About 22 non-test sites across wrkq, hrc-runtime, ACP and taskboard match task
  IDs by pattern. Anchored ones (`internal/id/id.go` `^T-\d{5}$`,
  `causedby.go`) reject a composite ID loudly. Unanchored ones
  (`wrkqapi/rooms.go` mention pattern, `rpccli/wrkp_git.go`,
  `rpccli/wrkp_just.go`, which already accepts a trailing `/`) would match the
  parent prefix silently.

Active architecture records this work must preserve or deliberately revise:

| Record | Treatment |
| --- | --- |
| `wrkq.task-hierarchy.cross-project-parents` | Child-task residency, depth and cross-project deletion unchanged; ownership is a separate relation. |
| `wrkq.collaboration-ledger.authority` | Wrkq ownership, campaign coalescing, per-scope obligations, post-completion replies preserved. |
| `wrkq.task-claim.authority` | Unchanged: the subtask's composite ID is the claimed task ID. |
| `wrkq.task-view.caller-state-selection` | Subtask inclusion and completion display specified without changing task rollups. |
| `wrkq.attribution.caller-principal-exact` | Unchanged: the full scope, including the composite task token, is attributed. |
| `wrkq.rpc.remote-transport-locator` | Resolution and storage stay on the canonical server; contracts and consumers update together. |

Scope strings are coordination identities, not authentication.

## Slice 0: identity tokens (role out, composite task tokens in)

A coordinated identity release, landed and activated in two steps (below) before
wrkq creates any subtask.

**Remove role.**

- ASP: remove `roleName`, `project-role`/`project-task-role`, `:role:`
  construction, `/role` handle parsing and `defaultRoleName`.
- The wrkq Go scope parser, HRC selectors/monitoring/claim-start, ACP and wrkc
  consumers drop role in the same release.
- Canonical `:role:` ScopeRefs remain decodable **read-only**, so old sessions,
  envelopes and attribution render. They are refused for new dispatch, birth and
  claim.
- Enumerate live role-scoped sessions, pending envelopes, handoffs and profile
  defaults. Given the dormancy above, the expected action is to let them lapse.
  Any pending obligation to a role scope is finished or explicitly withdrawn
  before cutover, never string-rewritten.
- Wrkf process roles, task role assignments and agent capabilities are domain data
  and stay. Role removal must not broaden primary-seat or restart authority.

**Allow composite task tokens.**

- A task token is `<base>` or `<task-id>/<slug>`, where each part matches the
  existing token charset, `<task-id>` is a wrkq `T-` ID, and the whole is at most
  64 characters. At most one `/`.
- ScopeRef: `agent:arris:project:demo:task:T-12345/architecture-diagram`.
  Handle: `arris@demo:T-12345/architecture-diagram`. Session handle:
  `arris@demo:T-12345/architecture-diagram~planning`.
- No new scope kind and no `subtaskId` field: a subtask scope is a
  `project-task` scope. SessionRef stays scope plus lane.
- The wrkq Go parser matches ASP against shared contract fixtures.
- Audit consumers that put a scope or task token into a filesystem path, tmux
  name, URL or log key (HRC session rendering and run helpers, artifact
  directories, taskboard routes) and escape or encode `/` where needed.

**Activate in two steps.** An old role handle such as `mable@wrkq:T-12345/reviewer`
is spelled exactly like a composite task handle. If both changes switched on at
once, a stale role handle would quietly birth a seat for a task named
`T-12345/reviewer`. So:

- **0a** removes role, and any handle containing `/` is refused. Refusals are
  logged with the caller, and the log is checked until no producer still sends
  role handles.
- **0b** then accepts composite task tokens. The base must be a wrkq task ID
  (`T-` form), so seat names like `primary/reviewer` stay refused. HRC also
  refuses to birth a composite scope unless wrkq confirms that the subtask
  exists, so a late role handle such as `T-12345/reviewer` cannot create a seat.

Canonical `:role:` and composite `task:` segments never collide, so historical
decoding is unaffected.

## Addresses

Three selectors identify a subtask:

```text
wrkq cat T-12345/architecture-diagram                # the ID
wrkq cat mvp/offline-editing/architecture-diagram    # full path
wrkq cat <subtask-uuid>
```

1. A subtask slug matches the task-slug charset and is short enough that the
   composite ID fits the 64-character token limit.
2. Slugs are unique under their owner, including deleted records, which the
   unique ID enforces directly. Different tasks can each own
   `architecture-diagram`.
3. Slug and owner are immutable in the first release, which is what keeps the ID
   stable. Titles are editable. Retry and reopen reuse the same record.
4. **ID-first selection.** A selector whose first segment is a task ID is
   resolved by exact ID match before any path walk. Container slugs are
   lowercase, so they cannot be mistaken for `T-` IDs.
5. **Path walk.** Containers are walked, a task is reached, then at most one
   subtask segment. A subtask is never matched as a container member, and a bare
   slug never resolves to a subtask.
6. **Container/task slug disjointness.** Creating, renaming or moving a task or
   container is refused when a sibling of the other kind has the same slug. Slice 1
   includes a one-time audit of existing collisions, each fixed by an explicit
   rename before the constraint is enabled. A later container can then never
   break a path already written in mail.
7. Moving or renaming the parent changes the displayed full path and leaves the
   ID, and so every scope, unchanged.
8. Moving the parent across projects is refused while any task or subtask claim is
   held. After release, new dispatch uses the new project. Old-scope mail remains
   visible and is resolved explicitly, never rebound to a new seat.
9. Promotion to a task, reparenting and slug renaming are deferred. Each would
   change the ID; if added they preserve UUID and history and handle the scope
   change explicitly.

## Storage

A subtask is a row in `tasks`. Every existing column keeps its meaning.

| Field | Ordinary task | Named subtask |
| --- | --- | --- |
| `id` | `T-12345` from the sequence | `T-12345/slug`, supplied at insert |
| `subtask_parent_uuid` (new) | NULL | Owner's UUID; this is the discriminator |
| `parent_task_uuid` | Optional child-task edge | NULL |
| `slug` | Unique in resident container | Unique under owner |
| resident container (`project_uuid`) | Authoritative | Materialized from owner |
| `campaign_uuid` | Existing enrollment | NULL; membership follows owner |

- `subtask_parent_uuid IS NOT NULL` identifies a subtask. `kind=subtask` is never
  authoritative. Existing rows keep NULL, including legacy `kind=subtask` child
  tasks; new `--parent-task` creation defaults to `kind=task`.
- `tasks_unique_slug_in_container` becomes partial (`WHERE subtask_parent_uuid IS
  NULL`); a second index covers `(subtask_parent_uuid, slug)`. The server builds
  the ID from the owner's ID and the slug and never accepts a client-supplied one.
- Constraints: the owner is an ordinary task (not itself a subtask), and it exists.
- The server updates materialized residency in the same transaction as an owner
  move. No API writes a subtask's residency directly.
- API and CLI responses add `subtaskParent` beside the existing `parentTask`, so
  the two relationships are never confused.

## ID-shape audit

Slice 1 changes every pattern that recognizes task IDs so it accepts the
optional `/slug` suffix, with tests where a subtask ID appears:

- **Silent-prefix sites (must fix, must test):** `wrkqapi/rooms.go` mention
  pattern; `rpccli/wrkp_git.go` commit attribution; `rpccli/wrkp_just.go` scope
  extraction. A subtask ID in a message body, commit message or scope must never
  resolve to its parent.
- **Loud-rejection sites (widen):** `internal/id/id.go`, `causedby.go` and similar
  anchored validators.
- **Out of scope:** `ARCH-EXCEPTION(T-…)` lint markers can keep requiring
  ordinary task IDs.

The complete list is regenerated with `rg` across wrkq, hrc-runtime,
agent-control-plane, agent-spaces and taskboard at slice start; the 22 counted
here are a floor.

## Lifecycle and completion

Subtasks use existing task states, transitions, claims and optimistic concurrency.

- Completing a subtask changes nothing else.
- Completing a parent does not complete, cancel, hide or release its subtasks.
  **The completion response and CLI output list the parent's open subtasks** as a
  notice. They never refuse the transition.
- Open subtasks under a terminal parent stay claimable, replyable and findable. A
  find selector (spelling to be settled, e.g.
  `wrkq find --subtasks --parent-state terminal --state open`) surfaces them, and
  room discovery shows a count of unfinished subtasks, so they cannot quietly rot.
- Parent `blocked`/`cancelled` is context, not a mutation of subtasks. No state
  change stops an HRC process.
- No subtask creation or claim under a deleted or archived owner. Existing
  conversations remain readable and replyable.
- Reopening a subtask does not reopen its parent.

**Required contributions are deferred.** In v1 they are expressed by a workflow on
the parent or by the parent's owner checking before completion. A later
`required_for_parent` flag fits the schema; when added it must be enforced in
every writer, including bulk/import and wrkf's task-state projection.

Typed relations work unchanged between any task IDs, so `T-12345/render-preview`
may depend on `T-12345/architecture-diagram` with existing blocker semantics.
Results are comments, outcome and attachments on the subtask. Process exit or a
wrkc reply never sets `completed`.

## Commands

```sh
# Explicit flag distinguishes a subtask from a task/container typo.
wrkq touch T-12345/architecture-diagram --subtask \
  -t 'Explain the offline editing architecture' -d 'Editable diagram and preview.'

wrkq cat mvp/offline-editing/architecture-diagram
wrkq set T-12345/architecture-diagram --state in_progress
wrkq comment add T-12345/architecture-diagram -m 'Draft ready for review.'

wrkq cat T-12345                          # parent detail lists its subtasks
wrkq ls T-12345 --subtasks
wrkq tree mvp                             # subtasks shown under their owner
```

- `--parent-task` keeps meaning "child task"; docs and output say so.
- Container listings, `tree` rollups and existing task-only machine queries keep
  their meaning and exclude subtasks unless explicitly included. `tree` never
  folds open subtasks into an "all done" parent; it shows "parent complete;
  2 subtasks open".
- Parent detail shows subtask summaries separately from its own content. Subtask
  detail shows its own brief plus parent reference and room locator.
- New RPC request fields follow the unknown-param refusal rule; the schema
  catalog and Go/TypeScript clients change together.

## Sessions, environment and claims

A subtask session is a task session whose task token is the composite ID. With
slice 0 in place, all of the following is today's logic applied to that ID:

- `AGENT_TASK=T-12345/architecture-diagram`. A skill running
  `wrkq set $AGENT_TASK --state completed` completes the subtask, not the parent.
  Agents that need the parent read it from the subtask record.
- `wrkq claim` records the holder on the subtask row. Parent and sibling claims
  are independent because their IDs differ; a subtask token cannot claim or
  complete the parent. Node identity, generations, takeover, fencing and
  no-expiry are unchanged.
- HRC session keys, birth and claim/start work from the scope. Workspace routing
  uses the resident project, which the subtask inherits. Separate sessions do not
  imply separate worktrees or permission to edit the same files concurrently.
- Two agents on one subtask have different scopes, share the room, and one claim
  holder applies. A lane remains conversation isolation within a scope.
- Dispatch is an addressed `wrkc say` to the subtask handle. Creating a record
  does not start an agent. Named subtasks do not get a wrkf instance; attaching
  one directly is refused in v1.

## Wrkc rooms

The one routing change: **`routeToTaskUUID` computes the room from the owner when
the task is a subtask, and tags the envelope with the subtask.** Everything that
funnels through it (explicit `T-12345/slug`, a subtask handle as target or sender)
follows.

| Identity | Example | Purpose |
| --- | --- | --- |
| Room anchor | Owner task, or its campaign | Shared durable conversation |
| Message subject | `T-12345/architecture-diagram` | Filtering and `--record` |
| Recipient scope | `arris@demo:T-12345/architecture-diagram` | Working session and reply obligation |

```sh
wrkc say T-12345/architecture-diagram \
  --to arris@demo:T-12345/architecture-diagram - <<'MESSAGE'
Create an editable architecture diagram from the parent brief and decisions.
MESSAGE

wrkc log T-12345                                   # whole shared room
wrkc log T-12345 --task T-12345/architecture-diagram
```

- No subtask room rows are created. Subtasks cannot enroll in a campaign.
- Reading a subtask's history mirrors the owner's read rules, including a
  pre-enrollment task room. Old rooms are not merged or rewritten.
- Replies through `EN-XXXXX`, `--record`, bare-addressee fallback and obligations
  behave as today, applied to the subtask ID. If lookup finds several scopes for
  the same agent, a full handle is required.
- Say never hard-fails because of subtask state.

## Deletion and movement

Subtasks are contained; child tasks keep residency rules, including cross-project
detachment.

- **Owner delete/archive does not mutate subtasks.** New claims and creation under
  the owner are refused; the subtasks appear in the terminal-parent find. Owner
  restore needs no bookkeeping.
- **Purge is refused** for a subtask and for any task that owns one. Archive
  instead.
- Subtask deletion keeps the ID reserved, so a stale scope never addresses
  unrelated replacement work.
- Same-project owner moves carry residency atomically; a held claim refuses
  cross-project moves.

## Decomposition example

1. Create `T-12345/decompose` asking for a breakdown, dependencies and acceptance
   criteria. Dispatch it to its own scope.
2. The agent reads the parent brief and room, then creates child tasks
   (persistence, synchronization, conflict resolution), each with a global ID.
3. Each creation is recorded as a `created` relation from the subtask as it
   happens. On resume the agent reads those relations before creating more. Crash
   safety beyond that belongs to the future attempt foundation.
4. A reviewer checks coverage; `decompose` completes when accepted.
5. The child tasks stay open; their dispatch and completion are separate acts.

## Forward compatibility with the Latent Work Graph

| Graph concept | Status here | Why it is not blocked |
| --- | --- | --- |
| Subtask node, `has subtask` relation | Provided | `subtask_parent_uuid`, stable UUID and ID |
| Standalone sessions, combined projection | Out of scope | Subtasks add stable source identities; no graph store |
| Reconciliation activation | Out of scope | Push dispatch is unchanged; subtasks are explicit intent a reconciler can read |
| Shared attempt foundation | Out of scope | Claims give one holder per record; attempts can key on the same ID |
| Deterministic executors | Out of scope | No executor field is required; a handler updates the record like any caller |
| Return policy, durable result before notify | Partial | Results live on the subtask; notification remains explicit wrkc |
| Required contributions | Deferred | Workflow or later `required_for_parent` |
| Keep-current / awaiting-evidence | Out of scope | Subtask state stays open; no auto-completion |

## Delivery slices

Each slice lands, installs and passes installed acceptance before the next.
Producer before consumers: the canonical wrkqd on mini migrates first (install,
`wrkqadm migrate`, restart), then clients install. Protocol-hash pins mean an
older client refuses a newer server, so client publication is coordinated.

0. **Identity tokens** (agent-spaces, wrkq Go scope, hrc-runtime, ACP, wrkc
   consumers), in two activations: 0a role removal with `/` refused and refusals
   logged; 0b composite task tokens with a `T-` base, plus the path/URL escaping
   audit.
1. **Wrkq subtasks**: slug-collision audit and fixes, migration
   (`subtask_parent_uuid`, partial indexes), creation with composite IDs,
   ID-first and path selection, listing exclusion, ID-shape audit, owner-room
   routing, completion notice and find selector, delete/purge rules, RPC schema and
   Go/TS clients.
2. **Docs and records**: SPEC, CLI guides, identity contract and the active
   records above, updated to delivered behavior.

## Acceptance scenarios

Each installed run keeps command transcripts, JSON responses, database/event
readback and session references in the task artifact directory.

1. **Role removal.** Role handles and new `:role:` ScopeRefs are refused for birth,
   claim and dispatch; historical role sessions and envelopes still render; no
   pending obligation is silently reassigned.
2. **Composite tokens.** `arris@demo:T-12345/architecture-diagram` round-trips
   through ASP, the wrkq Go parser and HRC; a token with two `/` or a non-`T-`
   base (`primary/reviewer`) is refused; HRC session files, names and URLs handle
   the `/`.
3. **Identity.** `architecture-diagram` created under two tasks gets two IDs; the
   task sequence is unchanged; a duplicate under one owner is refused.
4. **Selection.** ID, full path and UUID round-trip through
   show/set/comment/attach/search/export/import. The subtask does not appear as a
   member of the parent's container. Container/task slug clashes are refused in
   both directions.
5. **No silent parent match.** A subtask ID in a wrkc message body, a git commit
   message and a just-recipe scope resolves to the subtask, not the parent.
6. **Child tasks unchanged.** Existing child tasks, including cross-project ones,
   keep IDs, paths, rooms and delete behavior.
7. **Two subtasks, one agent.** Two scopes and sessions, one owner room, two
   subject tags, independent obligations: replying from one leaves the other owed.
8. **Room history.** Ordinary owner, campaign owner and owner with a
   pre-enrollment room: current sends match owner routing; old-envelope replies
   keep their room and subject.
9. **Claims.** Sibling subtasks and the parent claimed concurrently; no token
   completes another record; takeover fences the old holder.
10. **Completion and environment.** Completing a parent with open subtasks succeeds
    and lists them; they stay claimable and findable. In a subtask session
    `wrkq set $AGENT_TASK --state completed` completes the subtask only.
11. **Movement and deletion.** Same-project parent move keeps IDs, scopes and room
    history. Owner delete leaves subtask states untouched and refuses new claims;
    purge of an owner or subtask is refused.
12. **Decomposition resume.** Interrupted after two child creations, resumed from
    recorded `created` relations with no duplicates.
13. **HRC down.** Subtask CRUD and shared-room messages work through wrkq/wrkc.

## Open items for Daedalus review

- Exact flag, RPC field and find-selector spellings.
- Whether any active record needs revision beyond the treatments listed above.

## Source map

Repository-root-relative unless prefixed with a sibling checkout.

- `internal/wrkqapi/tasks.go`, `internal/store/tasks.go`,
  `internal/db/migrations/000031_cross_project_parent_edges.sql` — creation, ID
  trigger, slug index, parent constraints.
- `internal/selectors/selectors_local.go` — ID and path resolution.
- `internal/wrkqapi/rooms.go` (`routeToTaskUUID`, `roomOrDerivedForTask`, mention
  pattern), `internal/store/rooms.go` — routing and obligations.
- `internal/wrkqapi/claims.go`, `internal/id/id.go`, `internal/causedby/`,
  `internal/rpccli/wrkp_git.go`, `internal/rpccli/wrkp_just.go` — claims and
  ID-shape consumers.
- `internal/scope/`, `internal/attribution/` — Go scope parser and attribution.
- agent-spaces: `contracts/agent-scope/src/{types,scope-ref,scope-handle,input}.ts`,
  `docs/identity-scope-and-env-contract.md`.
- hrc-runtime: `packages/hrc-sdk/src/resolve-scope.ts`,
  `packages/hrc-core/src/selectors.ts`, `packages/hrc-server/src/scope-claim-core.ts`,
  `packages/hrc-cli/src/session-render.ts`,
  `packages/hrc-server/src/command-run-helpers.ts`.
- architecture-assessment: `graph-session-handoff.md`,
  `graph-detailed-artifact-reference.md` — design context.
