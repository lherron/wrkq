-- Migration: Restore task updated_at and state consistency triggers
-- Reintroduces automatic updated_at touch and completed/archived timestamps.

DROP TRIGGER IF EXISTS tasks_au_touch;
DROP TRIGGER IF EXISTS tasks_au_state_consistency;

CREATE TRIGGER tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_state_consistency
AFTER UPDATE OF state ON tasks
BEGIN
  -- Set completed_at on transition to completed (if not already set)
  UPDATE tasks
     SET completed_at = COALESCE(NEW.completed_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'completed'
     AND NEW.completed_at IS NULL;

  -- Set archived_at on transition to archived (if not already set)
  UPDATE tasks
     SET archived_at = COALESCE(NEW.archived_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'archived'
     AND NEW.archived_at IS NULL;
END;
