-- 000004_fix_actor_container_id_null.sql
-- Fix actors.id and containers.id to be nullable so AFTER INSERT triggers can set them

-- Note: Must be applied after 000003 which fixes tasks and attachments

-- Fix actors table first (no dependencies)
-- Step 1: Create new actors table with id nullable
CREATE TABLE actors_new (
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

-- Step 2: Copy data from old table
INSERT INTO actors_new SELECT * FROM actors;

-- Step 3: Drop old table
DROP TABLE actors;

-- Step 4: Rename new table
ALTER TABLE actors_new RENAME TO actors;

-- Step 5: Recreate indexes
CREATE UNIQUE INDEX actors_slug_unique ON actors(slug);

-- Step 6: Recreate triggers
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

-- Now fix containers table
-- Step 7: Drop views that depend on containers
DROP VIEW IF EXISTS v_container_paths;
DROP VIEW IF EXISTS v_task_paths;

-- Step 8: Create new containers table with id nullable
CREATE TABLE containers_new (
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
);

-- Step 9: Copy data from old table - EXPLICITLY MAP COLUMNS
INSERT INTO containers_new (uuid, id, parent_uuid, slug, title, description, etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
SELECT uuid, id, parent_uuid, slug, COALESCE(title, slug), '', etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid
FROM containers;

-- Step 10: Drop old table
DROP TABLE containers;

-- Step 11: Rename new table
ALTER TABLE containers_new RENAME TO containers;

-- Step 12: Recreate indexes
CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;

CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;

-- Step 13: Recreate triggers
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

-- Step 14: Recreate views
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
  FROM container_tree;

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
