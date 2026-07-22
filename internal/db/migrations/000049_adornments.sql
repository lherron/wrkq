-- Adornments for campaigns and typed comments.
-- Rebuilding comments requires foreign keys to be disabled while SQLite
-- renames and replaces the referenced table.
-- wrkq:foreign-keys-off

ALTER TABLE tasks ADD COLUMN outcome TEXT;
ALTER TABLE tasks ADD COLUMN campaign_uuid TEXT
  REFERENCES containers(uuid) ON DELETE RESTRICT;
CREATE INDEX tasks_campaign_idx ON tasks(campaign_uuid);

ALTER TABLE containers ADD COLUMN campaign_state TEXT
  CHECK (campaign_state IN ('active','completed','cancelled'));
ALTER TABLE containers ADD COLUMN specification TEXT;

DROP TRIGGER IF EXISTS comments_ai_touch_task;
DROP TRIGGER IF EXISTS comments_au_touch_task;
DROP TRIGGER IF EXISTS comments_ad_touch_task;

ALTER TABLE comments RENAME TO comments_old;

CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
    container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
    kind TEXT CHECK (kind IS NULL OR kind IN ('blocker','decision','postmortem','digest')),
    actor_uuid TEXT,
    created_by_principal_ref TEXT
      CHECK (created_by_principal_ref IS NULL OR (
        created_by_principal_ref LIKE 'agent:%'
        AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
        AND instr(substr(created_by_principal_ref, 7), ':') = 0
        AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
      )),
    created_by_scope_ref TEXT,
    body TEXT NOT NULL,
    meta TEXT,
    etag INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT,
    deleted_at TEXT,
    deleted_by_actor_uuid TEXT,
    deleted_by_principal_ref TEXT
      CHECK (deleted_by_principal_ref IS NULL OR (
        deleted_by_principal_ref LIKE 'agent:%'
        AND length(substr(deleted_by_principal_ref, 7)) BETWEEN 1 AND 64
        AND instr(substr(deleted_by_principal_ref, 7), ':') = 0
        AND substr(deleted_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
      )),
    deleted_by_scope_ref TEXT,

    CHECK (
      (task_uuid IS NOT NULL AND container_uuid IS NULL) OR
      (task_uuid IS NULL AND container_uuid IS NOT NULL)
    ),
    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);

INSERT INTO comments (
  uuid, id, task_uuid, container_uuid, kind, actor_uuid,
  created_by_principal_ref, created_by_scope_ref,
  body, meta, etag, created_at, updated_at, deleted_at,
  deleted_by_actor_uuid, deleted_by_principal_ref, deleted_by_scope_ref
)
SELECT
  uuid, id, task_uuid, NULL, NULL, actor_uuid,
  created_by_principal_ref, created_by_scope_ref,
  body, meta, etag, created_at, updated_at, deleted_at,
  deleted_by_actor_uuid, deleted_by_principal_ref, deleted_by_scope_ref
FROM comments_old;

DROP TABLE comments_old;

CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at, id);
CREATE INDEX idx_comments_container_created ON comments(container_uuid, created_at, id);
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at) WHERE actor_uuid IS NOT NULL;
CREATE INDEX idx_comments_principal_created ON comments(created_by_principal_ref, created_at)
  WHERE created_by_principal_ref IS NOT NULL;

CREATE TRIGGER comments_ai_touch_task
AFTER INSERT ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_au_touch_task
AFTER UPDATE ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_ad_touch_task
AFTER DELETE ON comments
WHEN OLD.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.task_uuid;
END;

CREATE TRIGGER comments_ai_touch_container
AFTER INSERT ON comments
WHEN NEW.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.container_uuid;
END;

CREATE TRIGGER comments_au_touch_container
AFTER UPDATE ON comments
WHEN NEW.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.container_uuid;
END;

CREATE TRIGGER comments_ad_touch_container
AFTER DELETE ON comments
WHEN OLD.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.container_uuid;
END;
