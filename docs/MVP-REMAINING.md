# wrkq MVP - Remaining Work

This document tracks remaining milestone tasks for the `wrkq` CLI based on SPEC.md requirements and current implementation status.

**Last updated:** 2025-11-20

---

## Completion Status Summary

| Milestone | Status | Completion |
|-----------|--------|------------|
| **M0 — Core CLI + DB** | ✅ Complete | 10/10 tasks |
| **M1 — Editing, History & Search** | ✅ Complete | 16/16 tasks |
| **M2 — Attachments, Scale & Packaging** | ✅ Complete | 11/11 tasks |
| **M3 — Comments & Collaboration** | ✅ Complete | 7/7 tasks |
| **M4 — Stretch (optional)** | ⏭️ Deferred | 0/2 tasks (archived) |
| **M5 — Git-ops & Bundle Commands** | 🚧 In Progress | 3/8 tasks |
| **wrkqadm Binary Separation** | ✅ Complete | 9/9 tasks |

---

## M5 — Git-ops & Bundle Commands (Remaining)

Goal: Enable PR-based agent workflows with bundle create/apply, ephemeral DB snapshots, and conflict detection.

### ✅ Completed (wrkqadm infrastructure)

- ✅ **T-00078**: `wrkqadm db snapshot` - WAL-safe DB snapshots for ephemeral work
- ✅ **T-00079**: `wrkqadm bundle apply` - Apply bundles to main DB with conflict detection
- ✅ **T-00080**: `wrkqadm attach path` - Resolve attachment filesystem paths

### ❌ Remaining (wrkq agent-facing + bundle format)

#### 1. Bundle Format & Internals

**Status:** Not started
**Priority:** P1 (blocking bundle create/apply full functionality)

**Deliverables:**
- `internal/bundle/`: Bundle creation, manifest generation, task export/import with `base_etag` tracking
- Bundle directory structure:
  - `manifest.json` - version, machine_interface_version, metadata
  - `events.ndjson` - event log slice for audit trail (optional with `--no-events`)
  - `containers.txt` - container paths to ensure exist
  - `tasks/<path>.md` - task documents with `base_etag` for conflict detection
  - `attachments/<task_uuid>/*` - attachment files when `--with-attachments`
- Deterministic, reviewable format using existing `wrkq cat` output
- `base_etag` computation from event log for conflict detection

**SPEC reference:** §18.1, §18.2, CLI-MVP M5 notes

---

#### 2. `wrkq bundle create` Command

**Status:** Not started
**Priority:** P1 (core M5 feature)

**Synopsis:**
```bash
wrkq bundle create [--out <dir>] [--actor <slug|A-xxxxx>] \
  [--since <ts>] [--until <ts>] [--with-attachments] [--no-events] \
  [--json|--porcelain]
```

**Deliverables:**
- CLI command in `internal/cli/bundle.go` (wrkq binary)
- Creates bundle directory (default `.wrkq/`)
- Filters by actor and time window
- Computes `base_etag` from earliest event per task
- Exports attachments when `--with-attachments`
- Machine outputs (`--json`, `--porcelain`)

**Acceptance:**
- `bundle create --actor agent-foo` produces deterministic bundle with only agent-foo's changes
- Round-trips correctly with `bundle apply`
- Attachments survive the round-trip

**SPEC reference:** §18.1, CLI-MVP demo script

---

#### 3. Typed Selectors (`t:<token>`, `c:<token>`)

**Status:** Not started
**Priority:** P1 (required for robust bundle apply and scripting)

**Deliverables:**
- `internal/selectors/`: Typed selector parsing
- `t:<token>` where `<token>` can be:
  - Friendly ID (`T-00123`)
  - UUID
  - Path (`portal/auth/login-ux`)
- `c:<token>` for comment IDs
- Extend existing pathspec resolution; backward compatible
- Update all commands to accept typed selectors

**Examples:**
```bash
wrkq cat t:T-00123
wrkq set t:portal/auth/login-ux priority=2
wrkq comment ls t:T-00123
wrkq comment cat c:C-00012
```

**Acceptance:**
- `t:T-00123` resolves identically to `T-00123` in all commands
- `t:portal/auth/login-ux` resolves to task by path
- `c:C-00012` resolves comment by friendly ID
- Backward compatible with existing addressing

**SPEC reference:** §6 (Addressing and Pathspecs), §18 typed selectors

---

#### 4. `wrkq apply --base` Flag Enhancement

**Status:** Not started
**Priority:** P2 (enhances merge capabilities)

**Synopsis:**
```bash
wrkq apply [<PATHSPEC|ID>] <FILE|-> --base <FILE> [--if-match <etag>] [--dry-run]
```

**Deliverables:**
- Extend `internal/cli/apply.go` with `--base` flag
- Perform 3-way merge (base/current/edited) using existing `internal/edit` machinery
- Honor `--if-match` for final write
- Exit code 4 on unresolvable conflicts

**Acceptance:**
- `apply --base` successfully merges non-conflicting concurrent edits
- Conflicts exit 4 with helpful error message
- `--if-match` honored for final write

**SPEC reference:** §18.4, CLI-MVP M5 notes

---

#### 5. Enhanced `wrkqadm bundle apply` with Full Bundle Format Support

**Status:** Partially complete (basic implementation exists)
**Priority:** P1 (complete the bundle apply implementation)

**Current state:**
- Basic `bundle apply` command exists (T-00079)
- Needs enhancement to support full bundle format

**Deliverables:**
- Read `manifest.json` and validate `machine_interface_version`
- Create containers from `containers.txt`
- Apply tasks using `t:<uuid>` selector (fallback to `t:<path>` for new tasks)
- Honor `base_etag` with `--if-match` semantics; exit 4 on conflict
- Re-attach files from bundle's `attachments/` directory
- Show `wrkq diff` output on conflicts to aid resolution
- Support `--dry-run`, `--continue-on-error`, `--json`

**Acceptance:**
- `bundle apply --dry-run` validates without writes
- `bundle apply` on main DB detects etag conflicts and exits 4 with helpful diff
- Manifest version check prevents applying bundles from incompatible CLI versions

**SPEC reference:** §18.2

---

#### 6. Bundle Documentation & Examples

**Status:** Not started
**Priority:** P2 (accompanies implementation)

**Deliverables:**
- Update `docs/SPEC.md` with bundle format specification
- Add examples to `docs/EXAMPLES.md` or similar
- Document agent workflow:
  1. Snapshot DB for ephemeral work
  2. Make changes with agent actor
  3. Export bundle for PR
  4. Validate with `--dry-run`
  5. Apply to main DB after review
- Document conflict resolution patterns
- Update `CLAUDE.md` with bundle workflow

**SPEC reference:** §18 examples, CLI-MVP M5 demo script

---

#### 7. `wrkqadm bundle replay` (Optional / Future)

**Status:** Not started
**Priority:** P4 (stretch goal, optional)

**Synopsis:**
```bash
wrkqadm bundle replay [--from <dir>] [--dry-run] [--strict-etag]
```

**Deliverables:**
- Replay `events.ndjson` in order
- With `--strict-etag`, verify each event's etag and exit 4 on divergence
- Useful when you know main hasn't diverged

**SPEC reference:** §18.5

---

#### 8. Integration Tests for Bundle Workflow

**Status:** Not started
**Priority:** P1 (must ship with bundle commands)

**Deliverables:**
- End-to-end tests for bundle create → apply round-trip
- Conflict detection tests (concurrent edits)
- Attachment round-trip tests
- Manifest version incompatibility tests
- Multi-actor bundle filtering tests
- Add to `test/smoke-m5.sh` or similar

---

## M4 — Stretch (Deferred)

These tasks are archived and not required for MVP:

- ⏭️ **T-00071**: HTTP server stub (future work, not blocking)
- ⏭️ **T-00072**: Enhanced doctor checks (current doctor is sufficient)

**Potential future work:**
- SQLite FTS virtual table for full-text search (`wrkq rg` command)
- Advanced diff/patch features (structured hunks for UI)
- Minimal Node or Go HTTP server implementing the spec
- Additional `wrkq doctor` checks (VACUUM/ANALYZE guidance)

---

## Meta Tasks (Open)

- 🔲 **T-00042**: Set up daily wrkq workflow and aliases
- 🔲 **T-00043**: Integrate wrkq with CI/CD pipeline

These are housekeeping tasks, not blocking MVP completion.

---

## Definition of Done for M5

Before M5 is considered complete:

1. ✅ `wrkqadm db snapshot` creates WAL-safe snapshots
2. ✅ `wrkqadm attach path` resolves attachment paths
3. ❌ `wrkq bundle create` exports deterministic bundles filtered by actor/time
4. ❌ `wrkqadm bundle apply` imports bundles with conflict detection
5. ❌ Bundle format documented and stable (manifest.json, tasks/*.md, etc.)
6. ❌ Typed selectors (`t:`, `c:`) work across all commands
7. ❌ `wrkq apply --base` supports 3-way merge
8. ❌ Integration tests pass for full bundle workflow
9. ❌ Documentation updated with agent workflow examples
10. ✅ `wrkq version --json` exposes `machine_interface_version: 1`

---

## Post-MVP Enhancements (Not Required)

These are potential future features beyond M5:

1. **Bundle encryption** - Encrypt bundles for secure PR sharing
2. **Bundle signing** - GPG/SSH signatures for bundle authenticity
3. **Incremental bundles** - Only export changed tasks since last bundle
4. **Bundle merge strategies** - Customizable conflict resolution
5. **HTTP API** - RESTful API for browser UI (Node/React)
6. **Real-time sync** - WebSocket-based event streaming for UI
7. **Content search** - SQLite FTS integration for `wrkq rg` command
8. **Recurrence engine** - Recurring tasks and calendar sync
9. **Named filters** - Save and reuse complex `find` queries

---

## Priority Guidance

**Immediate focus (P1):**
1. Bundle format implementation (`internal/bundle/`)
2. `wrkq bundle create` command
3. Typed selectors (`t:`, `c:`)
4. Enhanced `wrkqadm bundle apply` with full format support
5. Integration tests for bundle workflow

**Secondary (P2):**
1. `wrkq apply --base` flag
2. Bundle documentation and examples

**Nice to have (P3-P4):**
1. `wrkqadm bundle replay` (optional)
2. Meta tasks (workflow setup, CI integration)

---

## Notes

- All M5 tasks build on stable foundations from M0-M3
- Bundle format uses existing `wrkq cat` task document format for reviewability
- `base_etag` computed from event log enables conflict detection without schema changes
- Commands honor existing exit codes, addressing conventions, and machine interface stability
- Attachments use established `attach_dir/tasks/<task_uuid>/` layout

See `docs/SPEC.md` §18 for complete specification of bundle commands and workflow.
