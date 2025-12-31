# Deleted State and Restore Command

**Status:** Draft

## Overview

Add a `deleted` state for soft-deleted tasks and a `restore` command to bring them back. Also add `--force-uuid` flag to `touch` for external tooling that needs deterministic UUIDs.

## Motivation

- Distinguish between "user archived" and "soft deleted" tasks
- Allow restoration of deleted tasks with optional field updates
- Support external tooling that needs to create tasks with specific UUIDs

## Proposed Changes

### 1. Add `deleted` State

Add `deleted` to the task state enum.

**Schema migration:**
```sql
-- The CHECK becomes: state IN ('open','in_progress','completed','archived','deleted')
```

**Behavior:**
- `wrkq find` excludes `deleted` by default (same as `archived`)
- `wrkq find --state deleted` returns deleted tasks
- `wrkq find --state all` includes deleted tasks
- `wrkq set <task> --state deleted` marks task as deleted

### 2. Add `deleted_at` Timestamp Column

Track when a task was deleted (parallel to `archived_at`).

**Schema migration:**
```sql
ALTER TABLE tasks ADD COLUMN deleted_at TEXT;

-- Trigger to set deleted_at on state transition
CREATE TRIGGER tasks_au_deleted_at
AFTER UPDATE OF state ON tasks
WHEN NEW.state = 'deleted' AND OLD.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
  WHERE rowid = NEW.rowid AND deleted_at IS NULL;
END;

-- Clear deleted_at when restored
CREATE TRIGGER tasks_au_undelete
AFTER UPDATE OF state ON tasks
WHEN OLD.state = 'deleted' AND NEW.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = NULL WHERE rowid = NEW.rowid;
END;
```

### 3. Add `--force-uuid` Flag to `wrkq touch`

Allow creating a task with a specific UUID instead of auto-generating.

**Usage:**
```bash
wrkq touch inbox/my-task --force-uuid abc-123-def-456 -t "Task title"
```

**Behavior:**
- If UUID already exists → error (constraint violation)
- If UUID format invalid → error
- Otherwise → insert with provided UUID

**Validation:**
- Must be valid UUID v4 format (lowercase, with hyphens)
- Regex: `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`

### 4. Add `wrkq restore` Command

Restore a deleted (or archived) task to active state.

**Usage:**
```bash
# Restore by ID/UUID/path
wrkq restore T-00042
wrkq restore abc-123-def-456
wrkq restore inbox/my-task

# Restore with field updates (merge semantics)
wrkq restore T-00042 --title "Updated title" --description "New desc"

# Restore to different container (move + restore)
wrkq restore T-00042 --to inbox/new-location

# Add comment on restore
wrkq restore T-00042 --comment "Restored because..."
```

**Behavior:**
1. Find task by identifier (must be in `deleted` or `archived` state)
2. If `--to` specified and slug conflicts, error
3. Update any provided fields
4. Set `state = 'open'` (or `--state` if provided)
5. Clear `deleted_at` / `archived_at` timestamp
6. Bump `etag`
7. If `--comment` provided, add comment

**Flags:**
| Flag | Description |
|------|-------------|
| `--to <path>` | Restore to different container/slug |
| `--title <string>` | Update title on restore |
| `--description <string>` | Update description on restore |
| `--state <state>` | Restore to specific state (default: `open`) |
| `--priority <1-4>` | Update priority on restore |
| `--labels <json>` | Update labels on restore |
| `--assignee <slug>` | Update assignee on restore |
| `--if-match <etag>` | Conditional restore |
| `--comment <string>` | Add comment explaining restoration |

### 5. Query by UUID

Ensure `wrkq cat` by UUID finds the task regardless of state (explicit lookup, not filtered search).

```bash
wrkq cat abc-123-def-456  # Finds task even if deleted/archived
```

## Cascade Behavior

**When a parent task is deleted:**
- All subtasks are cascade deleted (set to `deleted` state)
- Subtask `deleted_at` timestamps are set

**When a parent task is restored:**
- All subtasks are cascade restored
- Subtask hierarchy is preserved

## Attachments

Attachments are **preserved** on deleted tasks. They remain in the filesystem and database, associated with the deleted task UUID. On restore, attachments are immediately available again.

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| `--force-uuid` with existing UUID | Error: "UUID already exists" |
| `restore` on non-deleted/archived task | Error: "Task is not deleted or archived" |
| Restore with slug conflict | Error unless `--to` specifies new path |
| Delete task with subtasks | Cascade delete all subtasks |
| Restore task with subtasks | Cascade restore all subtasks |
| `find` default behavior | Excludes `deleted` and `archived` |

## Migration Path

1. Add `deleted` to state CHECK constraint
2. Add `deleted_at` column
3. Add triggers for deleted_at management
4. Implement `--force-uuid` on touch
5. Implement `wrkq restore` command with cascade subtask handling
6. Update `find` to exclude deleted by default

## Testing

```bash
# Test 1: Force UUID creation
uuid=$(uuidgen | tr '[:upper:]' '[:lower:]')
wrkq touch inbox/test-task --force-uuid $uuid -t "Test"
wrkq cat $uuid --json | jq -e ".uuid == \"$uuid\""

# Test 2: Delete and restore
wrkq set inbox/test-task --state deleted
wrkq find inbox/test-task  # Should return nothing
wrkq find --state deleted inbox/test-task  # Should find it
wrkq restore $uuid
wrkq cat inbox/test-task --json | jq -e '.state == "open"'

# Test 3: Cascade delete/restore with subtasks
wrkq touch inbox/parent -t "Parent"
wrkq touch inbox/child --parent-task inbox/parent -t "Child"
wrkq set inbox/parent --state deleted
wrkq find --state deleted  # Should show both parent and child
wrkq restore inbox/parent
wrkq find inbox/parent  # Should show parent
wrkq find --parent-task inbox/parent  # Should show child

# Test 4: Restore with comment
wrkq set inbox/test-task --state deleted
wrkq restore inbox/test-task --comment "Restored for testing"
wrkq cat inbox/test-task  # Should show comment
```
