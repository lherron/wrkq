-- Migration: Planning Layer Extensions
-- Adds: container kinds, sections, subtasks, task relations, assignee support

-- Sequence for section friendly IDs
CREATE TABLE section_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

--------------------------------------------------------------------------------
-- CONTAINER EXTENSIONS
--------------------------------------------------------------------------------

-- Add kind to containers (project, feature, area, misc)
-- Default to 'project' for existing containers
ALTER TABLE containers ADD COLUMN kind TEXT NOT NULL DEFAULT 'project';

-- Add sort_index for ordering features within a section
ALTER TABLE containers ADD COLUMN sort_index INTEGER NOT NULL DEFAULT 0;

-- Trigger to validate container kind on insert/update
CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, or misc');
END;

CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, or misc');
END;

--------------------------------------------------------------------------------
-- SECTIONS TABLE
--------------------------------------------------------------------------------

CREATE TABLE sections (
  uuid TEXT NOT NULL PRIMARY KEY
        DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id TEXT UNIQUE,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE CASCADE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  order_index INTEGER NOT NULL DEFAULT 0,
  role TEXT NOT NULL DEFAULT 'ready',
  is_default INTEGER NOT NULL DEFAULT 0,
  wip_limit INTEGER,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  UNIQUE(project_uuid, slug)
);

-- Trigger to validate section role
CREATE TRIGGER sections_role_check_insert
BEFORE INSERT ON sections
WHEN NEW.role NOT IN ('backlog', 'ready', 'active', 'review', 'done')
BEGIN
  SELECT RAISE(ABORT, 'Invalid section role: must be backlog, ready, active, review, or done');
END;

CREATE TRIGGER sections_role_check_update
BEFORE UPDATE OF role ON sections
WHEN NEW.role NOT IN ('backlog', 'ready', 'active', 'review', 'done')
BEGIN
  SELECT RAISE(ABORT, 'Invalid section role: must be backlog, ready, active, review, or done');
END;

-- Auto-generate friendly ID for sections
CREATE TRIGGER sections_ai_friendly
AFTER INSERT ON sections
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO section_seq (id) VALUES (NULL);
  UPDATE sections
     SET id = 'S-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Auto-touch updated_at on sections
CREATE TRIGGER sections_au_touch
AFTER UPDATE ON sections
BEGIN
  UPDATE sections SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

CREATE INDEX sections_project_idx ON sections(project_uuid);
CREATE INDEX sections_role_idx ON sections(role);

-- Add section_uuid to containers (for features)
-- Must be added after sections table exists
ALTER TABLE containers ADD COLUMN section_uuid TEXT REFERENCES sections(uuid) ON DELETE SET NULL;

CREATE INDEX containers_section_idx ON containers(section_uuid) WHERE section_uuid IS NOT NULL;

--------------------------------------------------------------------------------
-- TASK TABLE RECREATION
-- SQLite doesn't support ALTER CHECK, so we recreate the table with:
-- 1. Extended state enum (blocked, cancelled)
-- 2. New columns: kind, parent_task_uuid, assignee_actor_uuid
--------------------------------------------------------------------------------

-- First, drop views that depend on tasks table
DROP VIEW IF EXISTS v_task_paths;
DROP VIEW IF EXISTS v_container_paths;

-- Temporarily disable foreign key checks for table recreation
PRAGMA foreign_keys = OFF;

-- Drop existing triggers that reference tasks
DROP TRIGGER IF EXISTS tasks_ai_friendly;
DROP TRIGGER IF EXISTS tasks_au_touch;
DROP TRIGGER IF EXISTS tasks_au_state_consistency;

-- Create new tasks table with updated schema
CREATE TABLE tasks_new (
  uuid TEXT NOT NULL PRIMARY KEY
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
  -- Extended state enum: added blocked, cancelled
  state TEXT NOT NULL CHECK (state IN ('open','in_progress','completed','archived','blocked','cancelled')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  -- New: task kind
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  -- New: parent task for subtasks
  parent_task_uuid TEXT REFERENCES tasks_new(uuid) ON DELETE CASCADE,
  -- New: assignee
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
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

-- Copy existing data (new columns get defaults)
INSERT INTO tasks_new (
  uuid, id, slug, title, project_uuid, state, priority,
  kind, parent_task_uuid, assignee_actor_uuid,
  start_at, due_at, labels, description, etag,
  created_at, updated_at, completed_at, archived_at,
  created_by_actor_uuid, updated_by_actor_uuid
)
SELECT
  uuid, id, slug, title, project_uuid, state, priority,
  'task', NULL, NULL,
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

-- Enforce single-level subtask constraint (subtasks cannot have subtasks)
CREATE TRIGGER tasks_subtask_depth_check_insert
BEFORE INSERT ON tasks
WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'Subtasks cannot have subtasks (max depth is 1)')
  WHERE EXISTS (
    SELECT 1 FROM tasks WHERE uuid = NEW.parent_task_uuid AND parent_task_uuid IS NOT NULL
  );
END;

CREATE TRIGGER tasks_subtask_depth_check_update
BEFORE UPDATE OF parent_task_uuid ON tasks
WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'Subtasks cannot have subtasks (max depth is 1)')
  WHERE EXISTS (
    SELECT 1 FROM tasks WHERE uuid = NEW.parent_task_uuid AND parent_task_uuid IS NOT NULL
  );
END;

--------------------------------------------------------------------------------
-- TASK RELATIONS TABLE
--------------------------------------------------------------------------------

CREATE TABLE task_relations (
  from_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  to_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  PRIMARY KEY (from_task_uuid, to_task_uuid, kind)
);

-- Trigger to validate relation kind
CREATE TRIGGER task_relations_kind_check
BEFORE INSERT ON task_relations
WHEN NEW.kind NOT IN ('blocks', 'relates_to', 'duplicates')
BEGIN
  SELECT RAISE(ABORT, 'Invalid relation kind: must be blocks, relates_to, or duplicates');
END;

-- Prevent self-referential relations
CREATE TRIGGER task_relations_no_self
BEFORE INSERT ON task_relations
WHEN NEW.from_task_uuid = NEW.to_task_uuid
BEGIN
  SELECT RAISE(ABORT, 'Task cannot have a relation to itself');
END;

CREATE INDEX task_relations_to_idx ON task_relations(to_task_uuid);
CREATE INDEX task_relations_from_idx ON task_relations(from_task_uuid);
CREATE INDEX task_relations_kind_idx ON task_relations(kind);

--------------------------------------------------------------------------------
-- RECREATE VIEWS
-- Note: Views were dropped earlier before task table recreation
--------------------------------------------------------------------------------

-- Recreate v_container_paths with new fields (kind, section_uuid, sort_index)
CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid IS NULL
  UNION ALL
  SELECT c.uuid, c.id, c.slug, c.title, c.parent_uuid, c.kind, c.section_uuid, c.sort_index,
         ct.path || '/' || c.slug AS path,
         ct.level + 1 AS level
    FROM containers c
    JOIN container_tree ct ON c.parent_uuid = ct.uuid
)
SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level
  FROM container_tree;

-- Recreate v_task_paths with new fields (kind, parent_task_uuid, assignee_actor_uuid)
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
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid;
