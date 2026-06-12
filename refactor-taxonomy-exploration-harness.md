# Latent-Space Exploration Harness for: High-Impact Code Refactor Methodologies & Techniques (Cross-Discipline Taxonomy)

## Brief commentary

This harness drives a 9-turn latent-space exploration whose *deliverable* is a cross-discipline, actionable taxonomy of refactor techniques — not a one-shot answer. The architecture (frame → default basin → source-domain dictionary → lens sweep → transposition engine → candidate structures → adversarial audit → compression restart → final synthesis) is preserved verbatim because it fits this topic well: refactoring is a field with a strong "default basin" (Fowler's catalog + SOLID + code-smell lists) that the exploration must deliberately escape to reach genuinely cross-disciplinary, mechanism-level techniques.

Topic-specific adaptations I made:

- **Default basin is named explicitly** as the Fowler/Beck refactoring catalog, the SOLID acronym, and the "code smells" checklist (incl. the seed list the user supplied: long methods, long params >4, deep nesting ≥4, duplication, magic numbers, primitive obsession, feature envy). Turn 2 maps this well so later turns can climb out of it.
- **Lenses are pre-selected for software-structure relevance**: control loops (tests/CI as feedback), information flow (coupling/cohesion as mutual information), boundaries/interfaces (seams, ports & adapters, ISP), lifecycle/entropy (software rot, decay), adversarial dynamics (Hyrum's Law, churn, Goodhart on metrics), ecology/evolution (code as evolving organism, vestigial code), formal models (behavior-preserving transformation, semantics), human factors (cognitive load, comprehension), institutions/governance (Conway's Law, ownership), markets (technical debt as a financial/options instrument), safety engineering (blast radius, regression risk, Swiss-cheese), design theory (patterns, modularity, Alexander), historical analogy, and failure analysis.
- **Source domains are pre-seeded** beyond software: compiler/IR transformation theory, database normalization, manufacturing/Lean (waste, kaizen, poka-yoke), medicine (diagnosis vs. treatment, triage, surgery, "first do no harm"), graph/network science (dependency graphs, modularity), information theory, economics, cognitive science, urban planning/civil architecture, and control theory.
- **The final schema is engineered for actionability**: every technique carries a *detection signal* (the smell/violation that triggers it), the *underlying mechanism* it repairs (not the surface symptom), *preconditions* (e.g., test coverage, characterization tests, seams), the *transformation mechanics*, a *behavior-preservation guarantee*, *blast radius / regression risk*, *impact estimate*, *measurable before/after signal*, *over-application failure mode*, *confidence level*, and *provenance* (canonical / borrowed-from-domain / approximation / invented name).
- **Anti-premature-collapse guards** are written into every turn ("do NOT write the final taxonomy yet"), and mechanism-over-label discipline plus explicit confidence markers are required throughout.

Two interchangeable harnesses follow. **Set A** assumes one continuous chat (each turn sees prior turns). **Set B** is paste-forward: you paste the previous turn's output, then paste the next prompt directly beneath it; prompts 2–9 refer to "the content above" as the working state. No paste placeholders appear in either set.

A persistent **WORKING STATE** block is maintained from Turn 1 onward and carried/updated each turn so the exploration accumulates rather than drifts.

---

# Set A — Single-Chat 9-Turn Harness

> Run all nine in one continuous chat. Each prompt assumes every prior turn is visible. Do not paste anything.

## Prompt 1 — Frame & Contract

```text
You are my research partner for a multi-turn exploration. Over the next 9 turns we will
build a cross-discipline, actionable TAXONOMY of high-impact code refactoring
methodologies and techniques. The end deliverable (much later) is a practitioner-usable
catalog of refactor techniques that can be applied to a real codebase, where each
technique is grounded in mechanism — not just a named smell.

This is TURN 1 of 9. Do NOT produce the taxonomy yet. Today we only set the contract.

Produce these sections:

1. OBJECTIVE — one paragraph: what the final taxonomy must let a practitioner do.
2. NON-OBJECTIVES — what we are deliberately NOT building (e.g., not a style guide, not a
   linter ruleset dump, not a language tutorial, not a rewrite-vs-refactor debate).
3. SCOPE & BOUNDARIES — code-level vs. architecture-level vs. process-level refactoring;
   which we include, which we touch only at the edges. Languages/paradigms in scope
   (OO, functional, procedural, data/SQL, concurrent). Greenfield excluded; we target
   existing code.
4. DEFINITIONS — define precisely: "refactoring" (behavior-preserving transformation),
   "code smell", "design violation", "technique", "methodology", "mechanism" vs.
   "symptom", "impact", "blast radius". Flag where industry usage is contested.
5. INCLUSION / EXCLUSION RULES — a concrete test for whether something belongs in the
   taxonomy (e.g., must change internal structure while preserving observable behavior;
   must have a detectable trigger; must have a defined transformation).
6. SUCCESS CRITERIA — how we will know the final taxonomy is good (coverage, mechanism
   grounding, actionability, non-overlap, measurability).
7. EPISTEMIC CAVEATS — where evidence is strong (canonical refactoring literature) vs.
   weak (cross-discipline analogies, impact claims, "highest-impact" rankings).
8. WORKING STATE v1 — a compact, copy-pasteable block titled "WORKING STATE" containing:
   open questions, provisional definitions, candidate scope edges, and a TODO list for
   turns 2–9. Keep it terse; it will grow each turn.

Constraints: prefer mechanism-level reasoning over labels. Mark confidence (High/Med/Low)
on any contestable claim. Breadth over polish. Do not synthesize the final structure yet.
```

## Prompt 2 — Default Basin (the gravitational well to escape)

```text
Continue the same exploration using all prior context. This is TURN 2 of 9.

Map the DEFAULT BASIN: the obvious, conventional way practitioners already think about
refactoring, so we know exactly which conceptual well later turns must climb out of.

Do this:

1. Describe the canonical default mental model in software, explicitly naming its pillars:
   - The Fowler/Beck refactoring catalog (Extract Method, Move Method, Replace
     Conditional with Polymorphism, etc.) and the "red-green-refactor" loop.
   - The SOLID acronym (S/O/L/I/D) as the dominant design-principle lens, including the
     detection heuristics commonly used:
       S — files >300 lines, functions >50 lines, classes >10 methods, mixed concerns.
       O — switch / if-else chains keyed on a type/enum that grow per new case.
       L — overrides that throw "not implemented", type checks before calls, no-op overrides.
       I — interfaces >10 members, implementors stubbing unused methods, fat services.
       D — direct `new Concrete()` in business logic, hardcoded singletons, missing seams.
   - The standard code-smell checklist: long methods, long parameter lists (>4), deep
     nesting (≥4 levels), duplicated blocks, magic numbers, primitive obsession,
     feature envy.

2. State WHY this basin is sticky: what makes these the defaults (tooling, books,
   interviews, linters), and what they optimize for.

3. Identify the BLIND SPOTS of the default basin — what this framing systematically
   misses (e.g., concurrency smells, data-model smells, temporal/lifecycle smells,
   socio-technical/ownership smells, performance-structure tradeoffs, distributed-systems
   coupling, observability gaps).

4. Articulate the "gravitational pull": the specific failure mode we must avoid — i.e.,
   that the final taxonomy just re-lists Fowler + SOLID + the smell checklist with new
   wording.

Do NOT yet propose new domains or the final structure. Update and reprint the full
WORKING STATE block (now v2): add "default basin pillars", "named blind spots", and a
note on what escape would look like. Mark confidence on each blind-spot claim.
```

## Prompt 3 — Broad Source-Domain & Evidence Dictionary

```text
Continue the same exploration using all prior context. This is TURN 3 of 9.

Build a BROAD SOURCE-DOMAIN DICTIONARY. Cast wide across disciplines for concepts,
mechanisms, and cases that could inform refactoring — WITHOUT synthesizing them into a
structure yet. Capture raw material.

For EACH source domain below, extract 3–8 transferable concepts. For each concept give:
the concept name, its native mechanism (one line), and a one-line "why it might map to
refactoring" note. Keep it as a dictionary, not prose.

Source domains (cover all; add others if strong):
- Software refactoring canon (beyond Fowler: Kerievsky "Refactoring to Patterns",
  Feathers "Working Effectively with Legacy Code" — seams, characterization tests).
- Design theory & patterns (GoF, GRASP, Clean/Hexagonal/Ports-&-Adapters, DDD bounded
  contexts/aggregates, Christopher Alexander pattern languages).
- Compiler / IR theory (semantics-preserving transformations, SSA, dead-code elimination,
  inlining, loop transforms, equivalence).
- Database / data modeling (normalization forms, denormalization tradeoffs, schema
  migration, referential integrity).
- Graph & network science (dependency graphs, modularity, centrality, cut sets,
  feedback cycles, fan-in/fan-out).
- Information theory (coupling/cohesion as mutual information, entropy, compression,
  channel capacity).
- Control theory & system dynamics (feedback loops, stocks/flows, debt accumulation,
  damping, instability).
- Manufacturing / Lean / TPS (waste types, kaizen, poka-yoke, jidoka, standard work,
  WIP limits).
- Medicine (diagnosis vs. treatment, triage, surgery vs. medication, contraindications,
  "first do no harm", iatrogenic harm).
- Safety / reliability engineering (failure modes, Swiss-cheese, blast radius,
  fault isolation, defense in depth).
- Economics & finance (technical debt, sunk cost, real options, amortization, interest).
- Cognitive science / human factors (working memory limits, chunking, cognitive load,
  comprehension, code as communication).
- Ecology / evolution (selection pressure, vestigial structures, mutation, fitness
  landscapes, co-evolution).
- Urban planning / civil architecture (broken windows, zoning, infrastructure, brownfield
  redevelopment).
- Institutions / governance (Conway's Law, ownership, code review, conventions, drift).

Rules: do NOT rank, merge, or build a taxonomy. Flag any concept you are unsure transfers
(confidence Low) so the audit turn can test it. Update and reprint WORKING STATE (v3):
append a "domain dictionary index" (domain → concept count) and any new open questions.
```

## Prompt 4 — Latent Lens Sweep

```text
Continue the same exploration using all prior context. This is TURN 4 of 9.

Run a LATENT LENS SWEEP: reinterpret "what makes a refactoring high-impact" through
multiple NON-ISOMORPHIC lenses. Each lens should reveal techniques or trigger-signals the
default basin (Turn 2) hides. Do not collapse lenses into each other.

For each lens, produce: (a) the reframing of refactoring under that lens in 1–2 lines;
(b) 2–4 candidate technique-seeds or detection-signals it surfaces; (c) at least one
thing this lens sees that SOLID/smells miss.

Lenses (cover all):
- Control loops — tests/CI/coverage as the feedback that makes refactoring safe; missing
  loops as the real defect.
- Information flow — coupling/cohesion as data/control flow; refactor as re-routing
  information to reduce cross-boundary leakage.
- Boundaries / interfaces — seams, ports & adapters, ISP, anti-corruption layers; where
  to cut.
- Lifecycle / time / entropy — software rot, decay rate, churn hotspots, age of code,
  "where is structure degrading fastest".
- Adversarial dynamics — Hyrum's Law (implicit contracts), Goodhart's Law (metrics gamed),
  shotgun-surgery as an attack surface, churn as adversary.
- Ecology / evolution — code as evolving population; vestigial/dead code; refactor as
  selection pressure; fitness landscape of a module.
- Formal models — behavior preservation, observational equivalence, invariants,
  pre/postconditions; when a transform is provably safe.
- Human factors / cognitive — comprehension load, chunk size, surprise/astonishment,
  naming as compression; refactor to fit working memory.
- Institutions / governance — Conway's Law, ownership boundaries vs. module boundaries,
  bus factor, review friction.
- Markets / economics — debt interest rate per module, refactor ROI, option value of a
  seam, where NOT to refactor.
- Safety engineering — blast radius, regression risk, fault isolation, reversibility of a
  refactor.
- Design theory — modularity, pattern-fit, missing abstraction vs. premature abstraction.
- Historical analogy — brownfield redevelopment, strangler-fig migrations, normalization
  history.
- Failure analysis — post-incident structural causes; which structural smells precede
  outages/bugs.

Rules: NO synthesis, NO final structure. Where a lens produces a technique-seed already in
the domain dictionary, cross-reference it. Mark Low confidence on speculative seeds.
Update and reprint WORKING STATE (v4): append "lens → top seeds" and any contradictions
between lenses (these are gold for the audit).
```

## Prompt 5 — Transposition / Abstraction Engine

```text
Continue the same exploration using all prior context. This is TURN 5 of 9.

Build the TRANSPOSITION ENGINE: explicit rules for translating concepts from the source
domains (Turn 3) and lenses (Turn 4) into concrete refactoring techniques — WITHOUT
shallow analogy. The goal is mechanism-preserving transfer.

Part A — Define the transposition protocol. For a borrowed concept to become a candidate
refactoring technique it must carry over ALL of:
  1. Causal mechanism (what actually does the work, not the metaphor).
  2. Detection signal (the observable code/process condition that triggers it).
  3. Boundary conditions (when it applies; when it does NOT — contraindications).
  4. Intervention logic (the actual transformation steps on code).
  5. Behavior-preservation story (how observable behavior stays intact).
  6. Failure/over-application mode (what breaks if mis-applied).
State a rejection rule: if a transfer survives only as metaphor (carries the word but not
the mechanism), tag it REJECTED — SHALLOW and explain why.

Part B — Apply the protocol to 12–20 of the strongest concept/lens seeds so far. For each,
produce a candidate technique record with the six fields above, plus a provisional name
and a provenance tag: CANONICAL (exists in refactoring literature), BORROWED (faithful
transfer from another domain), APPROXIMATION (lossy transfer), or INVENTED (new
taxonomy-specific coinage).

Rules: do NOT yet organize these into a taxonomy or rank them. Keep records atomic so the
audit can attack them individually. Mark confidence per record. Flag any record whose
detection signal is vague or unmeasurable. Update and reprint WORKING STATE (v5): append a
"candidate technique inventory" index (name → provenance → confidence).
```

## Prompt 6 — Candidate Organizing Structures

```text
Continue the same exploration using all prior context. This is TURN 6 of 9.

Generate CANDIDATE STRUCTURES for the final taxonomy. We are choosing how to organize the
technique inventory — not writing the taxonomy itself yet.

Produce 4–6 distinct candidate organizing schemes. Examples to consider (invent better
ones if you can):
  - By smell/violation type (the default-basin axis: SOLID + smells).
  - By mechanism repaired (coupling, cohesion, abstraction-level, control-flow,
    data-model, lifecycle/entropy, feedback-loop).
  - By scope/altitude (statement → method → class → module → service → system).
  - By blast radius / risk tier (local & reversible → wide & risky).
  - By workflow phase (detect → make-safe (tests/seams) → transform → verify → measure).
  - By driving force / "why" (comprehension, changeability, testability, performance,
    reliability, ownership).
  - A matrix/faceted scheme combining two axes.

For EACH candidate structure, evaluate on:
  - Explanatory power (does it reveal mechanism, or just sort symptoms?).
  - Practical usefulness (can a dev find the right technique fast at the moment of need?).
  - Extensibility (can new techniques/domains slot in without breaking it?).
  - Blind spots (what does this axis hide?).
  - Distortion risk (does it force-fit techniques or create false neighbors?).
  - Coverage of the cross-discipline material (or does it collapse back into the basin?).

End with a comparison table and a provisional recommendation (with confidence), but do NOT
commit yet — Turn 8 makes the final choice after the audit. Update and reprint WORKING
STATE (v6): append "candidate structures + scores" and the leading candidate.
```

## Prompt 7 — Adversarial Audit

```text
Continue the same exploration using all prior context. This is TURN 7 of 9.

RED-TEAM everything so far: the candidate structures (Turn 6), the technique inventory
(Turn 5), the lens seeds (Turn 4), and the domain dictionary (Turn 3). Be harsh. Your job
is to find weaknesses before we commit.

Audit against ALL of these failure classes; for each finding give: the item, the problem,
severity (High/Med/Low), and a proposed fix (merge / split / drop / re-ground / re-scope):

1. Symptoms mislabeled as mechanisms (e.g., "long method" treated as a cause vs. a
   surface signal of a missing abstraction).
2. Duplicate or overlapping concepts (e.g., feature envy vs. inappropriate intimacy vs.
   coupling — collapse or distinguish with a sharp test).
3. Missing domains or missing technique classes (concurrency, distributed coupling, data
   migration, observability, performance-structure, security-relevant structure,
   socio-technical/ownership).
4. Weak/shallow analogies that should be REJECTED (carry metaphor, not mechanism).
5. Scale sensitivity (technique works at function scale but not service scale, or vice
   versa).
6. Hidden assumptions (assumes test coverage exists, assumes a typed/OO language, assumes
   single-team ownership, assumes monorepo).
7. Boundary ambiguity (where does refactoring end and rewrite/redesign begin?).
8. Temporal blind spots (one-shot vs. continuous; churn; decay; migration over time).
9. Incentive blind spots (why teams won't apply a technique; Goodhart on any metric we
   propose).
10. Measurement problems (is the detection signal actually observable/tool-detectable? is
    "impact" falsifiable or hand-wavy?).
11. Operational uselessness (technique too abstract to act on at 2pm in a real PR).
12. Cases where the taxonomy/structure would FAIL outright (give concrete codebases or
    scenarios).

Do NOT rewrite the taxonomy or pick the final structure here. Output a prioritized findings
list and a "kill / merge / keep / re-ground" recommendation per affected item. Update and
reprint WORKING STATE (v7): append "audit findings (ranked)" and a "must-fix before
synthesis" shortlist.
```

## Prompt 8 — Compression Restart

```text
Continue the same exploration using all prior context. This is TURN 8 of 9.

COMPRESSION RESTART. Discard noisy exploration residue and lock the design for the final
artifact. Still do NOT write the full taxonomy.

Produce:

1. CHOSEN ORGANIZING PRINCIPLE — pick one structure (or a defined hybrid) from Turn 6 as
   revised by the Turn 7 audit. Justify in 3–5 lines on explanatory power + actionability.
2. FINAL SCHEMA — the exact record format every technique entry will use. Require these
   fields (refine names if better):
     - Technique name (+ provenance tag: CANONICAL / BORROWED / APPROXIMATION / INVENTED)
     - Category / position in the chosen structure
     - Detection signal(s) — observable, ideally tool-detectable trigger
     - Mechanism repaired — the underlying structural cause (NOT the symptom)
     - Preconditions / make-safe steps (tests, characterization tests, seams)
     - Transformation mechanics — concrete steps
     - Behavior-preservation guarantee — why observable behavior is unchanged
     - Blast radius / regression risk + reversibility
     - Expected impact + a measurable before/after signal
     - Over-application / contraindication (when NOT to do it)
     - Confidence (High/Med/Low) and dependencies on other techniques
3. MERGE / SPLIT RULES — how to decide when two candidate techniques are one entry vs.
   two; how to handle multi-scale techniques.
4. MANDATORY COVERAGE LIST — the categories/domains the final artifact MUST include
   (carry forward the audit's "missing domains": concurrency, data-model, lifecycle/churn,
   distributed coupling, observability, socio-technical, plus the full SOLID + smell set
   re-grounded by mechanism).
5. KNOWN GAPS & CAVEATS — what the taxonomy will NOT cover and where confidence is low
   (impact rankings, cross-discipline transfers, language-specific applicability).
6. ORDERING & SIZE TARGET — how entries are ordered and roughly how many.

Reprint a final, cleaned WORKING STATE (v8) that the synthesis turn can consume directly:
chosen structure, schema, coverage list, merge rules, known gaps.
```

## Prompt 9 — Final Synthesis

```text
Continue the same exploration using all prior context. This is TURN 9 of 9 — synthesis.

Now WRITE THE FINAL ARTIFACT: the cross-discipline, actionable taxonomy of high-impact
code refactoring techniques, using exactly the organizing principle and schema locked in
the Turn 8 WORKING STATE (v8), respecting its merge/split rules, mandatory coverage list,
and known gaps.

Requirements:
- Open with a 1-paragraph reader's guide: how to navigate the taxonomy and how to use it
  at the moment of need (detect → make-safe → transform → verify → measure).
- Present every technique as a record in the locked schema. Ground each in MECHANISM, not
  the surface label. Re-ground the SOLID violations and the classic smells (long method,
  long params >4, deep nesting ≥4, duplication, magic numbers, primitive obsession,
  feature envy) by the mechanism each repairs — do not just relist them.
- Include the cross-discipline techniques surfaced via the lens sweep and transposition
  engine, each tagged BORROWED / APPROXIMATION / INVENTED with its source domain named.
- Cover the mandatory list: SOLID (re-grounded), core smells (re-grounded), plus
  concurrency, data-model, lifecycle/churn, distributed coupling, observability, and
  socio-technical/ownership structure.
- For each technique, keep the detection signal observable and the contraindication
  explicit (where NOT to apply it).
- Provide confidence markers throughout, and a clear legend distinguishing CANONICAL terms
  from BORROWED terms, APPROXIMATIONS, and INVENTED taxonomy-specific names.
- Close with: usage guidance (how a team adopts this), the known gaps/caveats from Turn 8,
  and a short "highest-impact first" priority heuristic with its confidence and its
  Goodhart risk.

Output the taxonomy as a single, self-contained, copy-pasteable document. This is the
deliverable, so it may be long; favor completeness and mechanism clarity over brevity.
```

---

# Set B — Paste-Forward 9-Turn Harness

> Run each turn in a separate message or session. Paste the previous turn's full output first, then paste the next prompt directly beneath it. Prompts 2–9 treat "the content above" as the current working state. No paste placeholders are used.

## Prompt 1 — Frame & Contract

```text
You are my research partner for a multi-turn exploration carried across separate messages.
The end deliverable (much later) is a cross-discipline, actionable TAXONOMY of high-impact
code refactoring methodologies and techniques — a practitioner-usable catalog where each
technique is grounded in MECHANISM, not just a named smell, and can be applied to a real
existing codebase.

This is TURN 1 of 9. Do NOT produce the taxonomy yet. Today we only set the contract.

Produce these sections:

1. OBJECTIVE — one paragraph: what the final taxonomy must let a practitioner do.
2. NON-OBJECTIVES — what we are NOT building (not a style guide, not a linter dump, not a
   language tutorial, not a rewrite-vs-refactor debate).
3. SCOPE & BOUNDARIES — code-level vs. architecture-level vs. process-level refactoring;
   what's in vs. edge-only. Paradigms in scope (OO, functional, procedural, data/SQL,
   concurrent). Greenfield excluded; target existing code.
4. DEFINITIONS — define precisely: "refactoring" (behavior-preserving transformation),
   "code smell", "design violation", "technique", "methodology", "mechanism" vs.
   "symptom", "impact", "blast radius". Flag contested usage.
5. INCLUSION / EXCLUSION RULES — a concrete test for taxonomy membership (must change
   internal structure while preserving observable behavior; must have a detectable
   trigger; must have a defined transformation).
6. SUCCESS CRITERIA — coverage, mechanism grounding, actionability, non-overlap,
   measurability.
7. EPISTEMIC CAVEATS — strong evidence (canonical refactoring literature) vs. weak
   (cross-discipline analogies, impact/"highest-impact" claims).
8. WORKING STATE v1 — a compact, copy-pasteable block titled "WORKING STATE" with: open
   questions, provisional definitions, candidate scope edges, and a TODO list for turns
   2–9. Keep it terse; it will grow each turn.

Constraints: prefer mechanism-level reasoning over labels. Mark confidence (High/Med/Low)
on contestable claims. Breadth over polish. Do not synthesize the final structure yet.
End your output with the full WORKING STATE block so it can be carried forward.
```

## Prompt 2 — Default Basin (the gravitational well to escape)

```text
The content above is the current working state of this exploration. This is TURN 2 of 9.
Build on it; treat its WORKING STATE block as authoritative.

Map the DEFAULT BASIN: the obvious, conventional way practitioners already think about
refactoring, so later turns know which conceptual well to climb out of.

Do this:

1. Describe the canonical default mental model in software, naming its pillars:
   - The Fowler/Beck refactoring catalog (Extract Method, Move Method, Replace
     Conditional with Polymorphism, etc.) and "red-green-refactor".
   - The SOLID acronym, including common detection heuristics:
       S — files >300 lines, functions >50 lines, classes >10 methods, mixed concerns.
       O — switch / if-else chains keyed on a type/enum that grow per new case.
       L — overrides that throw "not implemented", type checks before calls, no-op overrides.
       I — interfaces >10 members, implementors stubbing unused methods, fat services.
       D — direct `new Concrete()` in business logic, hardcoded singletons, missing seams.
   - The standard smell checklist: long methods, long parameter lists (>4), deep nesting
     (≥4 levels), duplicated blocks, magic numbers, primitive obsession, feature envy.

2. State WHY this basin is sticky (tooling, books, interviews, linters) and what it
   optimizes for.

3. Identify its BLIND SPOTS — what it systematically misses (concurrency, data-model,
   temporal/lifecycle, socio-technical/ownership, performance-structure, distributed
   coupling, observability).

4. Name the "gravitational pull" we must avoid: a final taxonomy that just re-lists Fowler
   + SOLID + the smell checklist in new words.

Do NOT propose new domains or the final structure yet. Reprint the full WORKING STATE
block, updated to v2: add "default basin pillars", "named blind spots", and what escape
looks like. Mark confidence on each blind-spot claim. End with the updated WORKING STATE.
```

## Prompt 3 — Broad Source-Domain & Evidence Dictionary

```text
The content above is the current working state of this exploration. This is TURN 3 of 9.
Treat its WORKING STATE block as authoritative and extend it.

Build a BROAD SOURCE-DOMAIN DICTIONARY. Cast wide across disciplines for concepts,
mechanisms, and cases that could inform refactoring — WITHOUT synthesizing into a structure
yet. Capture raw material.

For EACH source domain below, extract 3–8 transferable concepts. For each concept give:
the concept name, its native mechanism (one line), and a one-line "why it might map to
refactoring" note. Dictionary form, not prose.

Source domains (cover all; add others if strong):
- Refactoring canon beyond Fowler (Kerievsky "Refactoring to Patterns"; Feathers "Working
  Effectively with Legacy Code" — seams, characterization tests).
- Design theory & patterns (GoF, GRASP, Clean/Hexagonal/Ports-&-Adapters, DDD bounded
  contexts/aggregates, Christopher Alexander pattern languages).
- Compiler / IR theory (semantics-preserving transforms, SSA, dead-code elimination,
  inlining, loop transforms, equivalence).
- Database / data modeling (normalization forms, denormalization tradeoffs, schema
  migration, referential integrity).
- Graph & network science (dependency graphs, modularity, centrality, cut sets, cycles,
  fan-in/fan-out).
- Information theory (coupling/cohesion as mutual information, entropy, compression,
  channel capacity).
- Control theory & system dynamics (feedback loops, stocks/flows, debt accumulation,
  damping, instability).
- Manufacturing / Lean / TPS (waste types, kaizen, poka-yoke, jidoka, standard work,
  WIP limits).
- Medicine (diagnosis vs. treatment, triage, surgery vs. medication, contraindications,
  "first do no harm", iatrogenic harm).
- Safety / reliability engineering (failure modes, Swiss-cheese, blast radius, fault
  isolation, defense in depth).
- Economics & finance (technical debt, sunk cost, real options, amortization, interest).
- Cognitive science / human factors (working memory limits, chunking, cognitive load,
  comprehension, code as communication).
- Ecology / evolution (selection pressure, vestigial structures, mutation, fitness
  landscapes, co-evolution).
- Urban planning / civil architecture (broken windows, zoning, infrastructure, brownfield
  redevelopment).
- Institutions / governance (Conway's Law, ownership, code review, conventions, drift).

Rules: do NOT rank, merge, or build a taxonomy. Flag concepts you're unsure transfer
(confidence Low) for the audit turn. Reprint the full WORKING STATE (v3): append a "domain
dictionary index" (domain → concept count) and any new open questions. End with the
updated WORKING STATE.
```

## Prompt 4 — Latent Lens Sweep

```text
The content above is the current working state of this exploration. This is TURN 4 of 9.
Treat its WORKING STATE block as authoritative and extend it.

Run a LATENT LENS SWEEP: reinterpret "what makes a refactoring high-impact" through
multiple NON-ISOMORPHIC lenses. Each lens should reveal techniques or trigger-signals the
default basin hides. Do not collapse lenses into each other.

For each lens, produce: (a) the reframing of refactoring under it in 1–2 lines; (b) 2–4
candidate technique-seeds or detection-signals; (c) at least one thing this lens sees that
SOLID/smells miss.

Lenses (cover all):
- Control loops — tests/CI/coverage as the feedback making refactoring safe; missing loops
  as the real defect.
- Information flow — coupling/cohesion as data/control flow; refactor as re-routing
  information to cut cross-boundary leakage.
- Boundaries / interfaces — seams, ports & adapters, ISP, anti-corruption layers; where to
  cut.
- Lifecycle / time / entropy — software rot, decay rate, churn hotspots, code age, where
  structure degrades fastest.
- Adversarial dynamics — Hyrum's Law (implicit contracts), Goodhart's Law (gamed metrics),
  shotgun-surgery as attack surface, churn as adversary.
- Ecology / evolution — code as evolving population; vestigial/dead code; refactor as
  selection pressure; module fitness landscape.
- Formal models — behavior preservation, observational equivalence, invariants,
  pre/postconditions; when a transform is provably safe.
- Human factors / cognitive — comprehension load, chunk size, surprise, naming as
  compression; refactor to fit working memory.
- Institutions / governance — Conway's Law, ownership vs. module boundaries, bus factor,
  review friction.
- Markets / economics — debt interest rate per module, refactor ROI, option value of a
  seam, where NOT to refactor.
- Safety engineering — blast radius, regression risk, fault isolation, reversibility.
- Design theory — modularity, pattern-fit, missing vs. premature abstraction.
- Historical analogy — brownfield redevelopment, strangler-fig migrations, normalization
  history.
- Failure analysis — post-incident structural causes; which structural smells precede
  outages/bugs.

Rules: NO synthesis, NO final structure. Cross-reference seeds already in the domain
dictionary. Mark Low confidence on speculative seeds. Reprint the full WORKING STATE (v4):
append "lens → top seeds" and any contradictions between lenses (gold for the audit). End
with the updated WORKING STATE.
```

## Prompt 5 — Transposition / Abstraction Engine

```text
The content above is the current working state of this exploration. This is TURN 5 of 9.
Treat its WORKING STATE block as authoritative and extend it.

Build the TRANSPOSITION ENGINE: explicit rules for translating concepts from the source
domains and lenses above into concrete refactoring techniques — WITHOUT shallow analogy.
Mechanism-preserving transfer only.

Part A — Define the transposition protocol. For a borrowed concept to become a candidate
technique it must carry ALL of:
  1. Causal mechanism (what does the work, not the metaphor).
  2. Detection signal (observable code/process condition that triggers it).
  3. Boundary conditions (when it applies; when it does NOT — contraindications).
  4. Intervention logic (actual transformation steps on code).
  5. Behavior-preservation story (how observable behavior stays intact).
  6. Failure/over-application mode (what breaks if mis-applied).
State a rejection rule: if a transfer survives only as metaphor (word, not mechanism), tag
it REJECTED — SHALLOW and explain why.

Part B — Apply the protocol to 12–20 of the strongest concept/lens seeds above. For each,
produce a candidate technique record with the six fields, plus a provisional name and a
provenance tag: CANONICAL (in refactoring literature), BORROWED (faithful transfer),
APPROXIMATION (lossy transfer), or INVENTED (new coinage).

Rules: do NOT organize into a taxonomy or rank yet. Keep records atomic for the audit. Mark
confidence per record. Flag any record whose detection signal is vague or unmeasurable.
Reprint the full WORKING STATE (v5): append a "candidate technique inventory" index (name →
provenance → confidence). End with the updated WORKING STATE.
```

## Prompt 6 — Candidate Organizing Structures

```text
The content above is the current working state of this exploration. This is TURN 6 of 9.
Treat its WORKING STATE block as authoritative and extend it.

Generate CANDIDATE STRUCTURES for the final taxonomy. We are choosing how to organize the
technique inventory — not writing the taxonomy yet.

Produce 4–6 distinct candidate organizing schemes. Consider (and improve on):
  - By smell/violation type (the default-basin axis: SOLID + smells).
  - By mechanism repaired (coupling, cohesion, abstraction-level, control-flow,
    data-model, lifecycle/entropy, feedback-loop).
  - By scope/altitude (statement → method → class → module → service → system).
  - By blast radius / risk tier (local & reversible → wide & risky).
  - By workflow phase (detect → make-safe → transform → verify → measure).
  - By driving force / "why" (comprehension, changeability, testability, performance,
    reliability, ownership).
  - A matrix/faceted scheme combining two axes.

For EACH candidate, evaluate on: explanatory power (mechanism vs. symptom sorting);
practical usefulness (fast retrieval at the moment of need); extensibility (new
techniques/domains slot in cleanly); blind spots (what the axis hides); distortion risk
(force-fit / false neighbors); coverage of cross-discipline material (or collapse back to
basin).

End with a comparison table and a provisional recommendation (with confidence), but do NOT
commit — that happens after the audit. Reprint the full WORKING STATE (v6): append
"candidate structures + scores" and the leading candidate. End with the updated WORKING
STATE.
```

## Prompt 7 — Adversarial Audit

```text
The content above is the current working state of this exploration. This is TURN 7 of 9.
Treat its WORKING STATE block as authoritative.

RED-TEAM everything above: candidate structures, technique inventory, lens seeds, and
domain dictionary. Be harsh — find weaknesses before we commit.

Audit against ALL failure classes; for each finding give: the item, the problem, severity
(High/Med/Low), and a proposed fix (merge / split / drop / re-ground / re-scope):

1. Symptoms mislabeled as mechanisms (e.g., "long method" as cause vs. signal of a missing
   abstraction).
2. Duplicate/overlapping concepts (feature envy vs. inappropriate intimacy vs. coupling —
   distinguish with a sharp test or merge).
3. Missing domains/technique classes (concurrency, distributed coupling, data migration,
   observability, performance-structure, security-relevant structure, socio-technical).
4. Weak/shallow analogies that should be REJECTED (metaphor without mechanism).
5. Scale sensitivity (works at function scale but not service scale, or vice versa).
6. Hidden assumptions (test coverage exists; typed/OO language; single-team ownership;
   monorepo).
7. Boundary ambiguity (where does refactoring end and rewrite/redesign begin?).
8. Temporal blind spots (one-shot vs. continuous; churn; decay; migration over time).
9. Incentive blind spots (why teams won't apply a technique; Goodhart on proposed metrics).
10. Measurement problems (is the detection signal observable/tool-detectable? is "impact"
    falsifiable?).
11. Operational uselessness (too abstract to act on at 2pm in a real PR).
12. Cases where the taxonomy/structure would FAIL outright (concrete codebases/scenarios).

Do NOT rewrite the taxonomy or pick the final structure here. Output a prioritized findings
list with a "kill / merge / keep / re-ground" recommendation per affected item. Reprint the
full WORKING STATE (v7): append "audit findings (ranked)" and a "must-fix before synthesis"
shortlist. End with the updated WORKING STATE.
```

## Prompt 8 — Compression Restart

```text
The content above is the current working state of this exploration. This is TURN 8 of 9.
Treat its WORKING STATE block as authoritative.

COMPRESSION RESTART. Discard noisy residue and lock the design for the final artifact.
Still do NOT write the full taxonomy.

Produce:

1. CHOSEN ORGANIZING PRINCIPLE — pick one structure (or a defined hybrid) from above as
   revised by the audit. Justify in 3–5 lines on explanatory power + actionability.
2. FINAL SCHEMA — the exact record format for every technique entry (refine names if
   better):
     - Technique name (+ provenance: CANONICAL / BORROWED / APPROXIMATION / INVENTED)
     - Category / position in the chosen structure
     - Detection signal(s) — observable, ideally tool-detectable trigger
     - Mechanism repaired — underlying structural cause (NOT the symptom)
     - Preconditions / make-safe steps (tests, characterization tests, seams)
     - Transformation mechanics — concrete steps
     - Behavior-preservation guarantee — why observable behavior is unchanged
     - Blast radius / regression risk + reversibility
     - Expected impact + a measurable before/after signal
     - Over-application / contraindication (when NOT to do it)
     - Confidence (High/Med/Low) and dependencies on other techniques
3. MERGE / SPLIT RULES — one entry vs. two; handling multi-scale techniques.
4. MANDATORY COVERAGE LIST — categories/domains the artifact MUST include (carry forward
   audit's "missing domains": concurrency, data-model, lifecycle/churn, distributed
   coupling, observability, socio-technical, plus SOLID + smells re-grounded by mechanism).
5. KNOWN GAPS & CAVEATS — what's out of scope and where confidence is low (impact rankings,
   cross-discipline transfers, language-specific applicability).
6. ORDERING & SIZE TARGET — entry order and rough count.

Reprint a final, cleaned WORKING STATE (v8) the synthesis turn can consume directly: chosen
structure, schema, coverage list, merge rules, known gaps. End with the updated WORKING
STATE.
```

## Prompt 9 — Final Synthesis

```text
The content above is the current and final working state of this exploration — especially
the locked WORKING STATE (v8). This is TURN 9 of 9 — synthesis.

Now WRITE THE FINAL ARTIFACT: the cross-discipline, actionable taxonomy of high-impact code
refactoring techniques, using EXACTLY the organizing principle and schema locked above, and
respecting its merge/split rules, mandatory coverage list, and known gaps.

Requirements:
- Open with a 1-paragraph reader's guide: how to navigate the taxonomy and use it at the
  moment of need (detect → make-safe → transform → verify → measure).
- Present every technique as a record in the locked schema. Ground each in MECHANISM, not
  the surface label. Re-ground the SOLID violations and classic smells (long method, long
  params >4, deep nesting ≥4, duplication, magic numbers, primitive obsession, feature
  envy) by the mechanism each repairs — do not just relist them.
- Include the cross-discipline techniques surfaced via the lens sweep and transposition
  engine, each tagged BORROWED / APPROXIMATION / INVENTED with its source domain named.
- Cover the mandatory list: SOLID (re-grounded), core smells (re-grounded), plus
  concurrency, data-model, lifecycle/churn, distributed coupling, observability, and
  socio-technical/ownership structure.
- Keep each detection signal observable and each contraindication explicit (where NOT to
  apply it).
- Provide confidence markers throughout, and a legend distinguishing CANONICAL terms from
  BORROWED terms, APPROXIMATIONS, and INVENTED taxonomy-specific names.
- Close with: adoption/usage guidance, the known gaps/caveats from the prior turn, and a
  short "highest-impact first" priority heuristic with its confidence and its Goodhart
  risk.

Output the taxonomy as a single, self-contained, copy-pasteable document. This is the
deliverable, so it may be long; favor completeness and mechanism clarity over brevity.
```
