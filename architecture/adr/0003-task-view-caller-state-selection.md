# ADR 0003: Caller-selected task views preserve completion facts

- Status: Accepted design; implementation pending
- Date: 2026-09-07
- Authority: Lance's working-set and hard-cutover decisions; Daedalus approval
  of T-08216 (tree-state-selector), DESIGN.md revision 3
- Record: `wrkq.task-view.caller-state-selection` (proposed, not active law)
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

This is design verification, not evidence of implemented enforcement. No build,
test, installation, or service-health result is asserted. The proposed record
has no last_verified stamp and becomes active only on implementation evidence.
Its installed-surface acceptance obligations are those submitted in revision 3;
the requester owns their execution and Daedalus owns the record's activation.
