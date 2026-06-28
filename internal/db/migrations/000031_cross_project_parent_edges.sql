-- wrkq:foreign-keys-off
-- Make parent_task_uuid a bounded graph edge instead of containment.
-- Same-residency cascade behavior is now explicit store logic; the schema must
-- not hard-delete cross-project children through an unconditional FK cascade.

PRAGMA legacy_alter_table = ON;

ALTER TABLE tasks RENAME TO tasks_old;

PRAGMA legacy_alter_table = OFF;

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
  parent_task_uuid TEXT REFERENCES tasks(uuid),
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
  uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid,
  assignee_actor_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id,
  acknowledged_at, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id,
  run_status, start_at, due_at, labels, meta, description, etag, created_at, updated_at,
  completed_at, archived_at, deleted_at, deleted_by_principal_ref,
  created_by_actor_uuid, updated_by_actor_uuid, created_by_principal_ref, updated_by_principal_ref,
  created_by_scope_ref, updated_by_scope_ref, deleted_by_scope_ref,
  cp_work_item_id, specification, workflow_preset, preset_version, phase, risk_class
FROM tasks_old;

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
