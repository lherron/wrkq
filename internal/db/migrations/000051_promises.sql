-- First-class principal-owned attention promises.

CREATE TABLE promise_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);

CREATE TABLE promises (
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

  owner_principal_ref TEXT NOT NULL,

  subject TEXT NOT NULL CHECK (length(trim(subject)) > 0),
  review_question TEXT,

  subject_task_uuid TEXT
    REFERENCES tasks(uuid) ON DELETE SET NULL,
  subject_container_uuid TEXT
    REFERENCES containers(uuid) ON DELETE SET NULL,

  review_at TEXT NOT NULL
    CHECK (review_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z'),

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'resolved', 'abandoned')),
  closed_at TEXT,

  last_reviewed_at TEXT,
  last_review_note TEXT,

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
    subject_task_uuid IS NULL
    OR subject_container_uuid IS NULL
  ),

  CHECK (
    (state = 'open' AND closed_at IS NULL)
    OR
    (state IN ('resolved', 'abandoned') AND closed_at IS NOT NULL)
  )
);

CREATE INDEX promises_owner_ready_idx
  ON promises(owner_principal_ref, review_at)
  WHERE state = 'open';

CREATE INDEX promises_task_idx
  ON promises(subject_task_uuid)
  WHERE subject_task_uuid IS NOT NULL;

CREATE INDEX promises_container_idx
  ON promises(subject_container_uuid)
  WHERE subject_container_uuid IS NOT NULL;

CREATE TRIGGER promises_ai_friendly
AFTER INSERT ON promises
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO promise_seq (id) VALUES (NULL);
  UPDATE promises
     SET id = 'PR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Widen the append-only event resource vocabulary without losing historic
-- identities or principal/scope attribution.
DROP INDEX IF EXISTS event_log_resource_idx;
DROP INDEX IF EXISTS event_log_principal_idx;
DROP INDEX IF EXISTS event_log_scope_idx;

ALTER TABLE event_log RENAME TO event_log_old;

CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment','handoff','promise')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT,
  principal_ref TEXT,
  scope_ref     TEXT
);

INSERT INTO event_log (
  id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag,
  payload, principal_ref, scope_ref
)
SELECT
  id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag,
  payload, principal_ref, scope_ref
FROM event_log_old;

DROP TABLE event_log_old;

CREATE INDEX event_log_resource_idx
  ON event_log(resource_type, resource_uuid, id DESC);
CREATE INDEX event_log_principal_idx
  ON event_log(principal_ref, id DESC)
  WHERE principal_ref IS NOT NULL;
CREATE INDEX event_log_scope_idx
  ON event_log(scope_ref, id DESC)
  WHERE scope_ref IS NOT NULL;
