-- Migration: wrkf workflow engine tables

CREATE TABLE workflow_templates (
  id TEXT NOT NULL,
  version TEXT NOT NULL,
  hash TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  installed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  installed_by TEXT,
  PRIMARY KEY (id, version)
);

CREATE TABLE workflow_instances (
  id TEXT PRIMARY KEY,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  task_ref TEXT NOT NULL,
  project_id TEXT,
  template_id TEXT NOT NULL,
  template_version TEXT NOT NULL,
  template_hash TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('open', 'active', 'waiting', 'closed')),
  phase TEXT,
  outcome TEXT,
  revision INTEGER NOT NULL DEFAULT 0,
  context_hash TEXT NOT NULL,
  task_doc_etag TEXT NOT NULL,
  task_doc_hash TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  closed_at TEXT,
  FOREIGN KEY (template_id, template_version) REFERENCES workflow_templates(id, version)
);

CREATE UNIQUE INDEX workflow_instances_one_active_per_task
  ON workflow_instances(task_uuid)
  WHERE status != 'closed';

CREATE TABLE workflow_events (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  schema_version TEXT NOT NULL,
  type TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT,
  causation_id TEXT,
  correlation_id TEXT,
  observed_revision INTEGER,
  next_revision INTEGER NOT NULL,
  task_doc_etag TEXT,
  task_doc_hash TEXT,
  context_hash TEXT,
  idempotency_key TEXT,
  result TEXT,
  rejection_code TEXT,
  payload_json TEXT NOT NULL,
  prev_event_hash TEXT,
  event_hash TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(instance_id, seq),
  UNIQUE(instance_id, idempotency_key)
);

CREATE TABLE workflow_role_bindings (
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  actor TEXT NOT NULL,
  delivery_ref TEXT,
  lane TEXT,
  binding_mode TEXT NOT NULL CHECK (binding_mode IN ('required', 'optional', 'auto')),
  bound_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (instance_id, role, actor)
);

CREATE TABLE workflow_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_check_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_evidence_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_obligation_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_effect_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

CREATE TABLE workflow_runs (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  actor TEXT NOT NULL,
  delivery_ref TEXT,
  lane TEXT,
  external_run_ref TEXT,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  terminal_result TEXT
);

CREATE TABLE workflow_check_runs (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  transition_id TEXT NOT NULL,
  check_id TEXT NOT NULL,
  hook_id TEXT,
  input_hash TEXT NOT NULL,
  exit_code INTEGER,
  verdict TEXT NOT NULL CHECK (verdict IN ('pass', 'fail', 'block', 'error', 'inconclusive')),
  outcome TEXT,
  code TEXT,
  summary TEXT,
  facts_json TEXT,
  actor TEXT,
  role TEXT,
  run_id TEXT REFERENCES workflow_runs(id),
  started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT
);

CREATE TABLE workflow_evidence (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  summary TEXT,
  data_json TEXT,
  source_json TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT REFERENCES workflow_runs(id),
  task_etag_at_production TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE workflow_check_run_evidence (
  check_run_id TEXT NOT NULL REFERENCES workflow_check_runs(id) ON DELETE CASCADE,
  evidence_id TEXT NOT NULL REFERENCES workflow_evidence(id) ON DELETE CASCADE,
  PRIMARY KEY (check_run_id, evidence_id)
);

CREATE TABLE workflow_obligations (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  owner_role TEXT,
  owner_actor TEXT,
  blocking INTEGER NOT NULL CHECK (blocking IN (0, 1)),
  status TEXT NOT NULL CHECK (status IN ('open', 'satisfied', 'waived', 'cancelled')),
  reason TEXT,
  satisfied_by_evidence_id TEXT REFERENCES workflow_evidence(id),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE workflow_effects (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'leased', 'delivered', 'failed', 'cancelled', 'unsupported')),
  idempotency_key TEXT NOT NULL UNIQUE,
  attempts INTEGER NOT NULL DEFAULT 0,
  leased_by TEXT,
  leased_until TEXT,
  delivered_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
