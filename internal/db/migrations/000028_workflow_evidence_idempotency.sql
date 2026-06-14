-- wrkq:foreign-keys-off
-- Persist wrkf.evidence.add idempotency replay records and allow run_id to
-- carry external run identifiers.

CREATE TABLE workflow_evidence_new (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  summary TEXT,
  data_json TEXT,
  source_json TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT,
  task_etag_at_production TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  facts_json TEXT CHECK (
    facts_json IS NULL OR
    (json_valid(facts_json) AND json_type(facts_json) = 'object')
  ),
  task_hash_at_production TEXT
);

INSERT INTO workflow_evidence_new (
  id, instance_id, kind, ref, summary, data_json, source_json, actor, role,
  run_id, task_etag_at_production, produced_at, facts_json, task_hash_at_production
)
SELECT
  id, instance_id, kind, ref, summary, data_json, source_json, actor, role,
  run_id, task_etag_at_production, produced_at, facts_json, task_hash_at_production
FROM workflow_evidence;

DROP TABLE workflow_evidence;
ALTER TABLE workflow_evidence_new RENAME TO workflow_evidence;

CREATE TABLE IF NOT EXISTS workflow_evidence_idempotency (
  instance_id     TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL,
  request_hash    TEXT NOT NULL,
  result_json     TEXT NOT NULL,
  evidence_id     TEXT NOT NULL REFERENCES workflow_evidence(id) ON DELETE CASCADE,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (instance_id, idempotency_key)
);
