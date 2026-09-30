# Named subtasks in wrkq

September 29, 2026 · Proposal, revision 4 · Not approved for implementation

Authors: Astra (revision 1, **T-09873**); Mable (revisions 2–3, from Lance's
review); Clod (revision 4: `.` separator, role removal dropped, HRC consumer
audit, listing and residency invariants).
Design context: `architecture-assessment/graph-session-handoff.md` (Latent Work
Graph). Where this document and the handoff differ on subtask mechanics, this
document is the concrete proposal; the handoff remains the conceptual direction.

## Decision summary

Add **named subtasks**: assignments inside an existing wrkq task. A subtask is an
ordinary task row whose ID is its owner's ID, a `.`, and a local slug:

```text
mvp/                                  container
  offline-editing/                    T-12345   Add offline editing
    decompose                         T-12345.decompose
    architecture-diagram              T-12345.architecture-diagram
    render-preview                    T-12345.render-preview
```

**The composite ID is the design.** `T-12345.architecture-diagram` is at once the
stored ID, the selector, the scope's task token and the handle suffix. Because it
is a real, non-empty, stable, unique task ID, existing task logic keeps working:
ID lookup, claims, scope matching, session keys, attribution, obligations,
comments, attachments, events and `AGENT_TASK`. No global sequence number is
allocated, so Lance's "no new `T-XXXXX`" requirement holds.

**The composite ID is already a valid scope token.** ASP and the wrkq Go scope
parser accept `[A-Za-z0-9._-]{1,64}`, so `arris@demo:T-12345.architecture-diagram`
parses, renders and round-trips today. No identity-grammar change, role removal
or escaping audit is needed (see *Why `.`*).

The remaining changes are the places where a subtask must behave differently
from a task, plus consumers that assume an ID shape:

1. **ID-shape consumers** in wrkq, hrc-runtime, ACP and taskboard that match
   `T-\d+` learn the optional `.slug` suffix. Several unanchored matchers would
   otherwise quietly act on the owner; one (HRC worktree prune) could delete a
   live seat's worktree. These land first.
2. **Room routing** sends a subtask's traffic through its owner's effective room.
3. **Listing exclusion**: one shared predicate keeps subtasks out of container
   membership, slug uniqueness, listings and path resolution unless a query asks
   for them.
4. **Parent completion** reports open subtasks as a notice; it never refuses.
5. **Purge** is refused for a subtask or any task that owns one.

The proposal needs no graph interface, reconciler, executor catalog, attempt
engine or wrkf redesign, and does not preclude them (see *Forward compatibility*).
Wrkq remains usable with HRC unavailable.

## Why `.` and not `/`

Revision 3 spelled subtasks `T-12345/slug`. In handles `/` already means role
(`alice@demo:t1/reviewer`), and ASP tokens exclude `/`. That spelling required a
cross-repository identity release before any subtask existed: remove role from
ASP, the wrkq parser, HRC, ACP and wrkc; activate in two steps so a stale role
handle could not birth a seat for a task named `T-12345/reviewer`; make HRC ask
wrkq before birthing a composite scope; and audit every filesystem path, tmux
name, URL and log key for `/` escaping.

`.` avoids all of it:

- It is inside the existing token charset, so a subtask scope is an ordinary
  `project-task` scope with no parser change in any repository.
- wrkq slugs match `^[a-z0-9][a-z0-9-]*$` and never contain `.`, so the first
  `.` in a task ID unambiguously separates owner from slug.
- It is safe in paths, tmux names, URLs, git branch names and log keys.
- Role handles are unaffected. `mable@wrkq:T-12345/reviewer` keeps meaning what it
  means today, and cannot be confused with `T-12345.reviewer`.

Paths still use `/` (`mvp/offline-editing/architecture-diagram`). The ID and the
path are different selectors and look different. Role removal, if still wanted,
is independent cleanup and is out of scope here.

## Why subtasks and not campaigns plus child tasks

Most of this experience is available today: make the parent a campaign, enroll
child tasks, and dispatch each to `agent@project:T-child`. Tasks get distinct
scopes and claims, share the campaign room, and are filterable with
`wrkc log <campaign> --task T-x` (`docs/wrkc-reference.md`, routing rule 2).

What that arrangement cannot provide:

- **Readable, local names.** Lance rejected numeric local addresses.
  `T-12345.architecture-diagram` says what the work is and where it belongs; a
  fourth `T-` number does neither. This is the primary requirement.
- **A task-owned shared room without becoming a campaign.** A code task that grows
  a diagram, a render and a decomposition should not have to become a campaign
  container to share its conversation. Routing child tasks into a non-campaign
  parent's room would give the room but not the names, and would change the
  meaning of every existing child-task room.
- **A distinct node class in the combined graph.** The graph's MVP node types
  include Subtask separately from Task, with `has subtask` distinct from
  `has child task`.
- **Lifecycle within the owner's context.** Subtasks are non-blocking and may
  finish after the owner closes; they inherit residency and room.

Child tasks remain the tool for independently managed deliverables, including
cross-project ones. The distinction follows how work is managed, not its size.

## Task, child task and subtask

| Property | Task / child task | Named subtask |
| --- | --- | --- |
| Work boundary | Independently managed deliverable | Assignment in an existing task's context |
| ID | `T-12345` from the global sequence | `T-12345.slug`; no sequence number |
| Parent relationship | Optional child-task edge; may cross residency | Required owner, fixed by the ID; inherits residency |
| Discussion | Own effective room, including campaign coalescing | Owner's effective room |
| Agent scope | `agent@project:T-12345` | `agent@project:T-12345.slug` |
| Workspace | Task worktree if one exists | Owner's workspace |
| Completion | Existing task contract | Independent; never gates the owner (v1) |

For the first release a subtask cannot own subtasks or child tasks, so an ID has
at most one `.`. An ordinary child task can own named subtasks
(`T-12400.render-preview`). A decomposition subtask creates child tasks whose
parent is the owning task and records `created` relations to them.

## Current baseline

Source inspection on September 28–29, 2026: wrkq `ae861e8`, agent-spaces
`46a1e4e8`, hrc-runtime `ca0e3dd6`, plus HRC `state.sqlite` readback. These are
source and data observations, not claims of installed subtask behavior.

- Task creation requires only a title. A migration-000031 trigger allocates a
  global ID only when `id` is NULL, so an insert that supplies the composite ID
  consumes no sequence value. `tasks.id` has only `CHECK (length(id) > 0)`.
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
- ASP `TOKEN_PATTERN` (`contracts/agent-scope/src/types.ts`) and wrkq
  `scope.TokenPattern` (`internal/scope/types.go`) are both
  `^[A-Za-z0-9._-]+$`, 1–64 characters.
- Attachments are stored by task UUID (`internal/attach/attach.go`), not ID.
- HRC places a task seat by extracting `T-\d+` tokens from the scope and matching
  worktree branches and paths (`hrc-core/src/placement-policy.ts`).

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

## ID grammar

```text
task-id      = base-id | subtask-id
base-id      = "T-" 5DIGIT
subtask-id   = base-id "." subtask-slug
subtask-slug = %x61-7A *( %x61-7A / DIGIT / "-" ) ( %x61-7A / DIGIT )   ; a-z first, no trailing "-"
```

- The whole ID is at most 64 characters (the scope token limit), so a slug is at
  most 56.
- A subtask slug starts with a letter and does not end with `-`. This keeps
  `T-12345.2` (a version-like string) from being a subtask ID, and gives prose
  matchers a clean right edge.
- **Prose boundary.** A matcher extended to
  `\bT-\d{5}(?:\.[a-z](?:[a-z0-9-]*[a-z0-9])?)?\b` reads `see T-12345.` (sentence
  end) and `T-12345.Next` as the owner, and `T-12345.render-preview` as the
  subtask. Tests pin all three.
- One shared definition per language: wrkq `internal/id`, and an exported helper
  in hrc-core that ACP and taskboard import or mirror against shared fixtures.

## Addresses

Three selectors identify a subtask:

```text
wrkq cat T-12345.architecture-diagram                # the ID
wrkq cat mvp/offline-editing/architecture-diagram    # full path
wrkq cat <subtask-uuid>
```

1. Slugs are unique under their owner, including deleted records, which the
   unique ID enforces directly. Different tasks can each own
   `architecture-diagram`.
2. Slug and owner are immutable in the first release, which is what keeps the ID
   stable. Titles are editable. Retry and reopen reuse the same record.
3. **ID-first selection.** A selector matching `task-id` is resolved by exact ID
   before any path walk. Container and task slugs are lowercase and contain no
   `.`, so they cannot be mistaken for IDs.
4. **Path walk.** Containers are walked, a task is reached, then at most one
   subtask segment. A subtask is never matched as a container member, and a bare
   slug never resolves to a subtask.
5. **Container/task slug disjointness.** Creating, renaming or moving a task or
   container is refused when a sibling of the other kind has the same slug. The
   wrkq slice includes a one-time audit of existing collisions, each fixed by an
   explicit rename before the constraint is enabled. A later container can then
   never break a path already written in mail.
6. Moving or renaming the owner changes the displayed full path and leaves the
   ID, and so every scope, unchanged.
7. Moving the owner across projects is refused while any task or subtask claim is
   held. After release, new dispatch uses the new project. Old-scope mail remains
   visible and is resolved explicitly, never rebound to a new seat.
8. Promotion to a task, reparenting and slug renaming are deferred. Each would
   change the ID; if added they preserve UUID and history and handle the scope
   change explicitly.

## Storage

A subtask is a row in `tasks`. Every existing column keeps its meaning.

| Field | Ordinary task | Named subtask |
| --- | --- | --- |
| `id` | `T-12345` from the sequence | `T-12345.slug`, built by the server |
| `subtask_owner_uuid` (new) | NULL | Owner's UUID; this is the discriminator |
| `parent_task_uuid` | Optional child-task edge | NULL |
| `slug` | Unique in resident container | Unique under owner |
| resident container (`project_uuid`) | Authoritative | Materialized from owner |
| `campaign_uuid` | Existing enrollment | NULL; membership follows owner |

- `subtask_owner_uuid IS NOT NULL` identifies a subtask. `kind=subtask` is never
  authoritative. Existing rows keep NULL, including legacy `kind=subtask` child
  tasks, whose label stays valid; new `--parent-task` creation defaults to
  `kind=task`.
- `tasks_unique_slug_in_container` becomes partial (`WHERE subtask_owner_uuid IS
  NULL`); a second unique index covers `(subtask_owner_uuid, slug)`. The server
  builds the ID from the owner's ID and the slug and never accepts a
  client-supplied ID.
- Constraints: the owner exists and is an ordinary task (not itself a subtask);
  `parent_task_uuid` and `campaign_uuid` are NULL on a subtask.
- API and CLI responses add `subtaskOwner` beside the existing `parentTask`, so
  the two relationships are never confused.

### Invariant: materialized residency never drifts

A subtask's `project_uuid` always equals its owner's. Materializing it lets
workspace routing and existing project-scoped reads work unchanged, at the cost of
a copy that every writer must maintain.

- One store function writes residency for an owner and its subtasks in the same
  transaction. Every path that changes a task's `project_uuid` calls it: move,
  the move cascade, generic RPC update, restore, snapshot import and bulk apply.
  No API writes a subtask's residency directly.
- A SQL trigger refuses any update that leaves a subtask's `project_uuid`
  different from its owner's, so a missed writer fails loudly instead of drifting.
- A test enumerates every `UPDATE tasks ... project_uuid` site in the store and
  asserts each goes through the shared function.

### Invariant: one listing predicate

Because subtasks carry their owner's `project_uuid`, every existing query that
selects tasks by container would include them by default: `ls`, `find`, `tree`,
counts and rollups, `search`, container purge/archive cascades, snapshot export,
webhook fan-out, the task-view projections and taskboard's list RPCs.

- The store exposes one predicate (`taskMemberFilter`, spelling open) that
  excludes subtasks, and one explicit opt-in that includes them. Every
  container-scoped task query uses one or the other; none writes its own
  `subtask_owner_uuid` condition.
- A test lists every store query that reads `tasks` by container and fails when a
  new one bypasses the predicate. A second test builds a fixture with a subtask
  in every such view and asserts it is absent by default.
- Explicitly subtask-aware reads (`cat <owner>`, `ls <owner> --subtasks`, `tree`
  owner rows, `find --subtasks`, export/import) opt in by name.

## ID-shape audit

Every site that recognizes task IDs is updated to the grammar above, with tests
where a subtask ID appears. The inventory below is from source at the baseline
commits; it is regenerated with `rg --no-ignore 'T-\\d|T-\[0-9\]'` across wrkq,
hrc-runtime, agent-control-plane, agent-spaces and taskboard at slice start, and
the counts here are a floor.

**Unsafe: acts on the owner or drops the subtask (must fix, must test).**

| Site | Today | Hazard with `T-12345.slug` |
| --- | --- | --- |
| hrc-runtime `hrc-cli/src/worktree-prune.ts:315` | `task:(T-\d+)(?::|$)` | No match: a live subtask seat does not protect its owner's worktree, so the janitor can prune a worktree in use. **Data loss.** |
| hrc-runtime `hrc-core/src/placement-policy.ts:136` | `(?<!\d)T-\d+(?!\d)` | Extracts the owner. Correct outcome by accident; made deliberate (below). |
| hrc-runtime `hrc-cli/src/turn/taskId.ts:19` | `T-\d+\b` | Returns the owner as the turn's task. |
| hrc-runtime `hrc-cli/src/worktree-prune.ts:199` | `(?<!\d)T-\d+(?!\d)` | Reads the owner from branch names; consistent with placement once deliberate. |
| wrkq `internal/wrkqapi/rooms.go:275` mention pattern | `\bT-\d{5}\b` | A subtask mention links to the owner. |
| wrkq `internal/rpccli/wrkp_git.go:22` commit attribution | `\bT-\d{5}\b` | A commit for a subtask is attributed to the owner. |
| wrkq `internal/rpccli/wrkp_just.go:54` scope extraction | `:task:(T-\d{5})(?:/|$)` | No match: a subtask scope's just runs lose task attribution. |

**Loud: refuses a composite ID (widen).** wrkq `internal/id/id.go`
(`^T-\d{5}$`), `internal/causedby/causedby.go`, hrc-runtime
`hrc-cli/src/monitor/selector-shape.ts` (`^T-\d+$`),
`hrc-server/src/wrkq/session-project-events.ts` (`^T-\d{5}$`),
`hrc-core/src/placement-policy.ts:259` (`refineTaskWorktree`, `^T-\d+$`: a
subtask token passed through unstripped gets no worktree), and similar anchored
validators.

**Out of scope.** `ARCH-EXCEPTION(T-…)` lint markers (surfaceguard,
suppressionlint, layerguard, rotguard) keep requiring ordinary task IDs.

### Workspace placement and prune

- **Placement** takes the owner ID from a subtask token (the part before `.`) and
  applies today's worktree matching to it, including the anchored check in
  `refineTaskWorktree`, which receives the owner ID, never the composite token. A subtask seat therefore works in its
  owner's worktree, or the canonical checkout when the owner has none. Subtasks
  never get their own worktree in v1. Separate sessions still do not imply
  permission to edit the same files concurrently.
- **Prune** treats a live seat on `T-12345.slug` as holding `T-12345`. Its
  acceptance test puts a subtask seat on an owner worktree with no owner seat and
  asserts the worktree survives a prune pass.

## Lifecycle and completion

Subtasks use existing task states, transitions, claims and optimistic concurrency.

- Completing a subtask changes nothing else.
- Completing an owner does not complete, cancel, hide or release its subtasks.
  **The completion response and CLI output list the owner's open subtasks** as a
  notice. They never refuse the transition.
- Open subtasks under a terminal owner stay claimable, replyable and findable.
  `wrkq find --subtasks --owner-state terminal` lists them (the subtasks' own
  state follows find's default actionable set; `terminal` means completed,
  cancelled, archived or deleted). Room discovery shows a count of unfinished
  subtasks. Together these keep them from quietly rotting.
- Owner `blocked`/`cancelled` is context, not a mutation of subtasks. No state
  change stops an HRC process.
- No subtask creation or claim under a deleted or archived owner. Existing
  conversations remain readable and replyable.
- Reopening a subtask does not reopen its owner.

**Required contributions are deferred.** In v1 they are expressed by a workflow on
the owner or by the owner's assignee checking before completion. A later
`required_for_owner` flag fits the schema; when added it must be enforced in
every writer, including bulk/import and wrkf's task-state projection.

Typed relations work unchanged between any task IDs, so `T-12345.render-preview`
may depend on `T-12345.architecture-diagram` with existing blocker semantics.
Results are comments, outcome and attachments on the subtask. Process exit or a
wrkc reply never sets `completed`.

## Commands

```sh
# Explicit flag distinguishes a subtask from a task/container typo.
wrkq touch T-12345.architecture-diagram --subtask \
  -t 'Explain the offline editing architecture' -d 'Editable diagram and preview.'

wrkq cat mvp/offline-editing/architecture-diagram
wrkq set T-12345.architecture-diagram --state in_progress
wrkq comment add T-12345.architecture-diagram -m 'Draft ready for review.'

wrkq cat T-12345                          # owner detail lists its subtasks
wrkq ls T-12345 --subtasks
wrkq tree mvp                             # subtasks shown under their owner
wrkq find --subtasks --owner-state terminal
```

- `--parent-task` keeps meaning "child task"; docs and output say so.
- Container listings, `tree` rollups and existing task-only machine queries keep
  their meaning and exclude subtasks unless explicitly included (the listing
  predicate above). `tree` never folds open subtasks into an "all done" owner; it
  shows "complete; 2 subtasks open".
- Owner detail shows subtask summaries separately from its own content. Subtask
  detail shows its own brief plus owner reference and room locator.
- New RPC request fields follow the unknown-param refusal rule; the schema
  catalog and Go/TypeScript clients change together.

## Sessions, environment and claims

A subtask session is a task session whose task token is the composite ID. Because
the token is already valid, all of the following is today's logic applied to that
ID:

- ScopeRef `agent:arris:project:demo:task:T-12345.architecture-diagram`; handle
  `arris@demo:T-12345.architecture-diagram`; session handle
  `arris@demo:T-12345.architecture-diagram~planning`. No new scope kind and no
  `subtaskId` field.
- `AGENT_TASK=T-12345.architecture-diagram`. A skill running
  `wrkq set $AGENT_TASK --state completed` completes the subtask, not the owner.
  Agents that need the owner read it from the subtask record.
- `wrkq claim` records the holder on the subtask row. Owner and sibling claims
  are independent because their IDs differ; a subtask token cannot claim or
  complete the owner. Node identity, generations, takeover, fencing and
  no-expiry are unchanged.
- HRC session keys, birth and claim/start work from the scope. A handle naming a
  subtask that does not exist births a seat exactly as a handle naming a missing
  `T-99999` does today; this proposal adds no new birth check.
- Two agents on one subtask have different scopes, share the room, and one claim
  holder applies. A lane remains conversation isolation within a scope.
- Dispatch is an addressed `wrkc say` to the subtask handle. Creating a record
  does not start an agent. Named subtasks do not get a wrkf instance; attaching
  one directly is refused in v1.

## Wrkc rooms

The one routing change: **`routeToTaskUUID` computes the room from the owner when
the task is a subtask, and tags the envelope with the subtask.** Everything that
funnels through it (explicit `T-12345.slug`, a subtask handle as target or sender)
follows.

| Identity | Example | Purpose |
| --- | --- | --- |
| Room anchor | Owner task, or its campaign | Shared durable conversation |
| Message subject | `T-12345.architecture-diagram` | Filtering and `--record` |
| Recipient scope | `arris@demo:T-12345.architecture-diagram` | Working session and reply obligation |

```sh
wrkc say T-12345.architecture-diagram \
  --to arris@demo:T-12345.architecture-diagram - <<'MESSAGE'
Create an editable architecture diagram from the parent brief and decisions.
MESSAGE

wrkc log T-12345                                   # whole shared room
wrkc log T-12345 --task T-12345.architecture-diagram
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
  the owner are refused; the subtasks appear in the terminal-owner find. Owner
  restore needs no bookkeeping.
- **Purge is refused** for a subtask and for any task that owns one. Archive
  instead.
- Subtask deletion keeps the ID reserved, so a stale scope never addresses
  unrelated replacement work.
- Same-project owner moves carry residency atomically (residency invariant); a
  held claim refuses cross-project moves.

## Decomposition example

1. Create `T-12345.decompose` asking for a breakdown, dependencies and acceptance
   criteria. Dispatch it to its own scope.
2. The agent reads the owner brief and room, then creates child tasks
   (persistence, synchronization, conflict resolution), each with a global ID.
3. Each creation is recorded as a `created` relation from the subtask as it
   happens. On resume the agent reads those relations before creating more. Crash
   safety beyond that belongs to the future attempt foundation.
4. A reviewer checks coverage; `decompose` completes when accepted.
5. The child tasks stay open; their dispatch and completion are separate acts.

## Forward compatibility with the Latent Work Graph

| Graph concept | Status here | Why it is not blocked |
| --- | --- | --- |
| Subtask node, `has subtask` relation | Provided | `subtask_owner_uuid`, stable UUID and ID |
| Standalone sessions, combined projection | Out of scope | Subtasks add stable source identities; no graph store |
| Reconciliation activation | Out of scope | Push dispatch is unchanged; subtasks are explicit intent a reconciler can read |
| Shared attempt foundation | Out of scope | Claims give one holder per record; attempts can key on the same ID |
| Deterministic executors | Out of scope | No executor field is required; a handler updates the record like any caller |
| Return policy, durable result before notify | Partial | Results live on the subtask; notification remains explicit wrkc |
| Required contributions | Deferred | Workflow or later `required_for_owner` |
| Keep-current / awaiting-evidence | Out of scope | Subtask state stays open; no auto-completion |

## Delivery slices

Each slice lands, installs and passes installed acceptance before the next.
Producer before consumers: the canonical wrkqd on mini migrates first (install,
`wrkqadm migrate`, restart), then clients install. Protocol-hash pins mean an
older client refuses a newer server, so client publication is coordinated.

1. **ID-shape consumers** (hrc-runtime, agent-control-plane, taskboard, and the
   wrkq matchers that do not need the schema): shared grammar helper and
   fixtures, every unsafe and loud site above, deliberate placement and prune.
   This slice is inert until subtask IDs exist, so it lands and activates first;
   it must be live on every node before slice 2 can create a subtask, because the
   prune hazard is data loss.
2. **Wrkq subtasks**: slug-collision audit and fixes, migration
   (`subtask_owner_uuid`, partial indexes, residency trigger), creation with
   composite IDs, ID-first and path selection, the listing predicate and its
   enumeration tests, owner-room routing, completion notice and terminal-owner
   find, delete/purge rules, RPC schema and Go/TS clients.
3. **Docs and records**: SPEC, CLI guides, the identity contract (noting the
   composite task token), and the active records above, updated to delivered
   behavior.

## Acceptance scenarios

Each installed run keeps command transcripts, JSON responses, database/event
readback and session references in the task artifact directory.

1. **Tokens.** `arris@demo:T-12345.architecture-diagram` round-trips through ASP,
   the wrkq Go parser and HRC with no parser change; the role handle
   `arris@demo:T-12345/reviewer` still means role.
2. **Grammar.** `T-12345.2`, `T-12345.a.b`, `T-12345.slug-` and a >64-character
   ID are not subtask IDs; prose `see T-12345.` and `T-12345.Next` resolve to the
   owner in the mention, commit and scope matchers.
3. **Prune safety.** A live seat on `T-12345.slug` with no owner seat keeps the
   owner's worktree through a prune pass; the seat is placed in that worktree.
4. **Identity.** `architecture-diagram` created under two tasks gets two IDs; the
   task sequence is unchanged; a duplicate under one owner is refused.
5. **Selection.** ID, full path and UUID round-trip through
   show/set/comment/attach/search/export/import. Container/task slug clashes are
   refused in both directions.
6. **Listing exclusion.** A fixture with a subtask shows it in no default
   container-scoped view (`ls`, `find`, `tree` counts, `search`, export listing,
   taskboard list) and in each explicit opt-in.
7. **Residency.** Owner move, generic RPC update, restore and snapshot import each
   leave subtask residency equal to the owner's; a direct update that would
   diverge is refused by the trigger.
8. **No silent owner match.** A subtask ID in a wrkc message body, a git commit
   message and a just-recipe scope resolves to the subtask, not the owner.
9. **Child tasks unchanged.** Existing child tasks, including cross-project ones,
   keep IDs, paths, rooms and delete behavior.
10. **Two subtasks, one agent.** Two scopes and sessions, one owner room, two
    subject tags, independent obligations: replying from one leaves the other
    owed.
11. **Room history.** Ordinary owner, campaign owner and owner with a
    pre-enrollment room: current sends match owner routing; old-envelope replies
    keep their room and subject.
12. **Claims.** Sibling subtasks and the owner claimed concurrently; no token
    completes another record; takeover fences the old holder.
13. **Completion and environment.** Completing an owner with open subtasks
    succeeds and lists them; they stay claimable and appear in
    `find --subtasks --owner-state terminal`. In a subtask session
    `wrkq set $AGENT_TASK --state completed` completes the subtask only.
14. **Movement and deletion.** Same-project owner move keeps IDs, scopes and room
    history. Owner delete leaves subtask states untouched and refuses new claims;
    purge of an owner or subtask is refused.
15. **Decomposition resume.** Interrupted after two child creations, resumed from
    recorded `created` relations with no duplicates.
16. **HRC down.** Subtask CRUD and shared-room messages work through wrkq/wrkc.

## Open items for Daedalus review

- Spellings of the listing predicate, the create RPC fields and `--owner-state`.
- Whether any active record needs revision beyond the treatments listed above.

## Source map

Repository-root-relative unless prefixed with a sibling checkout.

- `internal/wrkqapi/tasks.go`, `internal/store/tasks.go`,
  `internal/db/migrations/000031_cross_project_parent_edges.sql` — creation, ID
  trigger, slug index, parent constraints, residency writers.
- `internal/selectors/selectors_local.go` — ID and path resolution.
- `internal/paths/slug.go` — slug charset.
- `internal/wrkqapi/rooms.go` (`routeToTaskUUID`, `roomOrDerivedForTask`, mention
  pattern), `internal/store/rooms.go` — routing and obligations.
- `internal/wrkqapi/claims.go`, `internal/id/id.go`, `internal/causedby/`,
  `internal/rpccli/wrkp_git.go`, `internal/rpccli/wrkp_just.go` — claims and
  ID-shape consumers.
- `internal/scope/types.go`, `internal/attribution/` — Go token pattern and
  attribution.
- agent-spaces: `contracts/agent-scope/src/types.ts` (`TOKEN_PATTERN`),
  `docs/identity-scope-and-env-contract.md`.
- hrc-runtime: `packages/hrc-core/src/placement-policy.ts`,
  `packages/hrc-cli/src/worktree-prune.ts`, `packages/hrc-cli/src/turn/taskId.ts`,
  `packages/hrc-cli/src/monitor/selector-shape.ts`,
  `packages/hrc-server/src/wrkq/session-project-events.ts`.
- architecture-assessment: `graph-session-handoff.md`,
  `graph-detailed-artifact-reference.md` — design context.
