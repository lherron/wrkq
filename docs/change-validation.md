# wrkq change validation

Use this as a carrier for choosing the right validation path. The executable
checks remain the source of truth; do not copy their flags or behavior here.

## Fast inner loop

Run `just verify` for normal implementation feedback before commit. Its recipe
in [justfile](justfile) chains the current fast guards: suppression-lint,
layer-boundary, lint, test, rot-sensor, surface-guard, and doc-links.

This is the default bar for scoped code and documentation changes, including the
S1-S4 and S8 guard surfaces.

## Slow backstop

Run `just verify-full` when the change affects executable behavior, cross-tool
contracts, workflow/RPC surfaces, or before closing tasks that require the full
gate. Its recipe in [justfile](justfile) routes through `just verify`, `just
smoke`, and `just verify-rpc`; this is the S5 backstop.

## Installed-binary smoke

After `just install`, manually exercise a real installed binary path with the
configured local project, such as an installed `wrkq` read command or an
installed `wrkf` command. Record the exact command and output in the task
evidence. Unit tests and `just test` are not substitutes for this SOP smoke.

## Code-change workflow

For multi-role wrkq code changes, attach the wrkf template at
[wrkf/templates/wrkq-code-change.workflow.json](wrkf/templates/wrkq-code-change.workflow.json).
It carries red, verify, full-verify, review, and installed-binary evidence
through the S10 workflow; it does not replace the commands above.

## New build-failing rules

Before adding a new lint, structural guard, or meta-lint, fill out the
[rule-authoring template](docs/rule-template.md). That S9 template decides
whether the rule belongs as an executable gate, a warning, training material, or
tacit convention.
