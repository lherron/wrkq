# WRKQ State Machine Spec (Current + Proposed)

This document is a self-contained description of how WRKQ task states work today and a proposal to improve triage handling. It is written for an LLM with no access to source code or other docs.

## 1. System Context (What WRKQ Is)

WRKQ is a task tracking system with a CLI. Tasks live inside containers (projects or subprojects). Tasks are the core work items and have workflow states, priorities, labels, and other metadata. The system persists tasks in a SQLite database and enforces allowed task states via a database CHECK constraint and a domain-level validator.

Key concepts:
- **Container**: A project or grouping of tasks. Tasks live under containers.
- **Task**: A unit of work with a lifecycle state.
- **State**: A workflow stage (idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted).
- **Labels**: User-defined tags (JSON array of strings) for categorization. Labels are orthogonal to workflow state.
- **Meta**: Freeform JSON attached to tasks (currently used in tests for triage metadata but not enforced by core behavior).
- **Archived vs Deleted**: Archived is a visibility filter (archived_at timestamp). Deleted is a state used for purge workflows.

Relevant user-facing CLI commands:
- `wrkq touch <path>` creates tasks, with `--state` to set initial state.
- `wrkq set <id>` updates task fields, including `--state`.
- `wrkq find` queries tasks, with default filters and `--state`.
- `wrkq rm` archives tasks; `--purge` deletes permanently.

## 2. Current Task Fields (Relevant to State Machine)

Core fields that matter to workflow:
- `state`: Current lifecycle state (enum).
- `archived_at`: Timestamp, not a state. When set, tasks are hidden from default queries.
- `deleted_at`: Timestamp set when state transitions to `deleted` (used for purges).
- `labels`: JSON array of strings.
- `meta`: JSON blob (freeform, not enforced by state machine).

Allowed task states today (enforced in DB and validation):
- `idea`
- `draft`
- `open`
- `in_progress`
- `completed`
- `blocked`
- `cancelled`
- `archived`
- `deleted`

### State Semantics (Current)

- **idea**: Pre-triage concept. Excluded from default `find` results and ignored by blocking logic. Used to capture early thoughts without committing to work.
- **draft**: Triage-ready but not yet committed to work. Included in default `find` results and treated as a blocking state in dependency checks.
- **open**: Ready to be worked on, not started.
- **in_progress**: Currently being worked on.
- **blocked**: Cannot proceed due to external dependency.
- **completed**: Done. Considered terminal for blockers.
- **cancelled**: Will not be done. Considered terminal for blockers.
- **archived**: Soft-deleted; typically set via `wrkq rm` (archived_at is set). Should be excluded by default queries.
- **deleted**: Hard-deleted state used for purges (deleted_at is set). Excluded by default queries.

### Lifecycle Flow (Current, Conceptual)

Common path:
- `idea` -> `draft` -> `open` -> `in_progress` -> `completed`

Other transitions:
- `open` or `in_progress` -> `blocked` -> `in_progress` or `completed`
- Any active state -> `cancelled`
- Any state -> `archived` (via `wrkq rm`)
- `archived` -> `deleted` (via `wrkq rm --purge`)

### Default Query Behavior (Current)

- Default `wrkq find` excludes `archived`, `deleted`, and `idea` tasks.
- `draft` tasks are included in default queries.

### Dependency Blocking Behavior (Current)

- A task is considered **blocked** if it has dependencies with kind `blocks` from tasks that are **not** in: `completed`, `archived`, `deleted`, `cancelled`, or `idea`.
- This means `draft`, `open`, `in_progress`, and `blocked` **do** block.
- `idea` tasks are **ignored** as blockers.

### Validation and Storage (Current)

- Allowed states are validated in the domain layer (explicit list).
- SQLite table has a CHECK constraint limiting state values.
- Adding a new state requires a DB migration that rebuilds the table (SQLite cannot alter CHECK constraints directly).

## 3. Current Triage Handling (Reality Today)

Triage is not a first-class state today. Instead:
- Some tests store triage status in `meta`, e.g. `{"triage_status":"queued"}` or `{"triage_status":"completed"}`.
- There is no runtime behavior wired to `meta.triage_status` in the state machine or query defaults.
- Docs describe `idea` as pre-triage and `draft` as triage-ready, but there is no explicit "in triage" stage.

Result: triage is fragmented between metadata and labels, and not well-aligned with actual state transitions or default views.

## 4. Proposed Change: Add a First-Class Triage State

### Proposal Overview

Introduce a new workflow state to represent active triage, e.g. `triage` or `in_triage`, and integrate it into all state handling (validation, DB constraints, default query behavior, and dependency logic).

This addresses current mismatch between labels/metadata and state by making triage explicit in the lifecycle.

### Candidate State Name

- `triage` (preferred for brevity)
- `in_triage` (explicit but longer)

Recommendation: use `triage` unless there is a strong need to match existing naming patterns (all current states are single tokens, snake_case; `triage` fits).

### Proposed Lifecycle (Option A - Most Consistent)

- `idea` -> `triage` -> `draft` -> `open` -> `in_progress` -> `completed`

Where:
- `idea`: untriaged concept (hidden by default)
- `triage`: actively being evaluated
- `draft`: triage complete, but not committed to execution
- `open`: ready to be picked up

### Alternative Lifecycle (Option B - Simplify)

- `idea` -> `triage` -> `open` -> `in_progress` -> `completed`

Here, `draft` is removed or repurposed. This reduces state complexity but is a larger conceptual change.

### Default Query Behavior for Triage

Two possible policies:
- **Policy 1 (include triage)**: Show triage tasks by default, like `draft` and `open`.
- **Policy 2 (exclude triage)**: Treat triage like `idea`, hidden from default queries.

Recommendation: include triage in default queries so triage work is visible and actionable.

### Dependency Blocking Behavior for Triage

Two possible policies:
- **Policy A (triage blocks)**: Treat `triage` like `draft`; it blocks tasks that depend on it.
- **Policy B (triage does not block)**: Treat `triage` like `idea`; it does not block.

Recommendation: make triage blocking if it represents active work that must finish before downstream tasks proceed.

## 5. Implementation Impact (What Must Change)

If a new state is added, the following must be updated:

1. **DB migration**
   - Add the new state to the `tasks.state` CHECK constraint.
   - SQLite requires rebuilding the table (similar to existing migrations that add `idea` or `draft`).

2. **Domain validation**
   - Update state validation to include the new state.

3. **CLI flags and docs**
   - Add the new state to `wrkq set --state` and `wrkq touch --state` help text.
   - Update `wrkq find --state` help and docs.

4. **Default find filters**
   - Decide whether to exclude or include `triage` by default.

5. **Blocked logic**
   - Decide whether triage should block and update the logic accordingly.

6. **Docs**
   - Update SPEC and CLI reference to reflect the new lifecycle.

7. **Tests**
   - Add tests validating the new state.
   - Update tests for default find filtering and blocked behavior.

## 6. Migration Strategy (For Existing Data)

Current triage status may exist in task meta. Suggested migration options:

### Option 1: No Automatic Migration
- Leave existing `meta.triage_status` untouched.
- Introduce new state for future tasks only.
- Pros: low risk.
- Cons: historical triage info remains fragmented.

### Option 2: Best-Effort Migration
- Map `meta.triage_status` to `triage` state, e.g.:
  - `queued` or `in_progress` -> `triage`
  - `completed` -> `draft` (if triage complete) or `open` (if accepted)
- Pros: cleans up old triage data.
- Cons: requires assumptions and may be incorrect.

Recommendation: start with Option 1 unless there is strong confidence in existing triage metadata semantics.

## 7. Open Questions (Need Decisions)

1. What should the new state be named: `triage` or `in_triage`?
2. Should triage be inserted between `idea` and `draft`, or should it replace `draft` entirely?
3. Should triage tasks appear in default `wrkq find` results?
4. Should triage tasks block dependencies?
5. Should `meta.triage_status` be migrated or deprecated?
6. Should any labels (e.g. `triage`) be auto-managed or explicitly discouraged?

## 8. Recommendations (Based on Current System)

- Add a new `triage` state between `idea` and `draft`.
- Include `triage` in default `wrkq find` results so triage work is visible.
- Treat `triage` as blocking in dependency checks to prevent premature downstream work.
- Deprecate use of `meta.triage_status` for workflow control; keep it optional for historical context only.
- Document the lifecycle clearly and keep labels strictly orthogonal to workflow.

## 9. Summary (One-Paragraph Version)

WRKQ currently models pre-triage with `idea` and triage-ready with `draft`, but active triage is only captured in metadata and labels, not the actual state machine. This leads to ambiguity in filtering and workflow logic. The proposed change is to add a first-class `triage` state between `idea` and `draft`, make it visible in default queries, and (optionally) treat it as a blocking state for dependencies. This requires a DB migration, state validation updates, CLI help updates, and test/doc changes. Open questions center on naming, lifecycle placement, default visibility, dependency impact, and whether to migrate existing metadata.
