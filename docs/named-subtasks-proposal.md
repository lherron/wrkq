# Named subtasks in wrkq

September 28, 2026 · Proposal for discussion · Not approved for implementation

Author: Astra, from Lance's task/subtask design discussion. Authoring record:
**T-09873 · Write standalone named subtasks proposal**.

## Purpose and decision

Add named subtasks as lightweight, independently executable assignments inside a
wrkq task. A subtask has a readable parent-local address, its own state and claim,
and its parent's context and effective wrkc room. It does not receive a new global
`T-XXXXX` ID.

For example, within project `demo`:

```text
mvp/                                  container
  offline-editing/                     task: Add offline editing
    decompose                         subtask
    architecture-diagram              subtask
    render-preview                    subtask
```

A user or agent can read `mvp/offline-editing/architecture-diagram` in a message
and know what work it identifies. A specialist can execute that assignment in a
separate scope while participating in the parent's conversation.

This proposal stands alone. It requires ordinary wrkq records, wrkc communication
and supporting identity/runtime changes. It does not require a graph interface,
reconciler, executor catalog, new attempt engine or wrkf redesign. Creation and
execution work through explicit commands and addressed dispatch. Wrkq remains
usable with HRC unavailable.

Lance selected the following product direction:

- Use named subtasks with parent-local slugs instead of a separate activity type
  or numeric local addresses.
- Retain independent child tasks as a distinct relationship.
- Share the parent's effective room while using separate scopes for separate
  subtask assignments.
- Remove the `role` dimension from ScopeRef and add explicit subtask support;
  do not reinterpret role as a subtask.

The storage layout, command spelling and bounded first-release choices below are
recommendations in this proposal. They require architecture review before build.

## Task, child task and subtask

| Property | Task / child task | Named subtask |
| --- | --- | --- |
| Work boundary | Independently managed deliverable | Assignment within an existing task's context |
| Stable record identity | UUID | UUID |
| Human address | Global task ID and container/task path | Parent task address plus subtask slug |
| Global task number | Allocated | Never allocated |
| Parent relationship | Optional child-task edge; may cross residency | Required owning task; inherits its residency |
| Discussion | Own effective room, including campaign coalescing | Parent's effective room |
| Agent scope | Agent + project + task | Agent + project + parent task + subtask |
| Content | Own brief, references to other context | Own instructions plus retrievable parent context |
| Completion | Existing task completion contract | Independent state; optional by default |

A child task is an ordinary task with an independent identity and an optional
parent-task relationship. It retains its resident container and existing campaign
semantics. A subtask is owned by one task and cannot independently select a
container, campaign or room.

The distinction follows how the work is managed, not its duration or size. A
focused investigation can be a subtask; a separately owned research deliverable
can be a child task. A decomposition subtask can create independent child tasks.

For the first release, a subtask cannot own subtasks or child tasks. A decomposition
records `created` references to child tasks whose parent is the owning task. An
ordinary child task can itself own named subtasks. This does not increase the
existing maximum depth of independent child-task edges.

## Current baseline and constraints

Source inspection on September 28, 2026: wrkq `f28700a`, agent-spaces `46a1e4e8`.
A targeted HRC consumer inspection used `ca0e3dd6`. These are source observations,
not claims of installed subtask behavior.

- Task creation already has little mandatory content: the API requires title;
  the CLI can derive it from the supplied path. Container/slug resolution and
  defaults supply state, priority and kind. Descriptions/specifications may be
  empty. There is no required executor or acceptance schema.
- `tasks.parent_task_uuid` currently means a bounded child-task graph edge. The
  existing CLI calls such records “subtasks” and usually sets `kind=subtask`.
  They still receive global IDs and retain independent residency. This legacy
  terminology must be separated from the new named-subtask semantics.
- Paths currently end at a task under a container. They do not traverse a task
  into a locally named assignment.
- A database trigger allocates a global task ID for every task lacking one.
- Wrkc already separates room anchor from envelope task tag for campaign sharing.
  Its task-room resolver does not consult parent-task edges.
- Wrkq claims already provide generation-fenced holdership without a workflow
  instance. They have no lease or TTL. Their scope checks assume a global task ID.
- ASP distinguishes ScopeRef from SessionRef (scope plus lane). Its current
  optional qualifier is role. Wrkq also has a Go implementation of that grammar.
  HRC uses role in selector defaults, monitoring and claim/start plumbing.

Relevant active architecture records, which remain authoritative until deliberately
revised by an implementation:

| Record | Required treatment |
| --- | --- |
| `wrkq.task-hierarchy.cross-project-parents` | Preserve independent child-task residency, depth and cross-project deletion boundaries. Add separate named-subtask containment. |
| `wrkq.collaboration-ledger.authority` | Preserve wrkq ownership, parent campaign coalescing, per-scope obligations and rooms that accept messages after task completion. |
| `wrkq.task-claim.authority` | Extend exact claim identity to a subtask; preserve node derivation, generation fencing and no lease/TTL. |
| `wrkq.task-view.caller-state-selection` | Explicitly specify subtask selection and completion displays without weakening existing task rollups. |
| `wrkq.attribution.caller-principal-exact` | Preserve caller principal and full scope attribution. |
| `wrkq.rpc.remote-transport-locator` | Keep resolution/storage on the canonical server; update machine contracts and remote consumers together. |

Scope strings are coordination identities, not authenticated proof of agent
identity. Existing node-authentication and attribution boundaries remain intact.

## Addresses and stable identity

Three selectors identify a subtask:

```text
wrkq cat mvp/offline-editing/architecture-diagram
wrkq cat T-12345/architecture-diagram
wrkq cat <subtask-uuid>
```

Here and below `T-12345` is the example parent **Add offline editing**, and commands
run in project `demo`. Syntax in this document is proposed, not installed.

The full root-relative path is `/demo/mvp/offline-editing/architecture-diagram`.
Existing project scoping applies to relative paths. A bare subtask slug does not
search globally or silently bind to an ambient task; use the parent-qualified
selector. In human output, always render a descriptive path, never only a UUID or
a generated sequence number.

Recommended first-release address rules:

1. A subtask slug is lowercase letters, digits and hyphens, starts with a letter
   or digit, and is 1–64 characters so it fits the scope token contract.
2. Slugs are unique under the owning task, including deleted records. Different
   tasks can each own `architecture-diagram`.
3. The slug and owning task are immutable in the first release. Titles are freely
   editable. Retry/reopen uses the same record. This keeps the readable scope
   stable without an alias service or runtime identity migration.
4. Moving/renaming a parent within its project changes the displayed full path but
   preserves the subtask UUID and `T-12345/architecture-diagram` selector. Task IDs
   remain stable. Existing stale full paths get the same treatment as task paths;
   this proposal does not promise permanent full-path aliases.
5. Moving the parent between top-level projects is refused while any task/subtask
   claim is held. After release, an explicit move changes future scope project
   identity. Historical messages keep their original scopes; new dispatch uses
   the new project. Unanswered old-scope mail remains visible and must be resolved
   explicitly, rather than silently rebound to a new seat.
6. Promotion to an independent task, subtask reparenting and subtask slug renaming
   are deferred. If later added, they require preserved UUID/history and explicit
   scope handling; they must not be implemented as delete-and-recreate shortcuts.

Path resolution must distinguish container traversal, an ordinary task, and its
one final subtask segment. A subtask cannot appear before the final segment.
Where an existing container and task collide at the same path prefix, refuse the
ambiguous path and offer the unambiguous `T-12345/slug` selector. Do not guess based
on whichever query happens to run first. Creation must report such a collision.

## Work records and storage recommendation

Reuse task content, states, comments, attachments, events, relations, search,
attribution and claim machinery. Recommended implementation: extend the existing
task-backed record model with a structural discriminator and a distinct owning
parent reference. Do not make `kind=subtask` alone authoritative: that value
already exists with different semantics, and kind also describes work categories.

Illustrative storage changes, subject to schema review:

| Field | Ordinary task | Named subtask |
| --- | --- | --- |
| `record_class` | `task` | `subtask` |
| `uuid` | Generated stable UUID | Generated stable UUID |
| `id` | Existing global task ID | NULL |
| `subtask_parent_uuid` | NULL | Required ordinary task UUID |
| `parent_task_uuid` | Existing optional child-task edge | NULL |
| `slug` | Unique in resident container | Unique under owning task |
| `project_uuid` | Authoritative residency | Inherited/materialized from parent |
| `campaign_uuid` | Existing enrollment | NULL; effective membership follows parent |
| `required_for_parent` | Not used for this proposal | Boolean, default false |

Keep the same core content fields: title, description, specification, state,
priority, labels, assignee, optional dates, outcome and attribution. Do not require
an agent/executor, a workflow, a separate handoff, or a structured completion
contract to create a subtask. Optional machine fields can be proposed separately
when a concrete consumer needs them.

All existing rows migrate to `record_class=task`, including old `kind=subtask`
rows. They retain IDs, parent edges, scopes, rooms, comments and attachments.
Use separate uniqueness constraints for container task slugs and parent subtask
slugs. Global-ID allocation runs only for `record_class=task`. Subtasks must not
consume sequence values or generate hidden global IDs.

The server maintains inherited residency in the same transaction as parent moves;
no API can write a different resident container to a subtask. Foreign keys and
validation prevent subtask ownership cycles and orphan records. Parent lookup is
by UUID, not repeated path-string matching.

The nullable ID affects more than insertion. Audit SQL scans, DTOs, error messages,
claims, event payloads, search, bundles, import/export and every client that assumes
a task row always has an ID. Never turn NULL into an empty displayed ID. Return
explicit record class, UUID, readable path and parent information.

## Lifecycle, completion and dependencies

Subtasks use wrkq's existing task-state vocabulary and permissive transitions:
`idea`, `draft`, `open`, `in_progress`, `blocked`, `completed`, `cancelled`,
`archived`, `deleted`. They do not introduce an activity state machine. Claims
and optimistic concurrency retain their current rules.

Default behavior:

- Completing a subtask does not complete its parent or siblings.
- Completing the parent does not complete, cancel, hide or release its subtasks.
  Optional unfinished subtasks can be claimed and finished under a completed parent.
- Parent blocked/cancelled state is context, not an implicit mutation of every
  assignment. Cancelling the whole set requires an explicit operation showing its
  affected records. A parent cancellation is not an HRC process-stop command.
- A deleted/archived owner is not eligible for new subtask creation or claims.
  Existing conversations remain readable and replyable under room rules.
- Reopening a subtask does not reopen the parent automatically.

Recommendation: support `required_for_parent=false` by default. If true, the parent
cannot transition to `completed` until the subtask is `completed`. Cancellation,
archive and deletion are not fulfillment. A waiver is an explicit change to the
requirement, with normal attribution/history, rather than pretending the work
finished. Reject creation of a required unfinished subtask, or reopening one,
under a completed parent until the parent is explicitly reopened or the requirement
is made optional.

Enforce this predicate in the same transaction as parent completion and in every
writer, including bulk/import operations and wrkf's existing task-state projection.
The check and subtask requirement mutations must serialize so neither can commit
an invalid parent-completed/required-unfinished combination. A required subtask is
a wrkq completion constraint; no new wrkf phase or workflow instance is necessary.
This predicate does not replace workflow acceptance rules where those exist.

Existing typed task relations can reference the UUID of either record class.
`render-preview` may depend on `architecture-diagram`; a dependency and a parent
completion requirement are different relationships. Preserve existing blocker
semantics unless separately revised. In particular, current task dependency
resolution also treats cancellation as non-blocking; an agent must still check
that a requested input artifact exists before rendering it. This proposal adds no
automatic dependency scheduler.

A result is recorded using comments, outcome and attachments. If review matters,
keep the subtask unfinished until acceptance or describe the review as another
assignment. Process exit or a wrkc reply never automatically sets `completed`.

## Command and API experience

Reuse the normal work commands. Proposed examples:

```sh
# Explicit creation distinguishes a named subtask from a task/container typo.
wrkq touch mvp/offline-editing/architecture-diagram --subtask \
  -t 'Explain the offline editing architecture' -d 'Editable diagram and preview.'

wrkq cat mvp/offline-editing/architecture-diagram
wrkq set mvp/offline-editing/architecture-diagram --state in_progress
wrkq comment add mvp/offline-editing/architecture-diagram -m 'Draft ready for review.'
wrkq set mvp/offline-editing/architecture-diagram --state completed

# Parent detail lists assignments; descendants retain their readable names.
wrkq cat mvp/offline-editing
wrkq ls mvp/offline-editing --subtasks
wrkq tree mvp

# Only an explicit requirement makes this block parent completion.
wrkq set mvp/offline-editing/architecture-diagram --required-for-parent
```

Retain `--parent-task` for independently identified child-task relationships; change
its documentation and default presentation to “child task.” It must not start
creating named subtasks implicitly. Legacy `kind=subtask` remains historical category
data, deprecated for new ordinary-task creation; creation via `--parent-task`
defaults to ordinary `kind=task`. New named subtasks are identified by record class.

Extend task RPC selectors to resolve both record classes, with explicit creation
inputs (`recordClass`, owning parent, slug, title) and response fields. The response
must separate `parentTask` (independent child edge) from `subtaskParent` (ownership),
and include `id: null` for named subtasks rather than inventing an ID. The public
schema catalog, Go/TypeScript clients and CLI rendering change together. Exact
method/flag spellings should follow the implementation's surface review.

Parent detail exposes subtask summaries and own content separately. Viewing a
subtask exposes its own brief plus parent references and room locator; it does not
silently concatenate every parent comment or session transcript. A bounded context
read can include selected parent brief/decisions and room excerpts with provenance.

Comments and attachments attach to the subtask UUID. Parent views expose their
references without copying their contents. `--record` in wrkc writes the comment
to the message's subject record, including a subtask. Task-scoped handoffs retain
exact full subtask scope where they describe that assignment.

Search/find/list outputs expose record class and path. Existing task-only machine
queries retain their scope/count meaning; offer an explicit record-class selector
for including subtasks, with `ls <task> --subtasks` as the direct view. Human `tree`
can show named subtasks under selected tasks with a distinct marker. It must not
fold optional assignments into a misleading parent “all done” predicate. Show
“parent complete; 2 optional subtasks open” instead. Existing child-task residency
rollups retain their established semantics.

Monitor/watch on the parent can include its subtask events as explicitly typed
related events. Monitoring the exact subtask selects its state and subject traffic.
Filtering happens before pagination. Project history and exports retain parent and
subtask UUIDs, not only mutable display paths.

## ScopeRef and session support

ASP owns the canonical grammar; wrkq owns whether a referenced work record exists.
The proposed forms are:

```text
ScopeRef:
  agent:arris:project:demo:task:T-12345:subtask:architecture-diagram

ScopeHandle:
  arris@demo:T-12345/architecture-diagram

SessionHandle:
  arris@demo:T-12345/architecture-diagram~planning
```

The slash suffix exclusively names a subtask. Add `subtaskId` (the parent-local,
immutable slug) and a `project-task-subtask` scope kind. Remove `roleName`,
`project-role`, `project-task-role` and active `:role:` construction. Subtask requires
a project and ordinary parent task. Reject role-plus-subtask and nested subtask
segments. Existing agent, project and parent-task scopes remain valid.

A separate stable record UUID backs the named subtask. Runtime bindings should
retain that UUID after wrkq resolution, alongside the canonical scope, to avoid
repeated work-identity inference. ASP's pure grammar package does not call wrkq;
work-aware resolution belongs in wrkc/HRC integration before birth or claim.

SessionRef remains scope plus lane. Different subtasks produce different scopes
and default sessions even for the same agent. A lane remains an optional separate
conversation within one scope; it does not replace subtask identity or split wrkc
reply obligations. Two agents working on the same subtask have different agent
scopes and can read the same room. One claim holder at a time still applies.

Environment recommendation:

| Variable | Value for a subtask session |
| --- | --- |
| `AGENT_TASK` | Parent global task ID |
| `AGENT_SUBTASK` | Local subtask slug |
| `AGENT_SCOPE_REF` | Full canonical scope including subtask |
| `AGENT_SESSION_REF` | Full scope plus lane |
| Existing claim token/generation variables | Claim for the subtask record |

Set `AGENT_SUBTASK` only for subtask sessions. Commands must not infer that
`AGENT_TASK` is the claimed record when the scope names a subtask. Keep explicit
selectors authoritative; expose the resolved work UUID/path through runtime
bindings and introspection.

Removing ScopeRef role does not remove wrkf action roles, task role assignments or
agent capabilities as domain concepts. Those describe functions in a process;
they no longer create a generic extra identity dimension. Inventory their consumers
so a role string is not accidentally dropped where it is still domain data.

## Independent claims and execution

Extend existing `wrkq claim`, `release`, claim validation and holder-guarded
completion to named subtasks. No new generic run/attempt system is required.

For a subtask claim, the server resolves both the parent task and subtask slug,
verifies the full scope names that exact UUID, and records the holder tuple on the
subtask row. Parent and sibling claims are independent. Claiming a subtask does
not claim the parent; a parent claim grants no implicit subtask holdership.

A subtask-scoped caller cannot claim or complete the parent by presenting the
subtask's token. An ordinary task claim must match an ordinary task scope with no
subtask segment. Validate project binding as well as parent/subtask identity.
Retain server-authenticated node identity, monotonically increasing generations,
explicit takeover, stale-holder fencing and no automatic expiry.

HRC's claim/start path must resolve the subtask record and pass its claim receipt,
while continuing to use the owning task/project for appropriate workspace routing.
Separate sessions do not imply separate worktrees or permission to edit the same
files concurrently. Workspace conflict management remains existing coordination.
Do not let a role-removal or subtask qualifier accidentally broaden service-restart
or other primary-seat privileges.

Dispatch remains an addressed wrkc message to the full subtask handle. Creating a
record does not start an agent. Deterministic scripts may update the record through
ordinary authorized commands; a scheduler/handler registry is outside this proposal.

Wrkf continues to govern its existing task workflows. Named subtasks do not receive
a workflow automatically. Initial support may explicitly refuse attaching a wrkf
instance directly to a named subtask; ordinary task workflows and independent child
tasks remain available. The required-subtask completion check is the only new
integration needed at wrkf's parent-task completion boundary.

## Wrkc rooms, subjects and recipients

Keep three identities separate:

| Identity | Example | Purpose |
| --- | --- | --- |
| Room anchor | Parent offline-editing task or its effective campaign | Shared durable conversation |
| Message subject | architecture-diagram subtask UUID | Exact work discussed; filtering and recording |
| Recipient scope | arris + demo + parent + architecture-diagram | Specific working session and reply obligation |

A subtask has no independent room and cannot independently enroll in a campaign.
Resolve new sends through the parent using exactly the parent's existing campaign
coalescing rules. Do not create subtask room rows. Ordinary child tasks keep their
existing room behavior.

The envelope's existing task UUID tag can identify a subtask if the task-backed
storage recommendation is adopted. Add parent/work-class/path data to read models
as needed; do not overwrite the tag with the parent's UUID and lose specificity.

Proposed interactions:

```sh
wrkc say mvp/offline-editing/architecture-diagram \
  --to arris@demo:T-12345/architecture-diagram - <<'MESSAGE'
Create an editable architecture diagram using the parent task brief and decisions.
MESSAGE

# Shared conversation; also includes parent and sibling-subtask discussion.
wrkc log mvp/offline-editing

# Exact subject filter within that shared conversation.
wrkc log mvp/offline-editing --task mvp/offline-editing/architecture-diagram
```

An explicit work selector determines message subject. Addressing a full subtask
handle without a separate selector derives its subject and inherited room.
A reply through `EN-XXXXX` retains the original room and subject even if current
work placement changed. A parent-level comment may intentionally be addressed to
a subtask session; subject and recipient need not be identical. `--record` follows
the subject, not the recipient.

Bare addressee fallback must use subtask subject context when a subtask was
explicitly selected, rather than deriving a parent seat from the room anchor.
Preserve exact reply-to routing. If membership/standing-obligation lookup yields
multiple same-agent scopes, require a full handle instead of guessing between
parent and subtask seats.

Reply obligations remain matched by sender scope, recipient scope and room. Mable
sends different subtask requests to different scopes, so a response from the
architecture-diagram scope does not answer a request sent to deployment-diagram.
No new per-subtask reply state machine is needed. Multiple requests within the
same scope still use existing default or explicit-envelope reply semantics.

Room history remains pull-based; membership never injects every message into all
sessions. A common room gives accessible context while separate sessions retain
independent working transcripts. Subtask-tagged `--record` comments and results
are discoverable from the parent without duplication.

Existing history needs care: a task room predating campaign enrollment remains
readable today while new sends go to the campaign room. Subtask room resolution
must mirror the parent's read/send rules and expose the same history links. Do not
merge old rooms or rewrite historical envelopes. Exact subject filters work in any
selected historical room; a context reader can follow the room links explicitly.

Task completion, room staleness and hidden labels continue to permit replies.
Room discovery should expose a count of unfinished subtasks and keep such work
findable even when the room anchor is completed. Specify this extension to the
room read projection without changing the stored parent task state.

## Deletion, movement and record preservation

Named subtasks are contained records. Independent child tasks retain existing
residency-based rules, including cross-project detachment rather than cascading
mutation. Do not apply one rule to both relationships merely because both have a
parent.

Recommended behavior:

- Soft-deleting/archiving an owning task includes its named subtasks in the impact
  preview and applies the existing contained-work deletion/archive convention.
  Restore only records changed by that operation, preserving prior states.
- Refuse destructive containment operations while a parent/subtask claim is held;
  require explicit release/takeover resolution first. A database mutation does
  not terminate a runtime.
- Purging an owner accounts for subtask comments/attachments and envelope references.
  Preserve historical room/envelope content and work attribution; use the existing
  supported tombstone/reference strategy or reject purge while references require
  the record. Never rely on a blind cascade that deletes conversation evidence.
- Same-project owner movement carries inherited subtask residency atomically.
  Independent child-task movement continues to follow the current residency rule.
- Subtask deletion reserves its slug and permits restore, preventing a stale scope
  from silently addressing unrelated replacement work. Cancel or reopen an existing
  assignment when appropriate.

The implementation review must settle the exact purge/reference strategy against
the current foreign keys; this proposal does not authorize loss of historical mail.

## Decomposition example

1. Create `mvp/offline-editing/decompose` with instructions to create a deliverable
   breakdown, dependencies and acceptance criteria. Dispatch to its own scope.
2. The agent reads the parent's brief and shared room, then creates independent
   child tasks for persistence, synchronization and conflict resolution. Those
   tasks receive normal global IDs and resident paths.
3. Persist each intended child UUID and brief against the decomposition before
   creation; use the existing explicit-UUID creation input. After an uncertain
   response, read that UUID and verify the record before retrying. Retain normal
   idempotency keys too, but do not assume they close every crash window: the
   inspected API stores replay data after task creation commits. Record completed
   effects as work progresses. Record proposed-but-not-created work as a proposal
   if that was the requested scope.
4. Review the breakdown for coverage and useful boundaries. Complete `decompose`
   when that assignment is accepted. Its child-task references stay available.
5. The newly created tasks remain open. Their own dispatch and completion are
   separate actions; a parent integration check may still be needed.

This scenario uses task/subtask records, comments, relationships and wrkc. It needs
no new automatic execution policy.

## Supporting changes and ownership

| Surface | Required changes |
| --- | --- |
| wrkq storage/domain | Record class, owning parent, ID allocation, uniqueness, containment, state/claim/completion rules, comments/attachments/relations |
| wrkq selectors/CLI | Container/task/subtask traversal, explicit subtask creation, readable output, task-relative selector, ambiguous-path refusal |
| wrkq RPC and clients | Nullable IDs, structural discriminator, parent references, query filters, schema catalog and client generation |
| wrkc | Effective parent-room routing, subject preservation, subject-aware fallback, log filters, history/discovery and exact-scope obligations |
| ASP agent-scope | Explicit subtask grammar, role removal, handles, ancestor scopes, session serialization, shared contract fixtures |
| ASP configuration/materialization | Remove role identity defaults; emit subtask environment/bindings and preserve complete scope attribution |
| wrkq Go scope implementation | Match ASP grammar and normalization; retain full subtask identity in FullRef and durable attribution |
| HRC and mail injector | Resolution before birth/claim, distinct session keys, parent workspace routing, pending obligations, monitoring, lifecycle authority checks |
| ACP and other consumers | Parse/display new scopes, generate valid destinations, preserve parent/subtask context and exact replyTo |
| wrkf | Enforce required-subtask condition when projecting parent completion; retire scope-role addressing in callers without removing workflow roles |
| Search, timelines, taskboard, bundles | Readable paths, record-class handling, null-ID support, explicit inclusion/counts and round-trip preservation |

No wrkq dependency on HRC or ASP runtime services is introduced for CRUD or room
persistence. ASP retains the shared identity contract; duplicated language parsers
must be verified against the same cases. Existing project-only context lookup may
normalize scopes for configuration, but durable attribution and routing must not
accidentally discard the subtask qualifier.

## Migration and delivery order

The work spans a persisted identity contract. The following order makes the
compatibility boundary explicit; it is not authorization to dispatch a campaign.

1. **Finalize the model and migration inventory.** Enumerate role-qualified scopes,
   profile defaults, live sessions/claims, pending envelopes, handoffs and consumer
   parsers. Confirm nullable-ID coverage and existing ambiguous paths. Review the
   proposed changes to the active records named above with Daedalus before build.
2. **Implement producer support and scope contracts together.** Build/test wrkq's
   data/room/claim changes plus ASP's new grammar and wrkq's matching parser against
   isolated acceptance data. Existing rows remain ordinary tasks. HRC/ACP/client
   changes can proceed against the agreed contract in parallel.
3. **Classify role migrations by meaning.** Real assignments receive explicit
   subtask records and new scopes; role uses that do not denote work map to their
   appropriate task/project seat only after collision review. No blind role-to-
   subtask conversion. No implicit creation of work because a parser sees a name.
4. **Prepare runtime and durable-obligation transition.** Drain or explicitly
   transfer old live scopes and their pending obligations. Keep historical strings
   readable through a legacy-history decoder; active dispatch rejects `:role:`
   after cutover. Canonical role strings and subtask strings remain distinguishable.
   Old `/role` handles share spelling with new `/subtask` handles, so update every
   active producer before enabling that interpretation. Ambiguous stale inputs must
   not silently birth the wrong seat.
5. **Deploy coordinated producer/consumer versions.** Back up and migrate the
   canonical wrkqd database under existing operations doctrine. Publish/synchronize
   the matching Go/TypeScript clients and ASP/HRC/ACP identity consumers. Account
   for schema-hash refusal by older clients; do not assume an additive field is
   compatible. Restart authority stays with the existing operator seats.
6. **Run real installed acceptance and publish evidence.** Then update SPEC, CLI
   guides, identity docs and active architecture records to the delivered behavior.
   The proposal itself is not evidence of installed support.

Until cutover, existing role scopes remain existing behavior. The target removes
role from active identity; read-only historical decoding is not continued support
for role-based dispatch. If pending mail cannot be transferred without losing its
exact addressee/obligation semantics, finish it before retiring that scope. This
must be resolved in the migration design rather than handled by string rewriting.

## Acceptance scenarios

Each installed acceptance run should preserve command transcripts, JSON responses,
relevant database/event readback and runtime/session references as a repeatable
artifact. No image or graph interface is required.

1. Create `architecture-diagram` beneath two tasks. Both succeed, have distinct
   UUIDs and no global IDs; task sequence allocation is unchanged. Duplicate names
   under one parent refuse. Displayed addresses remain descriptive.
2. Round-trip each selector through create/show/set/comment/attach/search/export
   and import. Subtask content and parent references survive without an ID invented
   by a client. Container/task name ambiguity is diagnosed, not guessed.
3. Keep existing child tasks, including cross-project ones, unchanged through
   migration. They retain IDs, paths, rooms and residency/delete behavior.
4. Dispatch two subtasks to the same agent through wrkc. Observe two scopes/sessions,
   one effective parent room, two subject tags and independent pending obligations.
   Reply from one and verify the other still owes a response.
5. Dispatch author and reviewer agents to the same subtask. They share work context
   and room, have different agent scopes, and preserve a single explicit claim holder.
6. Repeat room tests for an ordinary parent, a campaign parent, and a parent with
   a historical pre-enrollment room. Replies through old envelopes preserve their
   original room/subject; current sends agree with parent routing.
7. Claim two sibling subtasks concurrently while the parent has its own holder.
   Parent/sibling tokens cannot complete each other's records. Explicit takeover
   fences the old holder. Role/subtask qualifiers never grant primary-seat authority.
8. Complete a parent with an optional subtask open; the subtask remains discoverable,
   claimable and replyable. Completing it later does not reopen or otherwise mutate
   parent state.
9. Require one subtask and race its requirement/state changes with parent completion.
   All writers preserve the completion predicate. Cancellation/deletion cannot fake
   fulfillment. A workflow projection observes the same rule.
10. Rename/move a parent within a project and verify UUIDs, task-relative selectors,
    scopes, attachments and room history remain valid. Forbidden subtask rename,
    reparent or nested creation refuses with an actionable explanation.
11. Exercise archive/delete/restore/purge impact with subtask content and mail, held
    claims and independent cross-project children. No history is silently lost.
12. Run decomposition, interrupt after some task creations, resume using recorded
    references/idempotency keys, and verify no duplicate children. Completing the
    decomposition leaves the created work open.
13. Verify new canonical/handle/session/env round trips in ASP, wrkq and HRC. Old
    role scopes are readable as history but rejected for new dispatch after cutover;
    pending obligations are neither abandoned nor silently reassigned.
14. With HRC unavailable, create/read/update subtasks and persist/read shared-room
    messages through wrkq/wrkc. Execution availability does not own durable work.

## Remaining design decisions before implementation

The proposal recommends a concrete first-release model; the following still need
review against the complete consumer inventory:

- Approve the task-backed storage/discriminator versus a separate subtask table if
  null-ID consumers make reuse disproportionately costly. Preserve one coherent
  work API either way.
- Approve immutable subtask slug/owner for the first release, and the 64-character
  limit; later rename/promotion support is a separate identity change.
- Ratify exact query defaults, DTO fields, flags and required-completion predicate
  across every direct and workflow writer.
- Choose the historical-room/purge reference treatment and role-scope migration
  mapping, including stale shorthand handles and outstanding obligations.

None of these decisions requires adopting other graph concepts. Named subtasks
are useful with today's explicit task creation, addressed communication and claims.

## Source map

Paths below are repository-root-relative unless prefixed with a sibling checkout.
The sibling pointers are local inspection locations, not portable document links.

- `internal/wrkqapi/tasks.go`, `internal/wrkqapi/types.go`; `internal/store/tasks.go`;
  `internal/db/migrations/000031_cross_project_parent_edges.sql` — creation,
  global-ID allocation, residency and parent constraints.
- `internal/selectors/selectors_local.go` — current task path resolution.
- `internal/wrkqapi/rooms.go`; `internal/store/rooms.go`;
  `internal/domain/rooms.go` — room resolution, subjects, exact scopes and replies.
- `internal/wrkqapi/claims.go`;
  `internal/db/migrations/000048_task_claim_authority.sql` — current task claims.
- `internal/scope/`; `internal/attribution/` — Go identity parser and attribution.
- `docs/SPEC.md`, `docs/wrkc-reference.md`, `docs/wrkf-lean-agentic-workflow-spec.md`
  — existing task, collaboration and workflow boundaries.
- `architecture/records/invariants/` — active records named in this proposal.
- Sibling agent-spaces: `contracts/agent-scope/src/`,
  `docs/identity-scope-and-env-contract.md`,
  `compiler/agent-spaces/src/agent-session-env.ts` — identity grammar/environment.
- Sibling hrc-runtime: `packages/hrc-server/src/scope-claim-core.ts`,
  `packages/hrc-core/src/selectors.ts`, `packages/hrc-core/src/monitor/index.ts`,
  `packages/hrc-sdk/src/resolve-scope.ts`,
  `packages/hrc-server/src/server-lifecycle-authority.ts` — sampled role consumers;
  a complete consumer inventory remains required before migration.
