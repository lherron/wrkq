# Rule-Authoring Template

Use this template before adding any new build-failing rule.

## Rule Candidate

| Field | Fill in |
| --- | --- |
| rule | What invariant should the rule enforce? State the exact forbidden or required shape. |
| why | What defect, flail loop, stale carrier, or boundary break does this prevent? |
| bad example | Show the smallest concrete input the rule should reject. |
| good example | Show the blessed shape. If you cannot write this, the rule is too vague to encode. |
| exception policy | Name the sanctioned exception path, required evidence, and review point. |
| highest feasible rung | Choose the strongest rung this repo can actually support: ELIMINATE, GUARD, WARN, TRAIN, or TACIT. |
| sunset signal | Define when to demote or remove the rule, such as no fires, high suppression rate, or removal causing no regression. |

## When To Use It

This template is required for any new build-failing rule: a new lint, structural guard, or meta-lint. Place the candidate at the highest feasible rung that is expressible, false-positive-tolerable, and attention-positive. Taste rules stay TACIT or TRAIN unless they repeatedly cause real defects; recurring review flail can promote a convention toward WARN or GUARD, while noisy rules with high suppression cost should be documented, trained, demoted, or sunset instead of mechanized.

## Worked Examples

These existing guards show the template in action without retro-fitting every rule into a full table:

| Guard | Rule | Why |
| --- | --- | --- |
| [suppression-lint](../cmd/suppression-lint/main.go) | Suppression comments must use a sanctioned `ARCH-EXCEPTION(T-12345): reason` channel rather than bare disables. | Keeps suppression visible and reviewable before additional guards drain trust. |
| [layer-boundary](../cmd/layer-boundary/main.go) | Forbidden architecture edges, including transitive import chains, fail with the observed path and fix guidance. | Prevents package-boundary drift that direct-import greps miss. |
| [rot-sensor](../cmd/rot-sensor/main.go) | Stale rot markers and expired exception leases fail instead of letting prose claims lie silently. | Turns known stale carriers into a visible maintenance signal. |
| [surface-guard](../cmd/surface-guard/main.go) | New public surface requires contract evidence or an explicit reviewed exception. | Keeps green tests from hiding untested API expansion. |
| [doc-links](../cmd/doc-link-check/main.go) | Router markdown links, `@path` references, and known-extension inline paths must resolve. | Keeps always-loaded docs discoverable and reachable. |
