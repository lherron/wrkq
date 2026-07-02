# wrkq Agent Enablement Status

<!-- GENERATED: agent-enablement status; do not edit by hand. -->

Generated from `agent-enablement.json`. This markdown is a projection, not authority.

Rubric: rubric.md@d793717
Catalog: not-recorded
AE assessment: 2026-07-02 (agent-enablement/assessments/wrkq/assessment.json)
PM floor: 2026-07-02 (etag 7)

## Profile Summary
Required: 16
Frontier: 1
Deferred: 0
Open deltas: none
Failing/open axes: S1, TC

## PM Floor Axes
- F0: PARTIAL
- P0: PARTIAL
- S1: PRESENT
- S3: PRESENT
- S6: PARTIAL
- S7: PRESENT

## PM Observations
- validate-justfile: PRESENT (P0) - justfile: recipes present default, info, test, lint, verify
- validate-gitignore: PRESENT (F0) - .gitignore: checked 7 required entries; all present
- validate-readme: PRESENT (F0) - README.md: H1 title, description, quick start/getting started/install, usage/examples
- validate-runtime: PRESENT (F0) - go.mod present
- validate-agent-spaces: PRESENT (F0) - asp-targets.toml present; .gitignore present
- validate-agent-md: PRESENT (S3) - AGENTS.md present; CLAUDE.md present
- validate-githooks: ABSENT (P0, S6) - No lefthook.yml found in project root.
- validate-gitleaks: ABSENT (F0) - gitleaks hook not found; gitleaks local config/ignore not found
- validate-linting: PARTIAL (S6) - justfile: lint recipe present; lint config present; hook runs lint no; gaps: hooked lint
- validate-typechecking: PRESENT (S1, S6) - justfile: Go compile via test recipe present; tsconfig not applicable/missing; hook evidence missing
- validate-tests: PRESENT (S6, S7) - justfile: test recipe present; package test not applicable/missing; Go tests present

## Escalations
- F0: floor-gap / open (T-05445)
- P0: floor-gap / open (T-05446)
- S6: floor-gap / open (T-05448)

## Depth Axes
- F0: PRESENT.DORMANT.satisfied.dormant
- P0: PRESENT.EXERCISED.satisfied.exercised
- S1: PARTIAL.EXERCISED.open_delta
- S2: PRESENT.DORMANT.satisfied.dormant
- S3: PRESENT.EXERCISED.satisfied.exercised
- S4: PRESENT.EXERCISED.satisfied.exercised
- S5: PRESENT.EXERCISED.satisfied.exercised
- S6: PRESENT.EXERCISED.satisfied.exercised
- S7: PRESENT.EXERCISED.satisfied.exercised
- S8: PRESENT.EXERCISED.satisfied.exercised
- TA: PRESENT.EXERCISED.satisfied.exercised
- TB: PRESENT.EXERCISED.satisfied.exercised
- TC: PARTIAL.DORMANT.satisfied.dormant
- TD: PRESENT.DORMANT.satisfied.dormant
- TE: PRESENT.EXERCISED.satisfied.exercised
- TF: PRESENT.EXERCISED.satisfied.exercised
