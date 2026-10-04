# Hook duration measurement

`pre-commit` mirrors the existing local gitleaks → golangci-lint gate.
`pre-push` runs no gate: `just verify` runs after the push on mini's
self-hosted runner (T-10161). It keeps best-effort `wrkp git push` reporting.
Exit traps record passed, failed, or skipped whole-hook runs without changing
gate status.

Both hooks call `hook-duration.py` from the repository root. Python 3 is optional
for the gate: an unavailable interpreter/helper disables measurement only.
The helper appends genuine run records to
`.git/praesidium/hook-timings.jsonl` (Git's common directory in worktrees).
Metadata without a reliable value is omitted; changes are `unclassified`.
`file_count` counts staged paths for pre-commit and committed paths relative to
the configured upstream for pre-push, when available. It does not parse or
consume the pre-push ref stream.

The finish timestamp and monotonic duration are captured before journal writes.
One detached worker attempts `wrkp post wrkq --type hook.settled`, with explicit
inherited environment, null stdin/output, a two-second timeout, occurrence at
finish, and the stable `hook:<run_id>` key. A failed journal write or publication
cannot fail the gate; the gate never waits for publication. Hook sources must
remain present in the checkout for measurement to work.

Run the isolated shell fixture matrix with:

```sh
python3 tools/hooks/tests/instrument-e2e.py
```

The runner removes inherited `GIT_*` before Git initialization and passes every
child an explicit environment. Fake publishers are isolated from the real
ledger. It verifies command order, statuses, both skips, stdin replay, absent
measurement code, and missing/failing/hung publishers. Replay uses the same
key, occurrence, and attributes as the original attempt. Its JSON output is a
repeatable validation artifact.

Genuine journal records can be replayed explicitly:

```sh
python3 tools/hooks/hook-duration.py --backfill
```

Never copy fixture records into a real journal. Initial installation on max3
(T-10096, measure hook duration) found no journal and no hook.settled facts in
the prior 90 days: zero historical records, zero backfilled facts.

Activation is intentionally scoped to the existing two local hooks; do not use
producer package installation or restart any service. Compare each installed
hook with its original body before replacing it with the instrumented source,
and refuse on unrelated differences. Keep original copies as installation
evidence. `just hooks-install` retains its existing non-clobbering bootstrap
behavior and is not an upgrade command for this instrument.
