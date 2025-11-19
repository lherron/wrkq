-- 0001_init.sql — wrkq CLI (Go + SQLite)
-- Canonical schema. Apply inside one transaction.

-- -----------------------------
-- Pragmas (apply-time)
-- -----------------------------
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA recursive_triggers = OFF;  -- prevents UPDATE-in-trigger from re-firing

-- -----------------------------
-- Sequences for friendly IDs (monotonic, never reused)
-- -----------------------------
CREATE TABLE IF NOT EXISTS actor_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS container_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS task_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS attachment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS comment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

-- Helper: v4 UUID expression (used inline as DEFAULT)
-- lower(
--   hex(randomblob(4)) || '-' ||
--   hex(randomblob(2)) || '-' ||
--   '4' || substr(hex(randomblob(2)),2) || '-' ||
--   substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
--   hex(randomblob(6))
-- )

-- -----------------------------
-- Actors
-- -----------------------------
CREATE TABLE IF NOT EXISTS actors (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. A-00001 (set by trigger)
  slug TEXT NOT NULL UNIQUE
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  display_name TEXT,
  role TEXT NOT NULL CHECK (role IN ('human','agent','system')),
  meta TEXT,  -- JSON (app-level validated)
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TRIGGER IF NOT EXISTS actors_ai_friendly
AFTER INSERT ON actors
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO actor_seq DEFAULT VALUES;
  UPDATE actors
     SET id = 'A-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER IF NOT EXISTS actors_au_touch
AFTER UPDATE ON actors
BEGIN
  UPDATE actors SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

-- -----------------------------
-- Containers (projects/subprojects)
-- -----------------------------
CREATE TABLE IF NOT EXISTS containers (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. P-00001 (set by trigger)
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT,                           -- optional; app may default to slug
  parent_uuid TEXT REFERENCES containers(uuid) ON DELETE RESTRICT,
  etag INTEGER NOT NULL DEFAULT 1,      -- app increments for optimistic concurrency
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS containers_ai_friendly
AFTER INSERT ON containers
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO container_seq DEFAULT VALUES;
  UPDATE containers
     SET id = 'P-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Sibling-uniqueness (root vs child handled separately)
CREATE UNIQUE INDEX IF NOT EXISTS containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS containers_unique_child_slug
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;

CREATE INDEX IF NOT EXISTS containers_parent_idx ON containers(parent_uuid);
CREATE INDEX IF NOT EXISTS containers_slug_idx   ON containers(slug);

CREATE TRIGGER IF NOT EXISTS containers_au_touch
AFTER UPDATE ON containers
BEGIN
  UPDATE containers SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

-- -----------------------------
-- Tasks
-- -----------------------------
CREATE TABLE IF NOT EXISTS tasks (
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
  id   TEXT NOT NULL UNIQUE,   -- friendly, e.g. T-00001 (set by trigger)
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  state TEXT NOT NULL CHECK (state IN ('open','completed','archived')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,        -- JSON array; app validates
  body     TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq DEFAULT VALUES;
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Uniqueness within container
CREATE UNIQUE INDEX IF NOT EXISTS tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);

CREATE INDEX IF NOT EXISTS tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX IF NOT EXISTS tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX IF NOT EXISTS tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX IF NOT EXISTS tasks_slug_idx      ON tasks(slug);

-- Touch + state consistency
CREATE TRIGGER IF NOT EXISTS tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER IF NOT EXISTS tasks_au_state_consistency
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

-- -----------------------------
-- Comments
-- -----------------------------
CREATE TABLE IF NOT EXISTS comments (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. C-00001 (set by trigger)
  task_uuid  TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TRIGGER IF NOT EXISTS comments_ai_friendly
AFTER INSERT ON comments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO comment_seq DEFAULT VALUES;
  UPDATE comments
     SET id = 'C-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE INDEX IF NOT EXISTS comments_task_idx  ON comments(task_uuid);
CREATE INDEX IF NOT EXISTS comments_actor_idx ON comments(actor_uuid);

-- -----------------------------
-- Attachments (metadata only; bytes live under attach_dir)
-- -----------------------------
CREATE TABLE IF NOT EXISTS attachments (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. ATT-00001 (set by trigger)
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  filename  TEXT NOT NULL,
  relative_path TEXT NOT NULL,  -- relative to attach_dir
  mime_type TEXT,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  checksum  TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq DEFAULT VALUES;
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE UNIQUE INDEX IF NOT EXISTS attachments_task_filename_unique
  ON attachments(task_uuid, filename);

CREATE UNIQUE INDEX IF NOT EXISTS attachments_relpath_unique
  ON attachments(relative_path);

CREATE INDEX IF NOT EXISTS attachments_task_idx ON attachments(task_uuid);

-- -----------------------------
-- Event Log (append-only)
-- -----------------------------
CREATE TABLE IF NOT EXISTS event_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('task','container','attachment','actor','config','system')),
  resource_uuid TEXT,    -- UUID of target; nullable for 'system'
  event_type TEXT NOT NULL,   -- e.g. task.created, task.updated, task.deleted, task.moved
  etag INTEGER,               -- resulting etag if applicable
  payload TEXT                -- JSON diff or structured blob
);

CREATE TRIGGER IF NOT EXISTS event_log_no_update
BEFORE UPDATE ON event_log
BEGIN
  SELECT RAISE(ABORT, 'event_log is append-only');
END;

CREATE TRIGGER IF NOT EXISTS event_log_no_delete
BEFORE DELETE ON event_log
BEGIN
  SELECT RAISE(ABORT, 'event_log is append-only');
END;

CREATE INDEX IF NOT EXISTS event_log_res_idx   ON event_log(resource_type, resource_uuid);
CREATE INDEX IF NOT EXISTS event_log_ts_idx    ON event_log(timestamp);
CREATE INDEX IF NOT EXISTS event_log_actor_idx ON event_log(actor_uuid);

-- -----------------------------
-- Helper views: canonical paths
-- -----------------------------
CREATE VIEW IF NOT EXISTS v_container_paths AS
WITH RECURSIVE cte(uuid, id, slug, title, parent_uuid, depth, path) AS (
  SELECT uuid, id, slug, COALESCE(title, slug), parent_uuid, 1, slug
    FROM containers
   WHERE parent_uuid IS NULL
  UNION ALL
  SELECT c.uuid, c.id, c.slug, COALESCE(c.title, c.slug), c.parent_uuid,
         cte.depth + 1, cte.path || '/' || c.slug
    FROM containers c
    JOIN cte ON c.parent_uuid = cte.uuid
)
SELECT uuid, id, slug, title, parent_uuid, depth, path FROM cte;

CREATE VIEW IF NOT EXISTS v_task_paths AS
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

-- End 0001_init