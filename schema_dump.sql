CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		);
CREATE TABLE actor_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE sqlite_sequence(name,seq);
CREATE TABLE container_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE attachment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS "attachments" (
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
CREATE UNIQUE INDEX attachments_task_filename_unique
  ON attachments(task_uuid, filename);
CREATE UNIQUE INDEX attachments_relpath_unique
  ON attachments(relative_path);
CREATE INDEX attachments_task_idx ON attachments(task_uuid);
CREATE TRIGGER attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq (id) VALUES (NULL);
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE IF NOT EXISTS "tasks" (
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
CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);
CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);
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
CREATE TABLE IF NOT EXISTS "actors" (
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
  slug TEXT NOT NULL UNIQUE
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  display_name TEXT,
  role TEXT NOT NULL CHECK (role IN ('human','agent','system')),
  meta TEXT,  -- JSON (app-level validated)
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX actors_slug_unique ON actors(slug);
CREATE TRIGGER actors_ai_friendly
AFTER INSERT ON actors
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO actor_seq (id) VALUES (NULL);
  UPDATE actors
     SET id = 'A-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER actors_au_touch
AFTER UPDATE ON actors
BEGIN
  UPDATE actors SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE IF NOT EXISTS "containers" (
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
  parent_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  etag INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
, archived_at TEXT);
CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE TRIGGER containers_ai_friendly
AFTER INSERT ON containers
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO container_seq (id) VALUES (NULL);
  UPDATE containers
     SET id = 'P-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER containers_au_touch
AFTER UPDATE ON containers
BEGIN
  UPDATE containers SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid IS NULL
  UNION ALL
  SELECT c.uuid, c.id, c.slug, c.title, c.parent_uuid,
         ct.path || '/' || c.slug AS path,
         ct.level + 1 AS level
    FROM containers c
    JOIN container_tree ct ON c.parent_uuid = ct.uuid
)
SELECT uuid, id, slug, title, parent_uuid, path, level
  FROM container_tree
/* v_container_paths(uuid,id,slug,title,parent_uuid,path,level) */;
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
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid
/* v_task_paths(uuid,id,slug,title,state,priority,start_at,due_at,labels,etag,created_at,updated_at,completed_at,archived_at,project_uuid,path) */;
CREATE TABLE comment_sequences (
    name TEXT PRIMARY KEY,
    value INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,  -- friendly ID like C-00001
    task_uuid TEXT NOT NULL,
    actor_uuid TEXT NOT NULL,
    body TEXT NOT NULL,        -- Markdown content
    meta TEXT,                 -- JSON optional metadata for agents/tools
    etag INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT,           -- nullable; reserved for future editable comments
    deleted_at TEXT,           -- nullable; soft delete timestamp
    deleted_by_actor_uuid TEXT,  -- nullable; actor who soft-deleted this comment

    FOREIGN KEY (task_uuid) REFERENCES tasks(uuid) ON DELETE CASCADE,
    FOREIGN KEY (actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,
    FOREIGN KEY (deleted_by_actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,

    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);
CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at);
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at);
CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT
);
CREATE INDEX event_log_resource_idx ON event_log(resource_type, resource_uuid, id DESC);
