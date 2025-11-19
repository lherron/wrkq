# Milestone Task Verification

**Date**: 2025-11-18
**Status**: All milestones properly tracked in wrkq CLI
**Database**: `/tmp/claude/test-wrkq.db`

---

## Summary

✅ **All milestone tasks from SPEC.md and CLI-MVP.md are now tracked in the wrkq CLI**

This verification confirms that the project is successfully dogfooding itself - using wrkq to manage its own development.

---

## Milestone Breakdown

### M0 — Core CLI + DB (COMPLETE)

**Status**: ✅ All 10 tasks completed
**Container**: `todo/m0/` (P-00004)

| Task ID | Slug | Title | State |
|---------|------|-------|-------|
| T-00001 | core-cli | Core CLI commands (mkdir, touch, ls, stat) | completed |
| T-00002 | database | SQLite database with migrations | completed |
| T-00003 | friendly-ids | Friendly ID generation (T-, P-, A-) | completed |
| T-00004 | etag-concurrency | ETag-based optimistic locking | completed |
| T-00005 | event-log | Append-only event log | completed |
| T-00006 | actors | Multi-actor support | completed |
| T-00007 | pathspecs | Path resolution and globs | completed |
| T-00008 | mv-command | Move/rename command | completed |
| T-00009 | rm-restore | Archive and restore commands | completed |
| T-00010 | cat-set | Content viewing and mutation | completed |

**Reference**: M0-SUMMARY.md confirms implementation complete (~3,900 LOC)

---

### M1 — Editing, History & Search (COMPLETE)

**Status**: ✅ Core features complete (10/10), optional features deferred (5 open)
**Container**: `todo/m1/` (P-00005)

#### Core M1 Features (All Complete)

| Task ID | Slug | Title | State |
|---------|------|-------|-------|
| T-00011 | find-command | Implement find command with filters | completed |
| T-00012 | tree-command | Implement tree command | completed |
| T-00013 | log-command | Implement log command for history | completed |
| T-00014 | watch-command | Implement watch for event streaming | completed |
| T-00015 | diff-command | Implement diff for task comparison | completed |
| T-00016 | apply-command | Implement apply for file/stdin input | completed |
| T-00017 | edit-command | Implement edit with $EDITOR | completed |
| T-00018 | 3way-merge | Implement 3-way merge logic | completed |
| T-00019 | smoke-tests | Write smoke tests for M1 | completed |
| T-00020 | documentation | Write M1 documentation | completed |

#### Optional M1 Features (Deferred)

| Task ID | Slug | Title | State | Priority |
|---------|------|-------|-------|----------|
| T-00021 | fields-flag | Add --fields flag for field selection | open | 3 |
| T-00022 | sort-flag | Add --sort flag for custom sorting | open | 3 |
| T-00023 | golden-tests | Write golden tests for outputs | open | 2 |
| T-00024 | unit-tests | Write unit tests for merge logic | open | 2 |
| T-00025 | perf-benchmarks | Performance benchmarks (5k tasks) | open | 3 |

**Reference**: M1-SUMMARY.md confirms implementation complete (~2,350 LOC added)

---

### M2 — Attachments, Scale & Packaging (NEXT)

**Status**: 📋 All 10 tasks planned and open
**Container**: `todo/m2/` (P-00006)

| Task ID | Slug | Title | State | Priority |
|---------|------|-------|-------|----------|
| T-00026 | attachments | Implement attachment management (put/get/ls/rm) | open | 1 |
| T-00027 | pagination | Add pagination cursors for large result sets | open | 2 |
| T-00028 | bulk-ops | Add bulk operation support (--jobs, --continue-on-error) | open | 2 |
| T-00029 | copy-command | Implement cp command for copying tasks | open | 2 |
| T-00030 | purge-delete | Implement hard delete with --purge | open | 3 |
| T-00031 | doctor-tools | Database health check and repair tools | open | 3 |
| T-00032 | completions | Enhanced shell completions (bash/zsh/fish) | open | 3 |
| T-00033 | goreleaser | GoReleaser config for packaging | open | 2 |
| T-00034 | install-scripts | Install scripts for easy deployment | open | 3 |
| T-00035 | sbom | Software Bill of Materials generation | open | 4 |

**Per CLI-MVP.md**: M2 is the current active milestone after M0 and M1 completion.

**Key deliverables**:
- Attachment filesystem integration
- Pagination for large result sets
- Bulk operation flags
- Copy command
- Hard delete with purge
- Database health tools
- Shell completions
- Binary packaging and distribution

---

### M3 — API-ready Contracts + Comments (DEFERRED)

**Status**: ⏸️ Deferred per CLI-MVP.md line 174: "M3 — DEFERRED, DO NOT IMPLEMENT YET"
**Container**: `todo/m3/` (P-00007)

| Task ID | Slug | Title | State |
|---------|------|-------|-------|
| T-00036 | comments-schema | Add comments table schema | open |
| T-00037 | comment-commands | Implement comment add/ls/rm commands | open |
| T-00038 | machine-interface | Freeze machine interface v1 contracts | open |
| T-00039 | http-spec | HTTP/JSON façade specification | open |
| T-00040 | api-docs | Browser UI enablers documentation | open |

**Note**: These tasks are tracked but should not be implemented until M2 is complete. They prepare for future browser UI integration.

---

### M4 — Stretch Features (NOT TRACKED)

**Status**: ❌ Not yet tracked (optional future work)
**Container**: Does not exist

Per CLI-MVP.md, M4 includes:
- SQLite FTS for content search (`wrkq rg` command)
- Advanced diff/patch features
- Minimal HTTP server stub
- Additional doctor checks

**Decision**: M4 tasks are optional and will be added only if/when M3 is started.

---

### Meta Tasks

**Container**: `todo/meta/` (P-00003)

| Task ID | Slug | Title | State |
|---------|------|-------|-------|
| T-00041 | dogfood-migration | Migrate from TODO.md to wrkq CLI | completed |
| T-00042 | workflow-setup | Set up daily wrkq workflow and aliases | open |
| T-00043 | ci-integration | Integrate wrkq with CI/CD pipeline | open |

**Purpose**: Project management and workflow improvements for using wrkq to manage itself.

---

## Verification Against Specifications

### SPEC.md Coverage

All commands defined in SPEC.md §9 are accounted for:

- ✅ M0: `init`, `whoami`, `actors`, `mkdir`, `touch`, `mv`, `rm`, `restore`, `ls`, `stat`, `cat`, `set`, `version`, `completion`
- ✅ M1: `edit`, `apply`, `log`, `watch`, `diff`, `tree`, `find`
- 📋 M2: `attach`, `cp`, `doctor` (planned)
- ⏸️ M3: Comments commands (deferred)

### CLI-MVP.md Coverage

All milestone deliverables from CLI-MVP.md are tracked:

| Milestone | Deliverables Listed | Tasks Tracked | Status |
|-----------|---------------------|---------------|--------|
| M0 | Core CLI, DB, migrations, IDs, ETags, events, actors, CRUD | 10 | ✅ Complete |
| M1 | Edit, apply, log, watch, diff, tree, find, 3-way merge | 15 (10 core + 5 optional) | ✅ Core complete |
| M2 | Attachments, pagination, bulk ops, cp, purge, doctor, packaging | 10 | 📋 Planned |
| M3 | Comments, machine interface freeze, HTTP spec | 5 | ⏸️ Deferred |
| M4 | FTS, advanced diff, HTTP server stub | 0 | ❌ Not tracked |

---

## Database Statistics

```
Total containers: 7
Total tasks: 43
  - Completed: 21 (M0: 10, M1: 10, meta: 1)
  - Open: 22 (M1 optional: 5, M2: 10, M3: 5, meta: 2)
  - Archived: 0
```

---

## Recommended Next Actions

1. **Start M2 implementation**:
   ```bash
   wrkq find 'todo/m2/**' --type t --state open --json | \
     jq -r 'sort_by(.priority) | .[0:3] | .[] | "\(.id) \(.title)"'
   ```

2. **Set up workflow aliases** (per DOGFOOD.md):
   ```bash
   alias tl='wrkq tree todo'
   alias tn='wrkq find --state open'
   alias td='wrkq find --state completed'
   alias tw='wrkq watch --follow --ndjson'
   alias te='EDITOR=vim wrkq edit'
   ```

3. **Mark dogfood migration complete**:
   ```bash
   wrkq set T-00041 state=completed  # Already done
   ```

4. **Track M2 progress**:
   ```bash
   # See progress
   wrkq find 'todo/m2/**' --type t --state completed --json | jq 'length'

   # Watch activity
   wrkq watch --follow --ndjson | jq -r 'select(.resource_type == "task")'
   ```

---

## Compliance Checklist

- ✅ All M0 deliverables tracked and marked complete
- ✅ All M1 core deliverables tracked and marked complete
- ✅ All M1 optional features tracked as open
- ✅ All M2 deliverables tracked as open
- ✅ M3 deliverables tracked but clearly deferred per spec
- ✅ Meta tasks for dogfooding tracked
- ✅ Migration script fixed (todo/* paths instead of wrkq/* paths)
- ✅ All tasks have proper state (open/completed/archived)
- ✅ All tasks have priority set (1-4)
- ✅ Project is successfully dogfooding itself

---

## Conclusion

**Status**: ✅ **VERIFIED - All milestones and tasks properly tracked**

The wrkq project is now successfully using itself for development tracking. All milestone tasks from the specification documents (SPEC.md and CLI-MVP.md) are present in the database with correct states and priorities.

The system is ready for M2 development and demonstrates that wrkq is "ready to dogfood" as documented in READY-TO-DOGFOOD.md.

---

## Appendix: Tree View

```
todo
├── m0/ [P-00004] - 10 tasks, all completed
├── m1/ [P-00005] - 15 tasks (10 completed, 5 open)
├── m2/ [P-00006] - 10 tasks, all open (NEXT MILESTONE)
├── m3/ [P-00007] - 5 tasks, all open (DEFERRED)
└── meta/ [P-00003] - 3 tasks (1 completed, 2 open)
```

Total: 43 tasks across 4 active milestones + meta.
