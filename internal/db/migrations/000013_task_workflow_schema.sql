-- Migration: add ACP workflow metadata and evidence/transition tables

ALTER TABLE tasks ADD COLUMN workflow_preset TEXT;
ALTER TABLE tasks ADD COLUMN preset_version INTEGER;
ALTER TABLE tasks ADD COLUMN phase TEXT;
ALTER TABLE tasks ADD COLUMN risk_class TEXT
  CHECK (risk_class IS NULL OR risk_class IN ('low','medium','high'));

DROP TRIGGER IF EXISTS tasks_bi_workflow_consistency;
DROP TRIGGER IF EXISTS tasks_bu_workflow_consistency;

CREATE TRIGGER tasks_bi_workflow_consistency
BEFORE INSERT ON tasks
WHEN NOT (
  (NEW.workflow_preset IS NULL AND NEW.preset_version IS NULL AND NEW.phase IS NULL) OR
  (NEW.workflow_preset IS NOT NULL AND NEW.preset_version IS NOT NULL AND NEW.phase IS NOT NULL)
)
BEGIN
  SELECT RAISE(ABORT, 'workflow_preset, preset_version, and phase must all be NULL or all be set');
END;

CREATE TRIGGER tasks_bu_workflow_consistency
BEFORE UPDATE OF workflow_preset, preset_version, phase ON tasks
WHEN NOT (
  (NEW.workflow_preset IS NULL AND NEW.preset_version IS NULL AND NEW.phase IS NULL) OR
  (NEW.workflow_preset IS NOT NULL AND NEW.preset_version IS NOT NULL AND NEW.phase IS NOT NULL)
)
BEGIN
  SELECT RAISE(ABORT, 'workflow_preset, preset_version, and phase must all be NULL or all be set');
END;

CREATE TABLE evidence_item_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_transition_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

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
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  assigned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(task_uuid, role)
);

CREATE INDEX task_role_assignments_task_idx ON task_role_assignments(task_uuid);

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
  produced_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  produced_by_role TEXT NOT NULL,
  build_id TEXT,
  build_version TEXT,
  build_env TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);

CREATE INDEX evidence_items_task_produced_at_idx ON evidence_items(task_uuid, produced_at);
CREATE INDEX evidence_items_task_kind_idx ON evidence_items(task_uuid, kind);

CREATE TRIGGER evidence_items_ai_friendly
AFTER INSERT ON evidence_items
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO evidence_item_seq (id) VALUES (NULL);
  UPDATE evidence_items
     SET id = 'EV-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

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
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  actor_role TEXT NOT NULL,
  evidence_item_uuids TEXT,
  transitioned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);

CREATE INDEX task_transitions_task_transitioned_at_idx ON task_transitions(task_uuid, transitioned_at);

CREATE TRIGGER task_transitions_ai_friendly
AFTER INSERT ON task_transitions
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_transition_seq (id) VALUES (NULL);
  UPDATE task_transitions
     SET id = 'TR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

DROP VIEW IF EXISTS v_task_paths;

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
