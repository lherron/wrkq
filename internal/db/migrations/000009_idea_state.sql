-- Migration: Add 'idea' task state
-- This migration adds 'idea' as a pre-triage task state that precedes 'draft'.

-- SQLite doesn't support ALTER TABLE to modify CHECK constraints,
-- so we must recreate the table.

PRAGMA foreign_keys = OFF;

-- Drop view that depends on tasks table
DROP VIEW IF EXISTS v_task_paths;

-- Drop existing triggers
DROP TRIGGER IF EXISTS tasks_ai_friendly;
DROP TRIGGER IF EXISTS tasks_au_etag;
DROP TRIGGER IF EXISTS tasks_au_deleted_at;
DROP TRIGGER IF EXISTS tasks_au_undelete;

-- Create new tasks table with updated CHECK constraint including 'idea'
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
  -- Extended state enum: added 'idea' before 'draft'
  state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  parent_task_uuid TEXT REFERENCES tasks_new(uuid) ON DELETE CASCADE,
  assignee_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  requested_by_project_id TEXT,
  assigned_project_id TEXT,
  acknowledged_at TEXT,
  resolution TEXT,
  cp_project_id TEXT,
  cp_run_id TEXT,
  cp_session_id TEXT,
  sdk_session_id TEXT,
  run_status TEXT CHECK (run_status IN ('queued','running','completed','failed','cancelled','timed_out')),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,
  meta     TEXT,
  description TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  deleted_at   TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

-- Copy existing data
INSERT INTO tasks_new (
  uuid, id, slug, title, project_uuid, state, priority,
  kind, parent_task_uuid, assignee_actor_uuid,
  requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
  cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status,
  start_at, due_at, labels, meta, description, etag,
  created_at, updated_at, completed_at, archived_at, deleted_at,
  created_by_actor_uuid, updated_by_actor_uuid
)
SELECT
  uuid, id, slug, title, project_uuid, state, priority,
  kind, parent_task_uuid, assignee_actor_uuid,
  requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
  cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status,
  start_at, due_at, labels, meta, description, etag,
  created_at, updated_at, completed_at, archived_at, deleted_at,
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
CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);
CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);
CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;
CREATE INDEX tasks_cp_run_id_idx ON tasks(cp_run_id);
CREATE INDEX tasks_cp_session_id_idx ON tasks(cp_session_id);

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

-- Recreate v_task_paths view
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
       t.requested_by_project_id,
       t.assigned_project_id,
       t.acknowledged_at,
       t.resolution,
       t.cp_project_id,
       t.cp_run_id,
       t.cp_session_id,
       t.sdk_session_id,
       t.run_status,
       t.start_at,
       t.due_at,
       t.labels,
       t.meta,
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
