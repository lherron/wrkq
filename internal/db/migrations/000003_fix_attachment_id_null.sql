-- 000003_fix_attachment_id_null.sql
-- Fix attachments.id and tasks.id to be nullable so AFTER INSERT triggers can set them

-- SQLite doesn't support ALTER COLUMN, so we need to recreate the tables

-- Step 1: Create new table with id nullable
CREATE TABLE attachments_new (
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
  id   TEXT UNIQUE,  -- Made nullable so trigger can set it
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  filename  TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum   TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL
);

-- Step 2: Copy data from old table (if any exists)
INSERT INTO attachments_new
SELECT * FROM attachments;

-- Step 3: Drop old table (and its triggers)
DROP TABLE attachments;

-- Step 4: Rename new table
ALTER TABLE attachments_new RENAME TO attachments;

-- Step 5: Recreate indexes
CREATE UNIQUE INDEX attachments_task_filename_unique
  ON attachments(task_uuid, filename);

CREATE UNIQUE INDEX attachments_relpath_unique
  ON attachments(relative_path);

CREATE INDEX attachments_task_idx ON attachments(task_uuid);

-- Step 6: Recreate trigger
CREATE TRIGGER attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq (id) VALUES (NULL);
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Now fix tasks table
-- Step 7: Create new tasks table with id nullable
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
  id   TEXT UNIQUE,  -- Made nullable so trigger can set it
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  state TEXT NOT NULL CHECK (state IN ('open','in_progress','completed','archived')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,
  body     TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

-- Step 8: Drop views that depend on tasks table
DROP VIEW IF EXISTS v_task_paths;

-- Step 9: Copy data from old table
INSERT INTO tasks_new SELECT * FROM tasks;

-- Step 10: Drop old table (this also drops triggers)
DROP TABLE tasks;

-- Step 11: Rename new table
ALTER TABLE tasks_new RENAME TO tasks;

-- Step 12: Recreate indexes
CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);

CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);

-- Step 13: Recreate triggers
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

-- Step 14: Recreate views
CREATE VIEW v_task_paths AS
SELECT t.uuid,
       t.id,
       t.slug,
       t.title,
       t.state,
       t.priority,
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
