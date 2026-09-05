-- Foreign project facts: immutable production-time affiliation stamps merged
-- into the project timeline without copying wrkq-owned event_log mutations.

CREATE TABLE project_event_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);

CREATE TABLE project_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  fid TEXT NOT NULL UNIQUE,
  project_uuid TEXT NOT NULL,
  container_uuid TEXT NOT NULL,
  campaign_uuid TEXT,
  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE SET NULL,
  type TEXT NOT NULL,
  source TEXT NOT NULL,
  node TEXT,
  principal_ref TEXT NOT NULL,
  scope_ref TEXT,
  summary TEXT NOT NULL,
  payload TEXT,
  idempotency_key TEXT,
  occurred_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX project_events_container_idx
  ON project_events(container_uuid, id);

CREATE INDEX project_events_campaign_idx
  ON project_events(campaign_uuid, id)
  WHERE campaign_uuid IS NOT NULL;

CREATE INDEX project_events_task_idx
  ON project_events(task_uuid, id)
  WHERE task_uuid IS NOT NULL;

CREATE UNIQUE INDEX project_events_idem_idx
  ON project_events(project_uuid, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

CREATE TRIGGER project_events_ai_friendly
AFTER INSERT ON project_events
WHEN NEW.fid = ''
BEGIN
  INSERT INTO project_event_seq (id) VALUES (NULL);
  UPDATE project_events
     SET fid = 'PE-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
