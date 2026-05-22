-- Migration: handoff schema and event log resource types

CREATE TABLE handoff_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

CREATE TABLE handoffs (
  uuid TEXT PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  scope_ref TEXT NOT NULL,
  scope_kind TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  agent_actor_uuid TEXT,
  project_container_uuid TEXT,
  created_by_agent_id TEXT NOT NULL,
  created_by_actor_uuid TEXT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','acknowledged')),
  idempotency_key TEXT,
  acknowledged_at TEXT,
  acknowledged_by_agent_id TEXT,
  acknowledged_by_actor_uuid TEXT,
  acknowledgement_note TEXT,
  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  FOREIGN KEY (agent_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,
  FOREIGN KEY (project_container_uuid) REFERENCES containers(uuid) ON DELETE SET NULL,
  FOREIGN KEY (created_by_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,
  FOREIGN KEY (acknowledged_by_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,

  CHECK (length(id) > 0),
  CHECK (length(title) > 0),
  CHECK (length(body) > 0),
  CHECK (etag >= 1),
  CHECK (scope_kind IN ('agent','project','project-role','project-task','project-task-role'))
);

CREATE INDEX handoffs_scope_status_idx ON handoffs(scope_ref, status, created_at);
CREATE INDEX handoffs_agent_idx ON handoffs(agent_id, project_id, status);
CREATE INDEX handoffs_updated_idx ON handoffs(updated_at);
CREATE UNIQUE INDEX handoffs_idempotency_idx ON handoffs(scope_ref, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

DROP INDEX IF EXISTS event_log_resource_idx;

ALTER TABLE event_log RENAME TO event_log_old;

CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment','handoff')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT
);

INSERT INTO event_log (id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag, payload)
SELECT id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag, payload
  FROM event_log_old;

DROP TABLE event_log_old;

CREATE INDEX event_log_resource_idx ON event_log(resource_type, resource_uuid, id DESC);
