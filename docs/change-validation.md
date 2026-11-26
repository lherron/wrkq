# wrkq change validation

Use this as a carrier for choosing the right validation path. The executable
checks remain the source of truth; do not copy their flags or behavior here.

## Fast inner loop

Run `just verify` for scoped code and documentation changes. The recipe in
`Justfile` runs the local guards and test suites without installing binaries or
restarting a service. For a change to a durable record, regenerate with
`just architecture-records --write` and review the projection diff with the
record.

## Slow backstop

Run `just verify-full` when the change affects executable behavior, cross-tool
contracts, workflow/RPC surfaces, or before closing tasks that require the full
gate. Its recipe in [Justfile](Justfile) routes through `just verify`, `just
smoke`, and the canonical wrkf adoption probe; `verify-rpc` is already inside
the fast gate. This is the S5 backstop.

## Installed-binary smoke

When a change needs activation, exercise the installed entrypoint with the
real configuration after `just install`. Record the command and observed
output. For an embedded guide when activation is deferred, build its CLI into
scratch and check the emitted guide. Record the remaining activation boundary.

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
