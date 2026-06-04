# Artifact 2 — Synchronous 9-Turn Daedalus Harness for Latent-Space Exploration

## Purpose

This artifact defines a synchronous, sequential, multi-turn harness. It is designed for use in a single conversation with one model, where each turn progressively widens, structures, audits, and compresses the search.

Compared with the single-turn prompt, this plan creates explicit phase boundaries. The model cannot immediately collapse into a polished answer because each turn has a constrained role:

```text
frame → basin map → latent lens sweep → obstruction ledger → mechanism transfer
→ synthesis → adversarial audit → compression restart → final answer
```

The harness should be used for nontrivial design, strategy, architecture, research, or debugging tasks where the first plausible answer is likely to be incomplete.

## Source Context

The base Daedalus constitution already supplies a strong architectural worldview: it emphasizes invariants, interfaces, flows, control loops, feedback, failure, resilience, composability, and elegance. It also warns against brittle elegance: local beauty that creates global fragility.

This synchronous harness turns that worldview into a procedural execution plan. The goal is to make the model re-enter the relevant maps at multiple points rather than relying on one monolithic answer.

## Operating Rules

Use this harness when:

- the problem is ambiguous;
- the architecture has many interacting constraints;
- failure modes matter;
- incentives, operators, or organizations are part of the system;
- you want outside-basin thinking;
- you want the answer to improve through staged prompting.

Do not use the full harness for routine factual answers or small edits.

General rule:

```text
Do not ask for recommendations until Turn 6.
Do not ask for final compression until Turn 8.
Do not let failed branches disappear; turn them into constraints.
```

## Carry-Forward State

Maintain a working state object across turns. You do not need to use literal JSON, but the harness should preserve these fields:

```json
{
  "task_frame": {},
  "default_basin_map": {},
  "latent_lens_outputs": [],
  "obstruction_ledger": [],
  "mechanism_transfers": [],
  "candidate_syntheses": [],
  "audit_findings": [],
  "final_synthesis": {}
}
```

## Turn 1 — Task Frame

### Goal

Establish the local frame before solving. Identify objective, scope, invariants, jurisdiction, and volatile assumptions.

### Prompt

```text
You are operating under the Daedalus constitution.

For the following task, do not answer yet.

Construct a task-local frame.

Task:
[PASTE TASK]

Return:

1. Objective:
What is the user actually trying to decide, build, understand, or change?

2. Non-objectives:
What should not be solved or assumed?

3. Scope and jurisdiction:
What can be advised, diagnosed, or designed?
What authority remains with the human/operator?

4. Critical invariants:
What must remain true for the system to be correct, safe, useful, economically viable, and strategically coherent?

5. Volatile assumptions:
What assumptions are likely to change?

6. Relevant substrates:
Software, AI systems, data, infrastructure, organization, market, policy, physical world, user behavior, etc.

7. Success criteria:
What would count as a good answer or useful next step?

8. Task depth:
Classify as routine, design/debugging, architecture/strategy, or frontier/open-ended.

Do not recommend yet.
Return only the task frame.
```

### Expected Output

A compact task frame. This becomes the anchor for later turns.

### Progressive Disclosure

Shown to the user only if they want architectural trace. Otherwise keep it as harness state.

## Turn 2 — Default Basin and Bottleneck Map

### Goal

Make the conventional answer explicit so the model does not mistake it for the whole search space.

### Prompt

```text
Using the task frame below, map the default basin.

Task frame:
[PASTE TURN 1 OUTPUT]

Do not recommend yet.

Return:

1. Obvious framing:
How would a competent conventional answer frame the problem?

2. Standard approaches:
List the main conventional approaches.

For each approach:
- core mechanism;
- what it preserves;
- what it optimizes locally;
- hidden assumptions;
- bottleneck;
- failure mode;
- what kind of problem it cannot see.

3. Premature elegance traps:
Which simplifications look clean but may become globally fragile?

4. Default-basin summary:
What is the gravitational well the answer must avoid or deliberately justify?

Do not synthesize yet.
```

### Expected Output

A map of standard approaches and their bottlenecks.

### Progressive Disclosure

In final answers, summarize this as “Rejected alternatives” or “Why the obvious approach is insufficient.”

## Turn 3 — Latent Lens Sweep

### Goal

Force multiple non-isomorphic representations. This is the main latent-space expansion step.

### Prompt

```text
Using the task frame and default-basin map below, reinterpret the task through multiple non-isomorphic lenses.

Task frame:
[PASTE TURN 1 OUTPUT]

Default basin:
[PASTE TURN 2 OUTPUT]

Do not recommend yet.
Do not synthesize yet.
Push each lens hard enough to reveal a mechanism, not just vocabulary.

Use these lenses:

1. State machine
2. Flow / queueing network
3. Contract / interface boundary
4. Control loop
5. Incentive / game-theoretic system
6. Attack surface / adversarial system
7. Option portfolio under uncertainty
8. Production system with WIP, defects, rework, and inspection
9. Formal system with invariants, proofs, counterexamples, and minimal generators
10. Ecological/adaptive system

For each lens return:
- reframed problem;
- mechanism revealed;
- default-basin blind spot;
- new invariant or constraint;
- candidate move suggested by this lens;
- likely failure mode;
- cheap test or probe.

Do not choose a winner.
```

### Expected Output

Ten lens outputs, each with a mechanism and an obstruction or candidate move.

### Progressive Disclosure

Usually keep detailed lens outputs internal. In the final answer, expose only the 2–4 lenses that changed the recommendation.

## Turn 4 — Obstruction Ledger

### Goal

Turn failed or weak branches into useful constraints.

### Prompt

```text
Build an obstruction ledger from the task frame, default basin, and latent lens sweep.

Task frame:
[PASTE TURN 1 OUTPUT]

Default basin:
[PASTE TURN 2 OUTPUT]

Lens sweep:
[PASTE TURN 3 OUTPUT]

Do not recommend yet.

For each significant obstruction, return:

- obstruction;
- source branch or lens;
- hidden assumption exposed;
- design constraint implied;
- severity;
- reversibility;
- testability;
- whether it suggests a better representation;
- candidate mechanisms that might address it.

Do not discard minority or strange branches if they reveal a useful obstruction.
Cluster related obstructions, but preserve outliers.
```

### Expected Output

A typed obstruction ledger.

### Progressive Disclosure

In the final answer, this becomes the basis for “decisive constraints” and “risks.”

## Turn 5 — Mechanism Transfer

### Goal

Search distant domains for mechanisms that satisfy the obstruction ledger.

### Prompt

```text
Given the obstruction ledger, search for mechanisms from distant domains that could satisfy the constraints.

Obstruction ledger:
[PASTE TURN 4 OUTPUT]

Do not recommend yet.
Do not use analogies unless there is a transferable mechanism.

Consider mechanisms from:

1. Distributed systems
2. Compiler/runtime design
3. Biology / immune systems
4. Market microstructure
5. Manufacturing / Toyota-style production
6. Aviation / nuclear safety
7. Law / governance
8. Mathematics / extremal construction and obstruction
9. Urban infrastructure
10. Security engineering

For each useful mechanism return:
- source domain;
- mechanism;
- obstruction addressed;
- translation back to the original task;
- mismatch risk;
- cheap validation test;
- whether it should be promoted, parked, or rejected.
```

### Expected Output

A mechanism-transfer table or structured list.

### Progressive Disclosure

Only mechanisms that materially affect the recommendation need to be shown later.

## Turn 6 — Candidate Synthesis

### Goal

Generate concrete candidate strategies or architectures by combining branches with complementary failure modes.

### Prompt

```text
Synthesize candidate approaches.

Inputs:

Task frame:
[PASTE TURN 1 OUTPUT]

Default basin:
[PASTE TURN 2 OUTPUT]

Obstruction ledger:
[PASTE TURN 4 OUTPUT]

Mechanism transfers:
[PASTE TURN 5 OUTPUT]

Do not simply choose the most common branch.
Prefer synthesis between branches with complementary failure modes.

Produce 2–4 candidate approaches.

For each candidate:
- name;
- core mechanism;
- what it preserves;
- what volatility it hides;
- what feedback it exposes;
- operational burden;
- failure modes;
- rollback path;
- smallest reversible experiment;
- what must be true for it to work.

Then identify:
- elegant-but-fragile candidate;
- robust-but-heavy candidate;
- option-preserving candidate;
- candidate with fastest feedback;
- candidate most likely to survive adversarial pressure.

Do not finalize yet.
```

### Expected Output

A candidate set with tradeoffs.

### Progressive Disclosure

This is often worth showing in an L2/L3 answer as a compact comparison.

## Turn 7 — Adversarial Audit

### Goal

Attack the candidates before final selection.

### Prompt

```text
Adversarially audit the candidate approaches.

Candidate approaches:
[PASTE TURN 6 OUTPUT]

Use the following red-team lenses:

1. Overload
2. Partial failure
3. Stale state
4. Adversarial behavior
5. Ambiguous ownership
6. Incentive drift
7. Operator error
8. Scale transition
9. Platform/regulatory/substrate change
10. Success-induced complexity

For each candidate:
- fatal flaws;
- bounded risks;
- hidden coupling;
- observability gaps;
- ownership gaps;
- rollback weaknesses;
- tests required before commitment;
- design modifications that would improve robustness.

Then rank candidates by graceful degradation, not surface elegance.
Do not produce the final answer yet.
```

### Expected Output

Audit ledger and revised ranking.

### Progressive Disclosure

Final answer should include only the highest-leverage audit findings.

## Turn 8 — Compression Restart

### Goal

Restart from surviving invariants and mechanisms only. This prevents narrative overfitting to the exploratory path.

### Prompt

```text
Perform a compression restart.

Inputs:

Task frame:
[PASTE TURN 1 OUTPUT]

Obstruction ledger:
[PASTE TURN 4 OUTPUT]

Candidate synthesis:
[PASTE TURN 6 OUTPUT]

Adversarial audit:
[PASTE TURN 7 OUTPUT]

Ignore the historical exploration except where needed for justification.

Return:

1. Surviving invariants
2. Decisive constraints
3. Mechanisms that actually do work
4. Rejected mechanisms and why
5. Recommended kernel
6. Required contracts/interfaces
7. Required feedback loops
8. Operational probes
9. Rollback path
10. Next reversible step

Use the smallest coherent structure that preserves the essential invariants and fails safely.
```

### Expected Output

A compressed recommendation kernel.

### Progressive Disclosure

This forms the core of the final answer.

## Turn 9 — Final Answer

### Goal

Produce the user-facing answer with controlled disclosure.

### Prompt

```text
Now produce the final user-facing answer.

Use the compression restart below:
[PASTE TURN 8 OUTPUT]

Disclosure level:
[Choose L0, L1, L2, L3, or L4]

Output type:
[decision memo / design review / implementation plan / critique / research map / prompt / code review / other]

Rules:
- Be concise but not lossy.
- Separate facts, assumptions, inferences, risks, and recommendations when it matters.
- Include the decisive constraints.
- Include only the rejected alternatives that materially affect the decision.
- Include the next reversible step.
- Do not expose the full exploratory trace unless disclosure level is L3 or L4.
```

### Expected Output

A compact, useful answer.

## Optional Turn 10 — Harness Postmortem

Use this when improving the harness itself.

### Prompt

```text
Postmortem the harness run.

Return:
- which turn created the most value;
- which branch or lens was noise;
- which obstruction changed the answer;
- which mechanism transfer was real vs decorative;
- what should be added, removed, or made parallel next time;
- a revised harness configuration for similar tasks.
```

## Potential Approach for Use

For most high-leverage tasks, run Turns 1–9. For medium tasks, skip Turns 5 or 10 and compress Turn 3 to 4–5 lenses.

Recommended default:

```text
Turn 1: full
Turn 2: full
Turn 3: 10 lenses
Turn 4: full obstruction ledger
Turn 5: 5 most relevant mechanism domains
Turn 6: 3 candidates
Turn 7: 6 strongest audit lenses
Turn 8: full compression restart
Turn 9: L1 or L2 final answer
```

This creates enough breadth to explore latent space while staying operationally manageable.

## Main Failure Modes and Mitigations

### Failure Mode 1 — The model recommends too early

Mitigation:
Use explicit “Do not recommend yet” language through Turn 5.

### Failure Mode 2 — The lens sweep becomes decorative

Mitigation:
Require each lens to produce a mechanism, blind spot, constraint, failure mode, and cheap test.

### Failure Mode 3 — Obstructions are summarized away

Mitigation:
Keep the obstruction ledger as a first-class artifact and paste it into later turns.

### Failure Mode 4 — Mechanism transfer becomes metaphor

Mitigation:
Require translation back, mismatch risk, and validation test.

### Failure Mode 5 — Final answer becomes too verbose

Mitigation:
Use disclosure levels. Default to L1/L2. Use L3/L4 only when the user asks for trace or artifacts.

## Minimal Version

For a shorter synchronous harness, use six turns:

```text
1. Frame
2. Default basin
3. Lens sweep
4. Obstruction + mechanism transfer
5. Synthesis + audit
6. Compression + final answer
```

The full nine-turn version is better when the task benefits from deeper latent-space exploration.
