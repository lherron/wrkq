-- Hard cutover: project-event protocol revision 3. Historical freeform rows
-- are deliberately not migrated.
DROP TABLE project_events;
DROP TABLE project_event_seq;

CREATE TABLE project_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid TEXT NOT NULL UNIQUE,
  project_uuid TEXT NOT NULL,
  container_uuid TEXT NOT NULL,
  campaign_uuid TEXT,
  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE SET NULL,
  type TEXT NOT NULL,
  summary TEXT NOT NULL,
  attributes TEXT NOT NULL,
  principal_ref TEXT,
  scope_ref TEXT,
  idempotency_key TEXT,
  occurred_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX project_events_container_idx ON project_events(container_uuid, id);
CREATE INDEX project_events_campaign_idx ON project_events(campaign_uuid, id) WHERE campaign_uuid IS NOT NULL;
CREATE INDEX project_events_task_idx ON project_events(task_uuid, id) WHERE task_uuid IS NOT NULL;
CREATE UNIQUE INDEX project_events_idem_idx ON project_events(project_uuid, idempotency_key) WHERE idempotency_key IS NOT NULL;
