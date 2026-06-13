-- Move canonical attribution from local actors rows to external principal refs.
-- wrkq:foreign-keys-off

DROP VIEW IF EXISTS v_task_paths;
DROP VIEW IF EXISTS v_container_paths;
DROP TRIGGER IF EXISTS comments_ai_touch_task;
DROP TRIGGER IF EXISTS comments_au_touch_task;
DROP TRIGGER IF EXISTS comments_ad_touch_task;

-- containers: freeze actor UUIDs as nullable legacy cache fields and add
-- canonical principal/scope attribution.
ALTER TABLE containers RENAME TO containers_old;

CREATE TABLE containers (
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
  created_by_actor_uuid TEXT,
  updated_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  updated_by_principal_ref TEXT
    CHECK (updated_by_principal_ref IS NULL OR (
      updated_by_principal_ref LIKE 'agent:%'
      AND length(substr(updated_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(updated_by_principal_ref, 7), ':') = 0
      AND substr(updated_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
  updated_by_scope_ref TEXT,
  kind TEXT NOT NULL DEFAULT 'directory',
  sort_index INTEGER NOT NULL DEFAULT 0,
  section_uuid TEXT REFERENCES sections(uuid) ON DELETE SET NULL,
  webhook_urls TEXT
);

INSERT INTO containers (
  uuid, id, parent_uuid, slug, title, description, etag, created_at, updated_at,
  archived_at, created_by_actor_uuid, updated_by_actor_uuid,
  created_by_principal_ref, updated_by_principal_ref,
  created_by_scope_ref, updated_by_scope_ref,
  kind, sort_index, section_uuid, webhook_urls
)
SELECT
  c.uuid, c.id, c.parent_uuid, c.slug, c.title, c.description, c.etag, c.created_at, c.updated_at,
  c.archived_at, c.created_by_actor_uuid, c.updated_by_actor_uuid,
  CASE WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug END,
  CASE WHEN ua.slug IS NOT NULL THEN 'agent:' || ua.slug END,
  NULL, NULL,
  c.kind, c.sort_index, c.section_uuid, c.webhook_urls
FROM containers_old c
LEFT JOIN actors ca ON ca.uuid = c.created_by_actor_uuid
LEFT JOIN actors ua ON ua.uuid = c.updated_by_actor_uuid;

DROP TABLE containers_old;

CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE INDEX containers_section_idx ON containers(section_uuid) WHERE section_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_single_root ON containers(kind) WHERE kind = 'root';
CREATE INDEX containers_created_by_principal_idx ON containers(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;

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
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or root');
END;

CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or root');
END;

CREATE TRIGGER containers_parent_kind_consistency_insert
BEFORE INSERT ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant: root=>null parent, project=>parent=root, other=>non-root non-null parent');
END;

CREATE TRIGGER containers_parent_kind_consistency_update
BEFORE UPDATE OF kind, parent_uuid ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant violated');
END;

CREATE TRIGGER containers_root_immutable_update
BEFORE UPDATE OF parent_uuid, slug, kind, archived_at ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container is immutable (parent_uuid, slug, kind, archived_at)');
END;

CREATE TRIGGER containers_root_no_delete
BEFORE DELETE ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container cannot be deleted');
END;

-- tasks: add principal attribution and make actor UUIDs legacy nullable cache.
ALTER TABLE tasks RENAME TO tasks_old;

CREATE TABLE tasks (
  uuid TEXT PRIMARY KEY
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
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  parent_task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
  assignee_actor_uuid TEXT,
  assignee_principal_ref TEXT
    CHECK (assignee_principal_ref IS NULL OR (
      assignee_principal_ref LIKE 'agent:%'
      AND length(substr(assignee_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(assignee_principal_ref, 7), ':') = 0
      AND substr(assignee_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  requested_by_project_id TEXT,
  assigned_project_id TEXT,
  acknowledged_at TEXT,
  resolution TEXT,
  cp_project_id TEXT,
  cp_run_id TEXT,
  cp_session_id TEXT,
  sdk_session_id TEXT,
  run_status TEXT CHECK (run_status IN ('queued','running','completed','failed','cancelled','timed_out')),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,
  meta     TEXT,
  description TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  deleted_at   TEXT,
  deleted_by_principal_ref TEXT
    CHECK (deleted_by_principal_ref IS NULL OR (
      deleted_by_principal_ref LIKE 'agent:%'
      AND length(substr(deleted_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(deleted_by_principal_ref, 7), ':') = 0
      AND substr(deleted_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_actor_uuid TEXT,
  updated_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  updated_by_principal_ref TEXT
    CHECK (updated_by_principal_ref IS NULL OR (
      updated_by_principal_ref LIKE 'agent:%'
      AND length(substr(updated_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(updated_by_principal_ref, 7), ':') = 0
      AND substr(updated_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
  updated_by_scope_ref TEXT,
  deleted_by_scope_ref TEXT,
  cp_work_item_id TEXT,
  specification TEXT NOT NULL DEFAULT '',
  workflow_preset TEXT,
  preset_version INTEGER,
  phase TEXT,
  risk_class TEXT CHECK (risk_class IS NULL OR risk_class IN ('low','medium','high')),
  CHECK (
    (workflow_preset IS NULL AND preset_version IS NULL AND phase IS NULL) OR
    (workflow_preset IS NOT NULL AND preset_version IS NOT NULL AND phase IS NOT NULL)
  )
);

INSERT INTO tasks (
  uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid,
  assignee_actor_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id,
  acknowledged_at, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id,
  run_status, start_at, due_at, labels, meta, description, etag, created_at, updated_at,
  completed_at, archived_at, deleted_at, deleted_by_principal_ref,
  created_by_actor_uuid, updated_by_actor_uuid, created_by_principal_ref, updated_by_principal_ref,
  created_by_scope_ref, updated_by_scope_ref, deleted_by_scope_ref,
  cp_work_item_id, specification, workflow_preset, preset_version, phase, risk_class
)
SELECT
  t.uuid, t.id, t.slug, t.title, t.project_uuid, t.state, t.priority, t.kind, t.parent_task_uuid,
  t.assignee_actor_uuid,
  CASE WHEN aa.slug IS NOT NULL THEN 'agent:' || aa.slug END,
  t.requested_by_project_id, t.assigned_project_id,
  t.acknowledged_at, t.resolution, t.cp_project_id, t.cp_run_id, t.cp_session_id, t.sdk_session_id,
  t.run_status, t.start_at, t.due_at, t.labels, t.meta, t.description, t.etag, t.created_at, t.updated_at,
  t.completed_at, t.archived_at, t.deleted_at, NULL,
  t.created_by_actor_uuid, t.updated_by_actor_uuid,
  CASE WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug END,
  CASE WHEN ua.slug IS NOT NULL THEN 'agent:' || ua.slug END,
  t.created_by_scope_ref, NULL, NULL,
  t.cp_work_item_id, t.specification, t.workflow_preset, t.preset_version, t.phase, t.risk_class
FROM tasks_old t
LEFT JOIN actors aa ON aa.uuid = t.assignee_actor_uuid
LEFT JOIN actors ca ON ca.uuid = t.created_by_actor_uuid
LEFT JOIN actors ua ON ua.uuid = t.updated_by_actor_uuid;

DROP TABLE tasks_old;

CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);
CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);
CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_uuid) WHERE parent_task_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_idx ON tasks(assignee_actor_uuid) WHERE assignee_actor_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_principal_idx ON tasks(assignee_principal_ref) WHERE assignee_principal_ref IS NOT NULL;
CREATE INDEX tasks_kind_idx ON tasks(kind);
CREATE INDEX tasks_deleted_at_idx ON tasks(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);
CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);
CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;
CREATE INDEX tasks_cp_run_id_idx ON tasks(cp_run_id);
CREATE INDEX tasks_cp_session_id_idx ON tasks(cp_session_id);
CREATE INDEX tasks_cp_work_item_id_idx ON tasks(cp_work_item_id);
CREATE INDEX tasks_created_by_principal_idx ON tasks(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
CREATE INDEX tasks_updated_by_principal_idx ON tasks(updated_by_principal_ref)
  WHERE updated_by_principal_ref IS NOT NULL;

CREATE TRIGGER tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq (id) VALUES (NULL);
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_etag
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
  UPDATE tasks SET etag = OLD.etag + 1 WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_deleted_at
AFTER UPDATE OF state ON tasks
WHEN NEW.state = 'deleted' AND OLD.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
  WHERE rowid = NEW.rowid AND deleted_at IS NULL;
END;

CREATE TRIGGER tasks_au_undelete
AFTER UPDATE OF state ON tasks
WHEN OLD.state = 'deleted' AND NEW.state != 'deleted'
BEGIN
  UPDATE tasks
     SET deleted_at = NULL,
         deleted_by_principal_ref = NULL,
         deleted_by_scope_ref = NULL
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_state_consistency
AFTER UPDATE OF state ON tasks
BEGIN
  UPDATE tasks
     SET completed_at = COALESCE(NEW.completed_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'completed'
     AND NEW.completed_at IS NULL;

  UPDATE tasks
     SET archived_at = COALESCE(NEW.archived_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'archived'
     AND NEW.archived_at IS NULL;
END;

CREATE TRIGGER tasks_not_under_root_insert
BEFORE INSERT ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be created directly under the root container');
END;

CREATE TRIGGER tasks_not_under_root_update
BEFORE UPDATE OF project_uuid ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be moved directly under the root container');
END;

-- Rebuild workflow_instances after the tasks table rebuild so its task_uuid FK
-- points at the new tasks table instead of SQLite's temporary tasks_old name.
ALTER TABLE workflow_instances RENAME TO workflow_instances_old;

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

INSERT INTO workflow_instances (
  id, task_uuid, task_ref, project_id, template_id, template_version,
  template_hash, status, phase, outcome, revision, context_hash,
  task_doc_etag, task_doc_hash, created_at, updated_at, closed_at
)
SELECT
  id, task_uuid, task_ref, project_id, template_id, template_version,
  template_hash, status, phase, outcome, revision, context_hash,
  task_doc_etag, task_doc_hash, created_at, updated_at, closed_at
FROM workflow_instances_old;

DROP TABLE workflow_instances_old;

CREATE UNIQUE INDEX workflow_instances_one_active_per_task
  ON workflow_instances(task_uuid)
  WHERE status != 'closed';

ALTER TABLE workflow_events RENAME TO workflow_events_old;
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
  request_hash TEXT,
  result_json TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(instance_id, seq),
  UNIQUE(instance_id, idempotency_key)
);
INSERT INTO workflow_events (
  id, instance_id, seq, schema_version, type, actor, role, run_id,
  causation_id, correlation_id, observed_revision, next_revision,
  task_doc_etag, task_doc_hash, context_hash, idempotency_key, result,
  rejection_code, payload_json, prev_event_hash, event_hash, request_hash,
  result_json, created_at
)
SELECT
  id, instance_id, seq, schema_version, type, actor, role, run_id,
  causation_id, correlation_id, observed_revision, next_revision,
  task_doc_etag, task_doc_hash, context_hash, idempotency_key, result,
  rejection_code, payload_json, prev_event_hash, event_hash, request_hash,
  result_json, created_at
FROM workflow_events_old;
DROP TABLE workflow_events_old;

ALTER TABLE workflow_role_bindings RENAME TO workflow_role_bindings_old;
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
INSERT INTO workflow_role_bindings (
  instance_id, role, actor, delivery_ref, lane, binding_mode, bound_at
)
SELECT instance_id, role, actor, delivery_ref, lane, binding_mode, bound_at
FROM workflow_role_bindings_old;
DROP TABLE workflow_role_bindings_old;

ALTER TABLE workflow_runs RENAME TO workflow_runs_old;
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
  terminal_result TEXT,
  idempotency_key TEXT,
  request_hash TEXT
);
INSERT INTO workflow_runs (
  id, instance_id, role, actor, delivery_ref, lane, external_run_ref,
  status, started_at, completed_at, terminal_result, idempotency_key, request_hash
)
SELECT
  id, instance_id, role, actor, delivery_ref, lane, external_run_ref,
  status, started_at, completed_at, terminal_result, idempotency_key, request_hash
FROM workflow_runs_old;
DROP TABLE workflow_runs_old;
CREATE UNIQUE INDEX workflow_runs_instance_idempotency_key_unique
ON workflow_runs(instance_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX workflow_runs_external_run_ref_unique
ON workflow_runs(external_run_ref)
WHERE external_run_ref IS NOT NULL AND external_run_ref <> '';

ALTER TABLE workflow_check_runs RENAME TO workflow_check_runs_old;
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
INSERT INTO workflow_check_runs (
  id, instance_id, transition_id, check_id, hook_id, input_hash, exit_code,
  verdict, outcome, code, summary, facts_json, actor, role, run_id,
  started_at, completed_at
)
SELECT
  id, instance_id, transition_id, check_id, hook_id, input_hash, exit_code,
  verdict, outcome, code, summary, facts_json, actor, role, run_id,
  started_at, completed_at
FROM workflow_check_runs_old;
DROP TABLE workflow_check_runs_old;

ALTER TABLE workflow_evidence RENAME TO workflow_evidence_old;
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
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  facts_json TEXT
    CHECK (
      facts_json IS NULL OR
      (json_valid(facts_json) AND json_type(facts_json) = 'object')
    ),
  task_hash_at_production TEXT
);
INSERT INTO workflow_evidence (
  id, instance_id, kind, ref, summary, data_json, source_json, actor,
  role, run_id, task_etag_at_production, produced_at, facts_json,
  task_hash_at_production
)
SELECT
  id, instance_id, kind, ref, summary, data_json, source_json, actor,
  role, run_id, task_etag_at_production, produced_at, facts_json,
  task_hash_at_production
FROM workflow_evidence_old;
DROP TABLE workflow_evidence_old;

ALTER TABLE workflow_check_run_evidence RENAME TO workflow_check_run_evidence_old;
CREATE TABLE workflow_check_run_evidence (
  check_run_id TEXT NOT NULL REFERENCES workflow_check_runs(id) ON DELETE CASCADE,
  evidence_id TEXT NOT NULL REFERENCES workflow_evidence(id) ON DELETE CASCADE,
  PRIMARY KEY (check_run_id, evidence_id)
);
INSERT INTO workflow_check_run_evidence (check_run_id, evidence_id)
SELECT check_run_id, evidence_id
FROM workflow_check_run_evidence_old;
DROP TABLE workflow_check_run_evidence_old;

ALTER TABLE workflow_obligations RENAME TO workflow_obligations_old;
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
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  obligee_role TEXT,
  obligee_actor TEXT,
  waive_role TEXT,
  waive_actor TEXT,
  no_self_waive INTEGER NOT NULL DEFAULT 1 CHECK (no_self_waive IN (0, 1)),
  resolved_by_actor TEXT,
  resolved_by_role TEXT,
  resolved_at TEXT
);
INSERT INTO workflow_obligations (
  id, instance_id, kind, owner_role, owner_actor, blocking, status,
  reason, satisfied_by_evidence_id, created_at, updated_at,
  obligee_role, obligee_actor, waive_role, waive_actor, no_self_waive,
  resolved_by_actor, resolved_by_role, resolved_at
)
SELECT
  id, instance_id, kind, owner_role, owner_actor, blocking, status,
  reason, satisfied_by_evidence_id, created_at, updated_at,
  obligee_role, obligee_actor, waive_role, waive_actor, no_self_waive,
  resolved_by_actor, resolved_by_role, resolved_at
FROM workflow_obligations_old;
DROP TABLE workflow_obligations_old;

ALTER TABLE workflow_effects RENAME TO workflow_effects_old;
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
  lease_token TEXT,
  sequence INTEGER,
  semantic_key TEXT,
  receipt_json TEXT CHECK (receipt_json IS NULL OR json_valid(receipt_json)),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
INSERT INTO workflow_effects (
  id, instance_id, revision, kind, payload_json, status, idempotency_key,
  attempts, leased_by, leased_until, delivered_at, last_error, lease_token,
  sequence, semantic_key, receipt_json, created_at, updated_at
)
SELECT
  id, instance_id, revision, kind, payload_json, status, idempotency_key,
  attempts, leased_by, leased_until, delivered_at, last_error, lease_token,
  sequence, semantic_key, receipt_json, created_at, updated_at
FROM workflow_effects_old;
DROP TABLE workflow_effects_old;
CREATE UNIQUE INDEX workflow_effects_instance_sequence_unique
ON workflow_effects(instance_id, sequence)
WHERE sequence IS NOT NULL;
CREATE UNIQUE INDEX workflow_effects_instance_semantic_key_unique
ON workflow_effects(instance_id, semantic_key)
WHERE semantic_key IS NOT NULL AND semantic_key <> '';

-- comments.
ALTER TABLE comments RENAME TO comments_old;

CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    task_uuid TEXT NOT NULL,
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

    FOREIGN KEY (task_uuid) REFERENCES tasks(uuid) ON DELETE CASCADE,

    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);

INSERT INTO comments (
  uuid, id, task_uuid, actor_uuid, created_by_principal_ref, created_by_scope_ref,
  body, meta, etag, created_at, updated_at, deleted_at,
  deleted_by_actor_uuid, deleted_by_principal_ref, deleted_by_scope_ref
)
SELECT
  c.uuid, c.id, c.task_uuid, c.actor_uuid,
  CASE WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug END,
  NULL,
  c.body, c.meta, c.etag, c.created_at, c.updated_at, c.deleted_at,
  c.deleted_by_actor_uuid,
  CASE WHEN da.slug IS NOT NULL THEN 'agent:' || da.slug END,
  NULL
FROM comments_old c
LEFT JOIN actors ca ON ca.uuid = c.actor_uuid
LEFT JOIN actors da ON da.uuid = c.deleted_by_actor_uuid;

DROP TABLE comments_old;

CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at);
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at) WHERE actor_uuid IS NOT NULL;
CREATE INDEX idx_comments_principal_created ON comments(created_by_principal_ref, created_at)
  WHERE created_by_principal_ref IS NOT NULL;

CREATE TRIGGER comments_ai_touch_task
AFTER INSERT ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_au_touch_task
AFTER UPDATE ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_ad_touch_task
AFTER DELETE ON comments
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.task_uuid;
END;

-- attachments.
ALTER TABLE attachments RENAME TO attachments_old;

CREATE TABLE attachments (
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
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  filename  TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum   TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT
);

INSERT INTO attachments (
  uuid, id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum,
  created_at, created_by_actor_uuid, created_by_principal_ref, created_by_scope_ref
)
SELECT
  a.uuid, a.id, a.task_uuid, a.filename, a.relative_path, a.mime_type, a.size_bytes, a.checksum,
  a.created_at, a.created_by_actor_uuid,
  CASE WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug END,
  NULL
FROM attachments_old a
LEFT JOIN actors ca ON ca.uuid = a.created_by_actor_uuid;

DROP TABLE attachments_old;

CREATE UNIQUE INDEX attachments_task_filename_unique
  ON attachments(task_uuid, filename);
CREATE UNIQUE INDEX attachments_relpath_unique
  ON attachments(relative_path);
CREATE INDEX attachments_task_idx ON attachments(task_uuid);
CREATE INDEX attachments_created_by_principal_idx ON attachments(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;

CREATE TRIGGER attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq (id) VALUES (NULL);
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- task relations.
ALTER TABLE task_relations RENAME TO task_relations_old;

CREATE TABLE task_relations (
  from_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  to_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
  PRIMARY KEY (from_task_uuid, to_task_uuid, kind)
);

INSERT INTO task_relations (
  from_task_uuid, to_task_uuid, kind, meta, created_at,
  created_by_actor_uuid, created_by_principal_ref, created_by_scope_ref
)
SELECT
  r.from_task_uuid, r.to_task_uuid, r.kind, r.meta, r.created_at,
  r.created_by_actor_uuid,
  CASE WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug END,
  NULL
FROM task_relations_old r
LEFT JOIN actors ca ON ca.uuid = r.created_by_actor_uuid;

DROP TABLE task_relations_old;

CREATE TRIGGER task_relations_kind_check
BEFORE INSERT ON task_relations
WHEN NEW.kind NOT IN ('blocks', 'relates_to', 'duplicates')
BEGIN
  SELECT RAISE(ABORT, 'Invalid relation kind: must be blocks, relates_to, or duplicates');
END;

CREATE TRIGGER task_relations_no_self
BEFORE INSERT ON task_relations
WHEN NEW.from_task_uuid = NEW.to_task_uuid
BEGIN
  SELECT RAISE(ABORT, 'Task cannot have a relation to itself');
END;

CREATE INDEX task_relations_to_idx ON task_relations(to_task_uuid);
CREATE INDEX task_relations_from_idx ON task_relations(from_task_uuid);
CREATE INDEX task_relations_kind_idx ON task_relations(kind);
CREATE INDEX task_relations_created_by_principal_idx ON task_relations(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;

-- Workflow task role/evidence/transition tables.
ALTER TABLE task_role_assignments RENAME TO task_role_assignments_old;

CREATE TABLE task_role_assignments (
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
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('triager','owner','implementer','tester','reviewer','release_manager')),
  actor_uuid TEXT,
  principal_ref TEXT
    CHECK (principal_ref IS NULL OR (
      principal_ref LIKE 'agent:%'
      AND length(substr(principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(principal_ref, 7), ':') = 0
      AND substr(principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  assigned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(task_uuid, role)
);

INSERT INTO task_role_assignments (uuid, task_uuid, role, actor_uuid, principal_ref, assigned_at)
SELECT
  a.uuid, a.task_uuid, a.role, a.actor_uuid,
  CASE WHEN aa.slug IS NOT NULL THEN 'agent:' || aa.slug END,
  a.assigned_at
FROM task_role_assignments_old a
LEFT JOIN actors aa ON aa.uuid = a.actor_uuid;

DROP TABLE task_role_assignments_old;
CREATE INDEX task_role_assignments_task_idx ON task_role_assignments(task_uuid);
CREATE INDEX task_role_assignments_principal_idx ON task_role_assignments(principal_ref)
  WHERE principal_ref IS NOT NULL;

ALTER TABLE evidence_items RENAME TO evidence_items_old;

CREATE TABLE evidence_items (
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
  id TEXT UNIQUE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  content_hash TEXT,
  produced_by_actor_uuid TEXT,
  produced_by_principal_ref TEXT
    CHECK (produced_by_principal_ref IS NULL OR (
      produced_by_principal_ref LIKE 'agent:%'
      AND length(substr(produced_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(produced_by_principal_ref, 7), ':') = 0
      AND substr(produced_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  produced_by_role TEXT NOT NULL,
  build_id TEXT,
  build_version TEXT,
  build_env TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);

INSERT INTO evidence_items (
  uuid, id, task_uuid, kind, ref, content_hash, produced_by_actor_uuid,
  produced_by_principal_ref, produced_by_role, build_id, build_version, build_env,
  produced_at, meta
)
SELECT
  e.uuid, e.id, e.task_uuid, e.kind, e.ref, e.content_hash, e.produced_by_actor_uuid,
  CASE WHEN pa.slug IS NOT NULL THEN 'agent:' || pa.slug END,
  e.produced_by_role, e.build_id, e.build_version, e.build_env,
  e.produced_at, e.meta
FROM evidence_items_old e
LEFT JOIN actors pa ON pa.uuid = e.produced_by_actor_uuid;

DROP TABLE evidence_items_old;
CREATE INDEX evidence_items_task_produced_at_idx ON evidence_items(task_uuid, produced_at);
CREATE INDEX evidence_items_task_kind_idx ON evidence_items(task_uuid, kind);
CREATE INDEX evidence_items_produced_by_principal_idx ON evidence_items(produced_by_principal_ref)
  WHERE produced_by_principal_ref IS NOT NULL;

CREATE TRIGGER evidence_items_ai_friendly
AFTER INSERT ON evidence_items
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO evidence_item_seq (id) VALUES (NULL);
  UPDATE evidence_items
     SET id = 'EV-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

ALTER TABLE task_transitions RENAME TO task_transitions_old;

CREATE TABLE task_transitions (
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
  id TEXT UNIQUE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  from_phase TEXT,
  to_phase TEXT NOT NULL,
  from_lifecycle_state TEXT,
  to_lifecycle_state TEXT,
  actor_uuid TEXT,
  principal_ref TEXT
    CHECK (principal_ref IS NULL OR (
      principal_ref LIKE 'agent:%'
      AND length(substr(principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(principal_ref, 7), ':') = 0
      AND substr(principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  actor_role TEXT NOT NULL,
  evidence_item_uuids TEXT,
  transitioned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);

INSERT INTO task_transitions (
  uuid, id, task_uuid, from_phase, to_phase, from_lifecycle_state, to_lifecycle_state,
  actor_uuid, principal_ref, actor_role, evidence_item_uuids, transitioned_at, meta
)
SELECT
  tr.uuid, tr.id, tr.task_uuid, tr.from_phase, tr.to_phase, tr.from_lifecycle_state, tr.to_lifecycle_state,
  tr.actor_uuid,
  CASE WHEN aa.slug IS NOT NULL THEN 'agent:' || aa.slug END,
  tr.actor_role, tr.evidence_item_uuids, tr.transitioned_at, tr.meta
FROM task_transitions_old tr
LEFT JOIN actors aa ON aa.uuid = tr.actor_uuid;

DROP TABLE task_transitions_old;
CREATE INDEX task_transitions_task_transitioned_at_idx ON task_transitions(task_uuid, transitioned_at);
CREATE INDEX task_transitions_principal_idx ON task_transitions(principal_ref)
  WHERE principal_ref IS NOT NULL;

CREATE TRIGGER task_transitions_ai_friendly
AFTER INSERT ON task_transitions
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_transition_seq (id) VALUES (NULL);
  UPDATE task_transitions
     SET id = 'TR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Handoffs already tolerate nil actor UUIDs, but the containers rebuild above
-- causes SQLite to retarget existing FKs to containers_old. Rebuild handoffs so
-- project_container_uuid references the new containers table and add principal refs.
ALTER TABLE handoffs RENAME TO handoffs_old;

CREATE TABLE handoffs (
  uuid TEXT PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  scope_ref TEXT NOT NULL,
  scope_kind TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  agent_actor_uuid TEXT,
  agent_principal_ref TEXT,
  project_container_uuid TEXT,
  created_by_agent_id TEXT NOT NULL,
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','acknowledged')),
  idempotency_key TEXT,
  acknowledged_at TEXT,
  acknowledged_by_agent_id TEXT,
  acknowledged_by_actor_uuid TEXT,
  acknowledged_by_principal_ref TEXT,
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

INSERT INTO handoffs (
  uuid, id, scope_ref, scope_kind, agent_id, project_id,
  agent_actor_uuid, agent_principal_ref, project_container_uuid,
  created_by_agent_id, created_by_actor_uuid, created_by_principal_ref,
  title, body, status, idempotency_key,
  acknowledged_at, acknowledged_by_agent_id, acknowledged_by_actor_uuid,
  acknowledged_by_principal_ref, acknowledgement_note, meta, etag, created_at, updated_at
)
SELECT
  h.uuid, h.id, h.scope_ref, h.scope_kind, h.agent_id, h.project_id,
  h.agent_actor_uuid,
  CASE WHEN h.agent_id IS NOT NULL AND h.agent_id <> '' THEN 'agent:' || h.agent_id END,
  h.project_container_uuid,
  h.created_by_agent_id, h.created_by_actor_uuid,
  CASE
    WHEN h.created_by_agent_id IS NOT NULL AND h.created_by_agent_id <> '' THEN 'agent:' || h.created_by_agent_id
    WHEN ca.slug IS NOT NULL THEN 'agent:' || ca.slug
  END,
  h.title, h.body, h.status, h.idempotency_key,
  h.acknowledged_at, h.acknowledged_by_agent_id, h.acknowledged_by_actor_uuid,
  CASE
    WHEN h.acknowledged_by_agent_id IS NOT NULL AND h.acknowledged_by_agent_id <> '' THEN 'agent:' || h.acknowledged_by_agent_id
    WHEN aa.slug IS NOT NULL THEN 'agent:' || aa.slug
  END,
  h.acknowledgement_note, h.meta, h.etag, h.created_at, h.updated_at
FROM handoffs_old h
LEFT JOIN actors ca ON ca.uuid = h.created_by_actor_uuid
LEFT JOIN actors aa ON aa.uuid = h.acknowledged_by_actor_uuid;

DROP TABLE handoffs_old;

CREATE INDEX handoffs_scope_status_idx ON handoffs(scope_ref, status, created_at);
CREATE INDEX handoffs_agent_idx ON handoffs(agent_id, project_id, status);
CREATE INDEX handoffs_updated_idx ON handoffs(updated_at);
CREATE UNIQUE INDEX handoffs_idempotency_idx ON handoffs(scope_ref, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX handoffs_agent_principal_idx ON handoffs(agent_principal_ref)
  WHERE agent_principal_ref IS NOT NULL;
CREATE INDEX handoffs_created_by_principal_idx ON handoffs(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;

-- First-class event attribution columns. Historic rows are backfilled from the
-- legacy actor cache where possible; scope remains unknown unless already stored
-- on the resource.
ALTER TABLE event_log ADD COLUMN principal_ref TEXT;
ALTER TABLE event_log ADD COLUMN scope_ref TEXT;

UPDATE event_log
   SET principal_ref = (
     SELECT 'agent:' || slug FROM actors WHERE actors.uuid = event_log.actor_uuid
   )
 WHERE actor_uuid IS NOT NULL;

UPDATE event_log
   SET scope_ref = (
     SELECT created_by_scope_ref FROM tasks
      WHERE tasks.uuid = event_log.resource_uuid
        AND event_log.resource_type = 'task'
        AND tasks.created_by_scope_ref IS NOT NULL
   )
 WHERE scope_ref IS NULL;

CREATE INDEX event_log_principal_idx ON event_log(principal_ref, id DESC)
  WHERE principal_ref IS NOT NULL;
CREATE INDEX event_log_scope_idx ON event_log(scope_ref, id DESC)
  WHERE scope_ref IS NOT NULL;

CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid = '00000000-0000-4000-8000-000000000001'
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
       t.assignee_principal_ref,
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
       t.created_by_principal_ref,
       t.updated_by_principal_ref,
       t.deleted_by_principal_ref,
       t.created_by_scope_ref,
       t.updated_by_scope_ref,
       t.deleted_by_scope_ref,
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid;
