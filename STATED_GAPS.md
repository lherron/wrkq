# Latent-Space Exploration — Workflow Ontology applied to wrkf/wrkq

Run: single-chat 9-turn harness (Set A) via `hrcchat turn` against scopeRef `agent:clod:project:wrkq:task:latent-spaces`, model `claude-opus-4-8[1m]`. Continuity preserved at the scopeRef level (no --fresh-context). Date: 2026-06-07.

---

## Turn 1 — 

CONTRACT REGISTERED — turn 0 only. No gaps/fixes/overreach/taxonomy below, by constraint.

I grounded this against real artifacts before writing (so provenance tags are honest): `wrkf --help`,
the wrkf-authoring six-part model, `~/praesidium/workflow-spec/proposal/00–12_*.md`,
`~/praesidium/workflow-spec/schemas/*.json`, and `wrkq handoff --help`. Tags below: [obs]=read it in
an artifact this turn, [std]=standard CS/workflow term, [prov]=my provisional coinage for this study.

═══════════════════════════════════════════════════════════════════════
1. OBJECTIVE (restated)
═══════════════════════════════════════════════════════════════════════
Produce — over multiple turns — a 3-part assessment of the *workflow ontology* as embodied by wrkf
(engine) and wrkq (durable substrate): (1) gaps in the current contracts/functionality, (2)
recommended additions, (3) places the current methodology overreaches. The unit of analysis is the
SET OF CONCEPTS and their RELATIONSHIPS/EFFECTS a durable agentic-workflow system must model — and
how faithfully wrkf+wrkq model them.

NON-OBJECTIVES (explicit):
  N1. Not building/redesigning either system this study; it's an assessment, not an implementation.
  N2. Not evaluating execution runtime, transport, RPC, or adapter delivery mechanics — except where
      the *contract* depends on them.
  N3. Not CLI ergonomics, flags, output formats, or help text quality.
  N4. Not UI/rendering (taskboard/workboard/Discord).
  N5. Not storage-engine internals (SQLite schema, indexing, migration).
  N6. Not auth/identity/permissions.
  N7. Not benchmarking, performance, or scale.
  N8. Not a literature survey of other engines (Temporal, Airflow, BPMN) for their own sake — only as
      borrowed lenses when they sharpen a concept.

═══════════════════════════════════════════════════════════════════════
2. SCOPE BOUNDARY (decidable tests)
═══════════════════════════════════════════════════════════════════════
"The workflow/durable-task contract" = the normative interface that determines: what states/tasks
can exist, what transitions are legal, what must be true (evidence/checks) to move, what fires as a
consequence (effects/obligations), who may act (roles), and what persists across sessions
(tasks/handoffs/ledger).

INCLUSION TEST (a topic is IN scope iff ALL hold):
  I1. It names or constrains a first-class concept in {state, transition, check, validator, hook,
      effect, obligation, role, template, task, container, handoff, evidence, ledger} OR a relation
      between two of them.
  I2. Changing it would change what is *legal*, *required*, *guaranteed*, or *durable* — not merely
      how a thing is invoked or displayed.
  I3. It survives a session boundary OR governs crossing one (durability/handoff), OR it gates a
      transition.

EXCLUSION TEST (OUT of scope if ANY hold):
  E1. It only affects how a concept is *invoked/serialized/transported* (flags, RPC, stdin shape).
  E2. It only affects how a concept is *rendered* to a human.
  E3. It is a property of the *machine* (perf, storage layout, process model) with no contract effect.
  E4. Removing it would leave the set of legal states/transitions/effects unchanged.

BORDER CASES (in-scope ONLY via the seam they touch):
  - Effect *delivery semantics* (at-least-once? ack? idempotency?) — IN, because the contract promises
    "intent declared, adapter delivers"; the guarantee level is a contract property. The adapter's
    *code* is OUT (N2).
  - Supervisor/recovery — IN as a role+escalation concept; its transport is OUT.
  - Comments — IN only insofar as they overlap/compete with evidence as a fact-carrying substrate.

═══════════════════════════════════════════════════════════════════════
3. WORKING GLOSSARY (provenance + confidence on each inference)
═══════════════════════════════════════════════════════════════════════
state [obs, high] — a wrkf instance position `{status, phase?, outcome?}`,
  status ∈ {open, active, waiting, closed}. `active`+error phase = recoverable failure; `closed/failed`
  = terminal. DISTINCT from a wrkq *task* state (below). [Inference, med: that these two state
  vocabularies are only loosely bound — flagged as open question Q1, not asserted.]
transition [obs, high] — a directed edge: `from` a state pattern, performed `by` role(s), gated by
  `requires`(evidence)/`guards`/`checks`, fanning into routed `outcomes`, each `to` a next state and
  optionally minting obligations.
check [obs, high] — a referenced-by-id gate of kind builtin | predicate | hook | role; yields
  `{verdict, outcome}`. Reads the ledger; never embeds a command.
validator [obs, med] — NOT a separate template concept. It is the *executable script* backing a
  `type:hook` check (the "validator script contract"). The topic statement lists it beside checks;
  I treat it as the implementation side of a check, not a distinct ontological kind. [Inference, med.]
hook [obs, high] — a catalog entry (argv/stdin/stdout/timeout) that backs a check or effect, plus
  `stateHooks` that fire on entering a state. Mechanics live OUTSIDE the template (hook-catalog.json).
effect [obs, high] — a durable OUTBOX record declaring *intent* (e.g. wake_role,
  request_observer_review) on a transition. Declares intent, NOT delivery; an adapter delivers+acks.
  [Inference, med: delivery guarantee level — see Q4.]
obligation [obs, high] — a blocking debt minted on a routed outcome, owned by a role, cleared when
  satisfied/waived/cancelled. A SEPARATE surface from effects (`create_obligation` is not an effect).
role [obs, high] — a named actor slot (RoleSpec) referenced by transitions' `by` and a transition's
  `responsibility{role,scope,lane}`. Filled at run time by `wrkf run` actor binding.
preset / template [obs, high] — "preset" is the loose word for a *template*: an immutable JSON policy
  artifact (schemaVersion wrkf.workflow-template.v0), one instance per wrkq task. Policy is authored;
  the engine enforces. Immutable once installed (version is a string, frozen).
task [obs, high] — the wrkq durable unit; states {open, in_progress, completed, blocked, cancelled};
  carries comments, labels, priority, container membership. The substrate a workflow instance runs OVER.
container [obs, med] — a hierarchical grouping/namespace for tasks (`wrkq mkdir`), addressable by path.
handoff [obs, high] — an agent/project-scoped (NOT task/lane-scoped in v1), intentional context record
  for the same agent to consume in a later session; pending → acknowledged; auditable/searchable.
  A sibling primitive to tasks/comments/memory.
evidence [obs, high] — typed facts agents submit (declared by `evidenceKinds`); the content checks read
  and transitions `require`.
ledger [obs, med] — the append-only record of evidence (and, [inference med], workflow events — a
  workflow-event schema exists). The audit spine. [Whether evidence-store and event-log are one ledger
  or two is Q5.]
"contract" [prov] — MY coinage for the study: the normative interface = the union of guarantees and
  requirements binding {template policy ↔ engine ↔ task substrate ↔ adapters/roles}. What's promised,
  what's required, what's durable. This is the thing we assess for gaps/overreach.
"agentic workflow" [prov/std] — a workflow whose roles are filled by autonomous *agent sessions*
  (not humans), where session boundaries are frequent and handoffs/evidence carry the continuity that
  a human's working memory would otherwise hold. [Inference, high: this framing matches wrkf's design.]

═══════════════════════════════════════════════════════════════════════
4. SUCCESS CRITERIA (final artifact) + EPISTEMIC CAVEATS
═══════════════════════════════════════════════════════════════════════
The final 3-part assessment succeeds iff:
  S1. Every claimed GAP is stated as: concept X is unmodeled/under-modeled → concrete failure mode it
      permits → which contract surface (template schema / engine / wrkq / handoff) would carry the fix.
  S2. Every ADDITION is decidable: where it lives, what it changes about legal states/transitions/
      effects, and what it would break (migration/compat note).
  S3. Every OVERREACH names a concept the methodology models that costs more than it earns (ceremony
      without a corresponding guarantee), with the test that would confirm it's overreach.
  S4. Claims are tagged by evidence basis (read-the-schema vs inferred), as in this turn.
  S5. Out-of-scope items (§1 N-list) are not silently smuggled in.

EPISTEMIC CAVEATS / UNKNOWNS WE CARRY:
  C1. I have NOT yet read 02 (task model), 03 (template contract), 04 (validation/hooks),
      05 (next-action), 08 (agent-tasker mapping), 09 (supervisor), 11 (questions-not-asked),
      12 (legacy mapping) in full — only the authoring-skill distillation. Glossary is provisional
      until reconciled against the schemas directly.
  C2. I have not inspected a LIVE instance (`wrkf task`, `wrkf next`, an evidence ledger dump) this
      study — concepts are from policy/spec, not runtime behavior.
  C3. The wrkf↔wrkq state binding is the single biggest unknown (Q1) and colors most of the assessment.
  C4. "Methodology" (for the overreach part) is partly inferred from skills; I may be assessing an
      idealization rather than what's installed. Will pin to installed templates before judging.

═══════════════════════════════════════════════════════════════════════
5. WORKING STATE  (carry + update every turn)
═══════════════════════════════════════════════════════════════════════
[Objective]
  Assess wrkf+wrkq workflow ontology → 3-part output: gaps / additions / overreach. Concept-and-relation
  level. Scoped to the workflow/durable-task contract.

[Non-objectives]  N1 no redesign · N2 no runtime/transport · N3 no CLI ergonomics · N4 no UI ·
  N5 no storage internals · N6 no auth · N7 no perf · N8 no engine-survey-for-its-own-sake.

[Scope rules]  IN iff I1∧I2∧I3 · OUT if E1∨E2∨E3∨E4 · border cases: effect-delivery-GUARANTEE in /
  adapter-code out; supervisor-as-role in / its-transport out; comments in only vs evidence overlap.

[Glossary]  state·transition·check·validator(=hook impl, not a kind)·hook·effect·obligation·role·
  template(=preset)·task·container·handoff·evidence·ledger defined above; "contract" & "agentic
  workflow" are [prov]. Two distinct state vocabularies: wrkf instance {open,active,waiting,closed}
  vs wrkq task {open,in_progress,completed,blocked,cancelled} — binding UNRESOLVED.

[Open questions]
  Q1 How do wrkf instance states bind to wrkq task states? Independent, mirrored, or projected? (HIGH leverage)
  Q2 Are obligations surfaced in wrkq (e.g. as blocked tasks) or only inside the wrkf ledger?
  Q3 Is a handoff a workflow EFFECT, a wrkq primitive, or both? Relationship to roles/wake_role?
  Q4 What delivery guarantee does the effect outbox promise (at-least-once? ack? idempotency? ordering?)?
  Q5 Evidence-store vs event-log: one ledger or two? Is it append-only/immutable?
  Q6 Comments vs evidence — overlapping fact substrates? When does each win?
  Q7 Are temporal concepts (deadlines, timeouts, retries, SLA, escalation) first-class or absent?
  Q8 Can one workflow instance span multiple wrkq tasks (fan-out/sub-tasks), or strictly 1:1?
  Q9 Where does the spec ITSELF flag unmodeled concerns (doc 11_questions_you_did_not_ask)?

[Confidence ledger]
  wrkf template/policy concepts ......... HIGH (read authoring model + know schemas exist)
  wrkf runtime/instance behavior ........ LOW  (not exercised this study — C2)
  wrkf↔wrkq state binding ............... LOW  (Q1, unread — C3)
  wrkq task/handoff/container ........... HIGH (operate these daily + read handoff help)
  effect delivery semantics ............. LOW  (Q4)
  ledger structure (one vs two) ......... LOW  (Q5)
  "methodology" as installed vs ideal ... MED  (C4)

[Stage log]
  T0 (this turn): contract registered — objective, non-objectives, scope tests, glossary w/ provenance,
     success criteria, caveats, working state initialized. NEXT: read proposal/02,03,04 + schemas to
     resolve Q1/Q5 and harden the glossary before any gap enumeration.

Standing by for the go-ahead to proceed to the next stage (read-and-reconcile), or for you to reorder
the open questions by priority.


---

## Turn 2 — Frame & Contract

TURN 1 — DEFAULT BASIN (descriptive only; NOT endorsed; no exotic lenses; no gap/fix lists).

Tags as before: [std]=standard framing, [obs]=I can see wrkf/wrkq already departs from it, [prov]=my coinage.

═══════════════════════════════════════════════════════════════════════
1. THE STANDARD MENTAL MODEL — "a workflow over tasks"
═══════════════════════════════════════════════════════════════════════
The house style fuses three lineages that share a skeleton:
  • BPMN / flowchart: boxes (activities) joined by arrows (sequence flow), diamonds (gateways) that
    branch on a condition, a start node and one or more end nodes.
  • Ticket tracker (Jira/GitHub): a card with a STATUS that marches left-to-right across a board
    (Open → In Progress → Review → Done), an ASSIGNEE, and free-text comments.
  • CI pipeline: ordered STAGES, each a gate that must go green; red halts the line; success = the
    last stage passed.

Collapsed into one picture, the default says:

  A WORKFLOW IS [std]: a directed graph of STATES, where edges are transitions triggered by an actor
  or event, some edges guarded by a GATE (a boolean condition), terminating in a DONE state. The
  workflow's identity ≈ its diagram. Progress ≈ position in the diagram.

  A TASK IS [std]: a unit of work with a STATUS field (the workflow position), an OWNER, a
  description, and metadata (priority, labels, dates). The task IS its current row in a table. Its
  history is a side-channel (audit log) you rarely consult. "Doing the task" = moving the status to
  Done.

IMPLICIT ASSUMPTIONS baked in (the load-bearing ones):
  A1. SINGLE STATE VARIABLE. One status enum holds the whole position. Phase/outcome/health are
      flattened into it (hence sprawling status lists like "In Review", "Blocked-waiting-on-QA").
  A2. STATE IS THE TRUTH. If status=Done, it's done. The record of WHY/HOW (evidence) is decorative.
  A3. GATES ARE PURE BOOLEANS over trusted inputs. A green check means the thing it checks is true.
      Nobody lies to the gate; the gate cannot be gamed.
  A4. ONE ACTOR PER TASK, human, with persistent memory and good faith. The assignee remembers
      context across days; you don't need to re-hydrate them.
  A5. EFFECTS ARE FIRE-AND-FORGET side effects. "On Done, send a notification" — emitting it IS
      completing it. Delivery, ack, and idempotency are not the workflow's concern.
  A6. TIME IS LINEAR AND MONOTONIC. Work flows forward; rework is an exception (reopen), not a
      first-class loop. "Completed" is an absorbing terminal state.
  A7. THE GRAPH IS THE CONTRACT. Conformance = "did the card follow legal arrows?" Nothing obliges
      the actor to leave durable proof; the diagram polices position, not substance.
  A8. OBLIGATIONS = TODO ITEMS. A debt the workflow owes (e.g. "must get review", "must fix
      regression") is modeled, if at all, as another checklist row or a sub-ticket — inert text, not
      a blocking, owned, machine-tracked liability.

═══════════════════════════════════════════════════════════════════════
2. LOAD-BEARING SIMPLIFICATIONS (what each assumption buys, and quietly discards)
═══════════════════════════════════════════════════════════════════════
  • STATE-AS-WHOLE-TRUTH (A1/A2): buys a clean board and trivial queries; discards the distinction
    between "claimed done" and "demonstrated done." Loses the ledger as a first-class citizen.
  • COMPLETED-IS-TERMINAL (A6): buys a satisfying end node; discards post-completion obligations
    (warranty, monitoring, "reopen if the fix regresses"), and treats loops as failures rather than
    the normal shape of iterative/agentic work.
  • EFFECTS-AS-SIDE-EFFECTS (A5): buys simple "on-transition do X" wiring; discards the entire
    delivery-semantics surface — an effect that was emitted but never delivered looks identical to
    one that succeeded. The workflow can't tell intent from outcome.
  • EVIDENCE-AS-OPTIONAL-METADATA (A2/A7): buys speed (no proof required to advance); discards
    verifiability. The gate trusts the mover's say-so. [obs] wrkf consciously inverts this —
    transitions `require` typed evidence and checks READ the ledger — so wrkf already sits OUTSIDE
    this part of the basin.
  • OBLIGATIONS-AS-TODOS (A8): buys a familiar checklist UX; discards ownership, blocking force, and
    machine-tracked clearance. A todo can be ignored; an obligation should refuse to let the
    workflow close. [obs] wrkf models obligations as a separate, blocking, role-owned surface — again
    outside the basin.
  • GATES-AS-TRUSTED-BOOLEANS (A3): buys composability; discards adversarial robustness — see §4.

═══════════════════════════════════════════════════════════════════════
3. SEDUCTIVE-BUT-SHALLOW MOVES (the basin's default answers to "find gaps") — NAMED so we refuse them
═══════════════════════════════════════════════════════════════════════
  M1. STATE PROLIFERATION — "add more states/sub-statuses." Treats missing nuance as a missing enum
      value. Grows the status list, not the model. (Symptom: 14-status boards.)
  M2. GATE PILING — "add more validation/required fields." More boolean gates over the SAME untrusted
      inputs. Doesn't make evidence trustworthy; just adds friction the actor learns to satisfy
      formally.
  M3. NOTIFICATION SPRAY — "add notifications/reminders." Treats coordination gaps as an alerting
      problem. Conflates emitting with delivering (A5).
  M4. FIELD ACCRETION — "add a field for X." Bolts attributes onto the task row instead of asking
      whether X is a relation, an obligation, or evidence.
  M5. SUBTASK-IFICATION — "break it into more tickets." Pushes structure into a tree of cards;
      relationships between them stay informal.
  M6. ROLE/PERMISSION KNOBS — "add an approver role + a permission." Treats trust as access control,
      not as evidence + verification.
  M7. TEMPLATE COSMETICS — "make a preset for it." Captures the diagram shape without strengthening
      what the diagram GUARANTEES.
  Common thread [prov] — I'll call it the "ENUM-AND-GATE reflex": every gap is answered by adding a
  state, a gate, a field, or a message — never by adding a *relation, a guarantee, or a verification
  primitive*. The basin can only grow surface area, not depth.

═══════════════════════════════════════════════════════════════════════
4. WHY THE BASIN IS ATTRACTIVE — and what it makes INVISIBLE for AGENTIC workflows
═══════════════════════════════════════════════════════════════════════
WHY it pulls so hard:
  • Cognitive economy: a single status enum + a board is graspable in seconds; the diagram doubles as
    the documentation.
  • Tooling lineage: 20+ years of Jira/BPMN/CI have trained everyone (and the training corpora behind
    most LLMs) to reach for boxes-arrows-gates first. It's the literal default in the weights.
  • Demo-ability: it screenshots beautifully. A Kanban board or a green pipeline is instantly legible
    to a stakeholder; a ledger-and-obligation model is not.
  • Local correctness: for HUMAN, good-faith, single-actor work, the simplifications are mostly TRUE,
    so the model rarely visibly fails — which is exactly why it's hard to dislodge.

What it systematically makes INVISIBLE once the actor is a NON-DETERMINISTIC AGENT [prov framing]:
  • THE LYING-MOVER PROBLEM: A3 assumes nobody games the gate. An agent can fabricate evidence,
    assert "tests pass" without running them, or fill required fields with plausible nonsense. The
    basin has no concept of evidence that is *adversarially verified* vs merely *present*.
  • THE AMNESIAC ACTOR: A4 assumes persistent memory. Agent sessions are ephemeral; context dies at
    session end. The basin has no first-class HANDOFF — continuity is assumed, not modeled. [obs]
    wrkq's handoff primitive exists precisely because this assumption fails.
  • THE STALL: a non-deterministic actor can silently do nothing, loop, or wander. The basin's only
    liveness notion is "status hasn't changed" — it has no obligation that *actively comes due*, no
    deadline/escalation as a first-class concept (Q7).
  • CLAIMED-vs-DEMONSTRATED: A2 collapses these. For agents the gap is the whole ballgame — "I
    completed it" is cheap; "here is verifiable proof I completed it" is the only thing worth
    durably recording. The basin records the claim and discards the proof.
  • EFFECT-AS-PROMISE-NOT-FACT: A5's fire-and-forget is fatal when the "effect" is "wake another
    agent to review." If it's emitted but undelivered, the workflow believes review is underway when
    nothing is. Agents make the delivery-guarantee gap (Q4) load-bearing.
  • LOOPS ARE NORMAL, NOT EXCEPTIONS: A6 treats rework as failure. Agentic work is iterate-until-
    verified; reject→revise→resubmit is the main path, not the sad path. "Completed" being absorbing
    is a poor fit when obligations can outlive it.
  • GAMING THE GRAPH: A7 polices position. An agent optimizes for the legible signal (status=Done),
    not the latent goal — so a position-policing contract actively rewards the wrong thing
    (Goodhart). The basin cannot see that its own success metric is gameable.

Net [prov]: the basin is a model of TRUSTED, REMEMBERING, FORWARD-MARCHING, SINGLE human actors. Each
of those four adjectives is false for agents. wrkf already breaks several basin assumptions
(evidence-required, obligations-as-blocking, handoff-exists) — so part of THIS study is checking
whether it broke ENOUGH of them, and whether it imported any basin assumptions unexamined. (Not
answered this turn.)

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated)
═══════════════════════════════════════════════════════════════════════
[Objective] (unchanged) Assess wrkf+wrkq workflow ontology → 3-part output (gaps/additions/overreach),
  concept-and-relation level, scoped to the workflow/durable-task contract.

[Non-objectives] (unchanged) N1 no redesign · N2 no runtime/transport · N3 no CLI · N4 no UI ·
  N5 no storage internals · N6 no auth · N7 no perf · N8 no engine-survey-for-its-own-sake.

[Scope rules] (unchanged) IN iff I1∧I2∧I3 · OUT if E1∨E2∨E3∨E4 · effect-delivery-guarantee in /
  adapter-code out · supervisor-as-role in · comments in only vs evidence overlap.

[Glossary] (unchanged from T0) + note: "completed/Done" in the BASIN sense = absorbing terminal;
  contrast wrkf `closed` (an outcome-bearing status, not necessarily obligation-free).

[Default basin] (NEW)
  Model: workflow = state-graph + gates → Done; task = a status row with an owner. Three lineages:
  BPMN, ticket-tracker, CI-pipeline.
  Assumptions A1–A8: single-state-variable · state-is-truth · gates-are-trusted-booleans ·
  one-good-faith-human-actor-with-memory · effects-fire-and-forget · linear-monotonic-time/
  completed-terminal · graph-is-the-contract · obligations-are-todos.
  Shallow moves to REFUSE: M1 state-proliferation · M2 gate-piling · M3 notification-spray ·
  M4 field-accretion · M5 subtask-ification · M6 role/permission-knobs · M7 template-cosmetics.
  Unifying anti-pattern: the ENUM-AND-GATE reflex (grow surface area, never depth/guarantees).
  Why attractive: cognitive economy · tooling lineage (in the weights) · demo-ability · locally true
  for humans.
  Invisible for agents: lying-mover · amnesiac-actor · the-stall · claimed-vs-demonstrated ·
  effect-as-promise-not-fact · loops-are-normal · gaming-the-graph (Goodhart on status).
  wrkf observed to ALREADY exit basin on: evidence-required, obligations-as-blocking, handoff-exists.
  Open: did it exit ENOUGH, and did it import any basin assumption unexamined? (deferred)

[Open questions]  Q1–Q9 (unchanged) PLUS:
  Q10 Does wrkf model ADVERSARIAL evidence — verified vs merely-present? Is there a "verify the
      verifier" / independent-observer requirement, or do checks trust the submitter? (escapes A3)
  Q11 Is there any LIVENESS primitive — an obligation that comes DUE, a deadline, an escalation
      timer — or only change-detection? (escapes the-stall; relates to Q7)
  Q12 Does the contract distinguish a CLAIM (agent asserts done) from a VERDICT (independent
      confirmation)? Two evidence tiers? (escapes claimed-vs-demonstrated)
  Q13 Are post-`closed` obligations possible (debts that outlive completion)? (escapes completed-terminal)
  Q14 Is iteration (reject→revise→resubmit) a first-class loop in templates, or modeled as reopen/
      exception? (escapes linear-monotonic-time)

[Confidence ledger] (unchanged) wrkf policy HIGH · wrkf runtime LOW · state-binding LOW ·
  wrkq task/handoff HIGH · effect-delivery LOW · ledger structure LOW · methodology-as-installed MED.
  + NEW: "does wrkf escape the basin's agent-specific blind spots" — UNASSESSED (Q10–Q14 target this).

[Stage log]
  T0 contract registered.
  T1 (this turn): mapped default basin — standard model, assumptions A1–A8, shallow moves M1–M7
     (+ enum-and-gate reflex), attractor forces, agent-specific blind spots. Added Q10–Q14.
     NEXT (proposed): import EXOTIC LENSES / source domains (your next turn) — OR, if you prefer,
     a read-and-reconcile pass against proposal/02-04 + schemas to start answering Q1/Q10-Q14 with
     evidence rather than inference. Your call on ordering.


---

## Turn 3 — Default Basin

TURN 2 — SOURCE-DOMAIN DICTIONARY (harvest, not synthesis; no taxonomy; no gap/fix lists).
Format: Concept — mechanistic core → what it might illuminate for wrkf/wrkq  [tag].
Tags: [STRONG]=strong-transfer-candidate · [weak]=weak/decorative.

─── 1. PETRI NETS / WORKFLOW-PATTERN THEORY ───────────────────────────
• Token & marking — state = a DISTRIBUTION of tokens over places, not one enum; concurrency native →
  a single `status` can't represent several simultaneous in-flight obligations/branches  [STRONG]
• Transition enabling rule — fires only when ALL input places hold tokens; consumes+produces them →
  "all preconditions present" as token availability, richer than one boolean gate  [STRONG]
• Soundness (van der Aalst) — from every reachable marking the final marking is reachable; no dead
  transitions; proper completion → a CHECKABLE template property: can every state reach `closed`?  [STRONG]
• Deadlock / livelock — marking with no enabled transition / cycle that never progresses → formal
  name for the-stall; detect templates that can wedge  [STRONG]
• Milestone & deferred-choice patterns — a transition enabled only while a condition holds / choice
  resolved by environment not actor → "review window open", "first responder wins"  [STRONG]
• Free-choice / confusion structural conditions — when choice & concurrency interfere → template
  well-formedness lint  [weak]
• Data/resource patterns (case handling, role allocation) — binding data & performers to cases →
  resource = role binding  [weak]

─── 2. STATECHARTS (HAREL) ────────────────────────────────────────────
• Orthogonal regions (AND-states) — independent concurrent sub-machines inside ONE state → THE way to
  hold multiple simultaneous obligations/parallel tracks without a status explosion  [STRONG]
• Hierarchy (OR/nested states) — a superstate factors transitions shared by its children → one
  `active` superstate whose sub-phases share an escalation edge  [STRONG]
• History states (shallow/deep) — re-entering a superstate resumes where it left → resume-after-
  handoff and resume-after-supervisor-recovery semantics  [STRONG]
• Guard vs event — a transition needs BOTH a triggering event AND a true guard → cleanly separates
  "what happened" (evidence arrived) from "is it allowed" (check verdict)  [STRONG]
• Entry/exit actions — actions bound to occupying a state, not to an edge → wrkf stateHooks; tie
  effects to state OCCUPANCY not only transitions  [STRONG]
• Internal (self) transition — re-fires logic without re-entering the state → iterate-in-place vs
  reopen  [weak]

─── 3. DATABASE TRANSACTIONS ──────────────────────────────────────────
• Sagas — a long task = sequence of local commits, each with a COMPENSATING action; rollback runs
  compensations in reverse, no global lock → THE model for undo across long agentic workflows; effects
  get compensated, not erased  [STRONG]
• Compensating transaction — semantic undo (refund, not DELETE) → reversing an outcome may mint
  compensation-obligations  [STRONG]
• Idempotency key — dedupe a retried op by client-supplied key → effect outbox needs keys so
  re-delivery doesn't double-fire  [STRONG]
• Atomicity — transition + its effects + obligations commit all-or-nothing → partial transitions never
  observable (Q: does wrkf commit these atomically?)  [STRONG]
• Isolation levels — what concurrent actors observe (dirty/uncommitted reads) → two agents on one
  task/lane; can one see another's uncommitted evidence?  [STRONG]
• 2-phase commit — prepare→vote→commit; coordinator+participants → a multi-role transition where
  several roles must agree before it commits  [STRONG]
• Write-ahead log — log intent before applying, for durability/recovery → ledger as WAL for
  transitions (overlaps §4)  [STRONG]

─── 4. EVENT SOURCING / CQRS / APPEND-ONLY LOGS ───────────────────────
• Event log as source of truth — current state = a FOLD over an immutable event stream; state is
  derived & disposable → INVERTS basin-A2: ledger is truth, `status` is a projection (directly Q5)  [STRONG]
• Projections / read models (CQRS) — many derived views from one log → wrkq task-state could be a
  PROJECTION of the wrkf event log, not an independent field (directly Q1)  [STRONG]
• Command vs event — command = request that may fail; event = immutable fact that happened → "agent
  CLAIMS done" (command) vs "verdict recorded" (event); maps to Q12  [STRONG]
• Replay / rebuild — recompute state by replaying the log → audit + "what did the workflow believe at
  time T"  [STRONG]
• Eventual consistency (log↔projection) — projections lag the log → `status` may lag the ledger; which
  is authoritative on disagreement?  [STRONG]
• Snapshots — materialize state to bound replay cost → operational, mostly out of scope  [weak]

─── 5. DURABLE EXECUTION (TEMPORAL/CADENCE) ───────────────────────────
• "Exactly-once effect" illusion — reality is at-least-once execution + idempotency = effectively-once
  → demystifies Q4: you CANNOT get exactly-once delivery; you get at-least-once + dedupe key  [STRONG]
• Durable timer — a sleep that survives process death and fires as a real event → first-class
  deadline/SLA; the-stall escape (Q11)  [STRONG]
• Signal — external async input delivered into a running workflow → effect-reply / evidence-arrival /
  handoff-resume modeled as a signal into the instance  [STRONG]
• Activity vs orchestration split — deterministic decision code vs retryable side-effecting work →
  keep gating logic pure; externalize effects (wrkf already: checks ref ids, no embedded commands)  [STRONG]
• Retry policy w/ backoff — automatic re-execution under a declared policy → check/effect-handler
  failure policy; feeds supervisor recovery  [STRONG]
• Heartbeating — long activity must report liveness; silence ⇒ presumed dead → agent-stall detection  [STRONG]
• Determinism constraint — replay must be deterministic; nondeterminism (clock/random) only via engine
  → why policy must be pure & effects externalized  [STRONG]
• Continue-as-new — reset history while preserving logical continuity → long iteration loops  [weak]

─── 6. BUILD / DAG SYSTEMS (MAKE/BAZEL) ───────────────────────────────
• Content-addressing (hash inputs) — identity by content hash, not name → evidence pinned to artifact
  HASHES (this is the wrkf-tasker "fresh hash-pinned artifacts" discipline)  [STRONG]
• Dirtiness / staleness — a node is stale iff its inputs changed → "this verdict attested to artifact
  v1; artifact is now v2 ⇒ verdict is STALE ⇒ re-verify"  [STRONG]
• Cache + invalidation keyed by input hash — reuse prior result if inputs unchanged → reuse a prior
  check verdict on resubmit unless inputs changed (iteration efficiency)  [STRONG]
• Hermeticity / sandboxing — a step sees only declared inputs ⇒ reproducible → a verdict is
  trustworthy only if it was hermetic; anti-gaming  [STRONG]
• Dependency edges + topo order — do X only after deps satisfied → task/obligation ordering  [STRONG]
• Minimal incremental rebuild — redo only what changed → re-run only checks whose inputs changed  [STRONG]

─── 7. DEONTIC LOGIC & LEGAL CONTRACTS ────────────────────────────────
• Deontic triad O/P/F — Obligation(must) / Permission(may) / Prohibition(must-not) → wrkf models
  obligations; permissions & PROHIBITIONS as first-class are likely absent  [STRONG]
• Contrary-to-duty / conditional obligation — "if O1 is breached, then remedy O2 applies" → what
  obligation fires when a check FAILS or a deadline passes; escalation as a CTD duty  [STRONG]
• Directed obligation (obligor → obligee) — duties are relational: someone owes someone → wrkf
  obligations have ownerRole; do they have a BENEFICIARY/claimant who can demand discharge?  [STRONG]
• Breach & remedy — violation triggers a DEFINED consequence, not just a halt → failure outcomes
  should mint remedy-obligations, not merely route to error  [STRONG]
• Discharge modes — performed / waived / excused / cancelled → obligation lifecycle (wrkf has
  satisfied/waived/cancelled — strong fit)  [STRONG]
• Power vs duty (Hohfeld) — a "power" CHANGES the normative state (e.g. authority to waive) → who may
  waive an obligation is a power, distinct from holding a duty; anti-self-waiver  [STRONG]

─── 8. DOUBLE-ENTRY ACCOUNTING & AUDIT ────────────────────────────────
• Balanced entries (debit=credit) — nothing appears/vanishes without a matching counter-entry → every
  effect EMITTED needs a balancing DELIVERED/ACKED entry; imbalance = outstanding effect  [STRONG]
• Append-only + reversing entries — never edit a posting; correct via a new reversing entry → ledger
  discipline; "fix" = compensating entry (overlaps sagas/event-sourcing)  [STRONG]
• Reconciliation / trial balance — periodically prove the books are internally consistent → reconcile
  effects-emitted vs delivered vs obligations-open; detect drift  [STRONG]
• Per-entry provenance (who/when/supporting doc) — every posting cites its source → evidence must
  carry actor + time + source-artifact  [STRONG]
• Accrual recognition — recognize a liability when INCURRED, not when settled → obligation exists the
  instant an outcome routes it (already wrkf-ish)  [weak]

─── 9. HOARE LOGIC / DESIGN-BY-CONTRACT ───────────────────────────────
• Pre/postcondition {P}step{Q} — a step assumes P, guarantees Q → transition `requires` = precondition;
  its `to`+effects = a guaranteed, CHECKABLE postcondition  [STRONG]
• Invariant — property true in EVERY reachable state, enforced always → e.g. "an open blocking
  obligation ⇒ task cannot be closed", engine-enforced globally, not per-edge  [STRONG]
• Frame condition — what a step does NOT touch → an effect/transition declares its footprint;
  anti-surprise-side-effect  [STRONG]
• Assertion vs assumption (blame) — if a condition is false, is caller or callee at fault → if evidence
  is false: submitter's breach vs checker's miss; assigns responsibility  [STRONG]
• Behavioral subtyping (LSP) — a refined contract may not weaken postconditions / strengthen
  preconditions → template versioning/forking must not silently weaken guarantees  [STRONG]
• Ghost/auxiliary state — state existing only to make a property provable → evidence that exists only
  to make a property checkable  [weak]

─── 10. MECHANISM DESIGN / PRINCIPAL-AGENT ────────────────────────────
• Cheap talk vs costly signal — claims free-to-fake carry no info; signals expensive-iff-false do →
  "tests pass" (cheap talk) vs "reproducible run pinned to hash X" (costly signal): THE anti-fabrication
  lever  [STRONG]
• Incentive compatibility — mechanism is IC if truth-telling is the agent's best move → design checks
  so fabricating evidence is dominated by actually doing the work  [STRONG]
• Verification cost / random audit — costly or random checks deter gaming even if not every claim is
  verified → spot-check evidence; observer review as costly verification  [STRONG]
• Moral hazard / hidden action — principal sees outcome, not effort → can't observe "really tested";
  must infer from verifiable artifacts  [STRONG]
• Collusion among agents — agents reviewing each other can collude → independent-observer requirement;
  reviewer ≠ implementer (wrkf-tasker already enforces distinct roles)  [STRONG]
• Goodhart — optimizing the proxy degrades the goal → status=Done is a gameable proxy; reward the
  latent goal via evidence, not position  [STRONG]
• Adverse selection / screening — separating types via menu of options → weak fit here  [weak]

─── 11. PROVENANCE / SCIENTIFIC-WORKFLOW ──────────────────────────────
• W3C PROV triple (Entity/Activity/Agent + used / wasGeneratedBy / wasAssociatedWith) — a standard
  lineage model → ready-made schema: "agent X produced evidence E using artifact A in transition T"  [STRONG]
• Lineage graph (what-derived-from-what) — each artifact records inputs+process → trace any verdict
  back through its inputs; a provenance DAG over evidence  [STRONG]
• Reproducibility / re-execution — re-run the recorded process, get the same result → can a check be
  re-run from the ledger to reproduce its verdict?  [STRONG]
• Retraction / invalidation propagation — invalidating a source invalidates all derived from it → if
  an artifact is retracted, dependent verdicts go stale automatically (overlaps build-dirtiness)  [STRONG]
• Attribution — which agent produced which artifact → evidence actor attribution  [STRONG]

─── 12. SAFETY ENGINEERING ────────────────────────────────────────────
• Interlock — a condition that PHYSICALLY prevents an action unless safe → a blocking obligation IS an
  interlock; make blocking structural (unrepresentable-if-unsafe), not advisory  [STRONG]
• Fail-safe vs fail-operational — on failure default to the SAFE state vs keep running → on
  check error/timeout, default to NOT advancing (fail-closed); wrkf's "*"→supervisor_recovery  [STRONG]
• Defense in depth — multiple INDEPENDENT barriers, no single point of trust → layer claim +
  independent verdict + spot audit; one green check is not enough  [STRONG]
• Poka-yoke (mistake-proofing) — make the wrong action IMPOSSIBLE, not merely rejected → make illegal
  transitions unrepresentable, not rejected after attempt  [STRONG]
• Common-cause failure — one fault defeats all redundant barriers at once → if implementer & reviewer
  are the same agent/model, the redundancy is illusory (collusion/common-cause)  [STRONG]
• Safety case / assurance argument — an explicit argued claim "X is safe", backed by evidence → a
  `closed` workflow should carry an assurance argument (claim+evidence+verdict), not just a flag  [STRONG]
• HAZOP/FMEA — systematically enumerate failure modes & effects → a DISCIPLINE for the later gap-hunt:
  per concept, ask "what's the failure mode?"  [STRONG]

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated — added Source-domain dictionary; no synthesis)
═══════════════════════════════════════════════════════════════════════
[Objective / Non-objectives / Scope rules] — unchanged (see T0/T1).
[Glossary] — unchanged.
[Default basin] — unchanged (A1–A8, M1–M7, enum-and-gate reflex, agent blind spots).

[Source-domain dictionary] (NEW — compact index of STRONG candidates by domain; full mechanism notes above)
  Petri:    token/marking · enabling-rule · soundness · deadlock/livelock · milestone/deferred-choice
  Harel:    orthogonal-regions · hierarchy · history-states · guard-vs-event · entry/exit-actions
  TxnDB:    sagas · compensating-txn · idempotency-key · atomicity · isolation · 2PC · WAL
  EventSrc: log-as-truth · projections(CQRS) · command-vs-event · replay · eventual-consistency
  Durable:  exactly-once-illusion · durable-timer · signal · activity/orch-split · retry-policy ·
            heartbeat · determinism-constraint
  Build:    content-addressing · dirtiness/staleness · cache+invalidation · hermeticity · dep-topo ·
            incremental-rebuild
  Deontic:  O/P/F-triad · contrary-to-duty · directed-obligation · breach&remedy · discharge-modes ·
            power-vs-duty(Hohfeld)
  Audit:    balanced-entries · append-only+reversing · reconciliation · per-entry-provenance
  Hoare:    pre/postcondition · invariant · frame-condition · blame(assert-vs-assume) · LSP-subtyping
  MechDes:  cheap-talk-vs-costly-signal · incentive-compatibility · verification-cost/audit ·
            moral-hazard · collusion · Goodhart
  Provenance: W3C-PROV-triple · lineage-graph · reproducibility · retraction-propagation · attribution
  Safety:   interlock · fail-safe/closed · defense-in-depth · poka-yoke · common-cause · safety-case · FMEA
  [Recurring across domains, flagged for later dedup — NOT yet synthesized]:
    append-only/immutable ledger (Petri-WAL ∩ EventSrc ∩ Audit); idempotency/exactly-once illusion
    (TxnDB ∩ Durable); content-hash → staleness → re-verify (Build ∩ Provenance); costly-signal vs
    cheap-talk == evidence-must-be-expensive-to-fake (MechDes ∩ Safety defense-in-depth); compensation/
    reversal not deletion (Saga ∩ Audit-reversing-entry ∩ Deontic-remedy); fail-closed default
    (Safety ∩ Durable-determinism ∩ wrkf "*"→recovery).

[Open questions] Q1–Q14 unchanged, PLUS:
  Q15 Does the ledger support RETRACTION/invalidation propagation (stale verdict when artifact changes)?
  Q16 Are PERMISSIONS and PROHIBITIONS modeled, or only obligations? (deontic completeness)
  Q17 Do effects + obligations commit ATOMICALLY with their transition? Isolation across concurrent actors?
  Q18 Are transition POSTCONDITIONS explicit/checkable, or only preconditions (`requires`)?
  Q19 Does template forking honor behavioral-subtyping (no silent guarantee-weakening)?

[Confidence ledger] unchanged; +note: dictionary is domain knowledge [HIGH], its APPLICABILITY to
  wrkf/wrkq is [unverified] until we reconcile against schemas/spec.

[Stage log]
  T0 contract · T1 default basin.
  T2 (this turn): harvested 12-domain source dictionary (~70 concepts, STRONG/weak tagged); flagged 6
     recurring cross-domain threads for later dedup; added Q15–Q19. No synthesis performed (by constraint).
     NEXT (proposed): either (a) your planned next lens, or (b) a read-and-reconcile pass vs
     proposal/02-05 + schemas to convert Q1/Q4/Q5/Q10-Q19 from inference to evidence before any
     synthesis/taxonomy. Your call.


---

## Turn 4 — Source-Domain Dictionary

TURN 3 — LATENT LENS SWEEP (9 non-isomorphic reads; mechanism-level; no synthesis; no final lists).
Per lens: (a) reframe · (b) what it makes newly visible vs basin · (c) sharp Q's · (d) transfer confidence.

─── LENS 1 · CONTROL LOOPS / FEEDBACK ─────────────────────────────────
(a) Workflow = nested feedback loops. setpoint = acceptance criteria/goal state; sensor = checks
    reading evidence/world; actuator = transitions+effects that change the world; comparator = the
    gate (verdict vs setpoint); loop CLOSES when a sensor confirms the actuation hit the setpoint.
    Durable task = the plant being controlled; effect = an actuation command.
(b) Newly visible: OPEN LOOPS — effects emitted with no sensor confirming their result
    (`wake_role` with no signal the role woke; `request_observer_review` with no sensing the review
    happened). Basin's fire-and-forget IS a deliberately open loop. Also loop LAG (handoff latency
    between actuation and next sensing) and the controlled-variable gap (what's measured vs the latent
    goal).
(c) Q: does every effect have a confirmation-evidence that closes its loop, or are effects open-loop?
    Q: is "completion" SENSED (verified) or COMMANDED (asserted)?
(d) Transfer HIGH — clean mapping; "open-loop effect" is half-present already in the effect/ack split (Q4).

─── LENS 2 · INFORMATION FLOW ─────────────────────────────────────────
(a) Each transition/effect/handoff = a message on a channel. STATE = a lossy summary message (one
    symbol); LEDGER = the full transcript; HANDOFF = a deliberately serialized context packet across
    a session boundary (bounded-bandwidth channel).
(b) Newly visible: information DESTRUCTION. status open→closed keeps the WHY only in the ledger; if the
    ledger didn't capture it, it's gone. State-as-message is lossy compression and the basin treats the
    compressed symbol as the whole truth. Distinguishes "state is the message" (cheap, current
    position) from "ledger is the message" (history, why). Handoff bandwidth = what an agent CHOSE to
    write vs what died with the session.
(c) Q: what information is irrecoverably lost at session end that wasn't written to ledger/handoff?
    Q: is the ledger lossless w.r.t. DECISIONS (reconstruct WHY a transition fired) or only WHAT fired?
(d) Transfer HIGH — info-theoretic state-vs-ledger framing is sharp and non-obvious.

─── LENS 3 · BOUNDARIES / INTERFACES (wrkf↔wrkq seam) ─────────────────
(a) wrkf and wrkq = two components with a contract at their seam: wrkf promises policy/routing, wrkq
    promises durable substrate; the interface = how a wrkf instance reads/writes a wrkq task. Effects
    cross a SECOND boundary (out to adapters).
(b) Newly visible: what LEAKS vs what's AMBIENT. The two state vocabularies
    (wrkf {open,active,waiting,closed} vs wrkq {open,in_progress,completed,blocked,cancelled}) MEET
    here — who owns the mapping? Clean projection or a leak (wrkf reaching into wrkq.status)? Ambient
    coupling: wrkq comments/labels/priority that humans/agents mutate OUT-OF-BAND, invisible to the
    workflow. Key mismatch: handoff is scoped agent@project (wrkq side), roles are per-instance (wrkf
    side) — they don't share a key.
(c) Q: who is AUTHORITATIVE when wrkf says `closed` but a human sets wrkq `blocked`? Single writer?
    Q: can an out-of-band wrkq mutation (reopen, relabel) violate an invariant the engine believes holds?
(d) Transfer HIGH (this is THE Q1 surface) — but my READING confidence LOW: haven't read the mapping (C3).

─── LENS 4 · LIFECYCLE / TIME / ENTROPY ───────────────────────────────
(a) Tasks/obligations are objects whose lifespan can EXCEED the workflow run that created them. Entropy
    = drift between what the record SAYS and what is TRUE, growing with time. Effect = an event with a
    decay half-life (a review request goes stale).
(b) Newly visible: tasks outliving their workflow (template version frozen/uninstalled, task persists —
    what enforces invariants then?); abandoned `in_progress` (agent died mid-task; no liveness
    primitive notices); immortal obligations (never resolve — TTL? escalation?); evidence DECAY (verdict
    pinned to artifact v1 is stale at v2 — Q15); orphaned effects (target gone). Basin hid all of this
    by assuming monotonic motion + human cleanup.
(c) Q: what happens to open obligations / in-flight effects when a task is reopened/cancelled or its
    template uninstalled? Q: any TTL/freshness/escalation timer, or do stale records persist as silent lies?
(d) Transfer HIGH — entropy/staleness is under-modeled in basin; hits Q11/Q15 directly.

─── LENS 5 · FORMAL MODELS (3 views; disagreement = signal) ───────────
(a) Three simultaneous reads:
    • State machine: done = reached terminal `closed` via legal edges.
    • Deontic system: done = ALL obligations discharged, no prohibition violated.
    • Hoare chain: workflow = {P0}t1{P1}…{Pn} + global invariant I; done = Pn established AND I held throughout.
(b) Newly visible — the DISAGREEMENTS (the signal):
    D1 SM-done but deontic-NOT-done: closed with an open blocking obligation. Diverges UNLESS the
       invariant "open-blocking-obligation ⇒ ¬closed" is ENGINE-enforced.
    D2 SM-done but Hoare-NOT-done: reached `closed` but postcondition unproven (evidence present, not
       verified) — claimed-vs-demonstrated, now formal: reaching closed ≠ establishing Pn.
    D3 Deontic permits an act with no SM edge (or SM edge with no permitted role): `by` vs `requires`
       can disagree on who-may vs who-must.
    D4 Hoare frame violated: an effect mutated something outside its declared footprint, invisible to
       the SM (which tracks only status).
    Three non-coinciding "definitions of done" ARE the core ontological signal.
(c) Q: is the deontic⊨SM invariant (obligations gate closure) engine-enforced? Q: is there any notion
    of transition POSTCONDITION (Hoare Q), or only precondition (`requires`)? (Q18)
(d) Transfer VERY HIGH — triangulation is the most productive lens; each disagreement is a candidate gap.

─── LENS 6 · INCENTIVES (honest system, GAMING ACTOR) ─────────────────
(a) Agent = utility-maximizer; utility = "advance the workflow at least cost." Each transition is a
    move; template = rules; checks = referee; evidence = the agent's signals (cheap or costly).
(b) Newly visible — letter-without-substance attack surface (pass the check, skip the work):
    • presence-only checks: a predicate testing that an evidence RECORD EXISTS, not that it's true →
      submit a plausible record.
    • self-refereeing: if the verifying transition's `by` role can be the SAME actor who did the work
      (reviewer≠implementer enforced only by author CONVENTION, not engine) → agent reviews itself.
    • wrong-thing hook: exitMap 0→pass but the script checks "file exists" not "tests ran" → green &
      empty.
    • stale-but-present: resubmit v1's verdict for a changed artifact (no content-hash freshness, Q15).
(c) Q: which checks test evidence TRUTH vs mere PRESENCE — is presence-only gateable? Q: does the
    ENGINE (not just template discipline) forbid producer==verifier on the same task?
(d) Transfer VERY HIGH — directly the project's stated fear (agents fabricate evidence).

─── LENS 7 · ADVERSARIAL DYNAMICS (the SYSTEM'S surfaces lie) ─────────
(a) Sharpened vs lens 6: here the ACTOR may be honest but the SYSTEM misrepresents reality. The
    surfaces themselves (next-action, "legal transitions", validators, effect-acks) are the failure
    surface — they can ADVERTISE falsehoods.
(b) Newly visible:
    • surface advertises an illegal transition as legal: `wrkf next` lists a move whose guard would
      fail, or omits a required obligation → good-faith agent acts on a lying surface. (Exactly why the
      wrkf-debug skill exists.)
    • validator accepts paper-only evidence: script returns pass on well-formed-but-empty artifact —
      soundness gap in the CHECK, not the actor.
    • effect ack'd without delivery: outbox marks delivered though no adapter delivered (or a dry-run
      ack leaks to prod) → workflow believes review is underway; it isn't.
    • verdict laundering: an error/inconclusive result mapped via a loose exitMap `*` to pass instead
      of recovery.
(c) Q: soundness guarantee that legality surfaces never advertise a transition the engine would reject
    (surface ⊑ engine)? Q: is effect delivery acked by the ADAPTER with proof, or can the engine
    self-ack on emit?
(d) Transfer HIGH; gaps-exist confidence UNKNOWN (need to exercise surfaces), lens-is-right confidence HIGH.

─── LENS 8 · FAILURE ANALYSIS (FMEA; shapes cross-indexed to lenses) ──
(a) Catalog the canonical failure SHAPES, each tagged with the lens that surfaces it.
(b) Shapes:
    • STUCK/deadlock — no enabled transition / unsatisfiable guards (Petri; L1 loop never closes; L4 stall)
    • LYING — present-but-false evidence passes a check (L6/L7; cheap-talk)
    • DOUBLE-EFFECT — effect re-fired on retry w/o idempotency (L1 actuator fires twice; exactly-once illusion)
    • ORPHANED OBLIGATION — debt whose owner role is unbound/dead; undischargeable (L4; deontic)
    • LOST HANDOFF — context packet never written/acked; fresh session can't reconstruct (L2/L9)
    • PHANTOM PROGRESS — status advanced, postcondition not established (L5 D2)
    • INVARIANT BREACH via out-of-band mutation — direct wrkq edit falsifies a believed invariant (L3)
    • VERDICT STALENESS — v1 verdict silently applied to v2 (L4; Q15)
(c) Q: for each shape — does the engine DETECT, PREVENT, or only allow post-hoc FORENSICS? Q: which
    shapes are even REPRESENTABLE as a workflow state (is "stuck" a state, or just absence of progress)?
(d) Transfer HIGH — FMEA discipline applied; the BRIDGE toward the eventual gap list (not the list itself).

─── LENS 9 · HUMAN/AGENT FACTORS (reconstruction from record alone) ───
(a) The durable record (task + ledger + handoffs + comments) is the ENTIRE memory a fresh session has.
    A workflow, from here, is a MEMORY PROSTHESIS; effect/handoff = messages to a future amnesiac self.
(b) Newly visible — the reconstruction gap: what a fresh agent CAN rebuild (status, evidence records,
    open obligations, last handoff) vs CANNOT (the reasoning never written; why this path over that;
    tacit context; whether a green check was actually trustworthy AT THE TIME). Basin assumes the actor
    remembers; here the record IS the mind — anything unwritten is gone. Also record READABILITY:
    reconstruct intent fast, or re-derive it?
(c) Q: from the record ALONE, can a fresh session determine WHAT to do next AND WHY, without
    re-deriving context (is `next` + ledger + handoff sufficient)? Q: does the record distinguish
    "verified" from "claimed" so a fresh agent doesn't re-trust cheap-talk evidence?
(d) Transfer VERY HIGH — the agentic crux; durability exists precisely FOR cross-session reconstruction.

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated — added Lens sweep; no synthesis)
═══════════════════════════════════════════════════════════════════════
[Objective / Non-objectives / Scope rules / Glossary / Default basin / Source-domain dictionary] — unchanged.

[Lens sweep] (NEW — 9 distinct reads; full notes above)
  L1 control-loops → open-loop effects (no confirmation), sensed-vs-commanded completion.
  L2 info-flow → state=lossy summary, ledger=transcript, handoff=bounded channel; info DESTRUCTION at session end.
  L3 boundaries → wrkf↔wrkq seam owns the two-state-vocabulary mapping; out-of-band wrkq mutation = ambient coupling.
  L4 entropy → tasks outliving workflows, immortal obligations, evidence decay, abandoned in_progress.
  L5 formal-triangulation → SM-done vs deontic-done vs Hoare-done DISAGREE (D1–D4) = core signal.
  L6 gaming-actor → presence-only checks, self-refereeing, wrong-thing hooks, stale-but-present evidence.
  L7 lying-system → surfaces advertise illegal-as-legal, paper-only validators, ack-without-delivery, verdict laundering.
  L8 FMEA → 8 failure shapes (stuck/lying/double-effect/orphaned-obligation/lost-handoff/phantom-progress/
     invariant-breach/staleness); detect-vs-prevent-vs-forensic open.
  L9 reconstruction → record = the only memory; can a fresh session recover WHAT+WHY, verified-vs-claimed?
  Distinctness check: L6=gaming actor / L7=lying system (sharpened apart); L8 intentionally cross-indexes the others.

[Open questions] Q1–Q19 unchanged, PLUS:
  Q20 Does every effect have a loop-closing confirmation, or are some fire-and-forget (open-loop)? (L1)
  Q21 Is the ledger lossless w.r.t. DECISIONS (why a transition fired), or only what fired? (L2/L9)
  Q22 Single-writer/authority at the wrkf↔wrkq seam; can out-of-band wrkq mutation break a believed invariant? (L3)
  Q23 Lifecycle of open obligations & in-flight effects on task reopen/cancel or template uninstall? (L4)
  Q24 Do the three definitions of done coincide; is "obligations gate closure" (deontic⊨SM) engine-enforced? (L5/D1)
  Q25 Does the ENGINE (not convention) forbid producer==verifier on a task's verifying transition? (L6)
  Q26 Soundness: can `next` advertise a transition the engine would reject? Can effects be acked w/o adapter proof? (L7)
  Q27 Per failure shape — detected, prevented, or forensic-only? Which are representable as a state? (L8)

[Confidence ledger] unchanged; +note: lens READS are HIGH-quality; their APPLICABILITY/whether each
  gap is real is LOW-MED pending a reconcile against schemas + a live instance (C1/C2/C3 still open).

[Stage log]
  T0 contract · T1 basin · T2 source-dictionary.
  T3 (this turn): 9-lens sweep, kept non-isomorphic; L5 triangulation + L8 FMEA are the highest-yield;
     added Q20–Q27. No synthesis (by constraint).
     NEXT (proposed): the open-Q list (Q1–Q27) is now large and mostly INFERENCE. Strong recommendation
     before synthesis/taxonomy: one read-and-reconcile pass vs proposal/02-05,08,09,11 + the schemas
     (workflow-template, workflow-instance, workflow-event, effect, next-action) to convert the
     highest-leverage Q's (Q1,Q4,Q5,Q15,Q18,Q24,Q25,Q26) from inference→evidence. Say the word and I'll do it.


---

## Turn 5 — Latent Lens Sweep

TURN 4 — TRANSPOSITION ENGINE. No analogy survives without all five fields. No final lists; no organizing structure chosen.

⚑ GROUNDING NOTE: I read the source this turn (schemas: template/instance/event/effect/next-action;
proposal 02/03/04/07). Mechanism + Detection fields are now [verified-from-spec], not inference. This
RESOLVES several open questions — pruned below. Citations: [tmpl]=template schema, [eff]=effect schema,
[evt]=event schema, [inst]=instance schema, [02]/[04]/[07]=proposal docs.

═══ TRANSPOSITION RULES (import gate — ALL five required) ═══
A concept imports iff I can fill: (1) Causal structure in target terms · (2) Mechanism = the actual
wrkf/wrkq operation (guard / ledger event / obligation row / check / effect outbox row / kernel
invariant) · (3) Boundary (where it holds / breaks) · (4) Detection signal in real wrkf/wrkq data
(a row, a status, an event field) · (5) Intervention logic (the contract change implied — described,
not recommended). Else → [rejected: surface-only].

═══ TRANSPOSED CONCEPT INVENTORY (12 accepted; ★=core relationship/effect) ═══

T1 ★ LOG-AS-TRUTH + CQRS PROJECTION (event-sourcing)  — conf HIGH
 Causal: workflow.state is canonical; wrkq task.state is a DERIVED projection — a transition causes
   both, atomically.  Mech: kernel invariant "same-transaction workflow + task.meta.workflow update"
   [04]; "task being completed is evidence, not workflow completion" [02]; hash-chained event log
   (seq/prevEventHash/eventHash) [evt].  Boundary: holds for engine writes; BREAKS if a human/agent
   writes wrkq task.state out-of-band (projection diverges from canonical).  Detect: a wrkq task.state
   with no corresponding workflow_event seq; contextHash/taskDocEtag mismatch [inst].  Intervene:
   declare task.state writes that bypass the engine as either forbidden or auto-reconciled.

T2 ★ IDEMPOTENT OUTBOX / "EXACTLY-ONCE ILLUSION" (durable-exec + txn)  — conf HIGH
 Causal: a committed outcome causes an effect-INTENT row; an adapter later causes real-world delivery.
   Mech: EffectSpec→WorkflowEffect outbox row {status: pending→leased→delivered/failed/cancelled,
   idempotencyKey, attempts, leasedBy/Until, deliveredAt} [eff][07]; "adapter must not observe/deliver
   uncommitted effects" [07].  Boundary: effectively-once HOLDS for delivery dedup; BREAKS as a truth
   guarantee — "delivered" means the ADAPTER acked, not that the world changed (trust moves to adapter).
   Detect: effect rows status=delivered with no corresponding confirming evidence event.  Intervene:
   require a delivery PROOF payload (not a bare ack) for effect kinds whose real-world success is
   verifiable.

T3 ★ SAGA / COMPENSATION (txn)  — conf MED-HIGH
 Causal: an emitted effect can be undone by a compensating effect; a committed STATE change cannot be
   rolled back, only superseded forward.  Mech: `cancel_effect` is a first-class effect kind [tmpl][eff];
   recovery = forward transition to active/error phase, NOT rollback (afterCommit "no rollback" [04]).
   Boundary: compensation HOLDS for pending/leased effects; BREAKS once an effect is delivered &
   consumed (a handoff already read, a task already created) — irreversible.  Detect: cancel_effect
   rows targeting an already-delivered effect.  Intervene: define per-effect-kind compensability +
   compensation handlers for delivered-but-reversible effects.

T4 ★ INTERLOCK = BLOCKING OBLIGATION (safety + deontic)  — conf HIGH
 Causal: an open blocking obligation makes closure structurally impossible.  Mech: kernel invariant
   "blocking obligations are satisfied/waived" [04]; obligationCreateSpec.blocking [tmpl];
   predicate obligationStatus readable in guards [tmpl].  Boundary: HOLDS for blocking=true; BREAKS
   for blocking=false (advisory; does NOT gate closure → can orphan).  Detect: workflow_obligations
   rows blocking=false, status=open, on a closed instance.  Intervene: a lifecycle policy for
   non-blocking obligations at closure (auto-cancel, carry-forward, or escalate).

T5 ★ COSTLY SIGNAL vs CHEAP TALK (mechanism design)  — conf HIGH
 Causal: a check's TYPE determines whether passing it costs the work it attests to.  Mech: type:hook
   running a real script (e.g. live_smoke_verified, timeoutMs 300000) = costly signal; type:role
   attestation + predicate {evidenceExists} = cheap talk (tests presence/shape, not truth) [tmpl][04].
   Boundary: a hook is costly ONLY if hermetic & actually exercises the artifact; a hook that greps
   file-existence is cheap-talk in a hook costume.  Detect: transitions whose only gate is type:role
   or {evidenceExists}; evidenceKindSpec.schema validates shape, never substance.  Intervene: mark
   each check presence-class vs verification-class in the contract; forbid presence-only gating on
   completion transitions.

T6 ★ SEPARATION OF DUTIES / COMMON-CAUSE FAILURE (safety + principal-agent)  — conf MED
 Causal: if the verifying actor == the producing actor, the independent barrier collapses.  Mech:
   kernel invariant "separation-of-duties requirements hold" [04] — BUT the v0 template schema exposes
   NO declarative SoD slot (no field on transitionSpec/roleSpec to SAY producer≠verifier; roleSpec has
   only candidateActors/capabilities/bind) [tmpl].  Boundary: enforced IF requirements are expressible;
   the declaration surface is unclear in v0.  Detect: two workflow_events — producing transition.actor
   == verifying transition.actor (same actor, or same underlying model = common-cause).  Intervene: a
   first-class SoD constraint (transition.actor ≠ named prior transition's actor) the kernel can read.

T7 ★ CONTENT-HASH → STALENESS → RE-VERIFY (build/DAG + provenance)  — conf MED-HIGH
 Causal: changing an input should invalidate any verdict derived from it.  Mech: drift detection EXISTS
   at the task/context level (instance.taskDocHash/contextHash; kernel invariants "expected etag/context
   hash current") [inst][04].  Boundary: HOLDS for the task doc; BREAKS for EVIDENCE — evidence/check_run
   rows carry no first-class "produced-from artifact hash", so a verdict pinned to artifact v1 is NOT
   auto-stale at v2.  Detect: check_run referencing an artifact whose hash changed, with no
   re-run event.  Intervene: evidence carries producedFromHash; staleness invalidation propagates to
   dependent verdicts.

T8 CONCURRENCY-AS-OBLIGATION-SET (Harel orthogonal regions, REDIRECTED)  — conf MED-HIGH
 Causal: multiple simultaneous in-flight commitments are carried by the obligation SET, not by the
   state tuple.  Mech: state is a single {status,phase,outcome} + "one active instance per task" [04],
   so concurrency CANNOT live in states; it lives as N concurrent workflow_obligations rows [tmpl].
   Boundary: HOLDS as long as parallel tracks are expressible as independent obligations; BREAKS if two
   tracks need independent PHASE progression (the single phase scalar can't represent two positions).
   Detect: >1 open obligation of different kinds on one instance with a single phase value.  Intervene:
   either accept obligations as the concurrency primitive explicitly, or add orthogonal sub-phases.

T9 ★ DIRECTED OBLIGATION (obligor → obligee) (deontic / Hohfeld)  — conf HIGH
 Causal: a duty is relational — someone owes it TO someone who may demand discharge.  Mech: obligation
   models the OBLIGOR side only (ownerRole/ownerRoleFrom/ownerActor) [tmpl]; NO beneficiary/obligee
   field.  Boundary: HOLDS for "who must act"; BREAKS for "who may demand / who is harmed by breach"
   and for cross-task obligees.  Detect: n/a (field absent) — the absence IS the signal.  Intervene:
   add obligee/beneficiary + a "power to waive" distinct from "duty to perform" (anti-self-waiver).

T10 PROVENANCE TRIPLE / CAUSATION CHAIN (W3C-PROV)  — conf HIGH (mostly already realized)
 Causal: every event records agent + activity + the event that caused it.  Mech: events carry
   actor/role/runId/causationId/correlationId + hash chain [evt] ≈ PROV (agent=actor, activity=event,
   wasAssociatedWith/causedBy).  Boundary: lineage HOLDS over events; the entity→entity "wasDerivedFrom"
   link (evidence derived from which artifact) is the gap (= T7).  Detect: causationId chains in
   workflow_events.  Intervene: extend provenance from events to evidence-artifact derivation.

T11 ★ HOARE PRE/POST + INVARIANT (design-by-contract)  — conf HIGH
 Causal: a transition assumes a precondition and should guarantee a postcondition; invariants hold
   always.  Mech: preconditions = requires + guards [tmpl]; global invariants = the kernel-invariant
   list [04]; postcondition has NO declarative construct — only the beforeCommit hook ("final short
   invariant check") approximates it [04].  Boundary: pre + global-invariant present; per-transition
   POSTcondition (the `to` state's defining property actually holds) is absent as policy.  Detect: an
   outcome.to with no check asserting the to-state property.  Intervene: declarative postcondition on
   outcome.to, engine-verified at beforeCommit.

T12 ★ TASK↔TASK DEPENDENCY EDGE (build DAG + relationships core)  — conf MED-HIGH
 Causal: task B should be gated on task A's workflow reaching closure.  Mech: task CREATION is
   expressible (request_task_creation effect) [tmpl]; wrkq containers give parent/child; but there is
   NO first-class cross-task DEPENDENCY/blocks/derived-from edge in the workflow contract — "one active
   instance per task" scopes workflows per-task [04].  Boundary: intra-task obligations HOLD;
   inter-task gating BREAKS (a wrkq `blocked` task has no engine link to what unblocks it).  Detect: a
   wrkq task in state=blocked with no obligation/effect tying its release to another task's closure.
   Intervene: a cross-task obligation (obligee on task B, satisfied by task A's closure event).

═══ EXPLICIT KILLS (decorative — do NOT carry) ═══
✗ TWO-PHASE COMMIT (multi-party prepare/vote)  [rejected: surface-only]
   — wrkf transitions are single-actor atomic commits guarded by optimistic concurrency (revision/etag),
   with one active instance per task. There is no participant-vote/prepare phase and no coordinator/
   participant structure to map onto. "Multi-role agreement" is modeled as sequential transitions +
   obligations, not 2PC. Causal core (blocking cross-node prepare-then-commit) has no target operation.
✗ ISOLATION LEVELS / DIRTY READS  [rejected: surface-only for v0]
   — "one active instance per task" + optimistic-conflict rejection means there is no MVCC visibility
   model; concurrent actors serialize via revision conflict, not via read-isolation. No distinct target
   mechanism for "what uncommitted state can a concurrent reader see." (Re-open only if multi-lane
   concurrent writers land.)

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated — added Transposed concept inventory; pruned open Q's via reconcile)
═══════════════════════════════════════════════════════════════════════
[Objective / Non-objectives / Scope rules / Default basin / Source-domain dictionary / Lens sweep] — unchanged.

[Glossary] (pruned/corrected by reconcile — [verified] now)
  • workflow.state {open,active,waiting,closed} is CANONICAL; wrkq task.state {open,in_progress,
    completed,blocked,cancelled} is a PROJECTION written same-transaction [02][04]. "Task completed"
    = evidence, NOT workflow closure.
  • effect = durable outbox row, lifecycle pending→leased→delivered/failed/cancelled, idempotencyKey,
    adapter-acked (engine never self-acks) [eff][07]. declare_handoff & request_task_creation are
    effect KINDS → handoffs and child-tasks are emitted as effects.
  • ledger = ONE hash-chained append-only event log (seq/prevEventHash/eventHash + causation/
    correlation) [evt]. Evidence & obligations are rows written alongside.
  • check kinds: builtin/predicate/hook/role [tmpl]. obligation has owner side only (no obligee).
  • kernel invariants (non-bypassable) include: separation-of-duties, blocking-obligations-satisfied,
    revision/etag/contextHash current, idempotency non-conflicting, one-active-instance-per-task,
    same-transaction projection [04].

[Transposed concept inventory] (NEW) T1 log-as-truth/CQRS · T2 idempotent-outbox · T3 saga/cancel_effect ·
  T4 interlock=blocking-obligation · T5 costly-vs-cheap-signal · T6 SoD/common-cause · T7 hash→staleness ·
  T8 concurrency-as-obligation-set · T9 directed-obligation(obligee gap) · T10 provenance-triple ·
  T11 Hoare pre/post(no declarative post) · T12 task↔task dependency edge.  KILLS: 2PC, isolation-levels.

[Open questions] — PRUNED. Resolved by reconcile: Q1(projection✓) Q3(declare_handoff effect✓)
  Q4(lease+idempotency outbox✓) Q5(one hash-chained log✓) Q8(one active instance/task✓)
  Q17(atomic same-txn; concurrency=optimistic✓) Q22(etag/contextHash kernel invariant✓)
  Q24(blocking-obligation kernel-gated✓).  PARTIAL: Q11/Q7(fallback.on:timeout exists [tmpl]; is there
  an AUTONOMOUS timer firing on idle? unconfirmed) · Q15(task-doc drift✓ / evidence-artifact staleness✗
  =T7) · Q18(pre✓ post✗ =T11) · Q25(SoD invariant✓ / declaration surface✗ =T6) · Q26(effect ack=adapter
  not engine✓ / `next` surface-soundness untested) · Q16(permission via by[]/guards✓ / prohibition &
  obligee✗ =T9) · Q12(claim-vs-verdict = "task-completed-as-evidence ≠ workflow-closed" partially✓).
  STILL OPEN: Q2,Q6(comments=free-form vs evidence=typed+schema; overlap untested) · Q13(post-closed
  obligations) · Q14(iteration loop — outcome routing back to active/phase IS first-class; confirm) ·
  Q19(LSP/forking guarantees) · Q20(open-loop effects: status=delivered closes the DELIVERY loop, but a
  wake_role has no required confirming evidence — template-dependent) · Q23(obligation/effect lifecycle
  on reopen/cancel/uninstall) · Q27(per failure shape: detect/prevent/forensic — now answerable for most).
  NEW: Q28 does an autonomous scheduler fire fallback.on:timeout on an idle instance, or only on next act?
  Q29 are non-blocking obligations ever reconciled at closure, or silently orphaned? (T4 boundary)

[Confidence ledger] MAJOR UPGRADE: wrkf policy+contract HIGH [verified vs schema/spec]; effect/ledger/
  projection mechanics HIGH [verified]; SoD/timer/evidence-staleness MED [invariant or field present,
  enforcement/scheduler unconfirmed]; live runtime behavior still LOW (C2 — no instance exercised).

[Stage log] T0 contract · T1 basin · T2 dictionary · T3 lenses.
  T4 (this turn): stated 5-field transposition gate; reconciled against schemas+proposal (resolved 8
     open Q's, partially 7); ran engine → 12 grounded transpositions (T1–T12) + 2 explicit kills.
     Pruned glossary/open-questions to verified state. No organizing structure chosen (by constraint).
     NEXT (proposed): either (a) exercise a LIVE instance to lift C2 (run agent-tasker template on a
     throwaway task; observe real effect rows / event chain / next-action / a forced stall) — converts
     Q20/Q27/Q28 from spec-inference to observed; or (b) proceed to the organizing-structure/taxonomy
     turn now that transpositions are grounded. Your call.


---

## Turn 6 — Transposition Engine

TURN 5 — CANDIDATE ORGANIZING STRUCTURES (compare, do NOT choose; do NOT populate with findings).

Discriminating axis up front: OVERREACH COVERAGE. Overreach = "a concept the methodology models that
costs more than it earns" (success crit S3). A shape earns high overreach coverage only if "too much"
is a FIRST-CLASS position in it, not an afterthought. Shapes whose gravity is additive ("what's
missing per slot?") structurally can't see overreach — and they quietly re-import the T1 enum-and-gate
reflex (every answer = add a state/gate/field). I score that explicitly.

═══ FIVE CANDIDATE SHAPES ═══

S1 · ONTOLOGY-LAYERED  (entities → relationships → effects → obligations → evidence/ledger; gaps/
   additions/overreach hung off each layer)
   • Explanatory(relationships/effects): HIGH — relationships & effects are their own layers; the
     transposed inventory (T1–T12) slots in cleanly.
   • Practical: MED — actionable per layer, but a reader gets a concept map, not a to-do order.
   • Extensibility: HIGH — new presets/task-types add instances within fixed layers.
   • Blind spots: cross-layer phenomena (e.g. an effect that breaks an invariant that breaks a
     projection — T1∩T2∩T11) get split across layers and lose their causal thread.
   • Distortion: LOW-MED — layering is natural to an ontology; risk is implying clean layer boundaries
     where effects/obligations actually interpenetrate.
   • OVERREACH coverage: MED — representable ("this layer over-models X") but AGAINST THE GRAIN: a
     layered concept model pulls toward "what concept is missing here?" → biases to M1/M4 additive
     moves. Overreach is a guest, not a resident.
   • Failure mode: becomes a taxonomy that grows concepts; produces a rich gap/addition list and a thin
     overreach list.

S2 · LIFECYCLE  (creation → progress → transition → effect-delivery → closure → AFTERLIFE; flag
   contract gaps at each stage)
   • Explanatory: MED-HIGH — effects/transitions are inherently temporal, so they read well; but
     RELATIONSHIPS (task↔task, obligation↔evidence) are not stage-bound and get smeared.
   • Practical: HIGH — maps onto how an operator actually experiences a workflow; gaps are located
     "where in time" they bite.
   • Extensibility: HIGH — every preset has the same timeline.
   • Blind spots: cross-cutting concerns (a single concept over/under-modeled across ALL stages, e.g.
     provenance) can't be localized; "afterlife" (post-closure obligations, staleness — T7/Q13/Q23) is
     the one stage the basin forgets, so its inclusion is this shape's best feature.
   • Distortion: MED — forces a linear spine onto an iterate-until-verified domain (T1 basin A6); loops
     (reject→revise) read as backward arrows.
   • OVERREACH coverage: MED — "this stage has ceremony that doesn't pay" is sayable, but a cross-stage
     over-model is invisible.
   • Failure mode: privileges sequence; under-serves the relational substrate and cross-cutting overreach.

S3 · FAILURE-MODE  (FMEA catalog from L8: stuck/lying/double-effect/orphaned-obligation/lost-handoff/
   phantom-progress/invariant-breach/staleness → each mapped to a missing OR over-strong contract)
   • Explanatory: MED — vivid for what BREAKS; weak for the healthy structure (you infer the ontology
     from its scars).
   • Practical: VERY HIGH — each row is a concrete, testable defect; closest to an engineering backlog.
   • Extensibility: MED — new presets may introduce failure shapes not in the catalog (catalog is
     open-ended; risk of "we only see failures we already named" — the loop-until-dry concern).
   • Blind spots: SUCCESS modes (what the contract gets right, e.g. the idempotent outbox) are invisible
     — and you can't judge overreach without seeing what's already well-guarded.
   • Distortion: MED — defines the domain by pathology.
   • OVERREACH coverage: LOW ⚠ — this is the structural weakness. A failure-driven shape asks "what can
     go wrong?", and every answer is "add a guard/check/prevention." Overreach is the ABSENCE of a
     failure — too much protection isn't a failure mode, so this shape is nearly BLIND to it (and
     actively pushes the enum-and-gate reflex). The prompt's warning bites hardest here.
   • Failure mode: produces a strong gaps list, a moderate additions list, and almost no overreach —
     disqualifying as a SOLE spine for a tri-modal output.

S4 · FORMAL-VIEWS-TRIANGULATION  (state-machine / deontic / Hoare; per view: under-specified vs
   OVER-specified; disagreements D1–D4 from L5 are the gap signal)
   • Explanatory: HIGH for a single workflow's internal logic; the three-views disagreement is the
     sharpest lens we have (L5). Effects map to deontic/SM; weaker on task↔task RELATIONSHIPS (those
     live below the single-instance formalism).
   • Practical: MED — actionable for engine/template authors; less legible to operators.
   • Extensibility: HIGH — the three views apply to any preset.
   • Blind spots: the RELATIONAL/SUBSTRATE layer (containers, task↔task, handoff scoping) isn't well
     captured by per-instance formalisms; risks ignoring the wrkq side.
   • Distortion: MED-HIGH — forces the domain into three formalisms practitioners may not think in;
     could manufacture "disagreements" that are artifacts of the chosen formalism.
   • OVERREACH coverage: HIGH ✓ — "over-specified" is an explicit pole. Deontic over-spec = too many
     obligations; Hoare over-spec = postconditions nobody needs; SM over-spec = state proliferation.
     Overreach is a NATIVE position, symmetric with under-spec.
   • Failure mode: excellent at internal-logic gaps/overreach; under-serves cross-task relationships and
     the human/agent reconstruction lens (L9).

S5 · GUARANTEE-LEDGER  (INVENTED — hybrid of contract-surface-first + a tri-modal value axis)
   Shape: one row per GUARANTEE the system claims or could claim (e.g. "no closure with open blocking
   obligation"=T4; "effects effectively-once"=T2; "task.state faithfully projects workflow.state"=T1;
   "verifier≠producer"=T6; "verdict invalidates when its artifact changes"=T7). Each guarantee scored
   on ONE axis — value-delivered vs cost: {claimed-but-not-enforced = GAP · valuable-but-absent =
   ADDITION · enforced-beyond-its-value = OVERREACH · well-matched = leave alone}.
   • Explanatory: HIGH for EFFECTS & invariants (they ARE guarantees); MED for task↔task RELATIONSHIPS
     (a relationship must be recast as "the guarantee about that relationship" — works but is a slight
     bend).
   • Practical: VERY HIGH — every row is directly actionable and carries its own enforce/relax verdict.
   • Extensibility: HIGH — new presets add guarantees; the axis is fixed.
   • Blind spots: a guarantee NOBODY ARTICULATED is invisible (unknown-unknowns); depends on a good
     enumeration step (lean on T-inventory + kernel-invariant list to seed it).
   • Distortion: MED — forces a "guarantee" framing; some ontology (pure relationships, evidence
     typing) isn't naturally a guarantee and gets shoehorned.
   • OVERREACH coverage: HIGHEST ✓✓ — overreach is not a category bolted on; it's literally one end of
     the single scoring axis (over-enforced relative to value). This is the only shape where gaps /
     additions / overreach are SYMMETRIC outputs of the same measurement, which is exactly the
     tri-modal final artifact.
   • Failure mode: only as good as the guarantee enumeration; can miss un-named concerns and can
     over-"guarantee-ify" relational concepts. Subsumes contract-surface-first (a breach = a
     not-enforced guarantee).

═══ SCORECARD (H/M/L; ⚠=structural weakness, ✓=structural strength) ═══
  Shape                 Explain  Practical  Extensible  Distortion(low=good)  OVERREACH   ServesAll3?
  S1 Ontology-layered    HIGH     MED        HIGH        LOW-MED               MED          gaps/adds-heavy
  S2 Lifecycle           MED-HI   HIGH       HIGH        MED                   MED          time-biased
  S3 Failure-mode        MED      VERY-HI    MED         MED                   LOW ⚠        NO (no overreach)
  S4 Formal-views        HIGH     MED        HIGH        MED-HIGH              HIGH ✓       yes, instance-only
  S5 Guarantee-ledger    HIGH     VERY-HI    HIGH        MED                   HIGHEST ✓✓   yes, symmetric

Cross-cutting observations (NOT a choice):
  • Only S4 and S5 treat OVERREACH as a native position; S1/S2 admit it grudgingly; S3 is effectively
    blind to it. Any shape chosen as SOLE spine other than S4/S5 risks a lopsided tri-modal artifact.
  • RELATIONSHIPS (task↔task, obligation↔evidence, the core of this turn's mandate) are best served by
    S1; worst by S4. EFFECTS are best served by S2/S5. No single shape maxes both relationships AND
    effects AND overreach → a HYBRID is implied (e.g. S5 guarantee-ledger as spine, S1 ontology as the
    relationship sub-axis, S3 catalog cross-indexed as the gap-evidence per guarantee). Flagging
    combinability as a property; NOT selecting.
  • Two shapes (S3 failure-first, S1 ontology-first) carry a latent pull toward the enum-and-gate
    reflex; if either is chosen, the artifact needs an explicit overreach pass to counterweight.

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated — added Candidate structures + scorecard)
═══════════════════════════════════════════════════════════════════════
[Objective / Non-objectives / Scope rules / Default basin / Source-domain dictionary / Lens sweep /
 Glossary(verified) / Transposed concept inventory T1–T12] — unchanged.

[Candidate structures + scorecard] (NEW)
  S1 Ontology-layered · S2 Lifecycle(+afterlife) · S3 Failure-mode(FMEA) · S4 Formal-views-triangulation ·
  S5 Guarantee-ledger(invented; subsumes contract-surface-first).
  Scored on explain / practical / extensible / distortion / OVERREACH / serves-all-three (matrix above).
  Key result: overreach coverage separates them — S5 highest, S4 high, S1/S2 medium, S3 low(⚠ blind).
  Relationships best in S1, effects best in S2/S5 → hybrid implied (not chosen). Two shapes risk the
  enum-and-gate reflex (S1,S3).
  CONSTRAINT HONORED: compared, not chosen; not populated with findings.

[Open questions] Q2,Q6,Q13,Q14,Q19,Q20,Q23,Q27,Q28,Q29 still open (see T4). No new Q's this turn.
  + meta-Q (structure-selection, for a later turn, NOT now): do we want a single spine or an explicit
  hybrid (spine + cross-index)? And is overreach-coverage weighted heavily enough to force S4/S5?

[Confidence ledger] unchanged (T4 grounding holds). Structure choice is a DESIGN decision, not a
  factual one — no confidence rating applies; it's a fit-to-purpose judgment for you.

[Stage log] T0 contract · T1 basin · T2 dictionary · T3 lenses · T4 transposition(grounded).
  T5 (this turn): generated 5 candidate organizing shapes (incl. invented guarantee-ledger S5);
  scored each on 6 criteria with explicit overreach coverage + named failure mode; flagged S3's
  overreach-blindness and the enum-and-gate pull in S1/S3; noted a hybrid is implied. Did NOT choose.
  NEXT (proposed): either (a) you PICK a spine (or sanction a hybrid) and I do a dry-run skeleton of
  the chosen shape (still no findings); or (b) the deferred live-instance exercise to lift C2 before we
  commit. Your call.


---

## Turn 7 — Candidate Structures

TURN 6 — ADVERSARIAL AUDIT. Breaking our own work. No rewrite, no final artifact. Cite · severity · fix-direction (pointed, not fixed).

═══ CHECKLIST HITS ═══

1 · SYMPTOMS MISLABELED AS MECHANISMS
 • L8/S3 catalog lists "STUCK", "PHANTOM PROGRESS", "LOST HANDOFF" as failure SHAPES but these are
   SYMPTOMS, not causes. "Stuck" ← {unsatisfiable guard | orphaned-obligation owner unbound | required
   evidence no actor can produce | dead role binding}. Treating the symptom as the unit means S3 would
   map one symptom to many unrelated contract gaps. SEV MED. Fix-dir: decompose each L8 shape into
   cause→symptom; the contract gap attaches to the CAUSE.
 • T6 calls SoD-collapse "common-cause failure" — but that's the consequence; the mechanism is
   dual-role-binding / shared model. SEV LOW.

2 · DUPLICATE / OVERLAPPING CONCEPTS (prove distinct or merge)
 • ⚠ HIGH: "blocking OBLIGATION" (T4) vs wrkq "task.state=BLOCKED" vs "task↔task DEPENDENCY edge"
   (T12) are THREE encodings of one idea — "cannot proceed until X." We listed them as separate
   findings; that's a duplication in our OWN inventory and risks recommending a cross-task obligation
   (T12) when blocked-state already exists. Fix-dir: unify into one "blocked-until" relationship model;
   prove which layer owns it (workflow obligation vs task projection) or merge.
 • obligation vs check: PROVEN distinct — check = momentary predicate yielding a verdict AT a transition;
   obligation = durable row that outlives the transition and gates future closure [03 line 170 confirms
   separate surfaces]. Keep. SEV n/a (survives).
 • evidence vs check-run vs comment: BLUR — a type:role check PRODUCES evidence (evidenceKind), so
   check↔evidence isn't clean; comments (free-form) vs evidence (typed/schema'd) overlap is Q6-unresolved.
   SEV MED. Fix-dir: state the check→evidence generative edge explicitly; rule comments in or out as a
   fact substrate.

3 · MISSING DOMAINS / RELATIONSHIP & EFFECT TYPES
 • ⚠ MED-HIGH: we never modeled EVIDENCE↔EVIDENCE SUPERSESSION (a new verdict replacing an old one —
   which wins? the hash-chain orders events but doesn't say a later verdict INVALIDATES an earlier),
   nor the CHECK↔EVIDENCE-READ edge (which evidence a check consumes — the very edge T7 staleness needs).
   We hand-waved T7 without the dependency edge that would make staleness computable. Fix-dir: add both
   edges before T7 is actionable.
 • Effect TYPES under-examined: we leaned on wake_role/declare_handoff; never analyzed dispatch_role vs
   wake_role, notify, call_supervisor, cancel_effect semantics individually. SEV MED.
 • Missing source domains: QUEUEING/CAPACITY (finite actor throughput — bites at scale) and TASK-
   ALLOCATION / contract-net (multi-agent assignment) were never swept. SEV MED.

4 · WEAK ANALOGIES THAT SNUCK PAST THE 5-FIELD GATE
 • T8 "concurrency-as-obligation-set" — MANUFACTURED mapping. wrkf may not intend obligations as a
   concurrency primitive; I imposed the reading to rescue Harel orthogonal-regions. Detection signal is
   real but the causal claim is mine, not the system's. SEV MED. Fix-dir: demote to "observation" unless
   a preset actually uses obligations this way.
 • T10 provenance-triple & T1 log-as-truth — "already realized." A confirmation is NOT a transposition;
   these passed the import gate but bring nothing NEW. Risk: counting realized concepts as "additions."
   SEV MED. Fix-dir: segregate REALIZED-confirmations from genuine IMPORTS.
 • T11 "no postcondition" — OVERSTATED. beforeCommit hook ("final short invariant check, may block") IS
   a postcondition slot; the real claim is "no DECLARATIVE per-outcome postcondition," weaker than the
   headline. SEV LOW-MED.

5 · SCALE SENSITIVITY (3-step/1-task ✔ vs 200-task/multi-project ✘)
 • ⚠ HIGH: the ENTIRE framework (and wrkf) is PER-TASK — "one active instance per task" [04]. At program
   scale there is NO aggregate construct: no program/epic workflow, no cross-task completion guarantee,
   no "these 50 tasks must close before milestone M." A 200-task program can be "done" with 50 tasks
   silently abandoned and our per-instance model reports every instance well-formed. Fix-dir: the
   artifact must declare the per-task boundary and treat the aggregate layer as explicit gap or
   out-of-model.
 • next-action ranks WITHIN one instance; global "what next across 200 tasks/3 projects" is unaddressed.
   SEV MED. • Obligations/guarantees don't aggregate → S5 guarantee-ledger is implicitly per-task. SEV MED.

6 · HIDDEN ASSUMPTIONS (actor / evidence honesty / delivery)
 • ⚠ HIGH (actor): SoD (T6) keys on ACTOR ID, but actor-id ≠ model ≠ session. One agent holding TWO
   role bindings, or two "distinct" actors that are the same model, defeats SoD invisibly — and model
   identity is NOT in wrkf data, so it's unobservable. SEV HIGH. Fix-dir: SoD must constrain at binding
   time, not just compare actor strings.
 • ⚠ HIGH (evidence honesty): T5 "costly signal" ASSUMES the hook script is hermetic and does real
   work; the engine trusts only the EXIT CODE [04]. A script that touches a file / mocks the test exits
   0 = cheap-talk wearing a hook costume. "Costly" is unverified. SEV HIGH.
 • ⚠ HIGH (delivery): T2/L1 treat effect status=delivered as loop-closed truth — but "delivered" = the
   ADAPTER acked. We critiqued fire-and-forget in T1 then RE-IMPORTED the same trust by believing the
   ack. A buggy/dry-run adapter acks without delivering and our framework reports the loop closed. SEV HIGH.

7 · BOUNDARY AMBIGUITY (who owns task.state DURING a transition?)
 • In-transaction: engine owns both (kernel invariant "same-txn workflow + task.meta.workflow" [04]) ✔.
   BUT the COMMIT→DELIVER WINDOW is ambiguous: transition commits (task.state projected to completed)
   while afterCommit effects (declare_handoff) are still status=pending. The task READS done; the handoff
   that hands off control hasn't fired. A reader in that window sees a "done" task with no successor
   notified. SEV MED-HIGH. Fix-dir: define whether closure is "claimed" until its effects deliver.
 • Out-of-band wrkq write during a long validate hook: etag check rejects at commit ✔, but a human/agent
   reading task.state mid-transition sees in-flux state. SEV MED.

8 · TEMPORAL BLIND SPOTS
 • ⚠ HIGH (afterlife): template/catalog UNINSTALL bricks a LIVE instance — hook checks reference catalog
   scripts by path [04]; remove them and the check is un-runnable, so no transition can fire. The
   instance carries templateHash but not the scripts. Q23 had no mechanism answer; this is the concrete
   bite. SEV HIGH.
 • OUT-OF-ORDER EFFECTS: effect schema has createdAt but NO ordering/sequence guarantee [eff]; a
   cancel_effect could deliver before its target. T3 assumed ordered. SEV MED-HIGH. Detection:
   cancel_effect.deliveredAt < target.deliveredAt.
 • REPLAY re-emission: replaying the event log to rebuild state could re-emit effects; idempotencyKey
   dedupes the SAME key, but 07 says a re-driven transition emitting a NEW effect gets a NEW key → a
   replay/retry path can DOUBLE-fire (e.g. two handoffs). T2's idempotency guarantee is narrower than we
   implied. SEV MED-HIGH.

9 · INCENTIVE BLIND SPOTS
 • ⚠ HIGH (self-waiver): blocking-obligation interlock (T4) is satisfied by "satisfied/waived" [04]. If
   the obligation OWNER may WAIVE it, the agent clears its own blocker → interlock defeated trivially. We
   flagged "power to waive" (T9) but never checked waiver authority. SEV HIGH. Detection: obligation
   status open→waived where waiver-actor == owner-actor.
 • ⚠ HIGH (paper reward, META): our own S5 guarantee-ledger would mark "verification enforced" GREEN on
   the mere PRESENCE of a check on the completion transition — i.e. the ASSESSMENT TOOL inherits the
   cheap-talk vulnerability it's auditing. SEV HIGH. Fix-dir: score check SUBSTANCE, not presence (→ MST1).

10 · MEASUREMENT PROBLEMS (can the gaps even be observed?)
 • OVERREACH is NOT instrument-detectable — "enforced beyond its value" requires a value judgment; unlike
   gaps (observable rows/statuses), overreach is ARGUED. The final artifact must not pretend overreach is
   measurable. SEV MED.
 • "open-loop effect" (Q20): undetectable automatically — wrkf doesn't link an effect to its expected
   confirming evidence, so "unclosed loop" needs a convention that doesn't exist. SEV MED.
 • "costly signal" quality (T5) & "same-model" common-cause (T6): observable only by auditing scripts /
   with a model field that doesn't exist. SEV MED.

11 · OPERATIONAL USELESSNESS
 • L5 D4 (frame-condition violated): elegant but UNVERIFIABLE — wrkf has no frame declaration, so a frame
   violation can't be observed. True-but-useless. SEV LOW-MED. Fix-dir: drop unless frame is added.
 • T10 provenance & T8 concurrency: T10 is un-actionable (already realized → nothing to build); T8 may
   solve a non-problem at current single-track scale. SEV MED.

12 · CONCRETE FAILURE CASES (where our framework returns a WRONG/misleading verdict)
 FC1 PAPER EVIDENCE → false GREEN. Template gates completion on type:role attestation + {evidenceExists}.
   S5 sees "a check exists on completion" → marks verification-guarantee ENFORCED → NO gap. Reality:
   cheap-talk; agent attested without testing. ⇒ misses "validators accept paper-only evidence." [HIGH]
 FC2 SELF-WAIVED OBLIGATION → "interlock holds" but closed with substance gap. Owner waives own blocking
   obligation; kernel invariant satisfied; closure proceeds. T4 reports interlock enforced. ⇒ misses
   "workflow closes despite substance gap." [HIGH]
 FC3 ACK-WITHOUT-DELIVERY → "loop closed / handoff delivered." Adapter sets declare_handoff=delivered but
   never created the handoff row. L1/T2 read delivered=closed. ⇒ misses "effects ack'd without delivery"
   AND "lost handoff" simultaneously. [HIGH]
 FC4 LYING SURFACE → "legality sound." `wrkf next` advertises a transition whose guard would actually
   fail. Our analysis is STATIC/spec-based (C2 never lifted) → schema looks fine → verdict "surface
   sound." ⇒ structurally blind to the runtime surface bug the wrkf-debug skill exists for. [HIGH]
 FC5 200-TASK PROGRAM → "contract complete." Every per-task workflow well-formed; framework reports sound.
   Reality: no aggregate guarantee; 50 tasks abandoned, milestone falsely "reachable." [HIGH]
 FC6 DUPLICATED HANDOFF → "idempotent outbox holds." A re-driven transition emits a NEW declare_handoff
   (new idempotencyKey per 07) → two handoffs land. T2 claims idempotency protects. ⇒ misses
   "duplicated handoff" on the re-emit path. [MED-HIGH]

═══════════════════════════════════════════════════════════════════════
WORKING STATE (updated — added Audit findings + Must-survive tests)
═══════════════════════════════════════════════════════════════════════
[Objective / Non-objectives / Scope rules / Default basin / Source-domain dictionary / Lens sweep /
 Glossary(verified) / Transposed inventory T1–T12 / Candidate structures+scorecard] — unchanged.

[Audit findings] (NEW — ranked by severity)
 HIGH
  AF1 "blocked-until" TRIPLICATED across obligation(T4)/task.state=blocked/dependency(T12) — our own dup.
  AF2 SoD keyed on actor-id is shallow & partly unobservable (dual-binding / same-model common-cause) [T6].
  AF3 "costly signal" assumes hook hermeticity; engine trusts exit code only — costliness unverified [T5].
  AF4 effect status=delivered trusted as truth; we re-imported the fire-and-forget trust we critiqued [T2/L1].
  AF5 self-waiver defeats the blocking-obligation interlock; waiver authority unchecked [T4/T9].
  AF6 META: S5 (and the framework) marks verification ENFORCED on check PRESENCE → fooled by paper.
  AF7 per-task only; no program/aggregate layer — clean verdict hides abandoned-task programs [scale].
  AF8 template/catalog uninstall BRICKS live instances (un-runnable hook checks) [Q23/afterlife].
  AF9 C2 unlifted → static analysis structurally blind to runtime-surface lies (FC4).
 MED-HIGH / MED
  AF10 commit→deliver WINDOW ownership ambiguity (task "done" before handoff effect fires) [bnd].
  AF11 out-of-order effect delivery + replay re-emission can double-fire / mis-order [eff has no seq].
  AF12 evidence↔evidence supersession & check↔evidence-read edge UNMODELED (T7 not yet actionable).
  AF13 T8 concurrency-as-obligation possibly manufactured; T10/T1 realized-not-imported (segregate).
  AF14 missing domains: queueing/capacity, task-allocation/contract-net.
  AF15 L8/S3 mixes symptoms with causes (decompose to cause→symptom).
  AF16 measurement: overreach is ARGUED not observed; open-loop & signal-quality not auto-detectable.
 LOW
  AF17 L5 D4 frame-violation unobservable (no frame decl). AF18 T11 "no postcondition" overstated.

[Must-survive tests] (NEW — the final schema/artifact MUST pass these)
 MST1 Distinguish "a check exists" from "the check verifies SUBSTANCE." A guarantee is ENFORCED only with
      a named substance-verification (hermetic hook exercising the artifact), never presence/{evidenceExists}.
 MST2 For every blocking obligation, identify WHO may discharge/waive; interlock counts only if waiver
      authority ≠ obligated actor (no self-waiver).
 MST3 Distinguish effect status=delivered (adapter-acked) from real-world-confirmed; "delivered" guarantees
      must name the independent confirmation or be labeled trust-the-adapter.
 MST4 Tag every finding SPEC-verified vs RUNTIME-verified; legality/surface-soundness claims REQUIRE a
      live-instance test (C2 must be lifted before such claims).
 MST5 State the per-task boundary explicitly; the program/aggregate layer is an explicit gap, not a silent pass.
 MST6 Prove obligation vs task.state=blocked vs dependency-edge distinct, or MERGE — no triplicated "blocked-until."
 MST7 Separate already-REALIZED confirmations (T1,T10) from genuine ADDITIONS — don't recommend building what exists.
 MST8 Label OVERREACH findings as value-vs-cost JUDGMENTS, epistemically distinct from data-observable gaps.
 MST9 Survive out-of-order effect delivery & replay/re-emit (cancel-before-target; new-key duplicate) — or
      flag effect ordering as an unmodeled assumption.
 MST10 Answer template/catalog AFTERLIFE: what a live instance does when its template/scripts are uninstalled.

[Open questions] prior set stands; audit ELEVATES: Q23(→AF8 concrete), Q25(→AF2 deeper), Q20(→AF16 unobservable),
 +Q30 may an obligation owner waive their own obligation? (AF5 — needs spec/runtime check)
 +Q31 effect delivery ordering guarantee? (AF11) +Q32 program/aggregate-of-tasks construct? (AF7).

[Confidence ledger] unchanged on facts; NEW meta-caveat: the FRAMEWORK ITSELF has now-named vulnerabilities
 (AF6, AF9) — gaps it would miss. The must-survive tests exist to counter exactly these. Live-instance
 exercise (C2) is now the highest-value next move: 4 of 6 concrete failure cases (FC1-FC4) are runtime,
 not static, and our spec-only stance cannot adjudicate them.

[Stage log] T0 contract · T1 basin · T2 dictionary · T3 lenses · T4 transposition(grounded) · T5 structures.
 T6 (this turn): adversarial audit — 18 findings (9 HIGH), 6 concrete wrong-verdict cases, 10 must-survive
 tests. Surfaced that the FRAMEWORK inherits the cheap-talk/ack-trust vulnerabilities (AF6/AF4) and is
 runtime-blind (AF9). No rewrite (by constraint).
 NEXT (strongly recommended): lift C2 — exercise a live instance (drive agent-tasker on a throwaway task;
 attempt self-waiver, paper-evidence pass, observe effect rows/ack vs delivery, force a stall) to
 adjudicate FC1-FC4 and AF3/AF5/AF8/AF11 with OBSERVED data before the synthesis turn. Say the word.


---

## Turn 8 — Adversarial Audit

TURN 7 — COMPRESSION RESTART. Design locked. NO findings content below (spec only). WORKING STATE replaced by FINAL DESIGN STATE.

═══ 1 · DISCARDED (ruthless) ═══
 Lenses → fold, don't carry as spines: L2 info-flow (→ subsumed by ledger+handoff guarantees), L3
   boundaries (→ becomes the wrkf↔wrkq guarantee content, not a lens), L4 entropy (→ "afterlife"
   guarantees), L8 FMEA-as-structure (→ demoted to cross-index test set; overreach-blind per audit).
   KEEP only: L1 (open-loop), L5 (formal triangulation), L6/L7 (incentive/adversarial), L9 (reconstruction).
 Transpositions → drop/reclass: T8 concurrency-as-obligation [DROP — AF13 manufactured]; T10
   provenance-triple [RECLASS — already-realized, not an addition]; L5-D4 frame-violation [DROP — AF17
   unobservable]; T11 headline "no postcondition" [SOFTEN — keep only the narrow "no DECLARATIVE
   per-outcome postcondition" point].
 Structures → drop as SPINE: S1 ontology, S2 lifecycle, S3 failure-mode (overreach-blind), S4 formal
   (instance-only). They survive only in the roles assigned below.
 Source-dictionary [weak] items: dropped wholesale. Un-swept domains (queueing, task-allocation):
   acknowledged as known-gap §6, not imported.
 Net: from ~70 concepts + 9 lenses + 5 structures → 1 spine + 3 support roles + a bounded guarantee register.

═══ 2 · ORGANIZING PRINCIPLE (chosen: justified HYBRID) ═══
 SPINE = S5 GUARANTEE-LEDGER. Support roles: S1 ontology = the mandatory CLASSIFIER axis on every
 entry; S4 formal-triangulation = the under-/over-specified TEST applied per guarantee; S3+FC1–FC6 =
 the cross-indexed VALIDATION set each relevant guarantee must rule on.
 WHY it serves all three + survives the audit:
  • Gaps/additions/OVERREACH are symmetric outputs of ONE value-vs-cost axis (over-enforced is a native
    pole, not bolted on) → best overreach coverage [MST8].
  • S5's weak spot (relationships) is covered by forcing an ontology-locus field [§3] + mandatory
    relationship/effect coverage [§5] → the relationships/effects mandate is structural, not optional.
  • The audit's must-survive tests become REQUIRED FIELDS (substance-check=MST1, delivery/discharge
    authority=MST2/3, evidence-basis=MST4, realized?=MST7, verdict-epistemics=MST8) → the framework
    can no longer return the false-greens of FC1–FC6.
  • Subsumes contract-surface-first (a breach = a not-enforced guarantee).

═══ 3 · ARTIFACT SCHEMA (per entry — next turn is pure fill-in) ═══
 Atomic unit = a GUARANTEE-LEDGER ENTRY. Fields:
  G-id            stable id (G-01…)
  guarantee       one falsifiable sentence: the promise (claimed or candidate)
  ontology_locus  ONE primary ∈ {entity, relationship, effect, obligation, evidence, role, state-transition}
                  (+ optional secondary)
  rel_or_effect_subtype  REQUIRED when locus∈{relationship,effect}: e.g. task↔task, obligation↔evidence,
                  effect↔delivery, check↔evidence, role↔actor  (drives §5 coverage)
  verdict         exactly one ∈ {GAP, ADDITION, OVERREACH, WELL-MATCHED}
  enforcement     ∈ {enforced-and-sufficient, enforced-but-bypassable, claimed-not-enforced, absent, over-enforced}
  mechanism       the actual wrkf/wrkq operation (kernel invariant | guard | check-kind | obligation row |
                  effect outbox | projection | event) — or "none" if absent
  detection       observable signal in real data (row/status/event-field/query) OR "not auto-observable — argued"
  substance_check [MST1] for verification guarantees: names the hermetic substance test, or flags presence-only
  authority       [MST2/3] obligations: who may discharge/waive (self-waiver?); effects: delivered vs confirmed
  intervention    the implied contract change (described, not recommended); for OVERREACH: what to relax/remove
  confidence      low | med | high
  basis           [MST4] ∈ {schema-verified, spec-verified, runtime-verified, inferred} + source cite
  term_label      ∈ {[canonical], [borrowed-term], [approximation], [coined-here]}
  realized        [MST7] ∈ {already-realized, partial, not-yet}   (additions MUST be not-yet/partial)
  rules_on        which of FC1–FC6 / must-survive scenarios this entry adjudicates (or n/a)
 The artifact = (a) a 1-paragraph ontology preamble, (b) the guarantee-ledger table of these entries
 grouped by verdict (GAPS / ADDITIONS / OVERREACH), (c) a coverage-matrix appendix proving §5 met.

═══ 4 · MERGE / SPLIT RULES ═══
 MERGE when: (i) two entries share ontology_locus + mechanism and differ only in symptom → keep the
   CAUSE, fold symptoms (e.g. AF1 blocked-until: obligation/task.state=blocked/dependency → ONE entry);
   (ii) one entry is a strict consequence of another → fold into the cause.
 SPLIT when: (i) one concept spans two loci with DIFFERENT mechanism+intervention (e.g. handoff =
   effect↔delivery AND evidence-for-reconstruction) → two entries; (ii) one guarantee gets DIFFERENT
   verdicts at different scales (per-task WELL-MATCHED vs program ADDITION) → split by scale.
 TIE-BREAK: fewer entries wins; any entry that cannot name a distinct mechanism AND intervention is
   merged or dropped (no decorative rows).

═══ 5 · MANDATORY COVERAGE (the artifact MUST rule on each) ═══
 RELATIONSHIP types: task↔task (dependency/blocks/derived-from/container) · task↔workflow-instance
   (1:1 binding + projection) · workflow↔role (binding + SoD) · role↔actor (binding over time) ·
   obligation↔evidence (what discharges what) · check↔evidence (consume/read edge) · evidence↔evidence
   (supersession) · obligation↔obligation (CTD/remedy chains).
 EFFECT types: effect↔delivery (outbox lease/ack/idempotency) · effect↔state (in-txn obligation vs
   post-commit effect split) · and each KIND ruled on at least at class level: wake_role/dispatch_role ·
   declare_handoff · request_task_creation · call_supervisor · notify · cancel_effect (compensation).
 MUST-SURVIVE SCENARIOS (explicit verdict required): FC1 paper-evidence · FC2 self-waiver · FC3
   ack-without-delivery · FC4 lying-surface · FC5 program-scale · FC6 duplicated-handoff (covers the 5
   named real modes).

═══ 6 · KNOWN GAPS / RESIDUAL UNCERTAINTY (will NOT resolve; carry as caveats) ═══
  R1 RUNTIME (C2): unless a live instance is exercised, FC1–FC4 are adjudicated at SPEC level only →
     those entries capped at basis=spec-verified, confidence≤MED. [HIGH-impact residual]
  R2 Program/aggregate layer (AF7): we NAME it as a gap; we do NOT design the aggregate model (new
     subsystem, out of scope). [MED]
  R3 SoD model-identity (AF2): actor-id≠model≠session is not in wrkf data → we flag the limitation,
     cannot detect same-model collusion. [MED]
  R4 Overreach is ARGUED not measured (MST8/AF16): accepted epistemic asymmetry vs gaps. [structural]
  R5 Installed-template survey (C4): we assess spec + agent-tasker template only; other presets
     unsurveyed → methodology overreach judged on a sample. [MED]
  R6 Un-swept domains (queueing/capacity, task-allocation): low impact at current single-track/per-task
     scale; noted, not modeled. [LOW]

═══════════════════════════════════════════════════════════════════════
FINAL DESIGN STATE (replaces WORKING STATE; compressed)
═══════════════════════════════════════════════════════════════════════
[Objective] 3-part assessment (GAPS · ADDITIONS · OVERREACH) of the wrkf+wrkq workflow ontology at the
  concept/relationship/effect level, scoped to the workflow/durable-task CONTRACT.
[Scope] IN: states/transitions/checks/obligations/effects/evidence/roles/templates/tasks/handoffs and
  their relations + the wrkf↔wrkq + wrkf↔adapter contract surfaces. OUT: runtime/transport, CLI, UI,
  storage internals, auth, perf (except where they shape the contract).
[Organizing principle] SPINE = Guarantee-Ledger (S5). CLASSIFIER = ontology-locus (S1). PER-ENTRY TEST =
  formal triangulation (S4). VALIDATION = failure-catalog FC1–FC6 (S3/L8). Lenses retained: L1,L5,L6,L7,L9.
[Artifact schema] 15-field guarantee-ledger entry (§3); output = ontology preamble + ledger grouped by
  verdict + coverage-matrix appendix.
[Merge/split] §4. [Mandatory coverage] §5 (8 relationship + 8 effect classes + 6 FC scenarios).
[Verified facts carried] workflow.state canonical / task.state = same-txn projection; effect = lease-
  outbox (pending→leased→delivered/failed/cancelled, idempotencyKey, adapter-acked, never self-acked);
  ledger = hash-chained append-only event log; kernel invariants incl. SoD, blocking-obligations-
  satisfied/waived, revision/etag/contextHash current, one-active-instance-per-task. [all schema/spec-verified]
[Guarantee register to FILL next turn] (slots only — NO verdicts/mechanisms assigned here):
  g-closure-vs-blocking-obligation · g-task-state-faithful-projection · g-effect-effectively-once ·
  g-effect-delivered-means-confirmed · g-verifier-≠-producer(SoD) · g-check-verifies-substance ·
  g-verdict-stale-when-artifact-changes · g-obligation-has-obligee/waiver-authority · g-declarative-
  postcondition · g-cross-task-dependency · g-handoff-delivered-and-singular · g-legality-surface-sound ·
  g-effect-ordering/replay-safe · g-template-afterlife-defined · g-iteration-loop-first-class ·
  g-program-aggregate-completion. (Each → one+ ledger entries next turn.)
[Must-survive tests] MST1–MST10 (T6) → now encoded as required fields/coverage; carried verbatim.
[Residual uncertainty] R1–R6 (§6).
[Stage log] T0 contract·T1 basin·T2 dictionary·T3 lenses·T4 transposition(grounded)·T5 structures·
  T6 audit·T7 COMPRESSION (design locked; spine=guarantee-ledger; 15-field schema; merge/split + coverage
  + residuals fixed). NO findings produced (by constraint).
  NEXT = pure fill-in of the guarantee register into the schema → the 3-part artifact. ONE decision gates
  fidelity: run the live-instance exercise (lifts R1, lets FC1–FC4 reach basis=runtime-verified) BEFORE
  fill-in, or accept spec-level caps on those entries? Your call — either way the next turn produces the artifact.


---

## Turn 9 — Compression Restart

FINAL ARTIFACT — Workflow Ontology applied to wrkf/wrkq. Guarantee-ledger spine; locked 15-field schema (compacted). Basis tags: [schema]/[spec]=verified vs source; [inf]=inference; [runtime]=needs live instance (not done → capped). Term tags: [canonical]/[borrowed]/[approx]/[coined].

ONTOLOGY PREAMBLE (1¶): wrkf models a workflow as a single-instance state machine (status∈open/active/
waiting/closed) over ONE wrkq task, where the task's own state is a same-transaction PROJECTION of
canonical workflow state; legality is gated by kernel invariants + guards + checks; consequences split
into in-transaction OBLIGATIONS (durable blocking debts) and post-commit EFFECTS (a lease-based,
adapter-acked outbox); the audit spine is a hash-chained append-only event ledger. The ontology is
strong on per-task state/obligation/effect mechanics and weak on (i) the TRUTH of evidence/effects
under a non-deterministic actor and (ii) anything ABOVE a single task. Gaps/additions/overreach below
are each a guarantee scored on value-vs-cost.

──────────────────────────────────────────────────────────────────────
PART A · GAPS  (a required guarantee that wrkf/wrkq does not yet enforce)
──────────────────────────────────────────────────────────────────────
G-01 effect "delivered" ≠ confirmed.  locus=effect / effect↔delivery [canonical].
 verdict GAP · enforcement enforced-but-bypassable. Mechanism: outbox status set delivered by the
 ADAPTER [spec 07]; engine never independently confirms the real-world result. Detection: effect rows
 status=delivered with no corresponding confirming evidence event [schema]. rules_on FC3. realized
 partial. confidence MED-HIGH [spec]. → the delivery loop (L1) is open; engine trusts the ack.

G-02 checks gate on PRESENCE, not SUBSTANCE.  locus=evidence / check↔evidence [canonical].
 verdict GAP · enforced-but-bypassable. Mechanism: check kinds include type:role attestation and
 predicate {evidenceExists}; engine trusts only the hook EXIT CODE [schema/spec 04]. A schema-valid
 but empty/fabricated evidence record passes. Detection: completion transitions whose only gate is
 type:role or {evidenceExists}; evidenceKindSpec.schema validates shape only [schema]. substance_check:
 ABSENT for those kinds. rules_on FC1. confidence MED-HIGH [schema]+[inf that presets use them].

G-03 separation-of-duties is asserted but under-declared & actor-id-shallow.  locus=relationship /
 workflow↔role + role↔actor [canonical].  verdict GAP · claimed-not-enforced. Mechanism: "separation-
 of-duties requirements hold" is a kernel invariant [spec 04], BUT the v0 template schema exposes no
 field to DECLARE producer≠verifier (roleSpec has only candidateActors/capabilities/bind) [schema], and
 any enforcement keys on actor-id, which ≠ model ≠ session. Detection: producing vs verifying
 transition actor equality in events; same-MODEL collusion NOT observable (R3). confidence MED
 [schema-absence high; enforcement-path inferred].

G-04 obligations have no obligee & no waiver-authority → self-waiver defeats the interlock.  locus=
 obligation / obligation↔evidence [canonical].  verdict GAP · enforced-but-bypassable. Mechanism:
 obligationCreateSpec carries owner side only (ownerRole/ownerRoleFrom/ownerActor), no obligee; kernel
 invariant accepts "satisfied/WAIVED" [schema/spec 04] with no constraint on who waives. The owner can
 clear its own blocker. Detection: obligation open→waived where waiver-actor==owner-actor. authority:
 unconstrained. rules_on FC2. confidence MED-HIGH [schema-absence high; runtime waiver behavior [inf]].

G-05 verdicts do not go stale when their artifact changes.  locus=evidence / check↔evidence +
 evidence↔evidence [borrowed: build-dirtiness].  verdict GAP · absent (at evidence level). Mechanism:
 drift detection exists for the TASK doc (instance.taskDocHash + kernel etag/contextHash invariants)
 [schema/spec], but evidence/check_run rows carry no produced-from-artifact hash and there is no
 supersession edge; a verdict pinned to artifact v1 stays valid at v2. Detection: check_run referencing
 an artifact whose content-hash changed, with no re-run event. confidence MED-HIGH [schema].

G-06 no task↔task dependency edge; no program/aggregate completion.  locus=relationship / task↔task
 [canonical].  verdict GAP · absent. Mechanism: task creation is an effect (request_task_creation) and
 containers give parent/child, but there is NO first-class blocks/derived-from/dependency edge and NO
 aggregate construct; "one active instance per task" scopes everything per-task [schema/spec 04].
 (MERGE note: "blocked-until" was triplicated — obligation / task.state=blocked / dependency — per
 split rule this entry OWNS the CROSS-task case; the intra-task case is the obligation, which is
 well-matched.) Detection: a wrkq task=blocked with no engine link to its unblocker; a program with
 abandoned tasks where every instance reads well-formed. rules_on FC5. confidence HIGH [spec].

G-07 no declarative per-outcome POSTCONDITION.  locus=state-transition [borrowed: Hoare].  verdict GAP
 (mild) · absent-as-declarative. Mechanism: preconditions (requires/guards) and global kernel invariants
 exist; the only postcondition slot is the beforeCommit hook ("final short invariant check") [spec 04] —
 there is no declarative assertion that outcome.to's defining property holds. Detection: outcome.to with
 no check asserting the to-state property. confidence MED [schema]. (Softened from T6/AF18.)

G-08 legality-surface soundness — CANNOT ADJUDICATE at spec level.  locus=state-transition / the `next`
 surface [canonical].  verdict GAP-SUSPECTED · enforcement UNKNOWN. Mechanism: whether `wrkf next` ever
 advertises a transition the engine would reject is a RUNTIME property; the next-action schema looks
 sound but soundness = surface ⊑ engine, untestable statically. Detection: requires a live instance —
 next-advertised set vs transition-rejected set. rules_on FC4. basis [runtime — NOT done]. confidence
 LOW. (Honest non-ruling; capped per R1.)

G-09 effects have no ordering guarantee; idempotency is narrow.  locus=effect / effect↔delivery
 [canonical].  verdict GAP · partial. Mechanism: effect schema has createdAt but no sequence/ordering
 field; idempotencyKey dedupes the SAME key, but a re-driven transition emitting a NEW effect gets a NEW
 key [schema/spec 07] → out-of-order delivery (cancel before target) and double-fire (two handoffs) are
 possible. Detection: cancel_effect.deliveredAt < target.deliveredAt; ≥2 declare_handoff effects, distinct
 keys, one instance. rules_on FC6. confidence MED [schema].

G-10 template/catalog AFTERLIFE undefined → live instances can brick.  locus=entity(template) /
 task↔workflow [canonical].  verdict GAP · absent. Mechanism: hook checks reference catalog scripts by
 path [spec 04]; the instance pins templateHash but not the scripts. Uninstall/move the template →
 checks un-runnable → no transition can fire; open obligations freeze. Detection: an instance whose
 templateHash's catalog scripts no longer resolve. confidence MED [spec]+[inf brick behavior]. (=Q23/AF8.)

WELL-MATCHED (ruled sound — NOT gaps; prevents false "additions"): W1 task.state faithful same-txn
 projection [T1, realized, HIGH]; W2 effect effectively-once for same-key delivery [T2, realized, HIGH;
 caveat re-emit→G-09]; W3 closure gated on BLOCKING obligations [T4, realized, HIGH; caveat self-waiver
 →G-04]; W4 hash-chained provenance ledger [T10, realized, HIGH].

──────────────────────────────────────────────────────────────────────
PART B · RECOMMENDED ADDITIONS  (each → a gap, mechanism-level, with detection)
──────────────────────────────────────────────────────────────────────
B-01 →G-01  Split effect terminal state delivered→{acked, confirmed}; per-effect-kind require a
 confirming evidence kind (or a delivery-PROOF payload, not a bare ack). Detect closure: delivered
 effect with matching confirmation event. [canonical+approx]. conf MED.
B-02 →G-02  Classify every check presence-class vs verification-class in the template contract; add a
 kernel rule: completion/closure transitions may not be gated by a presence-only check. [coined: check-
 class]. conf MED-HIGH.
B-03 →G-03  Declarative SoD constraint on transitions (actor ∉ {actors of named prior transitions} or
 role-disjointness), enforced at role-BINDING time, not by string compare. [borrowed: SoD]. conf MED.
B-04 →G-04  Directed obligation: add obligee/beneficiary + a power-to-waive distinct from owner; kernel
 rejects self-waiver of a blocking obligation (waiver-actor ≠ owner-actor). [borrowed: Hohfeld]. conf MED-HIGH.
B-05 →G-05  Evidence carries producedFromHash; add an evidence-supersession edge + a builtin staleness
 check that invalidates dependent verdicts when an input artifact's hash changes. [borrowed: provenance/
 dirtiness]. conf MED-HIGH.
B-06 →G-06  A cross-task dependency obligation (obligee on task B, satisfied by task A's closure event);
 SEPARATELY, an optional program/rollup instance for aggregate completion — flagged as larger scope (R2),
 lower confidence. [coined: cross-task-obligation]. conf MED (edge) / LOW (aggregate).
B-07 →G-07  Declarative postcondition on outcome.to, engine-verified at beforeCommit. [borrowed: Hoare]. conf MED.
B-08 →G-09  Per-instance effect sequence + idempotency stable across re-emit, OR an explicit documented
 "effects may reorder; handlers must be commutative/idempotent" contract. [borrowed: durable-exec]. conf MED.
B-09 →G-10  Pin template+catalog CONTENT (not just hash) to the instance, or define a degraded/locked
 mode on uninstall; specify obligation/effect disposition on uninstall/cancel/reopen. [canonical]. conf MED.
B-10 →G-08  (CONDITIONAL on runtime work) a conformance assertion that `next` ⊑ engine-legality; only
 addable AFTER the live-instance test. [canonical]. conf LOW until R1 lifted.

──────────────────────────────────────────────────────────────────────
PART C · POTENTIAL OVERREACH  (too much / too rigid / ceremony / assumes too much honesty-determinism)
 NOTE per MST8: overreach is ARGUED (value-vs-cost judgment), not data-observable like gaps. All conf=argued.
──────────────────────────────────────────────────────────────────────
C-01 Six-phase hook machinery is heavy for the common preset.  locus=state-transition [canonical].
 over-enforced. beforeTransition/validate/beforeCommit/afterCommit/onReject/onError [spec 04] is rich
 capability whose payoff is only at high-assurance presets; simple author pays the conceptual tax.
 Relax: ship a minimal preset profile; don't require the full phase set. conf MED-argued.
C-02 Assurance THEATER: structural gating invested while the trust ROOT is unguarded.  locus=evidence/
 role [coined: assurance-theater].  over-enforced relative to value. The evidence/check apparatus LOOKS
 like assurance, but G-02 (substance) + G-03 (SoD root) mean the gates can be satisfied by cheap talk —
 so ceremony is paid for a guarantee not actually delivered. Relax OR close the root: either lower the
 gating ceremony, or fix the trust root (B-02/B-03/B-04) — paying for both the gate AND leaving the root
 open is the worst cell. conf MED-argued. (This is the honest gap/overreach twin.)
C-03 Hard template immutability + one-active-instance is rigid for iterative agentic work.  locus=entity
 (template) [canonical].  over-rigid. Any change = new template; mid-task evolution disallowed; LSP/
 forking (Q19) unaddressed. Relax: allow controlled in-place evolution guarded by behavioral-subtyping
 (may not weaken postconditions/strengthen preconditions). conf LOW-MED-argued+[inf].
C-04 Deterministic next-action ranking assumes more determinism than agents provide.  locus=state-
 transition [canonical].  over-invested. next-action carries mode:deterministic|probabilistic + a
 nextActionModel ranking surface [schema]; precise ranking is ceremony when a non-deterministic agent
 re-derives anyway. Relax: keep `next` advisory; don't over-invest in ranking precision. conf LOW-argued.
 ⚑LATE ADDITION (judgment new this turn — lowered confidence).
C-05 Beware over-modeling deontic logic when adding B-04.  locus=obligation [coined: deontic-creep].
 prospective over-reach. A full obligee/CTD/power-to-waive AST is ceremony most presets won't use. Relax:
 add obligee + no-self-waiver MINIMALLY; resist a general deontic engine. conf MED-argued. ⚑LATE (counter-
 weight to our own addition).

──────────────────────────────────────────────────────────────────────
MANDATORY-COVERAGE MATRIX  (proof the mandate is met)
──────────────────────────────────────────────────────────────────────
 RELATIONSHIPS: task↔task → G-06/B-06 ✓ · task↔workflow → W1(projection,sound)+G-10(afterlife) ✓ ·
  obligation↔evidence → G-04(discharge)+G-02(substance) ✓ · check↔evidence → G-02/G-05 ✓ ·
  workflow↔role/role↔actor → G-03/B-03 ✓ · evidence↔evidence → G-05/B-05 ✓ · obligation↔obligation →
  C-05 (CTD, deliberately minimized) ✓.
 EFFECTS: effect↔delivery → G-01/G-09/B-01/B-08 ✓ · effect↔state(in-txn vs post-commit) → W2/W3 + preamble ✓ ·
  kinds: wake/dispatch_role→G-01; declare_handoff→G-01/G-09(FC3/FC6); request_task_creation→G-06;
  call_supervisor→ (recovery, W-adjacent, no gap raised); notify→G-01 class; cancel_effect→G-09 ✓.
 MUST-SURVIVE FC VERDICTS: FC1 paper-evidence→G-02 (framework WOULD now flag via substance_check field) ·
  FC2 self-waiver→G-04 (flagged) · FC3 ack≠delivery→G-01 (flagged) · FC4 lying-surface→G-08 (CANNOT rule,
  capped, runtime needed) · FC5 program-scale→G-06 (flagged) · FC6 dup-handoff→G-09 (flagged). 5/6 ruled;
  FC4 honestly deferred.

──────────────────────────────────────────────────────────────────────
GLOSSARY (coined-here terms)
──────────────────────────────────────────────────────────────────────
 enum-and-gate reflex — the basin habit of answering every gap by adding a state/gate/field, never a
   guarantee. · guarantee-ledger — the organizing spine: one promise per row, scored value-vs-cost. ·
   assurance-theater (C-02) — structural gating whose trust root is unguarded, so it looks like
   assurance without delivering it. · deontic-creep (C-05) — over-modeling obligation logic past what
   presets use. · check-class (B-02) — presence-class vs verification-class checks. · cross-task-
   obligation (B-06). Borrowed terms used: costly-signal/cheap-talk (mechanism design), open-loop
   effect (control), interlock/fail-closed (safety), saga/compensation/idempotency (txn/durable-exec),
   Hohfeld power-vs-duty / contrary-to-duty (deontic), behavioral-subtyping/Hoare pre-post (DbC).

──────────────────────────────────────────────────────────────────────
USAGE GUIDANCE  (how to prioritize + what to validate empirically FIRST)
──────────────────────────────────────────────────────────────────────
 PRIORITIZE by (impact × confidence × cheap-to-verify). Suggested order:
  1. G-04 self-waiver — high impact, directly observable, likely a small kernel rule (B-04). Do first.
  2. G-02 substance-vs-presence — high impact on the core agentic fear; B-02 is a contract annotation.
  3. G-01 delivery-vs-confirmation — closes the open effect loop; B-01.
  4. G-03 SoD declaration — pairs with G-04; needs a schema field + binding-time check.
  5. G-05 evidence staleness, G-09 effect ordering — medium; mechanical.
  6. G-10 afterlife — operational safety; cheap policy decision.
  7. G-06 cross-task / aggregate — larger; the dependency edge before the program rollup.
  8. G-07 postcondition — nice-to-have rigor.
  9. G-08 — BLOCKED on runtime verification; do not act until tested.
 VALIDATE EMPIRICALLY BEFORE ACTING (lifts R1 — this is the single highest-value move): drive the
  agent-tasker template on a throwaway task and (a) attempt to WAIVE your own blocking obligation
  [G-04/FC2]; (b) pass a completion check with schema-valid empty evidence [G-02/FC1]; (c) inspect a
  delivered declare_handoff effect for an actual handoff row [G-01/FC3]; (d) diff `wrkf next` output
  against transitions the engine actually rejects [G-08/FC4]; (e) re-drive a transition and count
  emitted effects [G-09/FC6]. Each observation flips the relevant entry from spec-verified→runtime-
  verified and confirms/kills the gap before any engine change. Coordinate engine fixes with cody@wrkq
  via the wrkf-debug protocol.

──────────────────────────────────────────────────────────────────────
RESIDUAL KNOWN GAPS / DELIBERATELY UNRESOLVED
──────────────────────────────────────────────────────────────────────
 R1 runtime behavior unverified → FC1–FC4 / G-01,02,04,08 capped at spec-level, conf≤MED [HIGH-impact].
 R2 program/aggregate model named (G-06) but not designed — new subsystem, out of scope [MED].
 R3 SoD same-model collusion unobservable in wrkf data [MED]. R4 overreach is argued, not measured
 [structural]. R5 only the agent-tasker preset + spec surveyed; other installed presets not audited [MED].
 R6 queueing/capacity + task-allocation domains not swept [LOW].
 Open Qs left standing: Q2 (handoff effect→wrkq-row mechanics detail), Q6 (comments vs evidence as fact
 substrate), Q13 (post-closed obligations), Q14 (iteration-loop first-classness — routing back to
 active/phase appears supported; unconfirmed live), Q19 (LSP/forking guarantees).

— END ARTIFACT. Scope held (workflows/tasks/relationships/effects only). Late additions (C-04, C-05)
flagged with lowered confidence. Every claim carries confidence + basis; 5/6 must-survive scenarios
ruled, FC4 honestly deferred to runtime. Ready for your review, or I can lift R1 on your go.


---
