-- Migration: container directory kind and root-project invariant
-- wrkq:foreign-keys-off
--
-- SQLite cannot ALTER a column default in place, so rebuild containers with
-- kind DEFAULT 'directory'. Existing rows are copied without reclassification;
-- the live data repair is a separately gated operator step.

DROP VIEW IF EXISTS v_task_paths;
DROP VIEW IF EXISTS v_container_paths;

PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS containers_ai_friendly;
DROP TRIGGER IF EXISTS containers_au_touch;
DROP TRIGGER IF EXISTS containers_kind_check_insert;
DROP TRIGGER IF EXISTS containers_kind_check_update;
DROP TRIGGER IF EXISTS containers_project_root_insert;
DROP TRIGGER IF EXISTS containers_project_root_update;

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
  id   TEXT UNIQUE,
  parent_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  etag INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  kind TEXT NOT NULL DEFAULT 'directory',
  sort_index INTEGER NOT NULL DEFAULT 0,
  section_uuid TEXT REFERENCES sections(uuid) ON DELETE SET NULL,
  webhook_urls TEXT
);

INSERT INTO containers_new (
  uuid, id, parent_uuid, slug, title, description, etag,
  created_at, updated_at, archived_at,
  created_by_actor_uuid, updated_by_actor_uuid,
  kind, sort_index, section_uuid, webhook_urls
)
SELECT
  uuid, id, parent_uuid, slug, title, description, etag,
  created_at, updated_at, archived_at,
  created_by_actor_uuid, updated_by_actor_uuid,
  kind, sort_index, section_uuid, webhook_urls
FROM containers;

DROP TABLE containers;
ALTER TABLE containers_new RENAME TO containers;

CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE INDEX containers_section_idx ON containers(section_uuid) WHERE section_uuid IS NOT NULL;

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

CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or misc');
END;

CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or misc');
END;

CREATE TRIGGER containers_project_root_insert
BEFORE INSERT ON containers
WHEN NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'Invalid container project kind: projects must be root containers');
END;

CREATE TRIGGER containers_project_root_update
BEFORE UPDATE OF kind, parent_uuid ON containers
WHEN NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'Invalid container project kind: projects must be root containers');
END;

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
       t.cp_work_item_id,
       t.cp_run_id,
       t.cp_session_id,
       t.sdk_session_id,
       t.run_status,
       t.workflow_preset,
       t.preset_version,
       t.phase,
       t.risk_class,
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

PRAGMA foreign_key_check;
PRAGMA foreign_keys = ON;
