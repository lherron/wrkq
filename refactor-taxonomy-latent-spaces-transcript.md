# Latent-Spaces Run — Refactoring Taxonomy (9-turn single-chat harness)

_Session: clod@wrkq:latent-spaces · driven via hrcchat turn_

---

# Refactoring Taxonomy — TURN 1/9: THE CONTRACT

Mechanism-first throughout. Confidence tags on contestable claims: (High/Med/Low).

---

## 1. OBJECTIVE
The final taxonomy must let a practitioner, holding a *specific* piece of existing code, do three things in sequence: (a) **detect** — recognize a structural problem by an observable trigger, not by vibe or a memorized smell name; (b) **select** — choose a named transformation whose *mechanism* (the precise structural change it makes) addresses that trigger, while understanding its blast radius and preconditions; and (c) **apply safely** — execute the change behavior-preservingly, with a stated way to know the behavior was in fact preserved. In short: a catalog indexed by *mechanism and trigger*, usable as a decision tool mid-edit, not a glossary read once.

---

## 2. NON-OBJECTIVES
We are deliberately NOT building:
- A **style guide** (brace placement, naming casing, import ordering) — aesthetics without structural consequence.
- A **linter ruleset dump** — auto-fixable lexical rules are detection inputs at most, not techniques.
- A **language tutorial** — we assume the reader can write the language; we don't teach syntax.
- The **rewrite-vs-refactor debate** — we presume refactoring is the chosen mode; the strategic decision to rewrite is out of frame.
- A **design-patterns catalog** (GoF as ends in themselves) — patterns appear only as *targets* a transformation moves toward, never as a thing to memorize.
- A **metrics/quality-gate framework** — we may *cite* measures (cyclomatic complexity, coupling) as triggers or success signals, but we are not specifying a scoring system.
- A **tooling shootout** — IDE/automated-refactoring tool comparisons are out; tool-assistability is at most an attribute of a technique.
- A **performance-optimization manual** — optimization that changes complexity class or observable timing contracts is a different discipline; we touch only refactors that happen to enable it.

---

## 3. SCOPE & BOUNDARIES

**Three altitudes:**
- **Code-level (IN, primary focus):** transformations within/among functions, methods, classes, modules — extract, inline, move, rename-for-meaning, replace conditional with polymorphism, introduce parameter object, etc. This is the spine of the taxonomy.
- **Architecture-level (EDGES only):** module decomposition, dependency inversion at boundaries, strangler-fig migration, seam introduction. Included *only* where the move is mechanically a scaled-up code refactor (e.g., "extract module" is "extract function" with a bigger blast radius). Whole-system re-architecture excluded. (Med — the boundary between "big refactor" and "re-architecture" is genuinely fuzzy; we'll need an explicit cut line, see WORKING STATE Q3.)
- **Process-level (OUT, except as context):** code review cadence, CI gating, mob/pair practices, branch strategy. Mentioned only as *enablers of safe application*, never catalogued as techniques.

**Paradigms in scope:** OO, functional, procedural, data/SQL (schema + query refactoring), concurrent/async. We explicitly want cross-paradigm coverage because mechanism often transcends paradigm (e.g., "make a dependency explicit" = dependency injection in OO ≈ passing a function/argument in FP ≈ parameterizing a stored proc in SQL). (Med — the claim that these are "the same mechanism" is an analogy we must defend or downgrade per case.)

**Hard exclusion:** Greenfield. Every technique presumes *existing* code with existing behavior worth preserving. No "how to design it right the first time."

---

## 4. DEFINITIONS (precise; contested usage flagged)

- **Refactoring** — a transformation of internal code structure that *preserves observable behavior*. "Observable" = behavior visible across a defined boundary (public API, I/O, persisted state, externally-observable timing/ordering where contractual). What counts as the boundary is itself a parameter of the refactor. (High on the core definition — Fowler/Opdyke canonical. Med on "observable": the boundary is where most real disputes live.)
- **Code smell** — a *surface-observable heuristic indicator* that *suggests* a deeper structural problem, without being the problem itself. Critically: a smell is a *symptom*, not a *diagnosis*. (High that the term exists; **Med/contested** that any given smell reliably maps to a real problem — empirical evidence that smells predict defects/maintenance cost is mixed.)
- **Design violation** — a breach of a *stated* design principle or invariant (e.g., a dependency cycle, a Law-of-Demeter breach, an SRP violation). Distinct from a smell: a violation is defined against an *explicit rule*; a smell is a heuristic. (Med — "violation" presumes the rule is agreed; many "violations" are contextual trade-offs.)
- **Technique** — a single, named, *atomic-ish* transformation with: a trigger, a precondition set, a defined mechanism (the structural delta), and a behavior-preservation argument. (e.g., *Extract Function*.)
- **Methodology** — a *sequenced or principled discipline* of applying techniques (e.g., "Strangler Fig," "branch by abstraction," "make-the-change-easy-then-make-the-easy-change," TDD-driven refactoring). Methodologies *compose* techniques; they are a higher tier in the taxonomy.
- **Mechanism** — *what structurally changes and why that helps*: e.g., "introduces an indirection seam so a dependency can be substituted." The mechanism is the unit of grounding. The whole taxonomy is organized so that two differently-named techniques sharing a mechanism are visibly siblings.
- **Symptom** — the observable trigger (smell, metric spike, change-amplification pain). Mechanism is the cause-level fix; symptom is the presenting complaint. We index by *both* but ground in mechanism.
- **Impact** — the value delivered: reduced change-amplification, improved testability, lowered coupling, clarified intent, enabled subsequent change. We will resist a single scalar "impact score." (Low confidence in any cross-technique "impact ranking" — see Caveats.)
- **Blast radius** — the scope of code/behavior potentially affected by applying the technique: from local (one function body) to wide (every caller, serialized data, other services). A first-class attribute because it governs *risk* and *reversibility*.

---

## 5. INCLUSION / EXCLUSION RULES (a concrete membership test)

A candidate belongs in the taxonomy **iff ALL hold**:
1. **Behavior-preserving** — it changes internal structure while preserving observable behavior across a statable boundary. (If it changes the contract, it's a feature/migration, not a refactor — excluded or flagged as "behavior-changing migration.")
2. **Detectable trigger** — there is an observable condition (smell, metric, change-pain pattern, violation) that signals *when* to reach for it. (No trigger ⇒ it's advice, not a technique.)
3. **Defined transformation** — the structural delta is specifiable as a before→after, not "improve the design."
4. **Preservation argument** — there is a stated way to gain confidence behavior held (tests, equivalence reasoning, tool-guaranteed, characterization tests for legacy).
5. **Non-trivial structural consequence** — it alters dependency/coupling/cohesion/control-flow/data-shape, i.e., something a linter couldn't just auto-rewrite lexically.

**Excluded** if: purely cosmetic (rule 5 fails); changes observable contract (rule 1 fails); is a principle/aspiration with no transformation (rule 3 fails); or is a tool/process rather than a code change.

**Edge case handling:** Behavior-*changing* moves that are commonly conflated with refactoring (e.g., "tighten an over-broad API," "fix a latent bug found mid-refactor") get a **sidebar tag, not membership** — catalogued as "adjacent, not refactoring" so practitioners stop mislabeling them. (Med — this boundary is where teams most often deceive themselves.)

---

## 6. SUCCESS CRITERIA (how we'll judge the final taxonomy)
- **Coverage** — spans all in-scope paradigms and altitudes; common real smells each map to ≥1 technique; no large "obvious gap."
- **Mechanism grounding** — every entry names its mechanism; entries sharing a mechanism are explicitly linked (passes a "why does this help, structurally?" challenge for each).
- **Actionability** — a practitioner can go trigger → technique → safe application without external lookup.
- **Non-overlap / clean partition** — minimal duplicate entries; where two techniques overlap, the distinguishing axis (blast radius, paradigm, direction) is explicit.
- **Measurability of effect** — each technique states *what to observe* to know it helped (and that behavior held), even if qualitative.
- **Falsifiability of triggers** — triggers are stated concretely enough that two reviewers would agree whether one fires.
- **Honest uncertainty** — contestable impact/ranking claims are marked, not laundered into false precision.

---

## 7. EPISTEMIC CAVEATS
- **Strong evidence (High):** the canonical technique catalog (Fowler *Refactoring*; Opdyke's thesis; Feathers *Working Effectively with Legacy Code*; Kerievsky *Refactoring to Patterns*). Mechanisms and before/after transformations here are well-established and tool-validated.
- **Medium evidence:** smell→problem mappings (empirically mixed); cross-paradigm mechanism analogies (defensible case-by-case, not universally); blast-radius estimates (depend on codebase shape).
- **Weak evidence (Low):** any **"highest-impact" ranking** — impact is context-dependent (codebase, team, change profile); cross-discipline analogies (borrowing from systems theory, manufacturing/lean, biology/"refactoring as evolution") are *generative framing*, not proof; claims that a given refactor reduces defects/cost in general (the literature does not robustly support universal claims). **We will present "high-impact" as conditional ("high-impact *when* X"), never as a global leaderboard.**
- **Methodological caveat:** "behavior-preserving" is an *idealization*; in practice preservation is bounded by the boundary chosen and the tests available. We will be explicit that safety is relative to the preservation argument, not absolute.

---

## 8. WORKING STATE v1

```
=== WORKING STATE (refactoring-taxonomy) — v1 @ Turn 1/9 ===

PROVISIONAL DEFINITIONS (may revise):
- refactoring = behavior-preserving internal structural change, relative to a stated boundary
- smell = surface heuristic symptom (NOT diagnosis); evidence it predicts problems is mixed
- violation = breach of an explicit rule/invariant (vs smell = heuristic)
- technique = atomic transform: <trigger, preconditions, mechanism, preservation-arg, blast-radius>
- methodology = principled sequence/composition of techniques (Strangler, branch-by-abstraction, ...)
- mechanism = the structural delta + why-it-helps; the GROUNDING UNIT of the taxonomy
- impact = enabled change / testability / coupling / clarity — NO single scalar; conditional only

ORGANIZING AXES (candidate, to test in Turn 2+):
- A1 mechanism family (extract/inline, move, abstract/seam, replace-conditional, data-shape, ...)
- A2 altitude (code / arch-edge)
- A3 paradigm (OO / FP / procedural / SQL-data / concurrent)
- A4 blast radius (local / module / cross-module / cross-boundary)
- A5 direction (decompose vs consolidate; generalize vs specialize)
- A6 trigger type (smell / metric / change-pain / violation)
PRIMARY INDEX = A1 (mechanism). Others = facets/filters. (confidence Med)

OPEN QUESTIONS:
- Q1 Is "mechanism family" the right primary key, or should trigger be primary? (lean: mechanism)
- Q2 How to handle behavior-CHANGING moves teams mislabel as refactors? (lean: adjacency tag)
- Q3 Exact cut line between "big refactor (in)" and "re-architecture (out)"? NEEDS a test.
- Q4 Do cross-paradigm "same mechanism" claims survive scrutiny, or collapse to analogy? (per-case)
- Q5 Granularity: atomic techniques only, or also named composites? (lean: both, separate tiers)
- Q6 How do we represent "preservation argument" uniformly across techniques?
- Q7 What's the minimum schema for a taxonomy ENTRY? (draft below)

DRAFT ENTRY SCHEMA (to finalize Turn 2):
  name | aliases | mechanism | trigger(s) | preconditions | before→after |
  preservation-argument | blast-radius | paradigm-applicability | inverse-technique |
  related(same-mechanism) | impact(conditional) | confidence

CANDIDATE SCOPE EDGES (in/out, revisit):
  IN core: code-level transforms, all 5 paradigms
  EDGE: extract-module, dependency-inversion-at-boundary, seam introduction, strangler
  OUT: style, linter rules, GoF-as-catalog, perf manual, process, greenfield, tooling shootout
  ADJACENT-TAG: behavior-changing API tightening, bug-fix-during-refactor, data migration

TODO turns 2-9 (provisional):
  T2  Lock entry schema; lock primary axis; build the mechanism-family skeleton (top-level buckets)
  T3  Populate code-level OO + procedural techniques against schema
  T4  Populate FP + data/SQL + concurrent techniques; stress cross-paradigm mechanism claims (Q4)
  T5  Architecture-EDGE techniques + methodologies tier (Strangler, branch-by-abstraction, etc.)
  T6  Triggers & detection: map smells/metrics/change-pain to techniques; falsifiability pass
  T7  Blast-radius + preservation-argument + inverse-technique completeness pass
  T8  "High-impact WHEN X" conditional layer; de-overlap / partition audit; gap hunt
  T9  Final synthesis: assemble catalog; success-criteria self-audit; epistemic honesty pass

CONFIDENCE LEDGER (contestable items to keep honest):
  - smell predicts real problems ......... Med/contested
  - cross-paradigm mechanism unity ....... Med (per-case)
  - any global impact ranking ............ Low (use conditional framing only)
  - big-refactor vs re-architecture line . Med (Q3 unresolved)
=== END WORKING STATE ===
```

Turn 1 sets the contract only — no taxonomy synthesized, per instruction. Ready for Turn 2 (lock schema + primary axis + mechanism-family skeleton) on your go.



---

# Refactoring Taxonomy — TURN 2/9: THE DEFAULT BASIN

Goal: chart the well so later turns know exactly what they're climbing out of. No new domains proposed here.

---

## 1. THE CANONICAL DEFAULT MENTAL MODEL

Three pillars, tightly interlocked. They form a near-complete *folk theory* of refactoring that most practitioners never examine.

### Pillar A — The Fowler/Beck catalog + red-green-refactor loop
The mental model: refactoring is a **named-move catalog applied to one unit of code at a time**, under a green test bar.
- **The moves:** Extract Method/Function, Inline, Move Method/Field, Rename, Replace Conditional with Polymorphism, Replace Temp with Query, Introduce Parameter Object, Replace Magic Number with Constant, Decompose Conditional, Pull Up / Push Down, Extract Class/Superclass, Hide Delegate, Remove Middle Man.
- **The loop:** *red* (failing test) → *green* (make it pass, ugly) → *refactor* (clean up under green). Refactoring is the third beat — a cleanup phase gated by a passing suite.
- **Implicit ontology:** the *function/class/method* is the unit; the *test suite* is the safety net; the *named move* is the verb. Mechanism is present in Fowler's mechanics steps but in practice gets compressed to the name. (High — this is the documented canon.)

### Pillar B — SOLID as the dominant design-principle lens
The mental model: good structure = obeying five OO principles; refactoring = removing violations. The field-standard *detection heuristics*:
- **S (SRP):** files >300 LOC, functions >50 LOC, classes >10 methods, visibly mixed concerns (I/O + business logic + formatting in one place).
- **O (OCP):** switch / if-else chains keyed on a type or enum that grow a new arm per feature.
- **L (LSP):** overrides throwing `NotImplemented`, `instanceof`/type-checks before a call, no-op overrides, narrowed preconditions / widened postconditions.
- **I (ISP):** interfaces >10 members, implementors stubbing methods they don't use, "fat" service interfaces.
- **D (DIP):** `new Concrete()` inside business logic, hardcoded singletons/global access, no seam to substitute a dependency.
(High that these heuristics are *the* taught defaults. **Med/contested** that the heuristics reliably identify real harm — the thresholds are arbitrary proxies, see §3.)

### Pillar C — The standard code-smell checklist
A fixed roster of surface symptoms: long methods, long parameter lists (>4), deep nesting (≥4 levels), duplicated blocks, magic numbers, primitive obsession, feature envy — plus the usual extended set (data clumps, shotgun surgery, divergent change, large class, message chains, speculative generality, comments-as-deodorant). (High that this is the canonical roster.)

**How the three interlock:** smell (Pillar C) → diagnosed as principle violation (Pillar B) → fixed with a named move (Pillar A) → under the green bar (Pillar A's loop). It's a closed, self-consistent loop — which is *exactly* what makes it a basin.

---

## 2. WHY THE BASIN IS STICKY

It's not stupidity — it's a genuine local optimum reinforced from every direction:
- **Canonization:** Fowler's *Refactoring* and Martin's *Clean Code/Agile Principles* are the field's shared scripture; SOLID is a five-letter mnemonic engineered for recall. Mnemonics + canon = default.
- **Tooling embodiment:** IDEs ship the Fowler moves as one-click, behavior-safe transforms (Extract Method, Rename, Move). Linters/SonarQube/CodeClimate ship the smell checklist and SOLID-ish thresholds as *defaults you literally cannot avoid seeing*. The tool's ruleset becomes the definition of "quality." (High)
- **Interview/credential loop:** "name a code smell," "what does SOLID stand for," "refactor this method" are interview staples — so every new engineer over-learns exactly this set.
- **Reviewability:** these heuristics are *cheap to check in a PR* (line counts, nesting depth, `new` keywords). Defaults optimize for **cheap detectability in a code review**, not for predictive validity.
- **What it optimizes for (the real telos):** *local readability and single-file/single-class structural tidiness at low cognitive cost, verifiable by eye or linter.* That is a real good — it's just a narrow one. (Med on "narrow," argued in §3.)

The stickiness is the point: the basin is sticky *because* it's locally correct and cheap. Escaping requires showing there are whole problem classes it can't even see.

---

## 3. BLIND SPOTS (what the framing systematically cannot see)

The default basin's unit of analysis is **a single chunk of code at rest, read by one person**. Everything that lives *between* units, *over time*, or *under load* falls outside its sensors.

- **Concurrency / shared-state smells (High):** races, lock-ordering, non-atomic check-then-act, mutable shared state, false sharing, missing memory-visibility guarantees. Fowler's moves are explicitly *single-threaded* in their preservation argument; "Extract Method" can *break* thread-safety while looking clean. The checklist has *no* concurrency entry.
- **Data-model / schema smells (High):** denormalization-by-accident, nullable-everything, stringly-typed columns, implicit state machines encoded in flag combinations, missing invariants at the data layer, anemic-vs-rich-model tension. SOLID is code-shaped; it barely touches *data shape*, which often dominates change cost.
- **Temporal / lifecycle smells (Med-High):** initialization-order coupling, "valid only after step N," temporal coupling between calls, resource lifecycle (open/use/close) scattered across files, partially-constructed objects. The static catalog sees code at rest, not its required *call sequence*.
- **Socio-technical / ownership smells (Med):** code that's "clean" but sits on a team boundary; modules whose change requires N teams; knowledge silos; conway-misalignment. Refactoring's classic blind spot — the org isn't in the model. (Med because evidence is largely qualitative/case-study.)
- **Performance-structure tradeoffs (Med):** the "clean" extraction that adds an allocation/indirection in a hot loop; abstraction that defeats batching or the optimizer; N+1 query patterns hidden behind tidy methods. The basin treats performance as out-of-scope, so structure that *causes* bad performance is invisible to it.
- **Distributed-systems coupling (High):** chatty service calls, shared databases as hidden coupling, temporal coupling across network boundaries, ret\/idempotency/partial-failure handling, contract/versioning coupling. SOLID's "D" stops at the process boundary; cross-process coupling is unseen.
- **Observability / operability gaps (Med):** structure that can't be logged/traced/metered at the right seam; failure modes with no signal; debuggability as a design property. Never appears in the checklist.
- **Error-handling structure (Med-High):** error propagation paths, swallowed exceptions, partial-failure recovery, retry/compensation — treated as incidental, not structural, by the default basin.
- **Abstraction-validity blind spot (Med):** the basin *rewards* extracting/DRYing, but is nearly silent on the opposite failure — *premature/wrong abstraction*, where "Extract" coupled two things that should diverge. It has a one-way bias toward more structure.

**Meta blind spot (High):** the heuristics are **proxies validated by convention, not by outcome.** "Function >50 LOC" correlates weakly, at best, with defect/change cost; the empirical literature on smells→defects is mixed. The basin treats arbitrary thresholds as if they were measurements.

---

## 4. THE GRAVITATIONAL PULL (the failure mode to avoid)

**The specific risk:** the final taxonomy ends up as *Fowler + SOLID + the smell checklist, re-tabulated with fresh nouns.* Signs we'd be falling in:
- Top-level buckets that quietly mirror the smell checklist (a "Long Method" row, a "Feature Envy" row...).
- Every "mechanism" bottoming out in a Fowler move name.
- Zero entries whose *trigger* is a race, a schema shape, a call-sequence requirement, a cross-service round-trip, or a missing observability seam.
- Single-unit-at-rest as the only unit of analysis; no technique whose blast radius is *temporal* or *cross-process*.
- A one-way bias toward "more abstraction," with no de-abstraction / inline-the-wrong-seam techniques.

**What escape looks like (preview only, not built yet):** organize by **mechanism** such that the *same* mechanism (e.g., "introduce a substitution seam") is shown operating at code, data, temporal, concurrent, and distributed altitudes — so the catalog's spine is the structural delta, and Fowler/SOLID become *one paradigm's instances* of broader mechanisms rather than the table of contents. The escape test: **can a row exist whose trigger no Fowler move and no SOLID letter can name?** If yes, we've left the basin.

---

## 5. WORKING STATE v2

```
=== WORKING STATE (refactoring-taxonomy) — v2 @ Turn 2/9 ===

PROVISIONAL DEFINITIONS (carried from v1, unchanged):
- refactoring = behavior-preserving internal structural change, relative to a stated boundary
- smell = surface heuristic symptom (NOT diagnosis); smell->problem evidence is mixed
- violation = breach of explicit rule/invariant; technique = atomic transform
  <trigger, preconditions, mechanism, preservation-arg, blast-radius>
- methodology = principled composition of techniques
- mechanism = structural delta + why-it-helps = THE GROUNDING UNIT
- impact = conditional only; NO global scalar

DEFAULT BASIN PILLARS (NEW in v2 — the well we must climb out of):
- A. Fowler/Beck catalog + red-green-refactor loop
     unit=function/class | safety=test suite | verb=named move | loop gates cleanup
- B. SOLID lens w/ field-standard detection heuristics:
     S: file>300 / fn>50 / class>10 methods / mixed concerns
     O: growing type/enum switch chains
     L: NotImplemented overrides, instanceof-before-call, no-op overrides
     I: iface>10 members, stubbed unused methods, fat services
     D: new Concrete() in logic, hardcoded singletons, no seam
- C. Smell checklist: long method, params>4, nesting>=4, dup blocks, magic numbers,
     primitive obsession, feature envy (+data clumps, shotgun surgery, large class...)
- INTERLOCK: smell(C) -> violation(B) -> named move(A) -> under green bar(A). Closed loop.
- OPTIMIZES FOR: local readability + single-unit tidiness, cheaply checkable in a PR/linter
- STICKY BECAUSE: canon+mnemonic, IDE/linter embodiment, interview loop, cheap reviewability

NAMED BLIND SPOTS (what the basin's "single unit at rest, read by one person" sensor misses):
- concurrency / shared-state smells .............. High
- data-model / schema smells ..................... High
- temporal / lifecycle / call-sequence smells .... Med-High
- socio-technical / ownership / Conway smells .... Med
- performance-structure tradeoffs ................ Med
- distributed-systems coupling (cross-process) ... High
- observability / operability gaps ............... Med
- error-handling / partial-failure structure ..... Med-High
- premature/wrong-abstraction (one-way DRY bias).. Med
- META: heuristics are convention-proxies, not outcome-validated . High

ESCAPE LOOKS LIKE (preview, NOT built yet):
- spine = MECHANISM; same mechanism shown across code/data/temporal/concurrent/distributed
- Fowler+SOLID become instances of broader mechanisms, not the table of contents
- ESCAPE TEST: can a row exist whose trigger NO Fowler move & NO SOLID letter can name?
- include the inverse direction (de-abstraction), not just "more structure"

ORGANIZING AXES (carried; A1 still primary, now stress-tested by blind spots):
  A1 mechanism family (PRIMARY) | A2 altitude | A3 paradigm | A4 blast radius
  A5 direction (decompose/consolidate, generalize/specialize) | A6 trigger type
  NOTE: blind spots imply A3 must extend beyond OO; A4 must include temporal & cross-process

OPEN QUESTIONS (carried + new):
- Q1 mechanism vs trigger as primary key? (lean mechanism)
- Q2 behavior-CHANGING moves mislabeled as refactor? (lean: adjacency tag)
- Q3 big-refactor vs re-architecture cut line? NEEDS a test.
- Q4 cross-paradigm "same mechanism" — real or analogy? (per-case)
- Q5 atomic-only vs also composites? (lean: both, separate tiers)
- Q6 uniform representation of preservation-argument?
- Q7 minimum ENTRY schema? (draft below)
- Q8 NEW: do blind-spot domains need their own triggers, or do they reuse A1 mechanisms
       at new altitudes? (this is the core Turn-3+ question)
- Q9 NEW: how to encode the one-way DRY/abstraction bias as a first-class direction axis?

DRAFT ENTRY SCHEMA (unchanged, finalize Turn 2/3):
  name | aliases | mechanism | trigger(s) | preconditions | before->after |
  preservation-argument | blast-radius | paradigm-applicability | inverse-technique |
  related(same-mechanism) | impact(conditional) | confidence

TODO turns 3-9 (provisional, lightly revised):
  T3  Lock entry schema + primary axis; build mechanism-family SKELETON deliberately spanning
      blind-spot altitudes (not just Fowler buckets); populate OO+procedural code-level
  T4  FP + data/SQL + concurrent techniques; stress cross-paradigm mechanism unity (Q4)
  T5  Architecture-EDGE + methodologies tier (Strangler, branch-by-abstraction); distributed coupling
  T6  Triggers & detection: map smells/metrics/change-pain + BLIND-SPOT triggers; falsifiability
  T7  Blast-radius (incl temporal/cross-process) + preservation-args + inverse-techniques pass
  T8  "High-impact WHEN X" conditional layer; de-overlap/partition audit; gap hunt vs blind-spot list
  T9  Final synthesis; success-criteria self-audit; escape-test check; epistemic honesty pass

CONFIDENCE LEDGER (contestable items):
  - smell predicts real problems ............ Med/contested
  - SOLID heuristic thresholds = real harm .. Med/contested (arbitrary proxies)
  - cross-paradigm mechanism unity .......... Med (per-case, Q4)
  - any global impact ranking ............... Low (conditional framing only)
  - big-refactor vs re-architecture line .... Med (Q3 unresolved)
  - each blind-spot claim .................... tagged inline above
=== END WORKING STATE ===
```

Turn 2 maps the basin and names the escape test; no new domains or final structure proposed yet, per instruction. Ready for Turn 3 (lock schema + primary axis, build a mechanism-family skeleton that deliberately spans the blind-spot altitudes) on your go.



---

# Refactoring Taxonomy — TURN 3/9: SOURCE-DOMAIN DICTIONARY

Raw material only — no ranking, merging, or structure. Format per concept: **Name** — *native mechanism* | →maps: why it might transfer. `(Low)` = transfer is speculative, flag for audit turn.

---

## D1. Software refactoring canon (beyond Fowler)
- **Seam (Feathers)** — *a place to alter behavior without editing in place (object/link/preprocessing seam)* | →maps: the universal "insert a substitution point" mechanism; candidate spine primitive.
- **Characterization test** — *pin current behavior (even if "wrong") to detect change* | →maps: the preservation-argument tool when no spec/tests exist; legacy entry-gate.
- **Sprout method/class** — *add new code in a fresh unit, call it from the old tangle* | →maps: refactor *around* untestable code instead of through it.
- **Wrap method/class** — *interpose a wrapper to add behavior at a boundary* | →maps: decorator-as-refactor; change at seam, not in core.
- **Refactoring to Patterns (Kerievsky)** — *move toward/away from a GoF pattern by smell, bidirectionally* | →maps: patterns as *targets/directions*, and crucially "refactor *away* from a pattern" = de-abstraction direction (Q9).
- **Break dependencies (Feathers' catalog)** — *parameterize constructor/method, extract interface, subclass-and-override to test* | →maps: family of seam-introduction mechanisms at the unit level.
- **Lean on the compiler** — *use type errors to drive a transformation to completion* | →maps: tool-guaranteed preservation; mechanizable refactors.

## D2. Design theory & patterns
- **GoF Strategy/State** — *replace conditional-on-type with polymorphic object* | →maps: the canonical "conditional→dispatch" mechanism target.
- **GRASP Information Expert** — *assign responsibility to the holder of the needed data* | →maps: principled "move method toward its data" (feature-envy cure, mechanism-level).
- **Hexagonal / Ports & Adapters** — *isolate domain from I/O via interfaces at edges* | →maps: large-scale seam introduction; DIP at architecture-edge altitude.
- **DDD Bounded Context** — *draw a model boundary where language/meaning shifts* | →maps: module-extraction trigger driven by *semantic* divergence, not LOC.
- **DDD Aggregate** — *cluster entities under one consistency/transaction boundary* | →maps: data+behavior co-location around an invariant; consistency-boundary mechanism.
- **Anti-Corruption Layer** — *translate at a boundary to stop a foreign model leaking in* | →maps: wrap/adapter mechanism at cross-context altitude.
- **Alexander pattern language** — *named context→forces→resolution, composed generatively* | →maps: meta-template for *how to write a taxonomy entry* (forces = our trigger/precondition).
- **Christopher Alexander "quality without a name" / piecemeal growth** — *repair toward wholeness incrementally* | →maps: refactoring as continuous repair vs big-bang. (Low — risk of mysticism, audit.)

## D3. Compiler / IR theory
- **Semantics-preserving transformation** — *rewrite IR while provably preserving observable I/O* | →maps: the *formal* definition of behavior-preservation; gold standard for preservation-argument.
- **SSA form** — *each variable assigned once; make data-flow explicit* | →maps: "make implicit state/dependency explicit" mechanism (e.g., split temp, single-assignment).
- **Dead-code elimination** — *prove unreachable/unused, remove* | →maps: vestigial-removal with a *reachability proof* — stronger than "looks unused."
- **Inlining / outlining** — *substitute body for call, or extract body to callee* | →maps: Extract/Inline as inverse pair, with cost model (call overhead vs duplication).
- **Loop transforms (fusion/fission/interchange/unroll/hoist)** — *reorder/merge/split iteration preserving result* | →maps: structural transforms on *iteration*, a unit the basin ignores.
- **Common subexpression elimination** — *compute once, reuse* | →maps: DRY with an *equivalence proof* attached (vs blind extract).
- **Equivalence / observational equivalence** — *two programs indistinguishable to any observer* | →maps: rigorous frame for "behavior preserved relative to a boundary."
- **Memoization/purity analysis** — *cache when referentially transparent* | →maps: trigger = "is this transform legal?" depends on purity — a precondition formalism.

## D4. Database / data modeling
- **Normalization (1NF–BCNF)** — *eliminate redundancy by functional-dependency decomposition* | →maps: data-shape decomposition mechanism; the data-model blind spot's core toolkit.
- **Denormalization (deliberate)** — *reintroduce redundancy for read perf, accept update cost* | →maps: the *inverse* direction with explicit tradeoff — counters one-way DRY bias.
- **Expand/contract (parallel-change) migration** — *add new, dual-write, backfill, switch, retire old* | →maps: the canonical behavior-preserving change under *live data* — temporal/blast-radius exemplar.
- **Referential integrity / constraints** — *push invariants into the schema engine* | →maps: "enforce invariant at the lowest layer" mechanism (poka-yoke for data).
- **Surrogate vs natural key** — *introduce indirection key to decouple identity from meaning* | →maps: indirection-seam at the data layer.
- **Schema versioning / evolution** — *versioned migrations as ordered, reversible deltas* | →maps: refactor *with reversibility/inverse* made first-class.

## D5. Graph & network science
- **Dependency graph + SCC/cycle detection** — *find strongly-connected components = cyclic coupling* | →maps: detect *cyclic dependency* trigger objectively (vs eyeballing).
- **Modularity (community detection)** — *partition to maximize intra-/minimize inter-edges* | →maps: principled module-boundary discovery; quantifies cohesion/coupling.
- **Centrality / fan-in-fan-out** — *high-degree nodes = hubs/god-objects/load-bearing* | →maps: objective "this is a god object / single point of change" trigger.
- **Min-cut / cut-set** — *cheapest edge set to separate two regions* | →maps: where to *place a seam* for least-disruption module split.
- **Feedback cycle / back-edge** — *cycles prevent layering/topological order* | →maps: "break the cycle" as a named mechanism (invert one edge / introduce interface).
- **Articulation point / bridge** — *node/edge whose removal disconnects graph* | →maps: fragile single-dependency = reliability + refactor target.

## D6. Information theory
- **Coupling as mutual information** — *shared info between modules = dependence* | →maps: reframe coupling quantitatively rather than by heuristic. (Low — operationalizing MI on code is hard; audit.)
- **Cohesion as low conditional entropy** — *a module's parts predict each other* | →maps: cohesion measure beyond "feels related."
- **Compression / MDL (minimum description length)** — *best model = shortest description of data+model* | →maps: "right amount of abstraction" = whatever minimizes total description — a principled stop rule against over/under-DRY.
- **Channel capacity / bottleneck** — *max throughput limited by narrowest channel* | →maps: interface as channel; over-narrow interface (ISP) or over-wide (leaky) as capacity mismatch. (Low)
- **Entropy / disorder accumulation** — *systems drift to higher entropy absent energy input* | →maps: framing for *why* codebases decay without refactoring effort.

## D7. Control theory & system dynamics
- **Feedback loop (neg/pos)** — *output fed back to regulate (neg) or amplify (pos) input* | →maps: design pos-feedback decay (each hack makes next easier) vs neg-feedback hygiene (tests/CI damp drift).
- **Stocks & flows** — *accumulating stock driven by in/out rates* | →maps: tech-debt as a *stock*; refactoring = outflow rate; models *accumulation*, not point-in-time.
- **Damping / hysteresis** — *resistance that slows oscillation/change* | →maps: "friction to change" as the measurable target a refactor reduces.
- **Instability / runaway** — *gain >1 in a loop → divergence* | →maps: shotgun-surgery as a runaway loop (one change forces N more). 
- **Time constant / lag** — *delay between cause and observed effect* | →maps: why debt feels free now (lagged cost) — supports conditional-impact framing.

## D8. Manufacturing / Lean / TPS
- **Seven wastes (muda)** — *taxonomy of non-value activity (overprocessing, waiting, defects…)* | →maps: a *waste lens* on code (speculative generality = overprocessing; rework = defects).
- **Poka-yoke** — *make the error physically impossible* | →maps: "make illegal states unrepresentable" — types/constraints as error-proofing (strong mechanism).
- **Jidoka (autonomation / stop-the-line)** — *halt automatically on defect detection* | →maps: fail-fast/assert-at-seam; CI as andon cord.
- **Kaizen / standard work** — *small continuous improvement against a baseline standard* | →maps: refactoring cadence; "standard work" = the agreed structural convention.
- **WIP limits** — *cap in-progress to expose bottlenecks* | →maps: limit scope of a refactor pass; small-batch behavior-preserving steps.
- **Value-stream mapping** — *trace flow end-to-end to find delay/waste* | →maps: trace a *change* through the code to find where structure amplifies effort (change-pain trigger).
- **Single-Minute Exchange of Die (SMED)** — *convert internal setup to external to speed changeover* | →maps: reduce "setup cost to make a change" — testability/seam work. (Low)

## D9. Medicine
- **Diagnosis vs treatment** — *identify cause before intervening* | →maps: separate *trigger/symptom* from *mechanism/fix* — core of our contract.
- **Triage** — *order by severity × treatability under scarce resources* | →maps: conditional prioritization ("high-impact WHEN") without a global ranking.
- **Contraindication** — *condition under which a treatment is harmful* | →maps: per-technique *preconditions/anti-indications* — when NOT to apply.
- **Iatrogenic harm** — *harm caused by the treatment itself* | →maps: refactoring that introduces bugs/regressions; argues for preservation-argument rigor.
- **First, do no harm / watchful waiting** — *sometimes the right move is no intervention* | →maps: legitimizes "leave it" — counters compulsive refactoring.
- **Surgery vs medication** — *invasive one-shot vs gradual* | →maps: big-bang seam surgery vs incremental kaizen — a blast-radius/method axis.

## D10. Safety / reliability engineering
- **Failure mode (FMEA)** — *enumerate how each part can fail × effect × detectability* | →maps: per-technique "how can this transform fail" — structured risk.
- **Swiss-cheese model** — *defenses with holes; incidents when holes align* | →maps: structure that removes a defensive layer is a refactor risk.
- **Blast radius / fault isolation** — *limit how far a failure propagates* | →maps: already our blast-radius attribute; bulkheading as a refactor *goal*.
- **Defense in depth** — *layered independent safeguards* | →maps: argues against collapsing redundant checks "for DRY."
- **Bulkhead / circuit breaker** — *isolate so one failure can't sink the whole* | →maps: distributed-coupling refactor mechanism (the blind spot).
- **Error budget / graceful degradation** — *plan for partial function under failure* | →maps: error-handling structure as first-class (blind spot).

## D11. Economics & finance
- **Technical debt (interest)** — *shortcut now → recurring carrying cost later* | →maps: the field's dominant metaphor; carrying cost justifies refactor timing.
- **Sunk cost** — *past spend shouldn't drive future decisions* | →maps: counters "we already built this abstraction, keep it" — supports de-abstraction.
- **Real options** — *pay small now to preserve future choice* | →maps: seams/abstractions as *options* with a price — values reversibility/flexibility.
- **Amortization** — *spread cost over many uses* | →maps: refactor cost justified only if change-frequency amortizes it (conditional impact).
- **Opportunity cost** — *value of the best forgone alternative* | →maps: refactoring vs feature work tradeoff framing.
- **YAGNI / option premium** — *don't pay for flexibility you won't use* | →maps: anti-speculative-generality, priced.

## D12. Cognitive science / human factors
- **Working-memory limit (~4±1 chunks)** — *finite simultaneous items* | →maps: the *real* basis for "long method/param list" — ground the threshold in cognition, not arbitrary LOC.
- **Chunking** — *group items into higher-order units to fit memory* | →maps: extraction/naming as chunking; good names create chunks.
- **Cognitive load (intrinsic/extraneous/germane)** — *distinguish inherent vs incidental difficulty* | →maps: refactoring removes *extraneous* load only; reframes the goal precisely.
- **Code as communication (reader-centric)** — *code is read far more than written* | →maps: readability = decode cost for a future reader; the telos of the default basin, made explicit.
- **Locality of reference (cognitive)** — *related things understood together if near* | →maps: "move things that change together close" (also matches Connascence/proximity).
- **Schema (mental model) mismatch** — *comprehension fails when code violates expected pattern* | →maps: surprise/POLA violations as a refactor trigger. (Low — measurement is soft.)

## D13. Ecology / evolution
- **Vestigial structure** — *once-useful feature now inert but retained* | →maps: dead/legacy code with a *history*; removal = DCE with archaeology.
- **Selection pressure / fitness landscape** — *traits succeed relative to environment; local peaks* | →maps: the "default basin" itself is a local fitness peak; refactor = traverse to higher peak.
- **Mutation / neutral drift** — *changes accumulate, some neutral* | →maps: behavior-preserving edits = "neutral mutations" that reposition for future adaptation.
- **Co-evolution** — *coupled species change in lockstep* | →maps: code that must change together (connascence/shotgun surgery) as co-evolution.
- **Exaptation** — *feature repurposed for a new use* | →maps: refactoring to *generalize* an existing structure for a new need. (Low)
- **Convergent evolution** — *independent paths reach similar form* | →maps: duplicated-but-not-identical logic that converged; merge-vs-keep-separate decision.

## D14. Urban planning / civil architecture
- **Broken windows** — *visible disorder invites more disorder* | →maps: tolerated smell normalizes more; argues for hygiene cadence. (Med — popular but empirically contested even in criminology.)
- **Zoning** — *separate incompatible uses spatially* | →maps: layering/bounded-context separation by *kind of concern*.
- **Brownfield redevelopment** — *build on contaminated/used land, remediate first* | →maps: legacy refactoring = remediate-before-build (characterization tests first).
- **Infrastructure vs building** — *long-lived shared substrate vs replaceable units* | →maps: distinguish stable cores (refactor carefully) from leaf code (cheap to replace).
- **Desire paths** — *actual usage carves routes vs planned ones* | →maps: let real call/change patterns reveal the *right* boundaries (vs imposed design).
- **Load-bearing wall** — *structural element you can't naively remove* | →maps: high-fan-in code; removal/change needs shoring (seams) first.

## D15. Institutions / governance
- **Conway's Law** — *system structure mirrors org communication structure* | →maps: socio-technical blind spot; module boundaries should track team boundaries (or inverse-Conway maneuver).
- **Ownership / stewardship** — *clear responsibility for a region* | →maps: orphaned code as a smell; refactor to align ownership.
- **Convention / standard** — *shared agreed form reduces coordination cost* | →maps: "standard work"; deviation from convention as a (cheap) trigger.
- **Drift / erosion** — *gradual divergence from intended design over time* | →maps: architecture erosion as the accumulation the taxonomy fights (links to stocks/flows).
- **Governance / review as control** — *gates that enforce invariants socially* | →maps: code review as the social preservation-argument; ties process-edge back in.
- **Bus factor** — *knowledge concentrated in few people* | →maps: socio-technical risk; refactor for comprehensibility lowers it. (Med)

---

## EXTRA DOMAINS (added — strong transfer)
## D16. Type theory / formal methods
- **Make illegal states unrepresentable** — *encode invariants in types so bad states won't compile* | →maps: strongest poka-yoke; primitive-obsession cure with a *proof*.
- **Parametricity / "theorems for free"** — *generic signatures constrain behavior* | →maps: generalize-by-type as a behavior-narrowing (and reasoning) mechanism.
- **Refinement / behavioral subtyping** — *subtype must honor supertype contract* | →maps: the *formal* content of LSP — grounds the L heuristic in real preconditions/postconditions.
- **Totality** — *function defined on all inputs (no partial)* | →maps: eliminate NotImplemented/throw branches; partial→total refactor.

## D17. Connascence (Page-Jones) — a refactoring-native but under-used framework
- **Connascence (static: name/type/meaning/position/algorithm)** — *two elements must change together to stay correct* | →maps: a *graded coupling taxonomy* far richer than "coupling"; candidate organizing primitive.
- **Connascence (dynamic: execution-order/timing/value/identity)** — *coupling that only exists at runtime* | →maps: directly names the temporal/concurrency blind spots the basin misses.
- **Degree & locality rules** — *stronger connascence is OK only if local; weaken or localize it* | →maps: an actual *mechanism rule* — "reduce strength, or reduce distance" — a candidate spine principle.

---

## WORKING STATE v3

```
=== WORKING STATE (refactoring-taxonomy) — v3 @ Turn 3/9 ===

[DEFINITIONS, BASIN PILLARS, BLIND SPOTS, ESCAPE TEST — carried unchanged from v2]
  refactoring = behavior-preserving internal structural change rel. to a stated boundary
  mechanism = structural delta + why-it-helps = GROUNDING UNIT
  escape test = "can a row exist whose trigger no Fowler move & no SOLID letter names?"

DOMAIN DICTIONARY INDEX (NEW in v3 — domain -> concept count, raw material captured):
  D1  Refactoring canon (Feathers/Kerievsky) ...... 7
  D2  Design theory & patterns ..................... 8
  D3  Compiler / IR theory .......................... 8
  D4  Database / data modeling ...................... 6
  D5  Graph & network science ....................... 6
  D6  Information theory ............................ 5  (2 Low)
  D7  Control theory / system dynamics .............. 5
  D8  Manufacturing / Lean / TPS .................... 7  (1 Low)
  D9  Medicine ...................................... 6
  D10 Safety / reliability eng. ..................... 6
  D11 Economics & finance ........................... 6
  D12 Cognitive science / human factors ............ 6  (1 Low)
  D13 Ecology / evolution ........................... 6  (1 Low)
  D14 Urban planning / civil arch .................. 6
  D15 Institutions / governance .................... 6
  D16 Type theory / formal methods (added) ......... 4
  D17 Connascence (added) .......................... 3
  ------------------------------------------------------
  TOTAL: 17 domains, 101 concepts (12 flagged Low for audit)

HIGH-SIGNAL CROSS-DOMAIN ECHOES (noted, NOT yet merged — for Turn 4/8 synthesis):
  - SEAM appears as: Feathers seam / hexagonal port / surrogate key / min-cut / load-bearing
    wall / real-option — candidate UNIVERSAL MECHANISM #1 ("introduce substitution point").
  - "MAKE IMPLICIT EXPLICIT" appears as: SSA / normalization / poka-yoke types / connascence-
    of-meaning->name — candidate UNIVERSAL MECHANISM #2.
  - "RIGHT AMOUNT OF ABSTRACTION" stop-rule: MDL / YAGNI / parametricity / denormalization —
    candidate basis for the de-abstraction direction (Q9).
  - "CHANGE-TOGETHER" coupling: connascence / co-evolution / shotgun-surgery / mutual-info /
    locality — candidate UNIVERSAL TRIGGER ("things that change together are far apart / 
    things far apart change together").
  - PRESERVATION-ARGUMENT rigor ladder: characterization test < tests < type/compiler-proof <
    observational-equivalence — candidate standard field for the entry schema.

NEW OPEN QUESTIONS:
  Q10 Is CONNASCENCE the better primary organizing primitive than "mechanism family"?
      (it natively spans static/dynamic/temporal — directly addresses blind spots) — TEST in T4/T8.
  Q11 Should the entry schema's preservation-argument become a graded LADDER (above)? (lean yes)
  Q12 Which of the 12 Low-confidence transfers survive an audit? (defer to T8 audit turn)
  Q13 Do we adopt MDL as the formal "stop rule" for over/under-abstraction, or keep it informal?

[CARRIED OPEN Qs: Q1 mechanism-vs-trigger primary; Q2 adjacency tag; Q3 refactor-vs-rearchitect
 cut line; Q4 cross-paradigm unity; Q5 atomic+composite tiers; Q6 preservation rep; Q7 schema;
 Q8 blind-spot domains: own triggers vs reuse mechanisms; Q9 abstraction-direction axis]

DRAFT ENTRY SCHEMA (now +preservation-LADDER per Q11):
  name | aliases | mechanism | trigger(s) | preconditions/contraindications |
  before->after | preservation-argument (LADDER: char-test/test/type/equivalence) |
  blast-radius | paradigm-applicability | direction | inverse-technique |
  related(same-mechanism) | source-domain(s) | impact(conditional) | confidence

TODO turns 4-9 (revised):
  T4  Lock primary axis (mechanism-family vs connascence, Q1/Q10); build SKELETON spanning
      blind-spot altitudes; begin populating using the dictionary as feedstock
  T5  Architecture-EDGE + methodologies tier (Strangler, branch-by-abstraction, ACL, expand/contract)
  T6  Triggers & detection incl. blind-spot + graph-metric triggers; falsifiability pass
  T7  Blast-radius + preservation-LADDER + inverse-techniques completeness
  T8  AUDIT: test the 12 Low transfers; "high-impact WHEN X" layer; de-overlap; gap hunt vs blind spots
  T9  Final synthesis; success-criteria + escape-test self-audit; epistemic honesty pass

CONFIDENCE LEDGER:
  - smell predicts problems Med/contested | SOLID thresholds Med/contested
  - cross-paradigm mechanism unity Med (per-case) | global impact ranking Low
  - refactor-vs-rearchitect line Med (Q3) | broken-windows analogy Med (contested even in source)
  - 12 dictionary concepts flagged Low inline (info-theory ops, SMED, exaptation, schema-mismatch,
    channel-capacity, MI-on-code, Alexander QWAN, ...) -> T8 audit
=== END WORKING STATE ===
```

Turn 3 captured raw material only — 17 domains, 101 concepts, 12 flagged Low for the audit turn. No ranking, merging, or taxonomy built, per instruction. I did note (without merging) five high-signal cross-domain echoes and added two domains (type theory, connascence) plus Q10–Q13. Ready for Turn 4 (lock the primary axis and build the skeleton) on your go.



---

# Refactoring Taxonomy — TURN 4/9: LATENT LENS SWEEP

15 non-isomorphic lenses. Per lens: **(a)** reframing · **(b)** technique-seeds/detection-signals · **(c)** what SOLID/smells miss. Dictionary cross-refs in `[Dx]`. `(Low)` = speculative seed for the audit. No synthesis, no final structure.

---

### L1. Control loops
(a) Refactoring is only "safe" relative to a feedback loop that catches regressions; **the missing loop is the real defect**, not the messy code.
(b) seeds: • **characterization-test-first** before any legacy edit `[D1]` • **coverage-at-the-seam** signal: refactor-risk ∝ (blast radius ÷ test coverage of affected lines) • **mutation-testing as loop-quality probe** — tests that survive mutants are a fake loop (Low) • **CI-as-andon** stop-the-line `[D8 jidoka]`.
(c) Misses: SOLID grades the *code*; this lens grades the *safety net around* the code. A "clean" SRP class with zero real assertions is high-risk; SOLID can't see that.

### L2. Information flow
(a) Coupling/cohesion = actual data/control flow between regions; refactor = **re-route information so less of it crosses a boundary** `[D6, D5]`.
(b) seeds: • **dataflow-cut / min-cut seam placement** — split where the flow is thinnest `[D5 min-cut]` • **push computation to the data** `[D2 GRASP Info-Expert]` (feature-envy cure, flow-grounded) • **collapse a pass-through channel** (remove middle-man only when it carries no information) • **reduce fan-of-shared-mutable-state** `[D6 mutual-info]` (Low on quantifying).
(c) Misses: smells flag *syntactic* feature-envy (method uses another class's getters); this lens flags *semantic* leakage — a boundary that's "clean" by interface but carries huge implicit data dependence.

### L3. Boundaries / interfaces
(a) The high-impact question isn't "is this class big" but **"is the cut in the right place, and is the interface the right width?"** `[D2 hexagonal/ACL]`.
(b) seeds: • **introduce seam at a min-cut** `[D5]` • **anti-corruption layer** at a model-mismatch boundary `[D2]` • **narrow OR widen** an interface to match real channel use — ISP is *one* direction; under-wide leaky interfaces are the other `[D6 channel-capacity]` • **bounded-context split on language shift** `[D2 DDD]` (semantic trigger, not LOC).
(c) Misses: ISP only ever says "split fat interfaces." It never says "this interface is *too narrow* and leaks state through back-channels," nor "the boundary is in the wrong place entirely."

### L4. Lifecycle / time / entropy
(a) Structure doesn't degrade uniformly; refactor where it's **degrading fastest** — churn × complexity hotspots, not wherever a linter happens to fire `[D7 stocks/flows, D13]`.
(b) seeds: • **churn×complexity hotspot map** (git history × cyclomatic) → refactor target ranking • **change-coupling from co-commit history** — files that always change together but live apart `[D13 co-evolution, D17 connascence]` • **age/stability stratification** — stable core vs volatile leaf `[D14 infra-vs-building]` • **debt-accumulation rate** as a flow `[D7]`.
(c) Misses: SOLID/smells are *static snapshots*. They cannot see that file A is rewritten weekly (refactor it) while equally-"smelly" file B hasn't changed in 3 years (leave it `[D9 watchful-waiting]`). **History is the strongest signal the basin can't read.**

### L5. Adversarial dynamics
(a) Treat the codebase as under attack by its own change process: every implicit contract *will* be depended on (Hyrum), every metric *will* be gamed (Goodhart), every scattered concern *is* an attack surface.
(b) seeds: • **Hyrum's-Law audit** — find behaviors clients depend on that aren't in the contract; either codify or seal them (Med — detection is hard) • **shotgun-surgery = attack surface**: one logical change touching N sites is a vulnerability `[D7 runaway, D13]` • **Goodhart guard** — don't let "no smells" become the target (the meta blind spot, made adversarial) • **defensive seam** before a high-churn dependency `[D11 real-option]`.
(c) Misses: SOLID assumes contracts = declared interfaces. Hyrum's Law says the *real* contract is all observable behavior; the basin literally cannot see implicit contracts, so its "safe" refactors break clients.

### L6. Ecology / evolution
(a) A module is a population under selection; refactoring is applied **selection pressure** — kill vestigial structures, let fit ones spread `[D13]`.
(b) seeds: • **vestigial-code removal with reachability proof** `[D3 DCE]` • **converge duplicated-but-divergent code** only where it co-evolves `[D13 convergent-evo]` • **neutral-mutation staging** — behavior-preserving edits that reposition for a coming feature `[D13]` • **exaptation: generalize an existing structure for new use** (Low) `[D13]`.
(c) Misses: the basin removes duplication on *sight*; this lens asks whether the two copies are *diverging species* (should stay separate) or *convergent* (should merge) — a distinction smells can't make.

### L7. Formal models
(a) A refactor is high-confidence only when the transform is **provably behavior-preserving** relative to stated invariants/pre/postconditions `[D3, D16]`.
(b) seeds: • **observational-equivalence check** as top rung of the preservation ladder `[D3]` • **invariant extraction** — name and enforce a latent invariant `[D4 referential-integrity, D8 poka-yoke]` • **make illegal states unrepresentable** `[D16]` • **partial→total** function refactor (kill NotImplemented/throw arms) `[D16 totality]` — grounds LSP formally `[D16 behavioral-subtyping]`.
(c) Misses: smells/SOLID give *zero* preservation guarantee — "Extract Method" is assumed safe by ritual. This lens demands the safety be *argued*, and reveals refactors (e.g., reordering effectful calls) that *look* safe but aren't.

### L8. Human factors / cognitive
(a) Code's cost is decode-cost for a future reader; refactor to **fit working memory** (~4±1 chunks), minimize surprise, maximize naming-as-compression `[D12]`.
(b) seeds: • **chunk-to-working-memory** — extract until each unit fits ~4 chunks (grounds "long method" in cognition, not LOC) `[D12]` • **name-as-compression** — a good name *is* the abstraction; rename to create a chunk `[D12 chunking]` • **principle-of-least-astonishment audit** — flag behavior that violates the reader's schema `[D12]` (Low on measurement) • **reduce extraneous (not intrinsic) load** `[D12]`.
(c) Misses: the basin's "function > 50 LOC" is an arbitrary proxy; this lens says a 60-line linear list of well-named steps may be *easier* than a 20-line densely-nested clever one. **Cognitive load ≠ line count.**

### L9. Institutions / governance
(a) Module boundaries should track *team/ownership* boundaries; mismatch (Conway) makes every change a multi-team negotiation `[D15]`.
(b) seeds: • **Conway-alignment refactor** — split/merge modules to match team comms `[D15]` • **inverse-Conway maneuver** — reshape teams, then code follows (Med — org lever, edge of scope) • **orphaned/unowned-code flag** `[D15 ownership]` • **bus-factor hotspot** — concentrate-then-document or simplify single-owner code `[D15]`.
(c) Misses: a module can be perfect by SOLID yet sit exactly on a team seam so every change needs 3 teams. The basin has no sensor for *who* changes the code.

### L10. Markets / economics
(a) Each module has a **debt interest rate** (carrying cost × change frequency); refactor ROI is highest where rate is high — and crucially names **where NOT to refactor** `[D11]`.
(b) seeds: • **debt-interest ranking** = carrying-cost × churn `[D11, D7 stock]` (cross-ref L4 hotspots) • **option-value of a seam** — price the future flexibility a seam buys `[D11 real-options]` • **YAGNI / sunk-cost prune** — remove speculative abstractions nobody exercised `[D11]` (de-abstraction direction, Q9) • **amortization gate** — only abstract once change-frequency pays for it `[D11]`.
(c) Misses: SOLID says "always remove violations." Economics says **most smells aren't worth fixing** (low churn = low interest), and some abstractions should be *deleted* as overpriced options. The basin has no "don't bother" output.

### L11. Safety engineering
(a) A refactor's value must be netted against its **regression risk**; prefer transforms with small blast radius, fault isolation, and easy reversibility `[D10]`.
(b) seeds: • **blast-radius scoring** as a first-class entry field `[D10]` • **bulkhead/circuit-breaker introduction** at a failure-prone dependency `[D10]` (distributed blind spot) • **reversibility/inverse-technique** requirement — prefer refactors with a known undo `[D4 reversible migration]` • **expand/contract for zero-downtime structural change** `[D4]`.
(c) Misses: the basin treats all refactors as equally safe under green tests; it ignores that collapsing "redundant" defenses removes a Swiss-cheese layer `[D10]`, and that some clean-ups are irreversible.

### L12. Design theory
(a) The real axis is **abstraction-fit**: missing abstraction (duplication, primitive obsession) vs *premature/wrong* abstraction (speculative generality) — both are defects `[D2, D6 MDL]`.
(b) seeds: • **introduce missing abstraction** (toward a pattern) `[D1 Kerievsky]` • **collapse premature abstraction** (refactor *away* from a pattern — the under-served direction) `[D1 Kerievsky bidirectional]` • **MDL stop-rule** — right abstraction = shortest total description `[D6]` (Q13) • **replace-conditional-with-polymorphism *and its inverse*** (polymorphism→conditional when only one axis varies).
(c) Misses: the basin has a **one-way DRY/abstraction bias** — it can add structure but has almost no vocabulary for *removing wrong structure*. This lens makes de-abstraction first-class (Q9).

### L13. Historical analogy
(a) Large structural change on live systems is a *migration* problem with known playbooks, not a single edit `[D14, D4]`.
(b) seeds: • **strangler-fig** — grow new alongside old, redirect incrementally `[D2/D14]` • **branch-by-abstraction** — seam, then swap implementation behind it • **brownfield remediation order** — characterize/test before building `[D14, D1]` • **normalization-then-selective-denormalization** as a worked sequence `[D4]`.
(c) Misses: the basin is single-edit-under-green; it has no model for *behavior-preserving change across a live system over time* (dual-write, backfill, cutover). These are methodologies, not moves.

### L14. Failure analysis
(a) Work backward from incidents: **which structural properties actually preceded outages/bugs?** Let post-mortems, not linters, nominate refactor targets `[D10 FMEA]`.
(b) seeds: • **incident-archaeology refactor** — map past outages to the structures that enabled them, fix those first • **temporal-coupling & order-dependence hardening** `[D17 dynamic connascence]` (a top real-incident cause the basin can't see) • **non-atomic check-then-act / race removal** `[D17]` (concurrency blind spot) • **error-path structuring** — make swallowed/partial failures visible `[D10 error-budget]`.
(c) Misses: empirically, outages correlate more with concurrency, config, temporal coupling, and error-handling than with "long methods." The basin optimizes the *least* incident-predictive properties. (Med — based on practitioner postmortem patterns, not a clean dataset.)

### L15. (added) Operability / observability
(a) Structure that can't be observed at the right seam is unmaintainable regardless of tidiness; refactor to **create measurement points**.
(b) seeds: • **introduce an observability seam** (log/trace/metric at a boundary) — same seam mechanism, instrumentation purpose • **make state machine explicit so it can be asserted/traced** `[D3 SSA, D4]` • **separate decision from effect** so decisions are loggable/testable (Med).
(c) Misses: nothing in SOLID/smells references whether you can *see* the system run. Observability is a structural property the basin has no entry for.

---

## CROSS-LENS CONTRADICTIONS (gold for the audit turn)
These are genuine tensions, not errors — the taxonomy must *hold* them, not resolve them away:

1. **DRY (L12 add-abstraction) vs Defense-in-depth (L11) vs Evolution (L6).** Information-flow/design say "remove duplication"; safety says "redundant checks are defenses, keep them"; ecology says "the copies may be diverging species." → A "duplicate block" is not automatically a defect. **Audit: when is duplication load-bearing?**
2. **ISP "narrow interfaces" (L3 default) vs channel-capacity (L3/L2) "too-narrow leaks state."** Splitting can *increase* coupling via back-channels. → Width is a two-sided optimum, not "smaller is better."
3. **"Always fix violations" (basin) vs Economics "mostly don't" (L10) vs Medicine watchful-waiting (D9).** Direct contradiction with the default basin's telos. → Most smells are below the refactor-ROI threshold.
4. **Cognitive "small chunks" (L8) vs Formal/Safety "fewer seams = fewer places to break" (L7/L11).** More extraction = more readable but more indirection and more surface. → Extraction has a cost the basin never debits.
5. **Conway-alignment (L9) vs pure design modularity (L12).** The *technically* ideal boundary may cut across teams and be the wrong real boundary. → Socio-technical fit can override design "correctness."
6. **History-driven targeting (L4/L14) vs static-correctness (L7/L12).** A provably-improvable module that never changes may be lower priority than an "okay" module on fire. → Provable improvement ≠ worth doing.
7. **Premature-abstraction removal (L12/L6 prune) vs option-value of a seam (L10/L11).** One says "delete unused abstraction (YAGNI)"; the other says "the seam's value is the option it preserves." → Same unused seam, opposite verdicts depending on change-probability.

---

## WORKING STATE v4

```
=== WORKING STATE (refactoring-taxonomy) — v4 @ Turn 4/9 ===

[DEFINITIONS / BASIN PILLARS / BLIND SPOTS / ESCAPE TEST / DOMAIN DICTIONARY (17 dom,101 concepts)
 — all carried from v3. Escape test = "row whose trigger no Fowler move & no SOLID letter names?"]

LENS -> TOP SEEDS (NEW in v4; 15 lenses, non-isomorphic):
  L1 control-loops .... char-test-first; coverage-at-seam risk = blast÷coverage; mutation-test loop-quality
  L2 info-flow ........ dataflow min-cut seam; push-compute-to-data; collapse pass-through channel
  L3 boundaries ....... seam@min-cut; ACL@model-mismatch; narrow-OR-WIDEN interface; ctx-split-on-language
  L4 lifecycle/time ... churn×complexity hotspot map; change-coupling from co-commits; age stratification
  L5 adversarial ...... Hyrum implicit-contract audit; shotgun=attack-surface; Goodhart guard
  L6 ecology .......... vestigial-removal w/ reachability; converge-only-if-co-evolving; neutral-mutation staging
  L7 formal ........... observational-equivalence rung; invariant extraction; illegal-states-unrepresentable; partial->total
  L8 cognitive ........ chunk-to-WM(4±1); name-as-compression; least-astonishment audit; cut extraneous load
  L9 governance ....... Conway-alignment split/merge; orphaned-code flag; bus-factor hotspot
  L10 economics ....... debt-interest=carrycost×churn; seam option-value; YAGNI prune; amortization gate; WHERE-NOT-to-refactor
  L11 safety .......... blast-radius scoring; bulkhead/breaker; reversibility/inverse req; expand-contract
  L12 design .......... introduce-missing-abstraction; COLLAPSE-premature-abstraction; MDL stop-rule; polymorphism<->conditional both ways
  L13 history ......... strangler-fig; branch-by-abstraction; brownfield remediate-first; normalize-then-denormalize
  L14 failure-analysis  incident-archaeology; temporal-coupling hardening; race removal; error-path structuring
  L15 operability(add)  observability seam; explicit state machine; separate decision-from-effect

CROSS-LENS CONTRADICTIONS (KEEP, don't resolve — audit feedstock):
  C1 DRY vs defense-in-depth vs diverging-duplicates -> "when is duplication load-bearing?"
  C2 ISP narrow vs too-narrow-leaks -> interface width is a two-sided optimum
  C3 "always fix" vs economics-mostly-dont vs watchful-waiting -> most smells below ROI threshold
  C4 cognitive more-chunks vs fewer-seams-safer -> extraction has an un-debited cost
  C5 Conway-fit vs design-modularity -> socio-technical can override "correct" boundary
  C6 history-driven priority vs static-correctness -> provable-improvable != worth-doing
  C7 YAGNI-prune-seam vs option-value-of-seam -> identical seam, opposite verdict by change-prob

STRENGTHENED CANDIDATE PRIMITIVES (from echoes + lenses; still NOT locked):
  - SEAM / substitution-point (L1,L2,L3,L11,L15 all invoke it) -> mechanism #1, strongest
  - MAKE-IMPLICIT-EXPLICIT (L7 invariants, L15 state machine, D3 SSA) -> mechanism #2
  - RELOCATE-BY-CHANGE-AFFINITY (L2,L4,L6,L9 + connascence) -> mechanism #3 (move things that change together together)
  - RIGHT-SIZE-ABSTRACTION bidirectional (L12,L10,L6) -> direction axis, resolves Q9
  - TWO new cross-cutting ENTRY FIELDS implied: (i) history/churn signal, (ii) where-NOT-to-apply

OPEN QUESTIONS (carried + new):
  Q1 mechanism-vs-trigger primary | Q10 connascence-as-primary? -> now leaning: mechanism PRIMARY,
     connascence supplies the TRIGGER vocabulary (they compose, not compete) [provisional resolution, confirm T8]
  Q14 NEW: should "where NOT to refactor" be a first-class output column? (lean yes; C3/C7 demand it)
  Q15 NEW: history/churn is a per-codebase signal, not intrinsic to a technique — store as
     "applicability signal" not as the technique's identity? (lean yes)
  [carried: Q2 adjacency-tag, Q3 refactor-vs-rearchitect line, Q4 cross-paradigm unity,
   Q5 atomic+composite tiers, Q6 preservation rep, Q7 schema, Q8 blind-spot triggers,
   Q9 -> resolving via bidirectional direction axis, Q11 preservation-ladder yes,
   Q12 audit 12 Low transfers, Q13 MDL stop-rule]

ENTRY SCHEMA (v4 — +churn/applicability-signal, +where-NOT, per Q14/Q15):
  name | aliases | mechanism | trigger(s) [smell|metric|change-pain|violation|connascence-type] |
  preconditions/contraindications | before->after | preservation-arg (LADDER) | blast-radius |
  paradigm-applicability | direction(add/remove abstraction) | inverse-technique |
  applicability-signal (churn/coverage/ownership) | where-NOT-to-apply | related(same-mechanism) |
  source-domain/lens | impact(conditional) | confidence

TODO turns 5-9:
  T5  Lock primary axis; build SKELETON (mechanism-family spine spanning blind-spot altitudes);
      add methodologies tier (strangler/branch-by-abstraction/expand-contract)
  T6  Populate entries from dictionary+lens seeds; triggers & detection incl. graph/churn/connascence;
      falsifiability pass
  T7  Blast-radius + preservation-LADDER + inverse-technique + where-NOT completeness
  T8  AUDIT: resolve C1-C7 (hold as tensions w/ guidance); test 12 Low transfers; high-impact-WHEN layer;
      de-overlap; gap hunt vs blind spots; confirm Q1/Q10
  T9  Final synthesis; success-criteria + escape-test self-audit; epistemic honesty pass

CONFIDENCE LEDGER: [carried] + L14 incident-correlation Med (postmortem patterns, not clean data);
  L5 Hyrum/Goodhart detection Med (hard to operationalize); seeds tagged Low inline (mutation-loop,
  exaptation, astonishment-measurement, MI-quantification).
=== END WORKING STATE ===
```

Turn 4 ran all 15 lenses non-isomorphically, surfaced ~50 technique-seeds (cross-referenced to the dictionary), and — most valuably — extracted **7 genuine cross-lens contradictions** to carry into the audit. No synthesis or final structure built. I added an operability lens (L15) and gave a *provisional* resolution to Q1/Q10 (mechanism = primary spine, connascence = trigger vocabulary; they compose). Ready for Turn 5 (lock the axis, build the skeleton) on your go.



---

# Refactoring Taxonomy — TURN 5/9: THE TRANSPOSITION ENGINE

Goal: mechanism-preserving transfer, not metaphor. No taxonomy/ranking yet; records kept atomic for the audit to attack individually.

---

## PART A — TRANSPOSITION PROTOCOL

A borrowed concept becomes a **candidate technique** only if it carries over ALL SIX:

1. **Causal mechanism** — the structural thing that does the work, stated without the source-domain noun. (Test: can you describe it to someone who's never heard the metaphor?)
2. **Detection signal** — an *observable* code/process condition that fires the trigger. (Must be checkable; if it needs human judgment, say so and downgrade.)
3. **Boundary conditions** — when it applies AND explicit contraindications (when applying it is wrong).
4. **Intervention logic** — concrete before→after transformation steps on real code.
5. **Behavior-preservation story** — how observable behavior stays intact, at which preservation-ladder rung (char-test / test / type-proof / observational-equivalence).
6. **Failure / over-application mode** — what breaks if mis-applied or over-applied.

**REJECTION RULE.** If a transfer carries the *word* but not the *mechanism* — i.e., it cannot fill fields 1+4 with something a programmer could execute, or its "detection" is pure vibes — tag it **REJECTED — SHALLOW** and state which field collapsed. A concept that's evocative but yields no executable transformation is framing, not technique.

**Rejection examples (to calibrate the bar):**
- **"Broken Windows" → "fix small smells to prevent decay."** REJECTED — SHALLOW. Field 1 (causal mechanism) is social-psychological, not structural; field 4 has no specific transformation — it's "be tidy." Survives only as motivation. (Keep as *cultural rationale*, not a technique.)
- **"Entropy always increases → code rots."** REJECTED — SHALLOW. True as framing (D6), but no detection signal and no transformation. It's a *why-refactor* argument, not a *what-to-do*.
- **"Channel capacity → right-size interfaces."** REJECTED — SHALLOW *as stated* (the info-theoretic quantity isn't measurable on code), BUT the underlying move survives independently as **Rebalance Interface Width** (record T07) sourced from ISP+leakage, not from Shannon. → the metaphor is dropped; the mechanism is kept under different provenance.
- **"Exaptation → repurpose code."** REJECTED — SHALLOW. Collapses into ordinary *Generalize* (T11); the evolutionary noun adds nothing executable.

This is the engine's value: it *kills* the analogies that would have made the taxonomy a metaphor zoo, and *re-sources* the survivors to their real mechanism.

---

## PART B — CANDIDATE TECHNIQUE RECORDS

Format: **[id] Name** · provenance · confidence — then the six fields, compact.

---

**[T01] Introduce Substitution Seam** · CANONICAL (Feathers) + BORROWED (graph min-cut) · **High**
1. *Mechanism:* insert an indirection point (interface/param/wrapper) so a dependency can be replaced without editing its callers.
2. *Signal:* `new Concrete()` or static/global access *inside* logic you need to test or vary; high fan-in node `[D5]`; the thing you can't substitute in a test.
3. *Boundary:* apply when you must test, vary, or isolate a dependency. **Contra:** stable leaf dependency that never varies and is trivially testable (adds indirection for nothing → C4).
4. *Intervention:* extract an interface / parameterize the constructor / wrap; route callers through the seam; inject the concrete at the edge.
5. *Preservation:* type-proof rung — extract-interface + inject is compiler-checkable; default wiring keeps behavior. 
6. *Failure:* seam proliferation (an interface per class); indirection tax with no substitution ever used → becomes a premature abstraction (T12 inverse).

**[T02] Place Cut at the Dependency Min-Cut** · BORROWED (graph science D5) · **Med**
1. *Mechanism:* split a module along the edge-set carrying the *least* cross-flow, so the new boundary severs the fewest dependencies.
2. *Signal:* build a symbol/dependency graph; find a near-articulation region where a small edge cut separates two dense clusters `[D5 modularity/min-cut]`.
3. *Boundary:* applies to genuinely two-cluster modules. **Contra:** densely-connected blob with no thin cut (forcing a cut just relocates coupling — C2).
4. *Intervention:* identify clusters (community detection / co-change), move each cluster's members into its own unit, convert the cut edges into an explicit interface.
5. *Preservation:* move-only edits (no logic change) → tests + type-proof.
6. *Failure:* cutting where the graph is dense → back-channel coupling, more interfaces, worse than before.
*FLAG: detection needs tooling (dependency-graph extraction); without it, "min-cut" is unmeasurable by eye.*

**[T03] Relocate by Change-Affinity** · BORROWED (connascence D17 + co-evolution D13) · **Med-High**
1. *Mechanism:* move elements that must change together into proximity; separate elements that change independently — minimize "distance × strength" of connascence.
2. *Signal:* co-commit coupling from git history (files always edited together but in different modules) `[L4]`; shotgun-surgery (one logical change → N sites).
3. *Boundary:* apply when change-affinity is stable over history. **Contra:** spurious co-change (two files edited together by coincidence/formatting); affinity that's about to diverge `[D13 divergent]`.
4. *Intervention:* co-locate the change-coupled members (move method/field/module); or introduce a single point that all the sites delegate to.
5. *Preservation:* move-only → tests; behavior unchanged.
6. *Failure:* merging things that look co-changing but are diverging species → creates the inverse smell (forced shared abstraction).
*Provenance note: this is the mechanism behind Move Method/Field, re-grounded in change-affinity rather than "feature envy by sight."*

**[T04] Make Implicit State Explicit (Reify State Machine)** · BORROWED (D3 SSA, D4) + CANONICAL-adjacent · **High**
1. *Mechanism:* replace flag-combinations / implied call-order with an explicit, named state representation so invalid states/transitions are visible.
2. *Signal:* multiple booleans whose *combinations* encode states; methods that only work "after init"; comments like "must call X first" `[L15, temporal-coupling blind spot]`.
3. *Boundary:* applies where an implicit state machine exists. **Contra:** genuinely stateless code (don't manufacture a state machine).
4. *Intervention:* enumerate the real states; introduce a state type/enum or state objects; make transitions explicit functions; reject illegal transitions.
5. *Preservation:* characterize current behavior first (legacy), then refactor under char-tests; ideally reach type-proof (illegal states unrepresentable, T13).
6. *Failure:* over-modeling — a state machine for two states adds ceremony; can ossify behavior that was deliberately loose.

**[T05] Convert Temporal Coupling to Structural Coupling** · BORROWED (D17 dynamic connascence) · **Med-High**
1. *Mechanism:* eliminate "must-call-in-this-order" by making the dependency explicit in the type/signature so the order is enforced or made impossible to get wrong.
2. *Signal:* connascence of execution-order; APIs where calling B before A silently corrupts; partially-constructed objects.
3. *Boundary:* apply to order-dependent APIs with real corruption risk. **Contra:** order that's genuinely free (don't constrain it).
4. *Intervention:* builder/typestate pattern; return the next-stage type from the previous call (you can't call B without A's output); or fold the sequence into one atomic operation.
5. *Preservation:* type-proof where typestate is available; else tests on the legal sequences.
6. *Failure:* rigid typestate that blocks legitimate alternate orderings; over-constraining a flexible API.

**[T06] Replace Conditional-on-Type with Dispatch — AND its Inverse** · CANONICAL (Fowler/GoF) + INVENTED (the inverse pairing) · **High**
1. *Mechanism:* forward: move per-type branches into polymorphic units so adding a type is local. Inverse: collapse polymorphism back to a conditional when only ONE call-site varies and the hierarchy adds navigation cost.
2. *Signal:* forward — switch/if-else on a type/enum growing one arm per feature `[O-violation]`. Inverse — a class hierarchy with a single overridden method used in one place (speculative polymorphism).
3. *Boundary:* forward when the *type axis* genuinely varies across many sites. **Contra (forward):** only one site switches → dispatch adds indirection (use inverse).
4. *Intervention:* forward — introduce interface, one implementor per arm, replace switch with virtual call. Inverse — inline the overrides into a conditional, delete the hierarchy.
5. *Preservation:* tests covering each arm/type; move-only logic.
6. *Failure:* forward over-application → polymorphism for a stable 2-arm switch (harder to read than the switch — C4); the inverse exists precisely to undo this.

**[T07] Rebalance Interface Width** · APPROXIMATION (ISP + leakage; Shannon dropped per rejection rule) · **Med**
1. *Mechanism:* match an interface's exposed surface to what callers actually use — *narrow* fat interfaces, *widen* interfaces so leaky back-channels (globals, casts, side outputs) become explicit.
2. *Signal:* narrow-direction: implementors stub unused members `[I-violation]`; widen-direction: callers reach around the interface via casts/globals/`instanceof` to get what it won't give them.
3. *Boundary:* apply per actual usage. **Contra:** speculative role-splitting with one implementor (premature, T12).
4. *Intervention:* segregate into role interfaces by caller cluster; OR promote a hidden back-channel into a first-class method/return.
5. *Preservation:* type-proof (interface change is compiler-checked); behavior intact.
6. *Failure:* over-segregation → interface explosion; the *widen* move can leak implementation if done carelessly.
*FLAG: "what callers actually use" needs call-site analysis to be non-vague.*

**[T08] Strangler-Fig Migration** · CANONICAL/BORROWED (D14/D13) · **High** · METHODOLOGY (composite)
1. *Mechanism:* grow the replacement alongside the original behind a routing seam, redirect call paths incrementally, retire the old once traffic is fully shifted.
2. *Signal:* a large unit you must replace but can't stop/rewrite atomically; change-pain concentrated in a legacy core `[L13]`.
3. *Boundary:* apply when a seam can intercept the call path. **Contra:** no interceptable boundary; change small enough for a direct refactor.
4. *Intervention:* introduce facade/router → implement slice of new behind it → route a fraction → verify parity → expand → delete old.
5. *Preservation:* parity tests at the router (old vs new) — observational-equivalence per slice; live behavior preserved throughout.
6. *Failure:* the strangler stalls half-done (two systems forever); router becomes permanent complexity.

**[T09] Expand/Contract (Parallel Change)** · CANONICAL/BORROWED (D4 schema migration) · **High** · METHODOLOGY
1. *Mechanism:* add the new shape, support old+new simultaneously, migrate consumers/data, then remove the old — never a breaking flip.
2. *Signal:* a structural change to a widely-used API/schema/format with many consumers you can't change in lockstep `[Hyrum's Law, L5]`.
3. *Boundary:* apply when consumers migrate asynchronously. **Contra:** single in-repo consumer (just change both atomically).
4. *Intervention:* add new field/method/table → dual-write/dual-read → backfill → migrate consumers → contract (delete old).
5. *Preservation:* during expand phase both contracts hold → equivalence; old consumers unaffected.
6. *Failure:* never completing contract (permanent dual surface); dual-write skew if not atomic.

**[T10] Branch by Abstraction** · CANONICAL · **High** · METHODOLOGY
1. *Mechanism:* introduce an abstraction over the thing to be replaced, build the new implementation behind it, switch, then remove the abstraction if no longer needed.
2. *Signal:* need to swap a large implementation while keeping mainline releasable `[L13]`.
3. *Boundary:* when the swap is too big for one commit. **Contra:** swap is small/atomic (abstraction is overhead).
4. *Intervention:* extract abstraction over current impl → new impl behind same abstraction → flip flag → delete old → optionally inline abstraction.
5. *Preservation:* both impls satisfy the same abstraction's tests → equivalence at the seam.
6. *Failure:* the temporary abstraction becomes permanent cruft; flag debt.

**[T11] Extract Missing Abstraction** · CANONICAL (Kerievsky) · **High**
1. *Mechanism:* name a recurring concept that's currently spread across duplicated/primitive code, giving it one definition (chunking `[D12]`).
2. *Signal:* duplicated blocks that are *truly* identical in intent; primitive obsession (a domain concept passed as raw string/int) `[L8 name-as-compression]`.
3. *Boundary:* apply when copies share intent AND co-evolve `[C1, T03]`. **Contra:** copies that merely look similar but diverge (C1 load-bearing duplication).
4. *Intervention:* introduce a type/function for the concept; replace each site with a call/use; centralize the definition.
5. *Preservation:* tests; type-proof if the concept becomes a type.
6. *Failure:* the canonical mistake — abstracting coincidental similarity → tight coupling of things that should vary independently (→ T12 to undo).

**[T12] Collapse Premature/Wrong Abstraction (De-abstract)** · INVENTED (under-served inverse; D11 sunk-cost/YAGNI, D1 Kerievsky-bidirectional) · **Med-High**
1. *Mechanism:* remove an abstraction whose variation never materialized, inlining it back to concrete code so the real shape is visible again.
2. *Signal:* an interface/base class/generic with exactly one implementor/instantiation; config flags never flipped; "framework" used once `[L10 YAGNI, speculative generality]`.
3. *Boundary:* apply when the abstraction has ~1 realized use and low future-variation probability. **Contra:** the abstraction is a *real option* on likely future change `[C7, D11 real-options]` — then keep it.
4. *Intervention:* inline the single implementor; delete the interface/indirection; collapse generics to concrete types.
5. *Preservation:* inline is mechanizable (lean-on-compiler) → type-proof.
6. *Failure:* deleting a seam you'll need next quarter (C7); judgment call on future variation — inherently probabilistic.
*This record exists specifically to break the basin's one-way DRY bias (Q9).*

**[T13] Make Illegal States Unrepresentable** · BORROWED (D16 type theory, D8 poka-yoke) · **High** (typed langs) / **Low** (dynamic langs)
1. *Mechanism:* encode invariants in the type/data model so invalid combinations cannot be constructed — push enforcement from runtime checks to compile-time.
2. *Signal:* repeated runtime validation of the same invariant scattered across call-sites; nullable fields that are "always set after X"; flag combos that should be exclusive.
3. *Boundary:* applies in languages with sum types / strong typing. **Contra:** dynamically-typed languages (degrades to runtime assertions, not a proof — downgrade confidence); invariants that genuinely vary at runtime.
4. *Intervention:* replace primitive+validation with a constrained type / sum type / smart constructor; remove now-impossible branches `[T16 totality]`.
5. *Preservation:* type-proof — strongest rung; the compiler enforces equivalence of legal states.
6. *Failure:* over-modeling rare cases into the type system → unwieldy types; in dynamic langs it's just ceremony.

**[T14] Introduce Characterization Test (Preservation Scaffold)** · CANONICAL (Feathers) · **High** · ENABLER
1. *Mechanism:* pin the *current* observable behavior (even if "wrong") with tests so any subsequent transform's regressions are detectable — manufactures the missing control loop `[L1]`.
2. *Signal:* code you must change that has no/low test coverage at the affected lines `[L1: blast÷coverage risk]`.
3. *Boundary:* always, before refactoring untested legacy. **Contra:** behavior is already specified by strong tests (redundant).
4. *Intervention:* capture inputs→outputs of the current code (golden master / approval tests); lock them; then refactor.
5. *Preservation:* this *is* the preservation mechanism for everything else; meta-technique.
6. *Failure:* characterizing buggy behavior and then treating it as sacred; brittle golden masters that pin incidental output.

**[T15] Insert Observability Seam** · INVENTED (L15) + BORROWED (D8 jidoka) · **Med**
1. *Mechanism:* introduce a boundary where state/decisions can be logged/traced/metered, separating *decision* from *effect* so behavior is inspectable.
2. *Signal:* failures with no signal; logic where you "can't tell what it decided"; effects entangled with the decision that produced them `[L15]`.
3. *Boundary:* apply at operationally-important boundaries. **Contra:** hot path where instrumentation cost matters; trivial code.
4. *Intervention:* extract the decision into a pure function returning a described result; emit at the seam; apply the effect separately.
5. *Preservation:* extract-function refactor → tests; output behavior unchanged, observability added.
6. *Failure:* log spam; turning every call into an event; perf overhead in hot loops.
*FLAG: "operationally important" is judgment-based — needs an explicit criterion to be non-vague.*

**[T16] Partial → Total (Eliminate Unreachable/Throw Arms)** · BORROWED (D16 totality, D3 DCE) · **Med-High**
1. *Mechanism:* make a function defined over all inputs — remove `NotImplemented`/throw branches by either handling the case or proving (via types) it can't occur.
2. *Signal:* overrides throwing "not implemented" `[L-violation]`; default branches that "can't happen"; partial functions guarded by upstream checks.
3. *Boundary:* apply where the impossible cases are genuinely impossible (provable) or genuinely handleable. **Contra:** the throw is a real, reachable precondition guard (keep it, make it explicit instead).
4. *Intervention:* tighten the input type so the bad case can't be passed (→T13); or implement the missing case; remove dead arms with a reachability argument `[D3 DCE]`.
5. *Preservation:* if cases are truly unreachable, removal is behavior-preserving by reachability proof; else type-proof via narrowed input.
6. *Failure:* removing a "can't happen" branch that *can* happen → latent crash; over-trusting the type narrowing.

**[T17] Bulkhead / Isolate Fault Domain** · BORROWED (D10 safety eng) · **Med** · (distributed/concurrent altitude)
1. *Mechanism:* introduce an isolation boundary (separate pool/queue/process/circuit) so a failure or resource exhaustion in one region can't propagate to others.
2. *Signal:* shared resource (thread pool, connection, mutable cache) whose exhaustion takes down unrelated features; one slow dependency stalling everything `[distributed blind spot]`.
3. *Boundary:* apply at real fault-propagation boundaries. **Contra:** single-tenant simple code where isolation adds ops complexity for no benefit.
4. *Intervention:* partition the shared resource; add circuit-breaker/timeout/bulkhead at the dependency edge.
5. *Preservation:* **NOT purely behavior-preserving under failure** — it *changes* failure behavior by design. Under the happy path it's preserving; flag as *behavior-preserving in success, behavior-changing in failure* → adjacency-tagged (Q2).
6. *Failure:* hides a dependency that should've been fixed; added latency; mis-tuned breakers cause false trips.
*Note: this record tests the boundary of "refactoring" — it's an honest edge case for the audit.*

---

## WORKING STATE v5

```
=== WORKING STATE (refactoring-taxonomy) — v5 @ Turn 5/9 ===

[CARRIED: definitions, basin pillars, blind spots, escape test, 17-domain dictionary,
 15 lenses, 7 cross-lens contradictions C1-C7, entry schema v4]

TRANSPOSITION PROTOCOL (NEW, locked): 6 mandatory fields (mechanism / signal / boundary+contra /
 intervention / preservation-story@ladder / failure-mode). REJECTION RULE: carries word not
 mechanism (can't fill 1+4, or signal=vibes) -> REJECTED-SHALLOW.
 Rejected so far: broken-windows, entropy, channel-capacity(re-sourced as T07), exaptation(->T11).

CANDIDATE TECHNIQUE INVENTORY (NEW; name -> provenance -> confidence -> flag):
  T01 Introduce Substitution Seam ............... CANONICAL+BORROWED .. High
  T02 Place Cut at Dependency Min-Cut ........... BORROWED ............ Med   [needs graph tooling]
  T03 Relocate by Change-Affinity .............. BORROWED ............ Med-High
  T04 Make Implicit State Explicit ............. BORROWED+canon-adj .. High
  T05 Temporal->Structural Coupling ............ BORROWED ............ Med-High
  T06 Conditional<->Dispatch (both directions) . CANONICAL+INVENTED .. High
  T07 Rebalance Interface Width ................ APPROXIMATION ....... Med   [needs call-site analysis]
  T08 Strangler-Fig Migration .................. CANONICAL/BORROWED .. High  [METHODOLOGY]
  T09 Expand/Contract (Parallel Change) ........ CANONICAL/BORROWED .. High  [METHODOLOGY]
  T10 Branch by Abstraction .................... CANONICAL ........... High  [METHODOLOGY]
  T11 Extract Missing Abstraction .............. CANONICAL ........... High
  T12 Collapse Premature Abstraction (de-abs) .. INVENTED ............ Med-High [breaks DRY one-way bias]
  T13 Make Illegal States Unrepresentable ...... BORROWED ........ High(typed)/Low(dynamic)
  T14 Introduce Characterization Test .......... CANONICAL ........... High  [ENABLER/meta]
  T15 Insert Observability Seam ................ INVENTED+BORROWED ... Med   [vague: "operationally important"]
  T16 Partial -> Total ......................... BORROWED ............ Med-High
  T17 Bulkhead / Isolate Fault Domain .......... BORROWED ............ Med   [adjacency: preserves success, changes failure]
  ---- 17 records. Provenance mix: CANONICAL 7, BORROWED 6, APPROX 1, INVENTED 3 (some hybrid).
  ---- VAGUE-SIGNAL FLAGS to fix in T6: T02, T07, T15. ADJACENCY (Q2) edge case: T17.

MECHANISM CLUSTERS VISIBLE (NOT yet locked into taxonomy — for T6 skeleton):
  - SEAM-family: T01, T08, T10, T15, T17 (all "introduce substitution/isolation point")
  - MAKE-EXPLICIT-family: T04, T05, T13, T16 (push implicit->explicit/compile-time)
  - RELOCATE-family: T02, T03 (move by flow/affinity)
  - ABSTRACTION-SIZING (bidirectional): T06, T07, T11, T12 (add OR remove structure)
  - MIGRATION methodologies: T08, T09, T10 (behavior-preserving change over live system/time)
  - ENABLER: T14 (preservation scaffold underpinning all)
  NOTE: this clustering is the EMBRYO of the mechanism-spine; confirm/lock in T6.

ESCAPE-TEST CHECK (interim): T03(change-affinity), T04/T05(temporal), T15(observability),
  T17(fault-domain) have triggers NO Fowler move & NO SOLID letter names. -> on track to escape basin.

OPEN QUESTIONS:
  Q1/Q10 -> provisional: mechanism = spine, connascence = trigger vocab (confirm T8)
  Q9 -> resolved-in-practice: T12 + T06-inverse + T07-widen give the de-abstraction direction
  Q2 -> T17 is the live test case for the adjacency tag
  Q14 where-NOT column -> validated useful (every record has a contraindication) -> KEEP
  Q15 churn/applicability as signal not identity -> validated (T03 detection IS history) -> KEEP
  Q16 NEW: T08/T09/T10 are methodologies, not atomic techniques -> confirm SEPARATE TIER (Q5, lean yes)
  Q17 NEW: T17 preservation is success-only -> do we admit "failure-behavior-changing" refactors
       as a tagged subclass, or exclude? (decide T8)
  [carried: Q3 refactor-vs-rearchitect line, Q4 cross-paradigm unity, Q6 preservation rep,
   Q11 ladder yes, Q12 audit Low transfers, Q13 MDL stop-rule]

TODO turns 6-9:
  T6  Lock mechanism-spine from the 6 clusters; build SKELETON; fix vague signals (T02/T07/T15);
      add 8-15 MORE records to fill blind-spot/paradigm gaps (concurrency, SQL, FP); falsifiability pass
  T7  Blast-radius + preservation-LADDER + inverse-technique + where-NOT completeness across all records
  T8  AUDIT: attack each record; resolve C1-C7 as held tensions; test 12 Low transfers; Q17/Q2 ruling;
      high-impact-WHEN layer; de-overlap; gap hunt
  T9  Final synthesis; success-criteria + escape-test self-audit; epistemic honesty pass

CONFIDENCE LEDGER: per-record above; cross-paradigm unity still Med (T13 splits typed/dynamic =
  evidence it's per-case, Q4); methodology/atomic tiering pending Q16.
=== END WORKING STATE ===
```

Turn 5 built the transposition engine (6-field protocol + rejection rule) and ran 17 atomic candidate records through it — provenance mix CANONICAL 7 / BORROWED 6 / APPROX 1 / INVENTED 3. The rejection rule did real work (killed broken-windows, entropy, channel-capacity-as-Shannon, exaptation; re-sourced the survivors). Flagged 3 vague-signal records (T02/T07/T15) and 1 adjacency edge case (T17) for the audit. No taxonomy or ranking yet; clusters noted only as the skeleton embryo. Ready for Turn 6 (lock the mechanism-spine, build the skeleton, fill paradigm/blind-spot gaps).



---

# Refactoring Taxonomy — TURN 6/9: CANDIDATE STRUCTURES

Choosing *how to organize* the 17-record inventory — not writing the taxonomy. 6 candidates, each scored on the six criteria (H/M/L). Comparison table + provisional recommendation at the end. No commitment — Turn 8 decides post-audit.

---

## S1 — By Smell / Violation Type (the default-basin axis)
**Shape:** top-level = SOLID letters + the smell checklist; techniques hang under the smell they cure.
- **Explanatory power: Low.** Sorts *symptoms*, not mechanisms; "Long Method" and "Large Class" are the same mechanism (extract/chunk) but live in different buckets. Hides that T01/T08/T10 are one mechanism.
- **Practical usefulness: High (familiar) but misleading.** A dev recognizes the smell name fast — then gets a fix that may be wrong (C3: most smells below ROI). Fast access, poor selection.
- **Extensibility: Low.** New blind-spot techniques (T03 change-affinity, T05 temporal, T17 bulkhead) have no smell to file under; you'd invent fake smells.
- **Blind spots: inherits ALL of Turn-2's.** Concurrency/data/temporal/distributed have no row.
- **Distortion risk: High.** Forces false neighbors (everything keyed on a type lands in "OCP" regardless of mechanism).
- **Cross-discipline coverage: collapses back into the basin** — by construction. This is the failure mode we named in Turn 2.
*Verdict: keep ONLY as an alias/index layer ("if you saw this smell, here's the entry"), never as the spine.*

## S2 — By Mechanism Repaired
**Shape:** top-level = the 6 mechanism families from T5 (seam/substitution, make-implicit-explicit, relocate-by-affinity, abstraction-sizing-bidirectional, migration-over-time, preservation-enabler) + coupling/cohesion/control-flow/data-model as sub-mechanisms.
- **Explanatory power: High.** This *is* the grounding unit (the contract's whole thesis). Reveals that T01/T08/T10/T15/T17 share "introduce substitution/isolation point"; that Fowler+SOLID are instances, not categories. Passes the escape test natively.
- **Practical usefulness: Med.** Excellent once you know the mechanism — but at the moment of need a dev usually starts from a *symptom/trigger*, not a mechanism. Needs a trigger→mechanism access layer bolted on.
- **Extensibility: High.** New domains slot in by mechanism; a new concurrency technique joins "isolate fault domain" without restructuring.
- **Blind spots: low** — but risks under-weighting *risk* and *cost* (a mechanism axis says nothing about blast radius or ROI; C3/C7 invisible here).
- **Distortion risk: Med.** Some techniques use two mechanisms (T13 = make-explicit + abstraction-sizing); needs a primary-mechanism rule or multi-home.
- **Cross-discipline coverage: High.** Built directly from the dictionary echoes.
*Verdict: strongest spine candidate; weak as a stand-alone access path.*

## S3 — By Scope / Altitude (statement → method → class → module → service → system)
**Shape:** vertical ladder by the size of the thing transformed.
- **Explanatory power: Med.** Reveals blast-radius-by-proxy and that the *same* mechanism recurs up the ladder (extract fn → extract module → strangler) — a genuinely useful insight. But altitude ≠ mechanism; it sorts by size, not by what's repaired.
- **Practical usefulness: High.** Devs *do* think "I'm working at the method/module/service level"; fast filter.
- **Extensibility: High.** New altitudes (data layer, network) append cleanly; this axis naturally hosts the distributed/data blind spots as new rungs.
- **Blind spots: hides driving-force and risk.** Two techniques at "module" level can have wildly different risk/intent.
- **Distortion risk: Med.** Cross-altitude methodologies (strangler spans method→system) don't have a home; temporal techniques aren't a "size."
- **Cross-discipline coverage: Med-High.** Hosts blind-spot altitudes well, but doesn't *explain* them.
*Verdict: excellent as a FACET, not the spine — it answers "where am I" not "what does this fix."*

## S4 — By Blast Radius / Risk Tier (local-reversible → wide-irreversible)
**Shape:** tiers — T0 local+reversible (rename, extract fn) → T1 module → T2 cross-module → T3 cross-boundary/live-data/irreversible (strangler, expand-contract, bulkhead).
- **Explanatory power: Med.** Reveals the *cost/safety* dimension the basin entirely ignores (C4: extraction has an un-debited cost) — real value. But says nothing about mechanism.
- **Practical usefulness: High for SEQUENCING.** Answers "what's safe to do now vs what needs a plan." Maps to the preservation-ladder (T14).
- **Extensibility: High.** Any technique gets a tier; new techniques self-classify.
- **Blind spots: hides mechanism and trigger** entirely — you can't *find* a technique here, only gauge its danger.
- **Distortion risk: Low** (risk tier is a property, rarely forces false neighbors) — but coarse.
- **Cross-discipline coverage: Med.** Safety/economics domains shine here; design/cognitive don't map.
*Verdict: strong FACET (risk/sequencing overlay), not a spine — it's a property of every entry, computed not categorical.*

## S5 — By Driving Force / "Why" (comprehension, changeability, testability, performance, reliability, ownership, operability)
**Shape:** top-level = the goal the refactor serves; techniques grouped by intent.
- **Explanatory power: Med-High.** Connects technique → *value*, which the basin never makes explicit (it assumes "cleaner = better"). Surfaces the blind-spot goals (reliability, operability, ownership) as first-class — good escape pressure.
- **Practical usefulness: High.** Devs often start from a goal ("I need this testable," "this keeps causing incidents"). Natural entry path.
- **Extensibility: High.** New goals append; cross-discipline material maps well (safety→reliability, Conway→ownership, cognitive→comprehension).
- **Blind spots: one technique serves many goals** (T01 seam → testability AND changeability AND reliability) → heavy multi-homing; the axis blurs.
- **Distortion risk: Med-High.** Goal attribution is partly subjective; risks becoming a motivational rather than mechanical sort.
- **Cross-discipline coverage: High.** Best axis for *connecting* the lenses to outcomes.
*Verdict: excellent ACCESS PATH / facet, poor spine — too many-to-many to be the primary key.*

## S6 — FACETED: Mechanism-Spine × Overlays (INVENTED synthesis) — *the integrator*
**Shape:** ONE primary key + several orthogonal facets + two access layers.
- **Primary spine = Mechanism repaired (S2).** Each technique lives in exactly one mechanism family (primary-mechanism rule; multi-mechanism noted as `related`).
- **Facets (every entry tagged on each, queryable):** altitude (S3) · blast-radius/risk-tier (S4) · driving-force (S5) · paradigm (OO/FP/proc/SQL/concurrent) · direction (add/remove abstraction) · trigger-vocabulary (smell|metric|change-pain|violation|connascence-type) · preservation-rung.
- **Access layer 1 — Trigger index (subsumes S1):** "saw smell X / metric Y / incident Z" → routes to mechanism entries. The basin's smell list becomes a *lookup table into* the taxonomy, not its structure.
- **Access layer 2 — Workflow phase (the detect→make-safe→transform→verify→measure loop):** not a category but a *protocol* the entry's fields populate (T14 = make-safe phase enabler; preservation-story = verify; applicability-signal = measure).
- **Explanatory power: High** (inherits S2's mechanism grounding).
- **Practical usefulness: High** (trigger/goal/altitude access paths fix S2's only weakness — you can enter from symptom, goal, OR location and arrive at mechanism).
- **Extensibility: High** (new technique = one mechanism home + facet tags; new facet = new column, no restructure).
- **Blind spots: low** — facets explicitly carry the risk/goal/altitude dimensions that single-axis schemes hide. Holds the contradictions C1–C7 because they live on *different facets* (e.g., C7 = same mechanism, opposite verdict by applicability-signal).
- **Distortion risk: Low-Med** — primary-mechanism rule prevents force-fit; multi-aspect techniques use facets instead of duplicate entries.
- **Cross-discipline coverage: High** — every dictionary domain maps to a facet or the spine.
- *Cost:* more complex to build/present than a single list; needs discipline to keep facets orthogonal (the main risk).
*Verdict: highest overall; it doesn't pick one axis — it makes mechanism the spine and demotes every other proposed axis (S1,S3,S4,S5 + workflow) to facet/access-layer, where each is individually strong.*

---

## COMPARISON TABLE
(H=High, M=Med, L=Low; for Distortion/Blind-spots, L is *better*)

| Scheme | Explanatory | Usefulness | Extensibility | Blind-spots(↓better) | Distortion(↓better) | X-disc Coverage | Net |
|---|---|---|---|---|---|---|---|
| S1 Smell/violation | L | H* | L | H (worst) | H | **collapses to basin** | ✗ Low |
| S2 Mechanism | **H** | M | H | L | M | **H** | ◎ High |
| S3 Altitude | M | H | H | M | M | M-H | ○ Med |
| S4 Risk tier | M | H(seq) | H | M | **L** | M | ○ Med |
| S5 Driving-force | M-H | H | H | M(many-to-many) | M-H | **H** | ○ Med-High |
| **S6 Faceted (mech×overlays)** | **H** | **H** | **H** | **L** | L-M | **H** | **★ Highest** |

\*S1's usefulness is fast-but-misleading: quick to *find*, poor to *select*.

---

## PROVISIONAL RECOMMENDATION (confidence: Med-High)
**Adopt S6 — mechanism-spine with faceted overlays and two access layers.** Rationale:
- It operationalizes the contract: **mechanism is the grounding unit** (S2 spine), so it passes the escape test by construction.
- It *uses* rather than discards the other axes — S3/S4/S5 become facets where each scores well, and the basin's smell list (S1) becomes a trigger *index into* the taxonomy rather than its skeleton (this is the precise inversion that escapes Turn 2's gravity well).
- It's the only candidate that can **hold contradictions C1–C7** without resolving them, because conflicting verdicts live on different facets (mechanism same, applicability-signal/risk differ).

**Why not commit now:** two unresolved risks for the audit (Turn 8): (i) the **primary-mechanism rule** must be tested against multi-mechanism records (T13) — does single-homing distort? (ii) facet **orthogonality** must be verified (do altitude and blast-radius collapse into each other? partially correlated — C-flag). Also Q16 (methodology tier) interacts: T08/T09/T10 may need a parallel "methodology" register rather than sitting on the atomic spine.

---

## WORKING STATE v6

```
=== WORKING STATE (refactoring-taxonomy) — v6 @ Turn 6/9 ===

[CARRIED: definitions, basin pillars + blind spots, escape test, 17-domain dictionary,
 15 lenses, contradictions C1-C7, transposition protocol + rejection rule,
 17-record candidate inventory (T01-T17), entry schema v4]

CANDIDATE STRUCTURES + SCORES (NEW in v6):
  S1 Smell/violation .... spine=NO (collapses to basin) -> demote to TRIGGER INDEX only
  S2 Mechanism repaired . spine=STRONG (explanatory H, x-disc H); weak access path
  S3 Altitude ........... FACET (answers "where", not "what fixed")
  S4 Risk/blast tier .... FACET (sequencing/safety overlay; property not category)
  S5 Driving-force/why .. ACCESS PATH + facet (great connector, too many-to-many for spine)
  S6 FACETED (mech-spine x overlays) .. ★ LEADING — net Highest
     = S2 spine + facets{altitude,risk,driving-force,paradigm,direction,trigger-vocab,preservation-rung}
       + access-layer-1 trigger-index(subsumes S1) + access-layer-2 workflow-phase(protocol not category)

LEADING CANDIDATE: S6  (confidence Med-High; final choice deferred to T8 post-audit)

S6 OPEN RISKS for audit:
  R1 primary-mechanism rule vs multi-mechanism records (T13) -> does single-homing distort?
  R2 facet orthogonality: altitude vs blast-radius partially correlated -> verify independence
  R3 Q16 methodology tier: T08/T09/T10 -> parallel METHODOLOGY register vs on atomic spine?

KEY INSIGHT (the escape, concretely): invert S1 -> the smell checklist becomes a LOOKUP INDEX
  *into* a mechanism-organized catalog, not the catalog's table of contents. Fowler/SOLID = leaves
  reachable from the trigger index, not the branches.

OPEN QUESTIONS:
  Q1/Q10 -> mechanism spine CONFIRMED by S6 leading; connascence = one trigger-vocab facet value
  Q5/Q16 -> methodology tier: lean SEPARATE register (R3), decide T8
  Q14 where-NOT / Q15 applicability-signal -> both become facets/fields in S6 (validated)
  Q17 T17 failure-behavior-changing -> tagged subclass on the spine (decide T8)
  [carried: Q2 adjacency tag, Q3 refactor-vs-rearchitect line, Q4 cross-paradigm unity,
   Q11 ladder-yes, Q12 audit-Low-transfers, Q13 MDL stop-rule]

TODO turns 7-9:
  T7  Adopt S6 provisionally; populate facets across ALL records; add 8-15 records to fill
      paradigm/blind-spot gaps (concurrency, SQL/data, FP); blast-radius + preservation-ladder
      + inverse + where-NOT completeness; fix vague signals (T02/T07/T15)
  T8  AUDIT + FINAL STRUCTURE CHOICE: resolve R1-R3; attack each record; hold C1-C7; test 12 Low
      transfers; rule Q2/Q17; high-impact-WHEN layer; de-overlap; gap hunt; COMMIT the structure
  T9  Final synthesis: assemble catalog under chosen structure; success-criteria + escape-test
      self-audit; epistemic honesty pass

CONFIDENCE LEDGER: S6 recommendation Med-High; spine=mechanism High; facet orthogonality
  UNVERIFIED (R2); methodology tiering pending (R3); per-record + per-transfer confidences carried.
=== END WORKING STATE ===
```

Turn 6 produced 6 candidate structures, scored each on all six criteria, and lands on a provisional **S6 (mechanism-spine × faceted overlays + trigger/workflow access layers)** at Med-High confidence — explicitly *not* committed; Turn 8 decides after the audit resolves R1 (single-homing distortion), R2 (facet orthogonality), R3 (methodology tier). The key escape move is concrete: invert the basin so the smell checklist becomes a lookup *index into* a mechanism catalog rather than its skeleton. Ready for Turn 7 (populate facets, fill paradigm/blind-spot gaps, completeness passes) on your go.



---

# Refactoring Taxonomy — TURN 7/9: RED-TEAM AUDIT

Harsh pass over Turns 3–6. Each finding: **[Fnn] item — problem — severity — disposition** (KILL/MERGE/KEEP/SPLIT/RE-GROUND/RE-SCOPE). No rewrite, no final structure. Ranked list + must-fix shortlist at end.

---

## FC1 — Symptoms mislabeled as mechanisms
- **[F01] The whole trigger-vocab facet risks re-importing symptoms as structure.** "Long method / params>4 / nesting≥4" are *signals of a missing chunk/abstraction*, not mechanisms. If they appear anywhere but the trigger-index access layer, S6 leaks the basin back in. **Sev: High.** → RE-GROUND: enforce a hard rule — smells live ONLY in access-layer-1; the spine never names a smell.
- **[F02] T07 "Rebalance Interface Width."** "Fat interface" and "leaky interface" are symptoms; the *mechanism* is "match exposed surface to actual caller-usage." As written the name foregrounds the symptom. **Sev: Med.** → RE-GROUND name/mechanism toward "Align Interface to Usage."
- **[F03] T04 "Make Implicit State Explicit" partly describes a symptom ("implicit state") as if it were the fix.** Mechanism is fine (reify), but the detection ("multiple booleans") is the symptom doing double duty. **Sev: Low.** → KEEP, tighten wording.

## FC2 — Duplicate / overlapping concepts
- **[F04] T01 (Substitution Seam) vs T08 (Strangler) vs T10 (Branch-by-Abstraction) vs T17 (Bulkhead) all = "introduce an indirection point."** Real overlap. **Sev: High.** → DISTINGUISH with sharp tests, don't merge: T01 = seam for *substitution/testing* (atomic); T10 = seam to *swap one implementation* (atomic→short-lived); T08 = seam to *incrementally replace a whole subsystem* (methodology, many slices); T17 = seam for *fault isolation* (changes failure behavior). Tests: *purpose* (test/swap/replace/isolate) × *lifespan of the seam* (permanent/temporary) × *count of things moved* (one/many).
- **[F05] T11 (Extract Missing Abstraction) vs T03 (Relocate by Change-Affinity).** Both touch duplication/co-change. **Sev: Med.** → DISTINGUISH: T11 = *create one definition* for duplicated intent (cohesion); T03 = *move existing code* to where it co-changes (locality). Sharp test: does the fix add a new abstraction (T11) or only relocate existing code (T03)?
- **[F06] Dictionary near-duplicates: feature-envy / inappropriate-intimacy / connascence-of-X / mutual-information / co-evolution are five names for "two things are coupled."** **Sev: Med.** → MERGE under one mechanism (coupling, graded by connascence strength×locality); keep the others as *trigger vocabulary* only, not separate mechanisms. Sharp test for the classic trio: feature-envy = method wants *another* object's data (direction: one→other); inappropriate-intimacy = *mutual* reach into internals (bidirectional); coupling = the umbrella. Distinguish by *directionality*.
- **[F07] T05 (Temporal→Structural) overlaps T04 (reify state) and T13 (illegal-states-unrepresentable).** All three "push runtime invariant to structure/type." **Sev: Med.** → KEEP separate but co-locate under one sub-mechanism "encode-invariant-structurally," distinguished by *what's encoded*: state identity (T04) / call-order (T05) / value-combination legality (T13).

## FC3 — Missing domains / technique classes
- **[F08] Concurrency is under-covered.** Only T17 (bulkhead) + T05 (order) touch it. Missing: *narrow a lock's scope*, *replace shared-mutable with message-passing/immutability*, *make a check-then-act atomic*, *eliminate false sharing*, *confine state to a single owner (actor/thread)*. **Sev: High.** → ADD ~4 records in T-final.
- **[F09] Data/SQL almost absent from the inventory** despite being a Turn-2 High blind spot. Only T09 (expand/contract) is data-shaped. Missing: *normalize to remove update-anomaly*, *deliberate denormalize*, *introduce surrogate key / split identity from meaning*, *push invariant into a constraint*, *extract a read model (CQRS-lite)*. **Sev: High.** → ADD ~4 records.
- **[F10] Performance-structure refactors missing entirely.** No record for *batch an N+1*, *hoist invariant work out of a loop* `[D3]`, *replace per-item allocation with reuse*, *introduce a cache at a referentially-transparent seam* `[D3 memoization]`. These are behavior-preserving structural changes that the basin AND our inventory both miss. **Sev: Med-High.** → ADD ~3 records; tag preservation carefully (complexity-class change is observable timing → boundary question, F18).
- **[F11] Security-relevant structure entirely missing** (the prompt names it; we never covered it). E.g., *centralize a scattered trust boundary / input-validation seam*, *narrow over-broad privilege surface*, *remove a confused-deputy by making the authority explicit*. These are structural and largely behavior-preserving on valid inputs. **Sev: Med.** → ADD a small class; flag that some are behavior-*changing* on malicious input (adjacency, Q2).
- **[F12] Socio-technical techniques are thin.** L9 produced seeds (Conway-align, ownership) but only as lenses, no records. **Sev: Med.** → ADD 1–2 records OR honestly RE-SCOPE as "process-edge, catalogued as context not technique" per the Turn-1 scope. Decide in T8.
- **[F13] Error-handling structure (Turn-2 Med-High blind spot) has no record** beyond T15-adjacent. Missing: *convert exception-control-flow to explicit result types*, *consolidate scattered error handling to a boundary*, *make partial-failure recovery explicit*. **Sev: Med.** → ADD ~2 records.

## FC4 — Weak/shallow analogies to reject
- **[F14] Already-flagged-Low dictionary entries mostly fail the rejection rule on inspection:** info-theoretic coupling-as-MI (not measurable on code), channel-capacity (re-sourced, OK), Alexander QWAN (mystical, no transformation), schema-mismatch/astonishment (no observable signal), SMED (no code mechanism). **Sev: Med** (they're framing, risk of laundering into "technique"). → KILL as techniques; DEMOTE to "rationale/framing" appendix. Survivors that re-grounded (min-cut→T02, MDL→stop-rule, poka-yoke→T13) KEEP.
- **[F15] "Neutral-mutation staging" (L6) and "exaptation" are evolutionary re-labels of ordinary techniques.** Neutral-mutation = "behavior-preserving prep refactor" (that's just... refactoring); exaptation = generalize (T11-family). **Sev: Low.** → KILL the labels; the mechanism already exists elsewhere.
- **[F16] "Broken windows" / "desire paths" / "zoning" (D14).** Broken-windows already rejected (Turn 5). Desire-paths (use real usage to find boundaries) actually RE-GROUNDS into T03/co-change — KEEP re-grounded. Zoning = layering, already covered. **Sev: Low.** → 1 kill, 1 re-ground, 1 merge.

## FC5 — Scale sensitivity
- **[F17] T06 (Conditional↔Dispatch) doesn't scale up.** Polymorphism is great at class scale; at service scale "dispatch" means a routing/plugin system with deployment implications — different mechanism, different risk. **Sev: Med.** → SPLIT by altitude facet (don't pretend one record covers method→service).
- **[F18] T11/T01 abstraction & seam techniques invert in value with scale.** A seam is cheap locally, expensive (network hop, versioned contract) across services. The *same mechanism* has opposite cost at different altitudes — the facet must carry this or the taxonomy will mislead. **Sev: High.** → RE-GROUND: blast-radius/altitude facet must be *mandatory* and must flip the cost/recommendation, not just label size.

## FC6 — Hidden assumptions
- **[F19] The entire inventory assumes tests exist or can be added (T14 mitigates but presumes you *can* characterize).** Untestable code (heavy I/O, nondeterminism, no seams yet) breaks the preservation story for most records. **Sev: High.** → KEEP but make explicit: T14/seam-first is a *precondition gate* for the rest; add "what if you can't test it" guidance.
- **[F20] Strong typed/OO bias.** T13 (illegal-states), T16 (totality), T06 (polymorphism) assume sum types / classes. Dynamic langs (Python/JS/Ruby) and pure-procedural (C) get a degraded catalog. **Sev: High.** → RE-GROUND: paradigm facet must be mandatory; provide the dynamic-lang degradation per record (e.g., T13 → runtime smart-constructor + assertions, confidence drops). Confirms Q4: cross-paradigm unity is *partial*, per-case.
- **[F21] Assumes single-team / monorepo for many "move" techniques (T03, T02).** Moving code across a repo boundary or team boundary is a coordination problem, not a refactor edit. **Sev: Med.** → RE-SCOPE: flag cross-ownership moves as methodology+social, not atomic.

## FC7 — Boundary ambiguity (refactor vs rewrite/redesign) — Q3
- **[F22] Q3 STILL UNRESOLVED and now load-bearing.** T08 (strangler) and T17 (bulkhead) sit on the refactor/rewrite/redesign edge; without a cut line the taxonomy's scope is undefined. **Sev: High.** → MUST-FIX. Proposed test: *it's still refactoring iff (a) observable behavior is preserved at a stated boundary AND (b) the change is decomposable into individually behavior-preserving steps.* Strangler passes (per-slice parity); a "stop and rewrite from spec" fails (b). Bulkhead fails (a) under failure → adjacency. Adopt in T8.

## FC8 — Temporal blind spots
- **[F23] Most atomic records are one-shot; the taxonomy under-represents continuous/decay dynamics** despite Turn-4 L4 surfacing them. Churn/decay are in the *applicability-signal* facet but no *technique* addresses "establish a refactoring cadence / fitness function." **Sev: Med.** → ADD: "Continuous-fitness-function" as a methodology/enabler (or RE-SCOPE as process-edge). Note: this is process, may be out-of-scope per Turn 1.
- **[F24] Migration records (T08/T09) have a real temporal failure mode the inventory underweights: the *stall* (permanent dual-system).** **Sev: Med.** → KEEP, but elevate "completion obligation" to a first-class field for methodology-tier entries.

## FC9 — Incentive blind spots
- **[F25] No record accounts for *why teams won't do this*.** Several techniques (T12 de-abstract = "delete code you wrote"; T14 = "write tests for legacy nobody owns") face strong disincentives. A taxonomy that ignores adoption friction is operationally optimistic. **Sev: Med.** → ADD an "adoption-friction / incentive" note per record (light). Goodhart guard: do NOT propose a single "refactor score" — it will be gamed (C3-adjacent). **Sev of Goodhart risk: Med.** → explicitly forbid a global metric in success criteria.

## FC10 — Measurement problems
- **[F26] T02 (min-cut), T07 (interface-usage), T15 (observability seam) have non-observable-by-eye signals** — flagged in Turn 5, still unfixed. **Sev: High** (operational uselessness if unaddressed). → RE-GROUND each with a concrete, tool-available detection (T02: import/dependency graph; T07: static call-site count per member; T15: explicit "is this on an incident-prone path" checklist, accept it's judgment).
- **[F27] "Impact" is still hand-wavy across the board.** We committed to "conditional impact (high WHEN X)" but no record states a *falsifiable* observable for "did this help." **Sev: High.** → MUST-FIX: every record needs a *measurable post-condition* (e.g., "change-coupling co-commit rate drops," "affected-line coverage rises," "lock-hold time falls"), even if coarse. Without it, success-criteria #5 (measurability) fails.

## FC11 — Operational uselessness
- **[F28] Methodology records (T08/T09/T10) are too big to "do at 2pm in a PR."** They're projects. Mixing them with atomic moves on one spine misleads a dev about effort. **Sev: Med-High.** → SPLIT TIERS (confirms R3/Q16): atomic techniques vs methodologies as separate registers, cross-linked.
- **[F29] T13/T16/T04 can read as academic** without a worked before/after. **Sev: Med.** → KEEP, require a concrete code snippet per record in synthesis (T9).

## FC12 — Scenarios where the structure FAILS outright
- **[F30] A large untyped Python ML pipeline (notebooks + glue), no tests, single data scientist owner.** S6 spine still works, but ~40% of records (typed-lang, test-dependent) degrade to "first build seams/tests" — the taxonomy correctly routes everything through T14/T01 first but offers thin *direct* value. **Sev: Med.** → This is honest, not a failure — but document it as a known limited-applicability profile.
- **[F31] A distributed microservices estate where "the smell" is cross-service chattiness and shared DB.** The atomic, single-process inventory has little; needs the distributed/data records (F08/F09) + methodologies. Without F08–F13 additions, the taxonomy *fails its own escape test* here. **Sev: High.** → MUST-FIX via the missing-class additions.
- **[F32] A codebase where the "right" structure cuts across team ownership (Conway conflict).** The taxonomy can name the technique (Conway-align) but applying it is an org change it can't authorize. **Sev: Low** (honest scope limit). → Document as out-of-band; KEEP as context.
- **[F33] Greenfield-ish / rapidly-pivoting startup code where churn is so high that ANY abstraction is premature.** Here the *correct* output is mostly T12 (de-abstract) + "don't refactor yet." The basin would over-refactor; our taxonomy handles it IF the where-NOT/economics facet is honored. **Sev: Low.** → validates the where-NOT facet; KEEP.

---

## RANKED FINDINGS (by severity × leverage)

**HIGH (must address before synthesis):**
F01 (smells must stay in access-layer only) · F04 (seam-family distinguish-don't-merge) · F08 (concurrency gap) · F09 (data/SQL gap) · F18 (scale inverts seam cost — mandatory altitude facet) · F19 (test-existence assumption → seam-first gate) · F20 (typed/OO bias → mandatory paradigm facet + degradations) · F22 (Q3 refactor/rewrite cut line) · F26 (vague signals → tool-grounded detection) · F27 (no falsifiable impact post-condition) · F31 (distributed scenario fails escape test without additions)

**MED:** F02, F05, F06, F07, F10, F11, F13, F17, F21, F23, F24, F25, F28, F29, F30

**LOW:** F03, F12*, F14, F15, F16, F32, F33  (*F12 may rise if we keep socio-technical in scope)

---

## MUST-FIX-BEFORE-SYNTHESIS SHORTLIST (the 8 that gate Turn 9)
1. **F22 — adopt the refactor/rewrite cut line** (behavior preserved at boundary AND decomposable into preserving steps). Unblocks scope.
2. **F08 + F09 + F10 + F11 + F13 — add the missing technique classes** (concurrency, data/SQL, performance-structure, security-structure, error-handling). Without these the taxonomy fails its own escape test (F31).
3. **F20 + F19 — mandatory paradigm facet + seam/test precondition gate**, with per-record degradation for dynamic/untested code.
4. **F18 — make altitude/blast-radius facet mandatory and cost-flipping**, not cosmetic.
5. **F27 — every record gets a falsifiable post-condition** ("how you'd know it helped").
6. **F26 — re-ground the 3 vague signals** (T02/T07/T15) on tool-available detection.
7. **F01 + F04 + F06 + F07 — spine hygiene**: smells only in the access layer; distinguish the seam-family and the coupling/invariant-encoding clusters with sharp tests.
8. **F28 — split atomic vs methodology tiers** (resolves R3/Q16).

---

## WORKING STATE v7

```
=== WORKING STATE (refactoring-taxonomy) — v7 @ Turn 7/9 ===

[CARRIED: definitions, basin+blind spots, escape test, 17-domain dict, 15 lenses, C1-C7,
 transposition protocol+rejection rule, 17-record inventory, S6 leading structure]

AUDIT FINDINGS (ranked) — 33 findings F01-F33:
  HIGH(11): F01 smells-in-access-only | F04 seam-family distinguish | F08 concurrency-gap |
    F09 data/SQL-gap | F18 scale-inverts-seam-cost | F19 test-existence-assumption |
    F20 typed/OO-bias | F22 refactor/rewrite-cutline | F26 vague-signals | F27 no-falsifiable-impact |
    F31 distributed-scenario-fails-escape
  MED(15): F02,F05,F06,F07,F10,F11,F13,F17,F21,F23,F24,F25,F28,F29,F30
  LOW(7): F03,F12*,F14,F15,F16,F32,F33

DISPOSITIONS SUMMARY:
  KILL (as techniques->demote to framing): MI-coupling, Alexander-QWAN, SMED, schema-mismatch,
    astonishment-as-signal, neutral-mutation label, exaptation label, broken-windows (already)
  MERGE/DISTINGUISH: F04 seam-family (4 records, sharp tests: purpose×lifespan×count),
    F06 coupling-cluster (feature-envy/intimacy/connascence/MI/co-evo -> one mechanism, rest=triggers),
    F07 invariant-encoding cluster (T04/T05/T13 by what's-encoded)
  ADD (missing classes): concurrency x4, data/SQL x4, perf-structure x3, security-structure x~3,
    error-handling x2  [-> inventory will ~double in T-final]
  RE-GROUND: F02 T07-name, F26 vague signals -> tool detection, F18 altitude cost-flip,
    F20 paradigm degradations, desire-paths->T03
  RE-SCOPE (decide T8, may be out-of-scope per Turn1): F12 socio-technical, F23 refactor-cadence

MUST-FIX-BEFORE-SYNTHESIS (8, gate Turn 9):
  1 F22 cut line  2 F08/F09/F10/F11/F13 missing classes  3 F20+F19 paradigm facet+seam gate
  4 F18 mandatory cost-flipping altitude facet  5 F27 falsifiable post-condition per record
  6 F26 reground vague signals  7 F01/F04/F06/F07 spine hygiene  8 F28 atomic vs methodology tiers

S6 RISKS now resolved by audit:
  R1 single-homing distortion -> ADDRESS via sharp distinguish-tests (F04/F06/F07) + `related` links; OK
  R2 facet orthogonality -> altitude & blast-radius ARE correlated (F18) -> treat as ONE facet
     "altitude/blast-radius" with cost-flip, not two; reduces orthogonality problem
  R3 methodology tier -> CONFIRMED split (F28); two registers cross-linked

ENTRY SCHEMA v5 (audit-driven additions):
  + falsifiable-post-condition (F27)  + paradigm-degradation (F20)  + adoption-friction note (F25)
  + tool-available-detection (F26)  + (methodology tier adds) completion-obligation (F24)
  altitude & blast-radius MERGED into one mandatory cost-flipping facet (R2/F18)

OPEN QUESTIONS resolved/updated:
  Q3 -> PROPOSED cut line (F22), adopt T8 | Q4 -> cross-paradigm unity is PARTIAL/per-case (F20) CONFIRMED
  Q5/Q16/R3 -> two tiers CONFIRMED (F28) | Q12 -> Low transfers audited (F14-F16): mostly KILL/demote
  Q2/Q17 -> adjacency tag needed for T17, security-on-malicious-input (F11), bulkhead-under-failure; rule T8
  Q14 where-NOT validated (F33) | Q15 applicability-signal validated (F30/F33)
  Q13 MDL -> KEEP as informal stop-rule only (not a measured field)

TODO turns 8-9:
  T8  COMMIT structure (S6 w/ merged altitude facet + 2 tiers); execute must-fix shortlist;
      ADD missing-class records; rule Q2/Q17 adjacency; resolve C1-C7 as held tensions w/ guidance;
      high-impact-WHEN layer; final de-overlap + gap re-check vs blind spots
  T9  SYNTHESIS: assemble full catalog under committed structure; worked before/after per record (F29);
      success-criteria + escape-test self-audit; epistemic honesty pass

CONFIDENCE LEDGER: S6 now Med-High->High pending must-fix; cross-paradigm unity Med/partial;
  inventory KNOWN-INCOMPLETE until missing classes added (F08-F13); impact measurability UNMET until F27.
=== END WORKING STATE ===
```

Turn 7 red-teamed everything: **33 findings (11 High / 15 Med / 7 Low)** across all 12 failure classes, each with a disposition. Biggest exposures: the inventory is **known-incomplete** (concurrency, data/SQL, perf, security, error-handling classes missing — F08–F13, the taxonomy fails its own escape test on a distributed estate without them); **typed/OO + test-existence assumptions** baked in (F19/F20); **no falsifiable impact** yet (F27); and **Q3 still unresolved** (F22, now with a proposed cut line). Also resolved two S6 risks: altitude & blast-radius are correlated → merge into one cost-flipping facet (R2); two tiers confirmed (R3). No rewrite or final structure committed. 8-item must-fix shortlist gates Turn 9. Ready for Turn 8 (commit structure + execute must-fixes + add missing classes) on your go.



---

# Refactoring Taxonomy — TURN 8/9: COMPRESSION RESTART (DESIGN LOCK)

Residue discarded. Design committed. No full taxonomy yet — this is the spec Turn 9 consumes.

---

## 1. CHOSEN ORGANIZING PRINCIPLE
**S6 as revised by the audit: a mechanism-spine catalog with a mandatory facet overlay, a trigger index, and two tiers.**
- **Spine (primary key) = Mechanism repaired** — the underlying structural cause, grounded per the contract. This is what gives explanatory power and passes the escape test (Fowler/SOLID become *instances*, not categories).
- **Two registers:** **Tier-A atomic techniques** (one-PR moves) and **Tier-M methodologies** (multi-step, over-time, e.g. strangler/expand-contract), cross-linked. (audit F28/R3)
- **Mandatory facets** on every entry: altitude/blast-radius (merged, cost-flipping — F18/R2), paradigm-applicability, direction (add/remove abstraction), driving-force.
- **Access layer = Trigger index** (the SOLID letters + smell checklist live here ONLY, as a lookup *into* the spine — never as structure; audit F01).
- **Actionability:** a dev enters from a symptom/goal/location (facets+index), lands on a mechanism entry with executable steps + a falsifiable post-condition. Mechanism gives the *why*; facets + index give the *fast find*.

## 2. FINAL SCHEMA (exact record format — every entry, both tiers)

```
ID            : T-### (Tier-A) | M-### (Tier-M)
Name          : <verb-phrase> [provenance: CANONICAL | BORROWED | APPROXIMATION | INVENTED]
Mechanism     : the structural cause repaired (NOT the symptom) — one sentence, metaphor-free
Spine-family  : { Seam/Substitution | Encode-Invariant-Structurally | Relocate-by-Affinity |
                  Size-Abstraction(bidirectional) | Reshape-Control/Data-Flow | Isolate-Fault-Domain |
                  Migration-over-Time(Tier-M) | Preservation-Enabler }
Detection     : observable trigger(s); tag each [tool-detectable | static-analysis | history/churn |
                  human-judgment]; NO vague signals (audit F26)
Trigger-index : which basin smells/SOLID-letters route here (back-reference only)
Preconditions : make-safe gate — required tests / characterization tests / seam-first
                  (explicit "if untestable, do X first"; audit F19)
Mechanics     : concrete before->after steps
Preservation  : guarantee + LADDER rung [char-test | test-suite | type/compiler-proof |
                  observational-equivalence]; state the boundary behavior is preserved across
Blast/Risk    : altitude {statement|method|class|module|service|system} + regression-risk
                  {low|med|high} + reversibility {trivial|moderate|hard} + inverse-technique-id
                  (this facet COST-FLIPS by altitude — F18)
Paradigm      : applicability across {OO|FP|procedural|data/SQL|concurrent}; per-paradigm
                  DEGRADATION note (e.g., typed->dynamic drops a rung; audit F20)
Direction     : add-abstraction | remove-abstraction | relocate | isolate (resolves Q9 bias)
Driving-force : {comprehension|changeability|testability|reliability|performance|operability|ownership}
Impact+signal : conditional impact ("high WHEN X") + a FALSIFIABLE before/after measure
                  (e.g., co-commit-coupling rate, affected-line coverage, lock-hold time; audit F27)
Contraindication: when NOT to do it / over-application failure mode (mandatory; audit C3/C7)
Adoption-note : incentive/friction reality (light; audit F25)
Completion-obl: (Tier-M only) the "don't stall half-done" obligation (F24)
Confidence    : High|Med|Low
Depends-on    : technique IDs (e.g., most depend on T-014 characterization-test / seam-first)
Related       : same-mechanism siblings (for multi-mechanism entries; merge/split per §3)
```

## 3. MERGE / SPLIT RULES
- **Same entry iff** same *mechanism* AND same *direction* AND the *intervention steps* are the same shape. Different symptom/name alone ≠ different entry (kills basin duplication — F06).
- **Split when** mechanism is shared but **purpose/lifespan/count differ** — the seam-family test (F04): `purpose {test|swap|replace|isolate}` × `seam-lifespan {permanent|temporary}` × `things-moved {one|many}`. (So Substitution-Seam, Branch-by-Abstraction, Strangler, Bulkhead stay distinct.)
- **Multi-scale (one mechanism, many altitudes):** ONE entry, with the altitude facet enumerated and the cost/recommendation **flipping** per rung (F18). Do NOT clone per altitude — but if the *mechanics* genuinely change at scale (e.g., polymorphism→service-routing, F17), split into Tier-A vs Tier-M variants linked by `Related`.
- **Multi-mechanism record:** assign the *primary* mechanism (the one the intervention is *for*); list others in `Related`. No multi-homing on the spine (preserves R1).
- **Tier assignment:** Tier-A if doable in one reviewable change under a green bar; Tier-M if it requires a sequence of releases / live-data / coordination.

## 4. MANDATORY COVERAGE LIST (the artifact MUST include all)
Re-grounded by mechanism; the basin set is *covered but demoted to the trigger index*.

**A. Spine families (all 8):** Seam/Substitution · Encode-Invariant-Structurally · Relocate-by-Affinity · Size-Abstraction (both directions) · Reshape-Control/Data-Flow · Isolate-Fault-Domain · Migration-over-Time (Tier-M) · Preservation-Enabler.

**B. Paradigm/domain classes that MUST each contribute ≥2 entries (audit F08–F13):**
- **Concurrency:** narrow lock scope · shared-mutable→immutable/message-passing · make check-then-act atomic · confine state to single owner.
- **Data-model / SQL:** normalize (remove update anomaly) · deliberate denormalize · split identity from meaning (surrogate key) · push invariant into a constraint · extract read-model.
- **Performance-structure:** batch an N+1 · hoist invariant out of loop · cache at a referentially-transparent seam (preservation = observable-timing boundary, note it).
- **Security-structure:** centralize trust/validation boundary · narrow over-broad privilege surface · make implicit authority explicit (confused-deputy) — flag behavior-change-on-malicious-input as adjacency.
- **Error-handling structure:** exceptions→explicit result types · consolidate scattered handling to a boundary · make partial-failure recovery explicit.
- **Lifecycle/churn:** (applicability-signal driven) hotspot-targeted refactor; plus Tier-M "establish fitness function" — **RE-SCOPED: catalogued as process-edge context, not a technique** (Turn-1 scope; audit F23).
- **Distributed coupling:** bulkhead/circuit-breaker · anti-corruption layer · collapse chatty round-trips (Tier-M-ish).
- **Observability:** insert observability seam · separate decision-from-effect.
- **Socio-technical/ownership:** **RE-SCOPED as process-edge context** (Conway-align, ownership) — named, not catalogued as code techniques (Turn-1 scope; audit F12).

**C. Trigger index MUST map the full basin set:** all 5 SOLID letters + the standard smell checklist (long method, params>4, nesting≥4, dup blocks, magic numbers, primitive obsession, feature envy, data clumps, shotgun surgery, large class, message chains, speculative generality) — each routed to its mechanism entry, demonstrating the inversion.

## 5. KNOWN GAPS & CAVEATS (state these in the artifact)
- **No global impact ranking** — impact is conditional ("high WHEN churn/coverage/ownership = X"); a single "refactor score" is explicitly refused (Goodhart; audit F25/F27, success-criteria guard).
- **Cross-paradigm unity is PARTIAL/per-case** (Q4 confirmed): mechanisms transfer, *guarantees* don't — typed→dynamic degrades the preservation rung. Each entry carries a degradation note; we do not claim universal applicability.
- **Cross-discipline transfers are re-grounded or killed**, not asserted by analogy; the rejected metaphors (broken-windows, MI-coupling, QWAN, SMED, exaptation/neutral-mutation labels) survive only in a **"Framing/Rationale" appendix**, not as techniques.
- **Out of scope:** process/social techniques (cadence, Conway, ownership) appear as *context only*; greenfield design; tooling/IDE comparison; performance optimization beyond structure-enabling; the rewrite-vs-refactor decision itself.
- **Refactor/rewrite cut line adopted (F22):** in-scope iff behavior preserved at a stated boundary AND decomposable into individually-preserving steps. Failure-behavior-changing moves (bulkhead, security-on-malicious-input) carry an **adjacency tag** (Q2/Q17 ruled: included, but tagged "preserves success / changes failure").
- **Test-dependence caveat:** most entries presuppose a make-safe gate; untestable codebases get routed through Preservation-Enabler/seam-first first and otherwise receive a degraded catalog (honest limited-applicability profiles, e.g. untyped untested ML pipeline — F30).
- **Detection tooling:** some signals require dependency-graph / call-site / git-history tooling; entries say which, and which are human-judgment.

## 6. ORDERING & SIZE TARGET
- **Order:** by spine-family (the 8), Tier-A before Tier-M within each; within a family, by ascending blast-radius (local→system) so the safest, most-reachable moves read first. Trigger index is a separate front-matter table (alphabetical by smell/SOLID letter).
- **Size:** **~36–44 Tier-A entries + ~6–8 Tier-M methodologies + ~12 trigger-index rows + 1 framing appendix.** (Current inventory 17 → ~+20 from mandatory missing classes.) Target each record ≤ ~12 lines so it's usable "at 2pm in a PR" (audit F28); methodologies may run longer. **Hard cap ~50 techniques** — beyond that, actionability decays.

---

## WORKING STATE v8 (CLEANED — synthesis-ready)

```
=== WORKING STATE (refactoring-taxonomy) — v8 @ Turn 8/9 — DESIGN LOCKED ===

STRUCTURE (committed): mechanism-spine catalog + mandatory facets + trigger-index + 2 tiers.
  SPINE FAMILIES (8): Seam/Substitution | Encode-Invariant-Structurally | Relocate-by-Affinity |
    Size-Abstraction(bidirectional) | Reshape-Control/Data-Flow | Isolate-Fault-Domain |
    Migration-over-Time(Tier-M) | Preservation-Enabler
  TIERS: Tier-A atomic (one PR) | Tier-M methodology (over-time), cross-linked
  FACETS (mandatory each entry): altitude/blast-radius(cost-flipping) | paradigm(+degradation) |
    direction(add/remove/relocate/isolate) | driving-force
  ACCESS LAYER: trigger-index = SOLID letters + smell checklist routing INTO the spine (only place
    smells appear). Workflow loop detect->make-safe->transform->verify->measure = protocol, not category.

SCHEMA (locked, per entry): ID | Name+provenance | Mechanism | Spine-family | Detection(+tool tag) |
  Trigger-index ref | Preconditions/make-safe | Mechanics | Preservation(+ladder rung+boundary) |
  Blast/Risk(altitude+risk+reversibility+inverse) | Paradigm(+degradation) | Direction |
  Driving-force | Impact+falsifiable-signal | Contraindication | Adoption-note |
  Completion-obl(Tier-M) | Confidence | Depends-on | Related
  PRESERVATION LADDER: char-test < test-suite < type/compiler-proof < observational-equivalence

MERGE/SPLIT RULES:
  - same entry iff same mechanism + same direction + same intervention-shape
  - split seam-family by purpose{test|swap|replace|isolate} x lifespan{perm|temp} x count{one|many}
  - multi-scale: ONE entry w/ cost-flipping altitude facet; SPLIT only if mechanics change at scale
  - multi-mechanism: primary mechanism on spine, rest in Related (no spine multi-homing)

MANDATORY COVERAGE:
  8 spine families (all) | classes w/ >=2 entries each: concurrency, data/SQL, perf-structure,
  security-structure, error-handling, distributed-coupling, observability
  | full SOLID+smell set re-grounded in trigger-index
  RE-SCOPED to process-edge CONTEXT (not techniques): socio-technical/ownership/Conway, refactor-cadence
  KILLED->framing appendix: broken-windows, MI-coupling, QWAN, SMED, exaptation/neutral-mutation labels

CONTRADICTIONS C1-C7: HELD, not resolved — each surfaces as a Contraindication/where-NOT on the
  relevant entries (e.g., C1 load-bearing duplication, C7 seam option-value vs YAGNI prune).

KNOWN GAPS/CAVEATS (state in artifact): no global impact ranking (Goodhart-refused);
  cross-paradigm unity partial/per-case; cross-discipline transfers re-grounded-or-killed;
  test-dependence (make-safe gate); refactor/rewrite cut line = preserve-at-boundary AND
  decomposable-into-preserving-steps; adjacency tag = preserves-success/changes-failure;
  detection tooling varies (tagged per entry).

ORDERING/SIZE: by spine-family; Tier-A before Tier-M; ascending blast-radius within family;
  trigger-index = alpha front-matter. TARGET ~36-44 Tier-A + ~6-8 Tier-M + ~12 index rows +
  1 framing appendix. Records <=~12 lines. Hard cap ~50 techniques.

ALL OPEN QUESTIONS Q1-Q17: RESOLVED (see prior turns; Q3 cut-line, Q4 partial-unity, Q9 direction-axis,
  Q16 two-tiers, Q17 adjacency-tag all settled). Nothing blocking synthesis.

TURN 9 JOB: assemble the full catalog under this locked spec — populate all spine families incl.
  the mandatory missing classes; worked before/after per record; run success-criteria + escape-test
  self-audit; epistemic-honesty pass. NO new structural decisions.
=== END WORKING STATE ===
```

Turn 8 locked the design: **S6 mechanism-spine + mandatory cost-flipping facets + trigger-index + two tiers (atomic/methodology)**, a fully-specified per-entry schema with a falsifiable-impact field and preservation ladder, sharp merge/split rules, a mandatory coverage list that bakes in the audit's missing classes (concurrency/data/perf/security/error-handling/distributed/observability), explicit gaps/caveats, and a size target (~36–44 atomic + ~6–8 methodologies, hard cap ~50). All Q1–Q17 resolved; contradictions C1–C7 are *held* as per-entry contraindications. Nothing blocks synthesis. Ready for Turn 9 — assemble the full catalog under this spec — on your go.



---

# A Mechanism-Grounded Taxonomy of High-Impact Code Refactoring Techniques

*Cross-discipline, actionable. Organized by the structural cause each technique repairs — not by the smell that announces it.*

---

## READER'S GUIDE (how to use this at the moment of need)

This catalog is a **spine of mechanisms**, not a list of smells. To use it under pressure, run the five-beat loop and let the catalog serve each beat:

1. **DETECT** — you notice a symptom (a smell, a metric spike, a recurring incident, change-pain). Look it up in the **Trigger Index** (§INDEX). It routes you to one or more *mechanism* entries — the actual structural cause.
2. **MAKE-SAFE** — before editing, satisfy the entry's **Preconditions**. If the code isn't covered, the catalog routes you first through the **Preservation-Enabler** family (characterization tests, seams). Untestable code gets made testable *before* anything else.
3. **TRANSFORM** — apply the entry's **Mechanics** (concrete before→after steps).
4. **VERIFY** — confirm behavior held using the entry's **Preservation guarantee** at its stated boundary, on the named rung of the ladder (char-test < test-suite < type/compiler-proof < observational-equivalence).
5. **MEASURE** — check the entry's **falsifiable impact signal** ("how you'd know it helped"). If it didn't move, you fixed the wrong thing.

Navigate primarily by **spine family** (the eight §sections). Filter by **facets** (altitude/blast-radius, paradigm, direction, driving-force). Every entry names where **NOT** to apply it. Read **Tier-A** (one-PR moves) before **Tier-M** (multi-step methodologies that run over many releases).

---

## LEGEND

**Provenance tags** (one per technique name):
- **CANONICAL** — exists in the refactoring literature (Fowler, Opdyke, Feathers, Kerievsky) with that meaning.
- **BORROWED** — faithful mechanism transfer from another discipline; the source domain is named in ⟨angle brackets⟩.
- **APPROXIMATION** — lossy transfer; the mechanism survives but the source's full rigor does not.
- **INVENTED** — a taxonomy-specific coinage for a real mechanism the literature under-names.

**Confidence:** High / Med / Low (on the technique's reliability + transferability).
**Preservation ladder (rungs, weakest→strongest):** char-test · test-suite · type/compiler-proof · observational-equivalence.
**Detection source tags:** [tool] tool-detectable · [static] static-analysis · [history] git-churn/co-commit · [judgment] human-judgment required.
**Adjacency tag:** *preserves-success / changes-failure* — a technique that is behavior-preserving on the happy path but deliberately changes behavior under failure/attack; included but flagged (it sits at the refactor/redesign edge).

**Compact record field key** (each entry renders these):
`Mech` mechanism repaired · `Detect` observable trigger(s) · `Routes` which basin smells/SOLID-letters land here · `Safe` make-safe preconditions · `Do` transformation mechanics · `Keep` preservation guarantee + rung · `Blast` altitude / regression-risk / reversibility / inverse · `Para` paradigm applicability + degradation · `Dir·Why` direction + driving-force · `Impact→signal` conditional impact + falsifiable measure · `Contra` when NOT to · `Dep·Rel` dependencies + same-mechanism siblings.

**Scope note:** this catalogues *behavior-preserving structural change to existing code*. The refactor/rewrite cut line: **in-scope iff behavior is preserved at a stated boundary AND the change decomposes into individually behavior-preserving steps.** Process/social work (team boundaries, cadence, ownership) appears as *context*, not as catalogued techniques.

---

# FAMILY 1 — SEAM / SUBSTITUTION
*Mechanism: insert an indirection point so a dependency can be replaced, tested, or instrumented without editing its callers.*

### [T01] Introduce Substitution Seam — CANONICAL + BORROWED⟨graph: min-cut⟩ · **High**
- **Mech:** add an interface/parameter/wrapper so a hard-wired dependency becomes replaceable at one point.
- **Detect:** `new Concrete()`, static/global access, or a singleton *inside* business logic you can't replace in a test [static][tool]. **Routes:** DIP violation; "hardcoded dependency."
- **Safe:** characterization test around current behavior if untested (→T40).
- **Do:** extract an interface / parameterize the constructor or method / wrap; route callers through the seam; wire the concrete at the edge (composition root).
- **Keep:** default wiring reproduces current behavior; extract-interface+inject is compiler-checked → **type/compiler-proof**.
- **Blast:** method–module / low risk / trivially reversible (inline the seam). inverse: T16.
- **Para:** OO (interface), FP (pass a function), procedural (function pointer/param), SQL (view as seam). Degr: dynamic langs lose the compiler check → test-suite rung.
- **Dir·Why:** add-abstraction · testability/changeability.
- **Impact→signal:** unlocks isolated testing; **signal:** affected-line test coverage becomes achievable / rises.
- **Contra:** stable leaf dependency that never varies and is already testable (indirection tax for nothing — see C4).
- **Dep·Rel:** T40 (char-test). Rel: T02, M01, M03, T05-family seams.

### [T02] Place the Cut at the Dependency Min-Cut — BORROWED⟨graph/network science⟩ · **Med**
- **Mech:** when splitting a module, sever the *thinnest* edge-set between two internal clusters so the new boundary breaks the fewest dependencies.
- **Detect:** build a symbol/import dependency graph; find two dense clusters joined by few edges (near-articulation point) [tool]. **Routes:** "Large Class," "God Object," low cohesion.
- **Safe:** tests on the public surface of the unit being split.
- **Do:** community-detect or co-change-cluster the members; move each cluster to its own unit; convert the cut edges into an explicit interface.
- **Keep:** move-only edits, no logic change → **test-suite** (+ type-proof for the moved symbols).
- **Blast:** module / med risk / moderate reversibility. inverse: re-merge.
- **Para:** all; needs a dependency-graph tool to be non-vague (without it, downgrade to judgment).
- **Dir·Why:** relocate · changeability/comprehension.
- **Impact→signal:** **signal:** inter-module edge count / coupling metric drops; cross-cluster co-commits fall.
- **Contra:** a densely-connected blob with *no* thin cut — forcing a cut just relocates coupling into back-channels (C2).
- **Dep·Rel:** Rel: T03 (co-change), T11.

### [T03] Relocate by Change-Affinity — BORROWED⟨connascence (Page-Jones) + co-evolution (ecology)⟩ · **Med-High**
- **Mech:** move elements that must change together into proximity (and the holder of needed data closest to its users); minimize *distance × strength* of coupling. Subsumes "move method to its data."
- **Detect:** (a) co-commit coupling — files always edited together but living apart [history]; (b) a method using another object's data more than its own [static]; (c) one logical change touching N sites. **Routes:** **Feature Envy, Shotgun Surgery, Divergent Change, Inappropriate Intimacy, data clumps** — all re-grounded as *coupling distance/strength*, distinguished by directionality (envy = one→other; intimacy = mutual).
- **Safe:** tests covering the moved members.
- **Do:** move the method/field/module to where it co-changes; or introduce a single point all the scattered sites delegate to.
- **Keep:** move-only → **test-suite**; behavior unchanged.
- **Blast:** method–module / low-med risk / moderate reversibility. inverse: split back.
- **Para:** all. Degr: cross-team/cross-repo moves become coordination problems → escalate to Tier-M / process-context.
- **Dir·Why:** relocate · changeability/comprehension.
- **Impact→signal:** **signal:** co-commit coupling rate between the affected files drops; sites-touched-per-change falls.
- **Contra:** *spurious* co-change (coincidence/formatting churn), or copies that are diverging species (C1) — relocating fuses things that should separate.
- **Dep·Rel:** Rel: T02, T11, T12.

### [T07] Align Interface to Actual Usage — APPROXIMATION⟨ISP + leakage; Shannon channel-capacity dropped⟩ · **Med**
- **Mech:** match an interface's exposed surface to what callers actually use — *narrow* fat interfaces; *widen* interfaces whose callers reach around them via casts/globals/side-channels.
- **Detect:** narrow-dir: implementors stub unused members [static]; per-member call-site count = 0 for some impls [tool]. widen-dir: callers use `instanceof`/downcasts/globals to get what the interface won't give [static]. **Routes:** **ISP violation** (both directions, not just "split fat").
- **Safe:** call-site analysis of every member; tests on consumers.
- **Do:** segregate into role-interfaces per caller-cluster; OR promote a hidden back-channel into a first-class method/return.
- **Keep:** interface change is compiler-checked → **type/compiler-proof**.
- **Blast:** module–service / med risk / moderate reversibility. inverse: T12 (collapse over-segregation).
- **Para:** OO/typed strong; dynamic langs → convention + tests. SQL: view-column narrowing.
- **Dir·Why:** narrow=remove / widen=add · changeability/comprehension.
- **Impact→signal:** **signal:** stubbed/unused members → 0; back-channel casts → 0.
- **Contra:** speculative role-splitting with a single implementor (premature — use T12).
- **Dep·Rel:** Rel: T11, T12.

### [T08] Centralize a Scattered Trust / Validation Boundary — BORROWED⟨safety eng: defense-in-depth; security⟩ · **Med** · *adjacency: preserves-success / changes-failure on malicious input*
- **Mech:** collapse input-validation / authority checks scattered across call-sites into one explicit boundary seam, so the trust transition is single and auditable.
- **Detect:** the same validation repeated at many entry points; some paths missing it; ambient authority used deep in logic (confused-deputy shape) [static][judgment]. **Routes:** "primitive obsession" on untrusted strings; scattered guards.
- **Safe:** characterization tests on valid inputs; enumerate current validation rules first.
- **Do:** introduce a validated/trusted type at the boundary (parse-don't-validate); convert inner code to require the trusted type; make authority an explicit parameter.
- **Keep:** on valid inputs → **type/compiler-proof** (inner code can't receive unvalidated data). **On malicious input, failure behavior changes by design** → adjacency-tagged.
- **Blast:** module–system / med-high risk / moderate reversibility.
- **Para:** typed strong (trusted types); dynamic → boundary function + assertions.
- **Dir·Why:** add-abstraction (trusted type) · reliability/security.
- **Impact→signal:** **signal:** count of validation sites → 1; paths reaching inner logic without the trusted type → 0.
- **Contra:** when scattered checks are *intentional* defense-in-depth layers (C1) — don't collapse a Swiss-cheese layer you needed.
- **Dep·Rel:** Rel: T13, T40.

### [T09] Insert an Observability Seam (Separate Decision from Effect) — INVENTED + BORROWED⟨Lean: jidoka⟩ · **Med**
- **Mech:** introduce a boundary where a decision is computed as inspectable data *before* its effect is applied, so behavior can be logged/traced/tested.
- **Detect:** failures with no signal; logic where you "can't tell what it decided"; decision entangled with side-effect [judgment + checklist: is this on an incident-prone / operationally-important path?]. **Routes:** (no basin smell — a pure blind-spot entry).
- **Safe:** tests on the current combined behavior.
- **Do:** extract the decision into a pure function returning a described result; emit/observe at the seam; apply the effect separately.
- **Keep:** extract-function → **test-suite**; output behavior unchanged, observability added.
- **Blast:** method–module / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** add-abstraction (pure decision) · operability/testability.
- **Impact→signal:** **signal:** mean-time-to-explain a given outcome drops; the decision is now unit-testable.
- **Contra:** hot path where instrumentation cost matters; trivial code (log spam, overhead).
- **Dep·Rel:** Rel: T01, error-handling family.

---

# FAMILY 2 — ENCODE INVARIANT STRUCTURALLY
*Mechanism: push a rule currently enforced at runtime (or by convention/comments) into the type/data structure, so violations become unconstructable or at least localized.*

### [T10] Reify an Implicit State Machine — BORROWED⟨compiler: SSA / explicit state⟩ + canonical-adjacent · **High**
- **Mech:** replace flag-combinations and implied call-order with an explicit, named state and explicit transitions, making invalid states/transitions visible.
- **Detect:** several booleans whose *combinations* encode states; methods that only work "after init"; comments like "must call X first" [static][judgment]. **Routes:** "primitive obsession" (boolean soup), temporal coupling.
- **Safe:** characterize current behavior across the real state combinations (→T40).
- **Do:** enumerate the actual states; introduce a state type/enum or state objects; make transitions explicit functions; reject illegal transitions.
- **Keep:** refactor under char-tests; aim for **type/compiler-proof** (illegal states unrepresentable, →T13).
- **Blast:** class–module / med risk / moderate reversibility.
- **Para:** typed strong; dynamic → state enum + guards (test-suite rung).
- **Dir·Why:** add-abstraction · comprehension/reliability.
- **Impact→signal:** **signal:** count of boolean-combination branches drops; illegal-state defects → 0.
- **Contra:** genuinely stateless code (don't manufacture a state machine); only 2 trivial states (ceremony).
- **Dep·Rel:** Dep: T40. Rel: T11, T12-T13.

### [T11] Convert Temporal Coupling to Structural Coupling — BORROWED⟨connascence-of-execution-order⟩ · **Med-High**
- **Mech:** eliminate "must call in this order" by encoding the dependency in types/signatures so wrong order is impossible or caught.
- **Detect:** dynamic connascence of order; calling B before A silently corrupts; partially-constructed objects [judgment][static]. **Routes:** temporal-coupling blind spot.
- **Safe:** tests on the legal call sequences.
- **Do:** builder/typestate (return the next-stage type from the prior call so B is uncallable without A's output); or fold the sequence into one atomic operation.
- **Keep:** **type/compiler-proof** where typestate exists; else test-suite on legal sequences.
- **Blast:** class–module / med risk / moderate reversibility.
- **Para:** typed strong; dynamic → runtime sequencing guard (Low confidence there).
- **Dir·Why:** add-abstraction · reliability.
- **Impact→signal:** **signal:** order-dependent defects → 0; API misuse caught at compile.
- **Contra:** order that is genuinely free — don't over-constrain a flexible API.
- **Dep·Rel:** Rel: T10, T13.

### [T12] Make Illegal States Unrepresentable — BORROWED⟨type theory; Lean: poka-yoke⟩ · **High (typed) / Low (dynamic)**
- **Mech:** encode invariants in the type/data model so invalid combinations can't be constructed; move enforcement from scattered runtime checks to compile time.
- **Detect:** the same invariant validated at many sites; nullable fields "always set after X"; mutually-exclusive flags both settable [static]. **Routes:** **primitive obsession**, repeated null-checks, validation duplication.
- **Safe:** enumerate the invariant's real cases; char-tests.
- **Do:** replace primitive+validation with a constrained type / sum type / smart constructor; delete now-impossible branches (→T17).
- **Keep:** **type/compiler-proof** — strongest rung; compiler enforces legal-state equivalence.
- **Blast:** method–module / low-med risk / moderate reversibility. inverse: T19.
- **Para:** typed/sum-type langs strong; **dynamic langs degrade to runtime smart-constructor + assertions (confidence Low — it's ceremony, not a proof).**
- **Dir·Why:** add-abstraction · reliability/comprehension.
- **Impact→signal:** **signal:** validation sites → 1; class of invalid-state bugs eliminated.
- **Contra:** over-modeling rare cases into unwieldy types; truly runtime-variable invariants.
- **Dep·Rel:** Rel: T10, T11, T17.

### [T13] Push a Data Invariant into a Constraint — BORROWED⟨databases: referential integrity / constraints⟩ · **High**
- **Mech:** move an invariant enforced in application code into the data engine (FK, UNIQUE, CHECK, NOT NULL), so it holds regardless of which code path writes.
- **Detect:** application code defending an invariant the schema permits to break; orphaned rows; dup "unique" values in prod [tool: schema audit][query]. **Routes:** data-model blind spot; scattered validation.
- **Safe:** audit existing data for current violations *before* adding the constraint (it will reject dirty data).
- **Do:** clean violating rows; add FK/UNIQUE/CHECK/NOT-NULL; remove now-redundant app checks.
- **Keep:** **observational-equivalence** on conforming data; engine enforces. (Behavior changes for *non-conforming writes* — they now fail fast: adjacency on the write path.)
- **Blast:** module–system (shared DB) / high risk / hard reversibility (data + migration).
- **Para:** data/SQL (NoSQL: app-level or schema-validation degrade).
- **Dir·Why:** add-abstraction (constraint) · reliability.
- **Impact→signal:** **signal:** integrity-violation incidents → 0; redundant app-side checks removed.
- **Contra:** high-write-throughput paths where the constraint's cost matters; data you can't clean.
- **Dep·Rel:** Rel: M05 (migration), T12.

### [T17] Partial → Total (Eliminate Throw / Unreachable Arms) — BORROWED⟨type theory: totality; compiler: DCE⟩ · **Med-High**
- **Mech:** make a function defined over all inputs — remove `NotImplemented`/throw branches by handling the case or proving (via types) it can't occur.
- **Detect:** overrides throwing "not implemented"; `default:` branches that "can't happen"; partial functions guarded upstream [static]. **Routes:** **LSP violation** (no-op/throwing overrides re-grounded as broken totality of the supertype contract).
- **Safe:** tests covering each branch; reachability analysis.
- **Do:** tighten the input type so the bad case can't be passed (→T12); or implement the missing case; remove dead arms with a reachability argument.
- **Keep:** unreachable removal → reachability proof (**observational-equivalence**); narrowed-input → **type-proof**.
- **Blast:** method–class / med risk / moderate reversibility.
- **Para:** typed strong; dynamic → exhaustive tests (Med).
- **Dir·Why:** remove-abstraction (dead arms) / add (type narrowing) · reliability/comprehension.
- **Impact→signal:** **signal:** throw/`NotImplemented` arms → 0; LSP substitution holds.
- **Contra:** the throw is a *real, reachable* precondition guard — keep it, make it explicit instead.
- **Dep·Rel:** Rel: T12.

### [T18] Replace Exceptions-as-Control-Flow with Explicit Results — BORROWED⟨FP: sum types / Result-Either⟩ · **Med-High**
- **Mech:** make error outcomes part of the return type instead of a non-local jump, so callers must handle them and the control flow is visible.
- **Detect:** exceptions used for expected outcomes; swallowed `catch{}`; error paths invisible in signatures [static]. **Routes:** error-handling blind spot; hidden control flow.
- **Safe:** char-tests on both success and failure paths.
- **Do:** return `Result/Either/Option`; convert throw-sites to returns; force handling at call-sites; reserve exceptions for truly exceptional.
- **Keep:** **type/compiler-proof** (callers must destructure) / test-suite in dynamic langs.
- **Blast:** module–service / med-high risk (touches many callers) / moderate reversibility.
- **Para:** FP/typed strong; OO with Result types; dynamic → tuples/conventions (Low rung).
- **Dir·Why:** add-abstraction · reliability/comprehension.
- **Impact→signal:** **signal:** swallowed-error sites → 0; unhandled-error defects fall.
- **Contra:** genuinely exceptional, unrecoverable conditions (don't `Result`-wrap OOM); pervasive rewrite cost may exceed value.
- **Dep·Rel:** Rel: T20 (consolidate handling), T09.

---

# FAMILY 3 — RELOCATE BY AFFINITY
*(Primary mechanism shared with T03 above; this family also hosts identity/data relocation.)*

### [T14] Split Identity from Meaning (Introduce Surrogate Key / Value Identity) — BORROWED⟨data modeling: surrogate vs natural key⟩ · **Med**
- **Mech:** introduce an indirection between *what a thing is* and *what identifies it*, so meaning can change without breaking references.
- **Detect:** a natural/business key used as a primary identifier and propagated everywhere; renames/merges cause cascading breakage [tool: schema/ref audit]. **Routes:** rigid coupling to mutable identity.
- **Safe:** map current references; expand/contract migration plan (→M05).
- **Do:** add a stable surrogate id; repoint references; demote the natural key to an attribute.
- **Keep:** dual-key period → **observational-equivalence**; consumers unaffected during expand phase.
- **Blast:** module–system / high risk / hard reversibility (data migration).
- **Para:** data/SQL primarily; also OO (identity vs equality).
- **Dir·Why:** add-abstraction (indirection) · changeability.
- **Impact→signal:** **signal:** cascade-on-rename breakage → 0.
- **Contra:** small/stable domain where the natural key never changes (surrogate is overhead).
- **Dep·Rel:** Dep: M05. Rel: T01.

---

# FAMILY 4 — SIZE THE ABSTRACTION (BIDIRECTIONAL)
*Mechanism: adjust the amount of abstraction up OR down to match realized variation. Counters the basin's one-way "more abstraction / always DRY" bias.*

### [T15] Extract a Missing Abstraction — CANONICAL⟨Kerievsky; cognition: chunking⟩ · **High**
- **Mech:** give a recurring concept currently spread across duplicated/primitive code one named definition (a chunk that fits working memory).
- **Detect:** duplicated blocks identical in *intent*; a domain concept passed as raw string/int; a long linear method doing several named things [static][tool: clone detection]. **Routes:** **Long Method, Duplicated Code, Magic Number, Primitive Obsession** — all re-grounded as *a missing named chunk / missing concept*, NOT as line-count. (Long Method = exceeds working-memory chunking, not >50 LOC.)
- **Safe:** tests over the duplicated sites.
- **Do:** introduce a function/type/constant for the concept; replace each site with a call/use; centralize the definition; name it for intent.
- **Keep:** **test-suite**; **type-proof** if it becomes a type.
- **Blast:** statement–module / low risk / trivially reversible (inline). inverse: T16.
- **Para:** all.
- **Dir·Why:** add-abstraction · comprehension/changeability.
- **Impact→signal:** **signal:** duplication ratio drops; a future change to the concept touches 1 site not N.
- **Contra:** **coincidental** similarity (copies that will diverge — C1); extracting before the third occurrence (rule of three) risks the wrong abstraction (→T16).
- **Dep·Rel:** Rel: T16 (inverse), T03, T21.

### [T21] Introduce Parameter Object / Whole Value — CANONICAL⟨Fowler⟩ + BORROWED⟨DDD value object⟩ · **High**
- **Mech:** group co-traveling primitive parameters/fields into a single meaningful type, replacing positional primitive lists.
- **Detect:** parameter lists >4; the same cluster of params passed together repeatedly (data clump); primitives that always move as a set [static]. **Routes:** **Long Parameter List (>4), Data Clumps, Primitive Obsession** — re-grounded as *a missing value concept*, and as reducing positional connascence.
- **Safe:** tests on the affected signatures.
- **Do:** create a value type for the cluster; replace the param list / scattered fields with it; push related behavior onto it.
- **Keep:** **type/compiler-proof**.
- **Blast:** method–module / low risk / trivially reversible.
- **Para:** all (record/struct/object/tuple).
- **Dir·Why:** add-abstraction · comprehension/changeability.
- **Impact→signal:** **signal:** arity drops; the cluster now changes in one place; positional-arg mistakes → 0.
- **Contra:** params that are genuinely unrelated (forcing a bag type reduces clarity).
- **Dep·Rel:** Rel: T15, T12.

### [T16] Collapse a Premature / Wrong Abstraction (De-abstract) — INVENTED⟨econ: sunk-cost/YAGNI; Kerievsky-bidirectional⟩ · **Med-High**
- **Mech:** remove an abstraction whose anticipated variation never materialized, inlining it so the real, concrete shape is visible again.
- **Detect:** an interface/base class/generic with exactly one implementor/instantiation; config flags never flipped; a "framework" used once [static][tool]. **Routes:** **Speculative Generality**, over-engineering.
- **Safe:** lean-on-the-compiler; tests over the single use.
- **Do:** inline the single implementor; delete the interface/indirection; collapse generics to concrete types.
- **Keep:** inline is mechanizable → **type/compiler-proof**.
- **Blast:** method–module / low-med risk / moderate reversibility (re-extract via T15).
- **Para:** all.
- **Dir·Why:** **remove-abstraction** · comprehension/changeability.
- **Impact→signal:** **signal:** indirection levels drop; one-implementor interfaces → 0; reader hop-count to understand falls.
- **Contra:** the abstraction is a **real option** on *likely* near-future variation (C7) — then keep it; this is a probabilistic call. **This entry exists to break the one-way DRY bias.**
- **Dep·Rel:** Rel: T15 (inverse), T19.

### [T19] Replace Conditional-on-Type with Dispatch — AND its Inverse — CANONICAL⟨Fowler/GoF Strategy/State⟩ + INVENTED (the inverse pairing) · **High**
- **Mech:** *forward:* move per-type branches into polymorphic units so adding a type is local. *inverse:* collapse a one-axis polymorphic hierarchy back to a conditional when the indirection costs more than it saves.
- **Detect:** *fwd:* a switch/if-else on a type/enum growing one arm per feature [static]. *inv:* a class hierarchy whose single overridden method is used in one place. **Routes:** **OCP violation** (re-grounded: the cost is *modification spread per new case*, not "open/closed" dogma).
- **Safe:** tests covering each arm/type.
- **Do:** *fwd:* introduce interface, one implementor per arm, replace switch with virtual call. *inv:* inline overrides into a conditional, delete the hierarchy.
- **Keep:** **test-suite** (+ type-proof on the dispatch).
- **Blast:** class–module / med risk / moderate reversibility (each direction undoes the other).
- **Para:** OO (polymorphism), FP (sum type + match — note: a `match` is often *better* than dispatch here), procedural (function table). Degr: in FP the "forward" target is exhaustive pattern-matching, not subclasses.
- **Dir·Why:** fwd add / inv remove · changeability/comprehension.
- **Impact→signal:** **signal fwd:** adding a type touches 1 unit not N switches. **inv:** reader hop-count drops.
- **Contra:** *fwd:* only one site switches, or the type set is stable and small (dispatch is harder to read than a 2-arm switch — C4). *inv:* exists to undo exactly that over-application.
- **Dep·Rel:** Rel: T16, T15.

---

# FAMILY 5 — RESHAPE CONTROL / DATA FLOW
*Mechanism: re-route control or data so less crosses a boundary, work happens where it's cheap, and flow is linear/visible. Hosts the control-flow, performance-structure, data-model, and concurrency clusters.*

### [T22] Flatten Nesting with Guard Clauses / Decompose Conditional — CANONICAL⟨Fowler⟩ · **High**
- **Mech:** convert deep nested control flow into early-return guards and named sub-decisions, reducing the simultaneous conditions a reader must hold.
- **Detect:** nesting depth ≥4; arrow-shaped code; long boolean conditions [static][tool]. **Routes:** **Deep Nesting (≥4)** — re-grounded as *control-flow exceeding working-memory* (cognition), not a depth number per se.
- **Safe:** tests over the branches.
- **Do:** invert conditions to early-return; extract complex predicates into named booleans/functions; collapse redundant branches.
- **Keep:** **test-suite**; branch-equivalent transformation.
- **Blast:** statement–method / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** reshape-control · comprehension.
- **Impact→signal:** **signal:** max nesting depth drops; cyclomatic complexity falls.
- **Contra:** flattening that scatters a genuinely cohesive decision (rare); over-extraction (C4).
- **Dep·Rel:** Rel: T15.

### [T23] Remove a Pass-Through / Middle Man (Collapse an Empty Channel) — CANONICAL⟨Fowler: remove middle man / inline⟩ + BORROWED⟨info-flow⟩ · **Med**
- **Mech:** delete an indirection that carries no information transformation, so callers talk to the real provider directly.
- **Detect:** a class/method that only delegates; long message chains `a.b().c().d()` [static]. **Routes:** **Message Chains, Middle Man.**
- **Safe:** tests on the call paths.
- **Do:** inline the delegating member; let callers use the delegate directly (or, if the chain leaks structure, hide it behind a *meaningful* method — the opposite move, by judgment).
- **Keep:** **test-suite**.
- **Blast:** method–module / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** remove-abstraction · comprehension.
- **Impact→signal:** **signal:** delegation hop-count drops.
- **Contra:** the "middle man" is a deliberate facade/ACL hiding a volatile structure (then keep it — C7-adjacent).
- **Dep·Rel:** Rel: T16, T07.

### [T24] Batch an N+1 / Set-Orient a Per-Item Loop — BORROWED⟨databases: set orientation⟩ · **Med-High**
- **Mech:** replace per-item round-trips with one set-based operation, changing the interaction-count complexity class.
- **Detect:** a query/call inside a loop over results; O(N) round-trips where O(1) is possible [tool: query log / profiler]. **Routes:** performance-structure blind spot.
- **Safe:** characterization tests on outputs; capture current result set.
- **Do:** hoist the call out of the loop; fetch/operate in bulk (join, `IN`, batch API); reassemble in memory.
- **Keep:** **observational-equivalence on results** — but **observable timing changes (that's the point)**; if timing is contractual, treat as adjacency.
- **Blast:** method–service / med risk / moderate reversibility.
- **Para:** data/SQL, any I/O-bound code.
- **Dir·Why:** reshape-data-flow · performance.
- **Impact→signal:** **signal:** round-trip count drops from N to ~1; latency falls.
- **Contra:** N is provably tiny and stable; batching that loads unbounded memory.
- **Dep·Rel:** Rel: T25, M04.

### [T25] Hoist Invariant Work Out of a Loop — BORROWED⟨compiler: loop-invariant code motion⟩ · **High**
- **Mech:** move computation whose result doesn't change across iterations to before the loop.
- **Detect:** a pure/constant expression recomputed each iteration [static][tool]. **Routes:** performance-structure.
- **Safe:** tests; confirm the expression is loop-invariant and side-effect-free.
- **Do:** compute once into a local; reference inside the loop.
- **Keep:** **observational-equivalence** (invariance proof, as compilers do).
- **Blast:** statement–method / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** reshape-control · performance.
- **Impact→signal:** **signal:** per-iteration work drops; hot-loop time falls.
- **Contra:** the expression has a side-effect or is NOT actually invariant (correctness break).
- **Dep·Rel:** Rel: T24, T26.

### [T26] Memoize at a Referentially-Transparent Seam — BORROWED⟨compiler/FP: memoization + purity analysis⟩ · **Med**
- **Mech:** cache results of a pure function so repeated identical calls are cheap.
- **Detect:** an expensive pure function called repeatedly with recurring inputs [profiler]. **Routes:** performance-structure.
- **Safe:** **prove referential transparency first** (no hidden state/IO); tests.
- **Do:** wrap with a cache keyed on inputs; bound the cache; invalidate if inputs' world can change.
- **Keep:** **observational-equivalence** *iff* the function is pure (precondition is the whole game).
- **Blast:** method–service / med risk (cache = new state) / moderate reversibility.
- **Para:** all; safest in FP where purity is explicit.
- **Dir·Why:** add-abstraction (cache) · performance.
- **Impact→signal:** **signal:** recompute count drops; latency falls.
- **Contra:** the function is *not* pure (stale/incorrect results) — the classic over-application; unbounded cache → memory leak.
- **Dep·Rel:** Rel: T25.

### [T27] Normalize to Remove an Update Anomaly — BORROWED⟨databases: normalization 1NF–BCNF⟩ · **High**
- **Mech:** decompose a table by its functional dependencies so each fact lives once, eliminating update/insert/delete anomalies.
- **Detect:** repeated fact across rows; update-one-place-miss-another bugs; multi-valued columns [tool: schema audit][query]. **Routes:** data-model blind spot; "duplication" at the data layer.
- **Safe:** expand/contract migration (→M05); backfill plan; data audit.
- **Do:** split into dependency-aligned tables; add FKs; migrate data; repoint reads/writes.
- **Keep:** **observational-equivalence** via dual-read/dual-write during migration.
- **Blast:** system (shared data) / high risk / hard reversibility.
- **Para:** data/SQL.
- **Dir·Why:** add-structure (decompose) · reliability/changeability.
- **Impact→signal:** **signal:** update-anomaly incidents → 0; redundant fact storage drops.
- **Contra:** read-heavy paths where the joins cost too much — then T28 (deliberate denormalize).
- **Dep·Rel:** Dep: M05. Rel: T28 (inverse), T13.

### [T28] Deliberately Denormalize for Read Path — BORROWED⟨databases: denormalization tradeoff⟩ · **Med**
- **Mech:** reintroduce controlled redundancy to serve reads cheaply, *explicitly accepting* the write-time consistency cost.
- **Detect:** a hot read doing many joins; provable read/write ratio skew [profiler][query]. **Routes:** performance-structure at the data layer.
- **Safe:** define the consistency mechanism (trigger/materialized view/app dual-write) up front; tests.
- **Do:** add the redundant/precomputed structure; maintain it on write; route reads to it.
- **Keep:** **observational-equivalence on reads** if the maintenance is correct; **adds a consistency obligation**.
- **Blast:** module–system / high risk / moderate reversibility.
- **Para:** data/SQL.
- **Dir·Why:** **remove-normalization (inverse of T27)** · performance.
- **Impact→signal:** **signal:** read latency / join count drops.
- **Contra:** write-heavy or strong-consistency-critical data (skew risk) — this is the over-application of "make it fast."
- **Dep·Rel:** Rel: T27 (inverse), M04.

### [T29] Extract a Read Model (CQRS-lite) — BORROWED⟨DDD/CQRS⟩ · **Med**
- **Mech:** separate the write model (invariant-enforcing) from a purpose-built read model, so each is shaped for its job.
- **Detect:** one model contorted to serve both complex writes and divergent read shapes; read queries fighting the write schema [judgment]. **Routes:** data-model blind spot; low cohesion across read/write concerns.
- **Safe:** char-tests on both query and command paths.
- **Do:** introduce a read-optimized projection updated from the write model; route queries to it.
- **Keep:** **observational-equivalence** if projection stays in sync; eventual-consistency is a deliberate semantic change → adjacency if reads were strongly consistent.
- **Blast:** service–system / high risk / hard reversibility.
- **Para:** data/SQL, distributed.
- **Dir·Why:** add-structure (split) · changeability/performance.
- **Impact→signal:** **signal:** read-query complexity drops; write-path no longer constrained by read shapes.
- **Contra:** simple CRUD where one model suffices (massive over-engineering).
- **Dep·Rel:** Rel: T28, M04.

### [T30] Narrow Lock Scope — BORROWED⟨concurrency / OS⟩ · **Med**
- **Mech:** reduce the code region a lock protects to the minimal critical section, lowering contention while preserving the guarded invariant.
- **Detect:** a lock held across I/O or long computation; coarse "lock the whole method"; contention in profiles [tool: profiler/lock analysis][judgment]. **Routes:** concurrency blind spot.
- **Safe:** **identify the exact invariant the lock protects** (this is the hard part); concurrency tests / stress tests.
- **Do:** shrink the locked region to just the shared-state mutation; move I/O/computation outside; ensure no protected invariant straddles the new boundary.
- **Keep:** **test-suite + reasoning** — preservation here is *delicate*; behavior under races is the risk. Rung rarely above test-suite.
- **Blast:** method–service / **high risk** / moderate reversibility.
- **Para:** concurrent only.
- **Dir·Why:** reshape-control · performance/reliability.
- **Impact→signal:** **signal:** lock-hold time / contention drops; throughput rises.
- **Contra:** when the wider region is protecting a compound invariant — narrowing introduces a race (the classic over-application). Default to caution.
- **Dep·Rel:** Rel: T31, T32.

### [T31] Replace Shared Mutable State with Immutability or Message-Passing — BORROWED⟨concurrency / FP / actor model⟩ · **Med-High**
- **Mech:** remove the need for locking by making the shared data immutable, or by confining mutation behind a single message-processing owner.
- **Detect:** mutable state read/written by multiple threads; lock-heavy code; heisenbugs [judgment][tool]. **Routes:** concurrency blind spot; "shared mutable state."
- **Safe:** char-tests; stress/concurrency tests; map all access paths.
- **Do:** make the structure immutable (copy-on-write / persistent data structures); or route all mutation through one owner (actor/queue/single thread).
- **Keep:** **test-suite + reasoning**; immutability gives strong local reasoning (approaches type-proof in FP).
- **Blast:** module–service / **high risk** / hard reversibility.
- **Para:** concurrent; natural in FP, harder in mutable OO/procedural.
- **Dir·Why:** reshape-data-flow · reliability.
- **Impact→signal:** **signal:** data races / locks → 0; concurrency defects fall.
- **Contra:** hot paths where copying is too expensive (immutability has a cost); over-actor-ing simple code.
- **Dep·Rel:** Rel: T30, T32, T39.

### [T32] Make a Check-Then-Act Sequence Atomic — BORROWED⟨concurrency: atomicity⟩ · **Med-High**
- **Mech:** collapse a non-atomic read-decide-write into a single atomic operation, eliminating the race window.
- **Detect:** check-then-act / read-modify-write on shared state without a single guard (e.g., `if (!map.has(k)) map.put(k,..)`) [static][judgment]. **Routes:** concurrency blind spot; TOCTOU.
- **Safe:** stress tests that expose the race; characterize intended behavior.
- **Do:** use an atomic primitive (CAS, `computeIfAbsent`, transaction, single lock around the whole sequence).
- **Keep:** **test-suite + reasoning**; correctness is the invariant, timing/throughput may shift.
- **Blast:** method–service / **high risk** / moderate reversibility.
- **Para:** concurrent only.
- **Dir·Why:** reshape-control · reliability.
- **Impact→signal:** **signal:** TOCTOU defects → 0; race reproductions stop.
- **Contra:** single-threaded code (no benefit, adds overhead).
- **Dep·Rel:** Rel: T30, T31.

---

# FAMILY 6 — ISOLATE FAULT DOMAIN
*Mechanism: introduce a boundary so a failure, resource exhaustion, or foreign model cannot propagate. Several entries here are adjacency-tagged (they change failure behavior by design).*

### [T33] Bulkhead / Partition a Shared Resource — BORROWED⟨safety/reliability eng⟩ · **Med** · *adjacency: preserves-success / changes-failure*
- **Mech:** split a shared resource (pool, queue, cache) so exhaustion in one region can't starve unrelated features.
- **Detect:** one shared thread/connection pool whose saturation takes down everything; one slow dependency stalling all requests [judgment][tool: incident data]. **Routes:** distributed/reliability blind spot.
- **Safe:** load/failure tests; map the shared resource's consumers.
- **Do:** partition the resource per consumer class; cap each partition.
- **Keep:** happy path **observational-equivalence**; **failure behavior changes by design** (isolated degradation) → adjacency.
- **Blast:** service–system / high risk / moderate reversibility.
- **Para:** concurrent/distributed.
- **Dir·Why:** isolate · reliability.
- **Impact→signal:** **signal:** blast radius of a single dependency failure shrinks (fewer features affected per incident).
- **Contra:** simple single-tenant code (ops complexity for no benefit); over-partitioning wastes capacity.
- **Dep·Rel:** Rel: T34, T35.

### [T34] Introduce a Circuit Breaker / Timeout at a Dependency Edge — BORROWED⟨reliability eng⟩ · **Med** · *adjacency: preserves-success / changes-failure*
- **Mech:** wrap an unreliable dependency so repeated failures fail fast / degrade gracefully instead of cascading.
- **Detect:** unbounded waits on a remote call; retry storms; cascading timeouts [tool: incident/trace data]. **Routes:** distributed coupling blind spot.
- **Safe:** define fallback/degraded behavior; failure-injection tests.
- **Do:** add timeout + breaker (open on failure threshold, half-open probe); define fallback.
- **Keep:** happy path equivalent; **failure path deliberately changed** → adjacency.
- **Blast:** service–system / med-high risk / moderate reversibility.
- **Para:** distributed.
- **Dir·Why:** isolate · reliability.
- **Impact→signal:** **signal:** cascade incidents drop; tail latency bounded.
- **Contra:** in-process calls that can't fail that way; mis-tuned thresholds cause false trips (over-application).
- **Dep·Rel:** Rel: T33, M04.

### [T35] Introduce an Anti-Corruption Layer — BORROWED⟨DDD⟩ · **Med**
- **Mech:** insert a translating boundary so a foreign/legacy model cannot leak its concepts into your domain.
- **Detect:** an external/legacy model's types spreading through your core; your domain language polluted by another system's terms [judgment]. **Routes:** cross-context coupling; distributed coupling.
- **Safe:** characterize the integration's current behavior.
- **Do:** define your domain's interface; build an adapter translating to/from the foreign model at the edge; depend only on your interface internally.
- **Keep:** **test-suite** at the boundary (translation parity).
- **Blast:** module–service / med risk / moderate reversibility.
- **Para:** all; common at service boundaries.
- **Dir·Why:** isolate (+add abstraction) · changeability/reliability.
- **Impact→signal:** **signal:** foreign-type references in core → 0.
- **Contra:** a trivial, stable integration (translation overhead unjustified).
- **Dep·Rel:** Rel: T01, M01.

### [T36] Confine State to a Single Owner — BORROWED⟨actor model / thread confinement⟩ · **Med**
- **Mech:** make one component the sole owner of a piece of state; others interact only via messages/calls, eliminating shared-access hazards.
- **Detect:** the same state mutated from many places/threads; unclear ownership [judgment][static]. **Routes:** concurrency + socio-technical (ownership) blind spots.
- **Safe:** map all current writers; concurrency tests.
- **Do:** designate an owner; convert external writes to requests to the owner; make the state private.
- **Keep:** **test-suite + reasoning**.
- **Blast:** module–service / high risk / hard reversibility.
- **Para:** concurrent; also clarifies single-threaded ownership.
- **Dir·Why:** isolate · reliability/comprehension.
- **Impact→signal:** **signal:** number of writers → 1; ownership ambiguity resolved.
- **Contra:** state that's genuinely shared-read-only (immutability is simpler — T31).
- **Dep·Rel:** Rel: T31, T01.

---

# FAMILY 7 — MIGRATION OVER TIME (TIER-M METHODOLOGIES)
*Mechanism: achieve a large structural change while the system stays live, by decomposing it into individually behavior-preserving steps. Each carries a **completion obligation** — the dominant failure mode is stalling half-done.*

### [M01] Strangler-Fig Migration — CANONICAL/BORROWED⟨urban redevelopment⟩ · **High**
- **Mech:** grow the replacement behind a routing seam, redirect call paths incrementally, retire the old once traffic fully shifts.
- **Detect:** a large subsystem you must replace but can't stop or rewrite atomically; change-pain concentrated in a legacy core [judgment]. **Routes:** "Large Class/God Module" at system scale.
- **Safe:** an interceptable boundary (facade/router); per-slice parity tests.
- **Do:** facade → implement a slice behind it → route a fraction → verify parity → expand → delete old.
- **Keep:** parity tests at the router (old vs new) → **observational-equivalence per slice**; live behavior preserved throughout.
- **Blast:** system / high risk / moderate reversibility (per slice).
- **Para:** all.
- **Dir·Why:** relocate/replace · changeability.
- **Impact→signal:** **signal:** % traffic on new path → 100%; legacy LOC retired.
- **Contra:** no interceptable seam; change small enough for a direct refactor.
- **Completion-obl:** **delete the old path and the router** — a half-strangled system is two systems forever.
- **Dep·Rel:** Dep: T01. Rel: M03, T35.

### [M02] Expand / Contract (Parallel Change) — CANONICAL/BORROWED⟨schema migration⟩ · **High**
- **Mech:** add the new shape, support old+new simultaneously, migrate consumers/data, then remove the old — never a breaking flip.
- **Detect:** a structural change to a widely-used API/schema/format with consumers you can't change in lockstep (Hyrum's Law) [judgment]. **Routes:** any widely-depended structure.
- **Safe:** both contracts hold during expand; consumer inventory.
- **Do:** add new → dual-write/dual-read → backfill → migrate consumers → contract (delete old).
- **Keep:** during expand, both contracts satisfied → **observational-equivalence**; old consumers unaffected.
- **Blast:** service–system / med-high risk / moderate reversibility.
- **Para:** all; canonical for data/SQL and public APIs.
- **Dir·Why:** migrate · changeability.
- **Impact→signal:** **signal:** consumers on new shape → 100%; old surface removed.
- **Contra:** a single in-repo consumer (just change both atomically).
- **Completion-obl:** **complete the contract phase**; permanent dual surfaces are debt; dual-write skew if not atomic.
- **Dep·Rel:** Rel: T13, T27, M05.

### [M03] Branch by Abstraction — CANONICAL · **High**
- **Mech:** introduce an abstraction over the thing to be replaced, build the new implementation behind it, switch, then remove the abstraction if no longer needed.
- **Detect:** need to swap a large implementation while keeping mainline releasable [judgment]. **Routes:** large in-place replacement.
- **Safe:** the abstraction's contract tests (both impls must pass).
- **Do:** extract abstraction over current impl → new impl behind same abstraction → flip flag → delete old → optionally inline the abstraction.
- **Keep:** both impls satisfy the same tests → **observational-equivalence** at the seam.
- **Blast:** module–service / med-high risk / moderate reversibility.
- **Para:** all.
- **Dir·Why:** migrate (via temporary add-abstraction) · changeability.
- **Impact→signal:** **signal:** old impl deleted; flag removed.
- **Contra:** swap small/atomic enough for one commit (abstraction is overhead).
- **Completion-obl:** **remove the flag and dead impl**; the temporary abstraction must not become permanent cruft.
- **Dep·Rel:** Dep: T01. Rel: M01.

### [M04] Coarsen a Chatty Service Interface (Collapse Round-Trips) — BORROWED⟨distributed systems⟩ · **Med**
- **Mech:** restructure a cross-service interaction so one coarse call replaces many fine-grained round-trips, reducing temporal coupling across the network.
- **Detect:** N sequential remote calls to render one result; latency dominated by round-trips [tool: tracing]. **Routes:** distributed coupling blind spot.
- **Safe:** trace the current call sequence; contract tests; expand/contract the API (→M02).
- **Do:** design a coarse endpoint / batch API / BFF aggregation; migrate consumers via M02; retire fine-grained calls.
- **Keep:** **observational-equivalence on results** via M02; timing improves (intended).
- **Blast:** service–system / high risk / hard reversibility.
- **Para:** distributed.
- **Dir·Why:** reshape-data-flow (over network) · performance/reliability.
- **Impact→signal:** **signal:** round-trips per operation drop; p99 latency falls.
- **Contra:** premature coarsening that couples services that should stay independent (over-application — re-creates a distributed monolith).
- **Completion-obl:** retire the fine-grained surface.
- **Dep·Rel:** Dep: M02. Rel: T24, T35.

### [M05] Normalize-then-Selective-Denormalize (Data Re-Modeling Program) — BORROWED⟨databases⟩ · **Med**
- **Mech:** a staged data migration: normalize to a correct base, then *selectively* denormalize measured hot read paths — under expand/contract throughout.
- **Detect:** a data model with both anomaly bugs (under-normalized) and slow reads (needs targeted denormalization) [tool: schema audit + profiler]. **Routes:** data-model blind spot at scale.
- **Safe:** expand/contract per step (→M02); data audit + backfill; reversible migrations.
- **Do:** T27 to normalize → measure → T28 on the proven hot paths only → maintain consistency mechanisms.
- **Keep:** **observational-equivalence** via dual-read/write per migration step.
- **Blast:** system / high risk / hard reversibility.
- **Para:** data/SQL.
- **Dir·Why:** migrate · reliability+performance.
- **Impact→signal:** **signal:** anomaly incidents → 0 AND hot-read latency within target.
- **Contra:** denormalizing speculatively before measuring (the classic data over-application).
- **Completion-obl:** retire intermediate dual structures; document each denormalization's consistency contract.
- **Dep·Rel:** Dep: M02. Rel: T27, T28, T13, T14.

### [M06] Incident-Archaeology Refactor Program — INVENTED⟨failure analysis / FMEA⟩ · **Med**
- **Mech:** target refactoring by working backward from real incidents — fix the structural properties that *actually* preceded outages, not whatever a linter flags.
- **Detect:** a post-mortem corpus; recurring incident structures (temporal coupling, races, shared-resource exhaustion, error-swallowing) [tool: incident data][judgment]. **Routes:** prioritization across the whole catalog by *observed* harm.
- **Safe:** map each incident class to its enabling structure and the technique that repairs it.
- **Do:** rank structures by incident frequency×severity; apply the matching atomic techniques (T30–T36, T18) hotspot-first.
- **Keep:** per-technique preservation as applied.
- **Blast:** program-level / risk per technique / per technique.
- **Para:** all.
- **Dir·Why:** mixed · reliability.
- **Impact→signal:** **signal:** recurrence rate of the targeted incident class drops.
- **Contra:** thin/biased incident data (don't over-fit to one loud outage).
- **Completion-obl:** close the loop — verify recurrence actually fell.
- **Dep·Rel:** Rel: all reliability-family techniques.

---

# FAMILY 8 — PRESERVATION ENABLERS
*Mechanism: manufacture the safety net (the missing control loop) that makes every other technique safe. These are the make-safe gate; most other entries `Depend-on` one of these.*

### [T40] Introduce Characterization Tests (Golden Master) — CANONICAL⟨Feathers⟩ · **High**
- **Mech:** pin current observable behavior (even if "wrong") so any later transform's regressions are detectable — creates the missing feedback loop.
- **Detect:** code you must change with no/low coverage at the affected lines; risk = blast-radius ÷ coverage [tool: coverage]. **Routes:** (precondition for nearly everything).
- **Safe:** n/a — this *is* the safety mechanism.
- **Do:** capture representative inputs→outputs of current code (approval/golden-master tests); lock them; then refactor.
- **Keep:** establishes the **char-test** rung that other techniques build on.
- **Blast:** local / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** add safety · testability.
- **Impact→signal:** **signal:** affected-line coverage rises above the threshold you set.
- **Contra:** behavior already covered by strong specs (redundant); pinning incidental output → brittle masters; never sanctify characterized *bugs*.
- **Dep·Rel:** enables T01–T36.

### [T41] Sprout Method / Sprout Class — CANONICAL⟨Feathers⟩ · **High**
- **Mech:** add new behavior in a fresh, testable unit and call it from the untestable tangle, instead of editing in place.
- **Detect:** you must add logic to code with no seams/tests and can't safely change it [judgment]. **Routes:** legacy-code change without a safety net.
- **Safe:** test the new sprouted unit in isolation.
- **Do:** write the new code in a new method/class with tests; invoke it from the minimal touch-point in the old code.
- **Keep:** old code mostly untouched → **test-suite** on the new unit; minimal-edit preservation on the old.
- **Blast:** local / low risk / trivially reversible.
- **Para:** all.
- **Dir·Why:** add-abstraction · testability/changeability.
- **Impact→signal:** **signal:** new behavior is covered though the host wasn't.
- **Contra:** when the host code *can* be safely refactored first (sprouting then leaves the tangle).
- **Dep·Rel:** Rel: T42, T01.

### [T42] Wrap Method / Wrap Class — CANONICAL⟨Feathers; GoF Decorator⟩ · **High**
- **Mech:** interpose a wrapper at a boundary to add behavior without modifying the wrapped code.
- **Detect:** need to add cross-cutting behavior (logging, caching, validation) to code you shouldn't edit [judgment]. **Routes:** legacy modification; cross-cutting concerns.
- **Safe:** tests on the wrapper.
- **Do:** create a wrapper with the same interface; delegate to the original; add behavior before/after; substitute the wrapper at the seam.
- **Keep:** original unchanged → **type/compiler-proof** (same interface) + tests on added behavior.
- **Blast:** method–module / low risk / trivially reversible.
- **Para:** OO (decorator), FP (higher-order wrapping), procedural (shim).
- **Dir·Why:** add-abstraction · changeability/operability.
- **Impact→signal:** **signal:** cross-cutting behavior added with 0 edits to core.
- **Contra:** wrapper proliferation; when the concern truly belongs inside the core.
- **Dep·Rel:** Dep: T01 (seam). Rel: T09, T41.

### [T43] Lean on the Compiler — CANONICAL⟨Feathers⟩ · **High (typed) / N/A (dynamic)**
- **Mech:** make a deliberate breaking change and let the type checker enumerate every site that must be updated, driving the transform to completeness.
- **Detect:** a wide rename/signature/type change across many call-sites in a typed language. **Routes:** safe large-scale mechanical change.
- **Safe:** version control; a green build as the goal state.
- **Do:** change the declaration; compile; fix each reported error; repeat until clean.
- **Keep:** **type/compiler-proof** — the compiler guarantees no site is missed.
- **Blast:** module–service / low risk *in typed langs* / moderate reversibility.
- **Para:** **typed only**; dynamic langs lack this safety entirely → fall back to T40 + search (confidence drops to Low for the equivalent maneuver).
- **Dir·Why:** mechanical · changeability.
- **Impact→signal:** **signal:** green build = all sites updated.
- **Contra:** dynamic languages (no compiler to lean on); changes the compiler *can't* see (reflection, serialized data, string-keyed access).
- **Dep·Rel:** Rel: T15, T16, T21.

---

# §INDEX — TRIGGER INDEX (the basin, demoted to a lookup)
*The classic smells and SOLID letters are NOT the structure — they are entry points. Each routes to the mechanism that actually repairs it.*

| You observed (symptom) | Real mechanism issue | Go to |
|---|---|---|
| **SRP** violation / file>300 / fn>50 / class>10 methods / mixed concerns | low cohesion; missing chunk; wrong cut | T15, T02, T03, T22 |
| **OCP** violation / growing type-switch | modification spread per new case | T19 (fwd) |
| **LSP** violation / throwing/no-op overrides / instanceof-before-call | broken supertype totality/contract | T17, T12 |
| **ISP** violation / fat interface / stubbed members | interface surface ≠ usage | T07 |
| **DIP** violation / `new Concrete()` in logic / singletons | no substitution seam | T01, T42 |
| **Long Method** | exceeds working-memory chunking; missing abstraction | T15, T22 |
| **Long Parameter List (>4) / Data Clumps** | missing value concept; positional connascence | T21 |
| **Deep Nesting (≥4)** | control flow exceeds working memory | T22 |
| **Duplicated Code** | missing abstraction *or* load-bearing/diverging copies | T15 (if co-evolving) / leave (if diverging — C1) |
| **Magic Numbers** | unnamed concept | T15 |
| **Primitive Obsession** | missing type encoding an invariant | T12, T21, T10 |
| **Feature Envy / Inappropriate Intimacy** | coupling distance > 0 (directional) | T03 |
| **Shotgun Surgery / Divergent Change** | change-affinity scattered | T03 |
| **Message Chains / Middle Man** | empty channel / leaked structure | T23 |
| **Speculative Generality** | abstraction without realized variation | T16 |
| **Large Class / God Object** | low cohesion; multiple responsibilities | T02, T15, M01 |
| heisenbug / race / TOCTOU | concurrency hazard | T30–T32, T36 |
| update-anomaly / orphan rows | data-model invariant not enforced | T27, T13 |
| N+1 / slow hot read | interaction/structure cost | T24, T25, T26, T28 |
| cascading outage / retry storm | no fault isolation | T33, T34, M04 |
| "can't tell what it did" | no observability seam | T09 |
| foreign model leaking into core | no anti-corruption boundary | T35 |
| change needs N teams | Conway misalignment (process-edge) | *context §C* |

---

# §C — SOCIO-TECHNICAL STRUCTURE (CONTEXT, NOT CATALOGUED TECHNIQUES)
*Per the scope: these are real and high-impact but are org/process changes, not behavior-preserving code edits. Named for completeness; apply out-of-band.*
- **Conway-Alignment** ⟨Conway's Law⟩: reshape module boundaries to match team-communication boundaries (or run the *inverse-Conway maneuver*: reshape teams, let code follow). Trigger: a module perfect by every code metric yet sitting exactly on a team seam so every change needs 3 teams.
- **Ownership Re-boundary**: assign clear stewardship to orphaned code; reduce bus-factor by simplifying or documenting single-owner hotspots.
- **Refactoring Cadence / Fitness Function** ⟨control theory⟩: a *continuous* negative-feedback practice (CI gates, tracked complexity/coupling fitness functions) that damps drift — process, not a one-shot technique.
> These are **out of the behavior-preservation frame** (you can't unit-test an org change). High-impact, low-catalog-fit. Confidence Med, evidence largely qualitative.

---

# §USAGE — HOW A TEAM ADOPTS THIS
1. **Don't refactor by checklist.** Start from a real trigger — a smell you hit, a metric, *or an incident* — and use §INDEX to find the mechanism. Resist "the linter said so."
2. **Always pass the make-safe gate first.** If the target isn't covered, do T40/T41/T42 before anything else. No safety net → no refactor.
3. **Prefer low-blast, reversible, high-frequency targets.** Use git churn (T03/[history]) to find where structure is degrading fastest; that's where refactoring pays.
4. **Honor the contraindications.** Most smells are below the ROI threshold (C3). The catalog's *where-NOT* is as important as its *what*.
5. **Verify and measure.** Use the entry's preservation rung to confirm behavior, and its falsifiable signal to confirm value. If the signal didn't move, revert — you fixed the wrong thing.
6. **Tier-M needs a finish plan.** Never start a migration without committing to its completion obligation; a half-done migration is worse than none.
7. **Hold the tensions, don't resolve them.** Duplication can be load-bearing (C1); an unused seam can be a valuable option (C7). These are judgment calls the catalog surfaces, not eliminates.

---

# §GAPS — KNOWN GAPS & CAVEATS (state these honestly)
- **No global "impact" ranking and no single refactor score.** Impact is conditional on churn, coverage, ownership, and change-profile. A scalar score *will* be gamed (Goodhart) — refused by design.
- **Cross-paradigm unity is partial.** Mechanisms transfer across OO/FP/procedural/SQL/concurrent; *guarantees* don't. Typed→dynamic degrades the preservation rung (e.g., T12, T43). Each entry carries a degradation note; no universal-applicability claim.
- **Cross-discipline transfers are re-grounded or killed, never asserted by analogy.** Rejected-as-techniques (kept only as framing/motivation): *broken-windows, entropy, coupling-as-mutual-information, Alexander's "quality without a name," SMED, exaptation/neutral-mutation re-labels.* They carried the word, not an executable mechanism.
- **Test-dependence.** Most entries presuppose the make-safe gate. Untested/untestable codebases (heavy I/O, nondeterminism) get routed through enablers first and otherwise receive a degraded catalog. Honest limited-applicability profile: an untyped, untested, single-owner ML/glue pipeline gets thin *direct* value until seams/tests exist.
- **Refactor/rewrite boundary.** In-scope iff behavior is preserved at a stated boundary AND decomposable into individually-preserving steps. Moves that change *failure* behavior (T08, T13-writes, T33, T34, T29-eventual) are **adjacency-tagged** — included but flagged as edge-of-refactoring.
- **Detection tooling varies.** Several signals need dependency-graph (T02), call-site (T07), git-history (T03), profiler (T24–T28), or incident-data (M06) tooling; entries tag which, and which remain judgment.
- **Socio-technical and cadence work is context, not catalogued** (§C) — real but outside the behavior-preservation frame.
- **Coverage is broad, not exhaustive.** ~37 atomic + 6 methodologies span the eight mechanism families and all mandatory blind-spot domains, but specific stacks (UI rendering, ML training, embedded/real-time) will need stack-specific entries grafted onto these mechanisms.

---

# §PRIORITY — "HIGHEST-IMPACT FIRST" HEURISTIC
**Confidence: Med** (this is the most contestable claim in the document — impact is context-dependent).

Order candidate refactors by, roughly:

> **priority ≈ (change-frequency × blast-radius-of-current-pain × incident-association) ÷ (refactor-cost × regression-risk)**, gated by *coverage exists or can be cheaply added*.

Operationally, in order:
1. **Establish the missing control loop** where you're about to work (T40-family). Highest leverage; everything else depends on it.
2. **Fix structures with incident history** (M06 → T30–T36, T18). Empirically the properties that actually cause outages — concurrency, temporal coupling, fault propagation, swallowed errors — beat cosmetic smells.
3. **Refactor churn×complexity hotspots** (T03/[history] + T15/T22). High change-frequency amortizes the cost (economics: high debt-interest).
4. **Encode invariants that keep breaking** (T12/T13/T10). Converts a class of recurring bugs to impossible.
5. **Everything else is largely below the ROI line** — apply opportunistically while already in the code, honoring contraindications.

**Goodhart risk (explicit):** *any* metric here — coverage %, complexity, churn, "smell count" — becomes harmful the moment it's a target rather than a signal. Teams will hit the number while missing the mechanism (e.g., extract-method-spam to lower function length, or characterization-test theater that pins nothing). **Mitigation:** treat these as *routing signals to a mechanism*, never as success metrics; measure value by the per-technique falsifiable signal (did the *specific* pain — the race, the cascade, the N-site change — actually go away?), not by the aggregate. Never publish a single "code health score."

---

*End of taxonomy. Built mechanism-first across 9 turns: contract → default-basin map → 17-domain source dictionary → 15-lens sweep → transposition engine (with a rejection rule that killed shallow analogies) → candidate structures → red-team audit → design lock → this synthesis. Escape-test self-check: entries T03, T09, T10, T11, T30–T36, T24–T29 have triggers no Fowler move and no SOLID letter can name — the catalog has left the default basin.*



---
