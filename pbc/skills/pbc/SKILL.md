---
name: pbc
description: Use when turning one-line product feedback into a Behavior Note, compact PBC, pressure pass, final PBC, or derived work/test/design seeds using the wrkq/wrkf progressive-refinement workflow in ./pbc.
---

# PBC Progressive Refinement

Use this skill when a user gives terse product feedback and wants it shaped into product intent without multiplying artifacts.

## Ownership

- `wrkq task.description` holds the Behavior Note.
- `wrkq task.specification` holds the PBC draft or final PBC.
- `wrkf evidence` records transition proof: intake metadata, pre-interview analysis, artifact snapshots, clarification answers, pressure passes, patch decisions, and finalization.
- Derived stories, UAT, tests, design checklists, and implementation tasks are created only after finalization.

## Preset

Install and attach the workflow template:

```bash
wrkf workflow validate ./pbc/workflow-template.json
wrkf workflow install ./pbc/workflow-template.json
wrkf task attach <task> --workflow pbc-progressive-refinement@4
```

Run transitions with `--role agent` unless the user/product owner is explicitly acting.

## Intake

For one-line feedback, create or use one wrkq task. Put only raw intake normalization in metadata/evidence first:

- Signal: normalized product signal.
- Likely type: `bug`, `UX gap`, `missing feature`, `unclear`, or `wont_fix`.
- Question: the highest-leverage question, if one is needed.

Record it:

```bash
wrkf evidence add <task> --kind intake_metadata --ref "feedback:<source>" --summary "<signal>" --data '<json>'
wrkf transition <task> normalize_feedback --role agent
```

## Behavior Note

Write one Behavior Note into `task.description` using `./pbc/templates/behavior-note.md`. Ask at most one open question. If a question is needed, include the default recommendation so the user can answer tersely.

Record and branch:

```bash
wrkq set <task> --description "$(cat /tmp/behavior-note.md)"
wrkf evidence add <task> --kind behavior_note --ref "wrkq:<task>#description" --summary "Behavior Note written"
```

Before deciding whether to ask the user anything, record pre-interview analysis as workflow evidence only. Do not create a separate task, note, or durable artifact for it.

The routing decision goes in `--facts` (the small surface wrkf branches on); the full analysis stays in `--data`.

- `--facts`: `{"clarification_needed": true|false}` — the only fact that gates `ask_clarification` vs `draft_pbc`.
- `--data` should include:
  - `inferred`: what the agent can infer from the feedback and available context.
  - `uncertain`: what remains unknown.
  - `question`: the single highest-leverage question, or empty if none.
  - `default_recommendation`: the answer the agent recommends by default.

```bash
wrkf evidence add <task> --kind pre_interview_analysis --ref "agent:pre-interview-analysis" --summary "clarification needed|no clarification needed" --facts '{"clarification_needed":true}' --data '<json>'
```

If no clarification is needed:

```bash
wrkf transition <task> draft_pbc --role agent
```

If clarification is needed:

```bash
wrkf transition <task> ask_clarification --role agent
```

After the answer:

```bash
wrkf evidence add <task> --kind clarification_response --ref "user:<source>" --summary "<answer>"
wrkf obligation list <task>
wrkf obligation satisfy <task> <obligation-id> --evidence <evidence-id>
wrkf transition <task> answer_clarification --role agent
```

## PBC Draft

Write the PBC into `task.specification` using `./pbc/templates/pbc.md`. Keep it declarative and product-owned: no implementation plan, ticket breakdown, test prose, or exhaustive edge-case inventory.

```bash
wrkq set <task> --specification "$(cat /tmp/pbc.md)"
wrkf evidence add <task> --kind pbc_draft --ref "wrkq:<task>#specification" --summary "PBC draft written"
wrkf transition <task> run_pressure_pass --role agent
```

## Pressure Pass

Check only:

1. Observable: can a user or tester see whether it happened?
2. Complete enough: are empty, success, working, and failure states covered?
3. Non-implementation: did it avoid prescribing internals?
4. Decision rules: are important product rules explicit?
5. Test-derivable: could UAT scenarios be generated without guessing?

Write a compact pressure pass using `./pbc/templates/pressure-pass.md`.

The verdict goes in `--facts` (it routes `finalize_ready_pbc`, `request_patch_decision`, or `revise_too_vague_pbc`); the tighten list and patch stay in `--data`.

- `--facts`: `{"verdict":"ready"|"needs_patch"|"too_vague"}`.
- `--data`: tighten list, patch, and any supporting detail.

```bash
wrkf evidence add <task> --kind pressure_pass --ref "agent:pressure-pass" --summary "ready|needs patch|too vague" --facts '{"verdict":"needs_patch"}' --data '<json>'
```

If ready, ensure `task.specification` is the final PBC and close:

```bash
wrkf evidence add <task> --kind pbc_final --ref "wrkq:<task>#specification" --summary "Final PBC"
wrkf transition <task> finalize_ready_pbc --role agent
wrkq set <task> --state completed
```

If it needs a patch:

```bash
wrkf transition <task> request_patch_decision --role agent
```

If it is too vague, revise the PBC and return to draft:

```bash
wrkf transition <task> revise_too_vague_pbc --role agent
```

After the user accepts or adjusts the patch, update `task.specification`, then route to finalization:

```bash
wrkf evidence add <task> --kind patch_decision --ref "user:<source>" --summary "accepted|adjusted" --facts '{"route":"finalize"}' --data '<json>'
wrkf obligation satisfy <task> <obligation-id> --evidence <evidence-id>
wrkf evidence add <task> --kind pbc_final --ref "wrkq:<task>#specification" --summary "Final PBC"
wrkf transition <task> finalize_after_patch_decision --role agent
wrkq set <task> --state completed
```

If the patch is rejected and the PBC needs another pass:

```bash
wrkf evidence add <task> --kind patch_decision --ref "user:<source>" --summary "revise" --facts '{"route":"revise"}' --data '<json>'
wrkf obligation satisfy <task> <obligation-id> --evidence <evidence-id>
wrkf transition <task> revise_after_patch_decision --role agent
```

## Disposition

If the feedback should close without a finalized PBC, record a disposition decision with a routing fact:

```bash
wrkf evidence add <task> --kind disposition_decision --ref "user:<source>" --summary "wont_fix|duplicate|unclear|out_of_scope" --facts '{"resolution":"wont_fix"}' --data '<json>'
wrkf transition <task> dispose_from_behavior_note --role agent
```

Use the matching dispose transition for the current phase: `dispose_from_behavior_note`, `dispose_from_pbc_draft`, or `dispose_from_pressure`.

## Derivation

Only derive after the workflow is `closed/finalized`. Create child or related wrkq tasks for `uat`, `story`, `design`, `tests`, or `tasks`. Each derived task should include source traceability:

- source PBC task id
- source PBC etag or workflow revision
- source PBC hash if available

Do not turn interview notes, requirements, acceptance criteria, and test basis into separate product artifacts. The durable product chain is:

```text
Feedback -> Behavior Note -> PBC -> Derived artifacts
```
