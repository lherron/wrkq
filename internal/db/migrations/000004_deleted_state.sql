-- Migration: Add 'deleted' task state and deleted_at column
-- This migration adds soft-delete support distinct from archiving.
-- Deleted tasks are hidden by default and can be restored.

-- SQLite doesn't support ALTER TABLE to modify CHECK constraints,
-- so we must recreate the table.

PRAGMA foreign_keys = OFF;

-- Drop view that depends on tasks table
DROP VIEW IF EXISTS v_task_paths;

-- Drop existing triggers
DROP TRIGGER IF EXISTS tasks_ai_friendly;
DROP TRIGGER IF EXISTS tasks_au_etag;

-- Create new tasks table with 'deleted' state and deleted_at column
CREATE TABLE tasks_new (
  uuid TEXT PRIMARY KEY
       DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id   TEXT UNIQUE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  -- Extended state enum: added 'deleted' for soft-delete
  state TEXT NOT NULL CHECK (state IN ('draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  parent_task_uuid TEXT REFERENCES tasks_new(uuid) ON DELETE CASCADE,
  assignee_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,
  description TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  deleted_at   TEXT,  -- New: soft-delete timestamp
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

-- Copy existing data (deleted_at will be NULL for all existing rows)
INSERT INTO tasks_new (
  uuid, id, slug, title, project_uuid, state, priority,
  kind, parent_task_uuid, assignee_actor_uuid,
  start_at, due_at, labels, description, etag,
  created_at, updated_at, completed_at, archived_at,
  created_by_actor_uuid, updated_by_actor_uuid
)
SELECT
  uuid, id, slug, title, project_uuid, state, priority,
  kind, parent_task_uuid, assignee_actor_uuid,
  start_at, due_at, labels, description, etag,
  created_at, updated_at, completed_at, archived_at,
  created_by_actor_uuid, updated_by_actor_uuid
FROM tasks;

-- Drop old table
DROP TABLE tasks;

-- Rename new table
ALTER TABLE tasks_new RENAME TO tasks;

-- Re-enable foreign key checks
PRAGMA foreign_keys = ON;

-- Recreate indexes
CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);
CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);
CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_uuid) WHERE parent_task_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_idx ON tasks(assignee_actor_uuid) WHERE assignee_actor_uuid IS NOT NULL;
CREATE INDEX tasks_kind_idx ON tasks(kind);
CREATE INDEX tasks_deleted_at_idx ON tasks(deleted_at) WHERE deleted_at IS NOT NULL;

-- Recreate triggers
CREATE TRIGGER tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq (id) VALUES (NULL);
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_etag
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
  UPDATE tasks SET etag = OLD.etag + 1 WHERE rowid = NEW.rowid;
END;

-- Trigger to set deleted_at when state transitions to 'deleted'
CREATE TRIGGER tasks_au_deleted_at
AFTER UPDATE OF state ON tasks
WHEN NEW.state = 'deleted' AND OLD.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
  WHERE rowid = NEW.rowid AND deleted_at IS NULL;
END;

-- Trigger to clear deleted_at when restored from 'deleted' state
CREATE TRIGGER tasks_au_undelete
AFTER UPDATE OF state ON tasks
WHEN OLD.state = 'deleted' AND NEW.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = NULL WHERE rowid = NEW.rowid;
END;

-- Recreate v_task_paths view with deleted_at
CREATE VIEW v_task_paths AS
SELECT t.uuid,
       t.id,
       t.slug,
       t.title,
       t.state,
       t.priority,
       t.kind,
       t.parent_task_uuid,
       t.assignee_actor_uuid,
       t.start_at,
       t.due_at,
       t.labels,
       t.etag,
       t.created_at,
       t.updated_at,
       t.completed_at,
       t.archived_at,
       t.deleted_at,
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid;
