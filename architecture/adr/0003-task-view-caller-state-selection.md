# ADR 0003: Caller-selected task views preserve completion facts

- Status: Accepted; implemented and activated at 5b265d0
- Date: 2026-09-07
- Authority: Lance's working-set and hard-cutover decisions; Daedalus approval
  of T-08216 (tree-state-selector), DESIGN.md revision 3
- Record: `wrkq.task-view.caller-state-selection` (active)
- Submission: `/Users/lherron/praesidium/var/wrkq-artifacts/T-08216/DESIGN.md`
- Counsel provenance: task room T-08216, revision 3 submitted as EN-05608

## Decision

The CLI specifies the task-state set; the server executes the filter and retains
ownership of traversal and pruning. The required states selector is independent
of lifecycle and pruneEmpty. The default working set is draft, open, in_progress.
The tree IncludeArchived/OpenOnly fields are removed in the same change, with
no aliases or deprecation window, as directed by Lance.

Both request DTOs join the workrpc schema catalog. Required states refuses an
omitting caller at the updated API; the existing schema-hash handshake refuses
an incompatible old server before business dispatch. The Cobra surface manifest
does not supply that handshake identity. Deployment replaces mini's producer
before installing consumer nodes; no database schema migration is involved.

Tree completion incorporates every built resident child before display pruning.
The direct-task closure predicate remains archived/deleted lifecycle or completed
state. Cancelled remains non-closed. The aggregation change removes false
completion claims caused by hidden unfinished descendants. With pruning disabled
by -a, every built child already participates, so this aggregation change
preserves its output. Filtered views may lose erroneous All done annotations.

## Verification and disposition

Source review confirmed the request-catalog/handshake connection, the recursive
result available before pruning, and the existing closure predicate. Revision 3
resolves the three flaws from prior reviews without changing task residency or
the external-backlink boundary in wrkq.task-hierarchy.cross-project-parents.
The remote boundary in wrkq.rpc.remote-transport-locator remains unchanged.

The record was initially proposed pending implementation evidence. Activation at
5b265d0 follows source verification and the producer's installed-surface results
in `/Users/lherron/praesidium/var/wrkq-artifacts/T-08216/GATES.txt`, together with
gate 16 submitted in EN-05740. The initial implementation at 7752a12 did not
enforce selection on single-task ls paths, suppressed selected completed tree
tasks, and accepted ls pruneEmpty without executing it. The 127a3d1 corrections
resolved those failures but pruned containers after the SQL page limit, making
later matches unreachable. The 5b265d0 query applies pruning before pagination
and removes the separate Go-side pruning path.

The acceptance evidence covers exclusion as well as inclusion, actual new-client
refusal against the old daemon during deployment, unchanged tree -a output,
removal of false completion annotations, cancelled remaining non-closed,
single-task selection, all-closed containers, non-default priority hydration,
and pruning with page limits. Daedalus independently repeated the installed
single-task exclusion/inclusion pair after 127a3d1. Other execution and fleet
results are producer-reported; no build, suite, installation, or restart was
performed by Daedalus for activation. The invariant carries these evidence
boundaries in last_verified, dated 2026-09-07.

The ls live-lifecycle default intentionally excludes archived/deleted task rows
that its former state-only SQL could return. The producer also reported that
the first schema cutover interrupted hcs, a Go pkg/client consumer outside the
four wrkq checkouts. The schema-compatibility deployment boundary therefore
extends beyond callers of the two changed methods. The reported consumer
inventory and separate TypeScript handshake question are not frozen by this
decision; the evidence does not establish a universal fleet inventory.
