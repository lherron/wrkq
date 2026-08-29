-- wrkq:foreign-keys-off
-- An ad-hoc room's identity is its active member set, never a topic. SQLite
-- cannot DROP subject directly because the original table-level kind check
-- references it, so rebuild the table while preserving every live row.

CREATE TABLE rooms_new (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  id TEXT UNIQUE,

  kind TEXT NOT NULL CHECK (kind IN ('campaign', 'task', 'project', 'adhoc')),

  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
  container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'closed', 'archived')),
  closed_at TEXT,
  reopened_at TEXT,

  last_activity_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  opened_by_principal_ref TEXT NOT NULL,
  opened_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),

  created_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  updated_by_principal_ref TEXT NOT NULL,
  updated_by_scope_ref TEXT,

  CHECK (
    (kind = 'task' AND task_uuid IS NOT NULL AND container_uuid IS NULL)
    OR
    (kind IN ('campaign', 'project')
                   AND container_uuid IS NOT NULL AND task_uuid IS NULL)
    OR
    (kind = 'adhoc' AND task_uuid IS NULL AND container_uuid IS NULL)
  ),

  CHECK (
    (state = 'open' AND closed_at IS NULL)
    OR
    (state IN ('closed', 'archived') AND closed_at IS NOT NULL)
  )
);

INSERT INTO rooms_new (
  uuid, id, kind, task_uuid, container_uuid,
  state, closed_at, reopened_at, last_activity_at,
  opened_by_principal_ref, opened_at, meta, etag,
  created_at, updated_at,
  created_by_principal_ref, created_by_scope_ref,
  updated_by_principal_ref, updated_by_scope_ref
)
SELECT
  uuid, id, kind, task_uuid, container_uuid,
  state, closed_at, reopened_at, last_activity_at,
  opened_by_principal_ref, opened_at, meta, etag,
  created_at, updated_at,
  created_by_principal_ref, created_by_scope_ref,
  updated_by_principal_ref, updated_by_scope_ref
FROM rooms;

DROP TABLE rooms;
ALTER TABLE rooms_new RENAME TO rooms;

CREATE UNIQUE INDEX rooms_task_idx
  ON rooms(task_uuid) WHERE task_uuid IS NOT NULL;
CREATE UNIQUE INDEX rooms_container_idx
  ON rooms(container_uuid) WHERE container_uuid IS NOT NULL;
CREATE INDEX rooms_adhoc_idle_idx
  ON rooms(last_activity_at) WHERE kind = 'adhoc' AND state = 'open';

CREATE TRIGGER rooms_ai_friendly
AFTER INSERT ON rooms
WHEN NEW.kind = 'adhoc' AND (NEW.id IS NULL OR NEW.id = '')
BEGIN
  INSERT INTO room_seq (id) VALUES (NULL);
  UPDATE rooms
     SET id = 'R-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

PRAGMA foreign_key_check;
