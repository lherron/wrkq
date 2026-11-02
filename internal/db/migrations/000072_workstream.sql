-- wrkq:foreign-keys-off

-- Rebuild from the complete schema through 000070; retain dependent views, triggers and indexes.

DROP TRIGGER comments_ad_touch_task;

DROP TRIGGER comments_ai_touch_task;

DROP TRIGGER comments_au_touch_task;

DROP TRIGGER containers_bi_slug_disjoint;

DROP TRIGGER containers_bu_slug_disjoint;

DROP TRIGGER tasks_ai_friendly;

DROP TRIGGER tasks_au_deleted_at;

DROP TRIGGER tasks_au_etag;

DROP TRIGGER tasks_au_state_consistency;

DROP TRIGGER tasks_au_subtask_residency;

DROP TRIGGER tasks_au_touch;

DROP TRIGGER tasks_au_undelete;

DROP TRIGGER tasks_bi_child_owner;

DROP TRIGGER tasks_bi_slug_disjoint;

DROP TRIGGER tasks_bi_subtask;

DROP TRIGGER tasks_bi_workflow_consistency;

DROP TRIGGER tasks_bu_child_owner;

DROP TRIGGER tasks_bu_slug_disjoint;

DROP TRIGGER tasks_bu_subtask_identity;

DROP TRIGGER tasks_bu_subtask_residency;

DROP TRIGGER tasks_bu_workflow_consistency;

DROP TRIGGER tasks_claim_generation_no_regression;

DROP TRIGGER tasks_claim_tuple_insert;

DROP TRIGGER tasks_claim_tuple_update;

DROP TRIGGER tasks_not_under_root_insert;

DROP TRIGGER tasks_not_under_root_update;

DROP TRIGGER tasks_requester_pair_insert;

DROP TRIGGER tasks_requester_pair_update;

DROP VIEW v_container_paths;

DROP VIEW v_task_paths;

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
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore','workstream')),
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
  risk_class TEXT CHECK (risk_class IS NULL OR risk_class IN ('low','medium','high')), claimed_by_principal_ref TEXT
  CHECK (claimed_by_principal_ref IS NULL OR (
    claimed_by_principal_ref LIKE 'agent:%'
    AND length(substr(claimed_by_principal_ref, 7)) BETWEEN 1 AND 64
    AND instr(substr(claimed_by_principal_ref, 7), ':') = 0
    AND substr(claimed_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
  )), claimed_scope_ref TEXT, claimed_node TEXT, claimed_at TEXT, claim_generation INTEGER NOT NULL DEFAULT 0
  CHECK (claim_generation >= 0), claim_token_hash TEXT, outcome TEXT, campaign_uuid TEXT
  REFERENCES containers(uuid) ON DELETE RESTRICT, subtask_owner_uuid TEXT REFERENCES tasks(uuid) ON DELETE RESTRICT, requester_principal_ref TEXT, requester_scope_ref TEXT,
 covers TEXT CHECK (covers IS NULL OR (kind = 'workstream' AND json_valid(covers))),
  CHECK (
    (workflow_preset IS NULL AND preset_version IS NULL AND phase IS NULL) OR
    (workflow_preset IS NOT NULL AND preset_version IS NOT NULL AND phase IS NOT NULL)
  )
);

INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid, assignee_actor_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id, acknowledged_at, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status, start_at, due_at, labels, meta, description, etag, created_at, updated_at, completed_at, archived_at, deleted_at, deleted_by_principal_ref, created_by_actor_uuid, updated_by_actor_uuid, created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref, deleted_by_scope_ref, cp_work_item_id, specification, workflow_preset, preset_version, phase, risk_class, claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_generation, claim_token_hash, outcome, campaign_uuid, subtask_owner_uuid, requester_principal_ref, requester_scope_ref) SELECT uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid, assignee_actor_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id, acknowledged_at, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status, start_at, due_at, labels, meta, description, etag, created_at, updated_at, completed_at, archived_at, deleted_at, deleted_by_principal_ref, created_by_actor_uuid, updated_by_actor_uuid, created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref, deleted_by_scope_ref, cp_work_item_id, specification, workflow_preset, preset_version, phase, risk_class, claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_generation, claim_token_hash, outcome, campaign_uuid, subtask_owner_uuid, requester_principal_ref, requester_scope_ref FROM tasks_old;

DROP TABLE tasks_old;

CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;

CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);

CREATE INDEX tasks_assignee_principal_idx ON tasks(assignee_principal_ref) WHERE assignee_principal_ref IS NOT NULL;

CREATE INDEX tasks_campaign_idx ON tasks(campaign_uuid);

CREATE INDEX tasks_claimed_by_idx ON tasks(claimed_by_principal_ref)
  WHERE claimed_by_principal_ref IS NOT NULL;

CREATE INDEX tasks_claimed_node_idx ON tasks(claimed_node)
  WHERE claimed_node IS NOT NULL;

CREATE INDEX tasks_created_by_principal_idx ON tasks(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;

CREATE INDEX tasks_deleted_at_idx ON tasks(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE INDEX tasks_kind_idx ON tasks(kind);

CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_uuid) WHERE parent_task_uuid IS NOT NULL;

CREATE INDEX tasks_project_idx   ON tasks(project_uuid);

CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);

CREATE INDEX tasks_slug_idx      ON tasks(slug);

CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);

CREATE UNIQUE INDEX tasks_unique_slug_in_container ON tasks(project_uuid, slug) WHERE subtask_owner_uuid IS NULL;

CREATE UNIQUE INDEX tasks_unique_slug_under_owner ON tasks(subtask_owner_uuid, slug) WHERE subtask_owner_uuid IS NOT NULL;

CREATE INDEX tasks_updated_by_principal_idx ON tasks(updated_by_principal_ref)
  WHERE updated_by_principal_ref IS NOT NULL;

CREATE INDEX tasks_updated_idx   ON tasks(updated_at);

CREATE TRIGGER comments_ad_touch_task
AFTER DELETE ON comments
WHEN OLD.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.task_uuid;
END;

CREATE TRIGGER comments_ai_touch_task
AFTER INSERT ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER comments_au_touch_task
AFTER UPDATE ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;

CREATE TRIGGER containers_bi_slug_disjoint
BEFORE INSERT ON containers
BEGIN
 SELECT RAISE(ABORT, 'container slug conflicts with sibling task') WHERE EXISTS (SELECT 1 FROM tasks WHERE project_uuid=NEW.parent_uuid AND slug=NEW.slug AND subtask_owner_uuid IS NULL);
END;

CREATE TRIGGER containers_bu_slug_disjoint
BEFORE UPDATE OF slug, parent_uuid ON containers
BEGIN
 SELECT RAISE(ABORT, 'container slug conflicts with sibling task') WHERE EXISTS (SELECT 1 FROM tasks WHERE project_uuid=NEW.parent_uuid AND slug=NEW.slug AND subtask_owner_uuid IS NULL);
END;

CREATE TRIGGER tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq (id) VALUES (NULL);
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER tasks_au_deleted_at
AFTER UPDATE OF state ON tasks
WHEN NEW.state = 'deleted' AND OLD.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
  WHERE rowid = NEW.rowid AND deleted_at IS NULL;
END;

CREATE TRIGGER tasks_au_etag
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
  UPDATE tasks SET etag = OLD.etag + 1 WHERE rowid = NEW.rowid;
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
     SET completed_at = NULL
   WHERE rowid = NEW.rowid
     AND OLD.state = 'completed'
     AND NEW.state != 'completed';

  UPDATE tasks
     SET archived_at = COALESCE(NEW.archived_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'archived'
     AND NEW.archived_at IS NULL;
END;

CREATE TRIGGER tasks_au_subtask_residency
AFTER UPDATE OF project_uuid ON tasks WHEN NEW.subtask_owner_uuid IS NULL AND NEW.project_uuid IS NOT OLD.project_uuid
BEGIN
 UPDATE tasks SET project_uuid=NEW.project_uuid WHERE subtask_owner_uuid=NEW.uuid;
END;

CREATE TRIGGER tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
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

CREATE TRIGGER tasks_bi_child_owner
BEFORE INSERT ON tasks WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask cannot own child tasks') WHERE EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.parent_task_uuid AND subtask_owner_uuid IS NOT NULL);
END;

CREATE TRIGGER tasks_bi_slug_disjoint
BEFORE INSERT ON tasks WHEN NEW.subtask_owner_uuid IS NULL
BEGIN
 SELECT RAISE(ABORT, 'task slug conflicts with sibling container') WHERE EXISTS (SELECT 1 FROM containers WHERE parent_uuid=NEW.project_uuid AND slug=NEW.slug);
END;

CREATE TRIGGER tasks_bi_subtask
BEFORE INSERT ON tasks WHEN NEW.subtask_owner_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask owner must be an ordinary task') WHERE NOT EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.subtask_owner_uuid AND subtask_owner_uuid IS NULL);
 SELECT RAISE(ABORT, 'subtask residency must equal owner residency') WHERE NEW.project_uuid IS NOT (SELECT project_uuid FROM tasks WHERE uuid=NEW.subtask_owner_uuid);
 SELECT RAISE(ABORT, 'subtask cannot have parent or campaign') WHERE NEW.parent_task_uuid IS NOT NULL OR NEW.campaign_uuid IS NOT NULL;
 SELECT RAISE(ABORT, 'invalid subtask identity') WHERE length(NEW.slug) NOT BETWEEN 1 AND 56 OR NEW.slug NOT GLOB '[a-z]*' OR NEW.slug GLOB '*[^a-z0-9-]*' OR NEW.slug GLOB '*-' OR NEW.id IS NOT ((SELECT id FROM tasks WHERE uuid=NEW.subtask_owner_uuid)||'.'||NEW.slug);
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

CREATE TRIGGER tasks_bu_child_owner
BEFORE UPDATE OF parent_task_uuid ON tasks WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask cannot own child tasks') WHERE EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.parent_task_uuid AND subtask_owner_uuid IS NOT NULL);
END;

CREATE TRIGGER tasks_bu_slug_disjoint
BEFORE UPDATE OF slug, project_uuid ON tasks WHEN NEW.subtask_owner_uuid IS NULL
BEGIN
 SELECT RAISE(ABORT, 'task slug conflicts with sibling container') WHERE EXISTS (SELECT 1 FROM containers WHERE parent_uuid=NEW.project_uuid AND slug=NEW.slug);
END;

CREATE TRIGGER tasks_bu_subtask_identity
BEFORE UPDATE ON tasks
BEGIN
 SELECT RAISE(ABORT, 'subtask owner is immutable') WHERE NEW.subtask_owner_uuid IS NOT OLD.subtask_owner_uuid;
 SELECT RAISE(ABORT, 'subtask identity is immutable') WHERE OLD.subtask_owner_uuid IS NOT NULL AND (NEW.id IS NOT OLD.id OR NEW.slug IS NOT OLD.slug);
 SELECT RAISE(ABORT, 'subtask cannot have parent or campaign') WHERE NEW.subtask_owner_uuid IS NOT NULL AND (NEW.parent_task_uuid IS NOT NULL OR NEW.campaign_uuid IS NOT NULL);
END;

CREATE TRIGGER tasks_bu_subtask_residency
BEFORE UPDATE OF project_uuid ON tasks WHEN NEW.subtask_owner_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask residency must equal owner residency') WHERE NEW.project_uuid IS NOT (SELECT project_uuid FROM tasks WHERE uuid=NEW.subtask_owner_uuid);
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

CREATE TRIGGER tasks_claim_generation_no_regression
BEFORE UPDATE OF claim_generation ON tasks
WHEN NEW.claim_generation < OLD.claim_generation
BEGIN
  SELECT RAISE(ABORT, 'task claim generation cannot regress');
END;

CREATE TRIGGER tasks_claim_tuple_insert
BEFORE INSERT ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
END;

CREATE TRIGGER tasks_claim_tuple_update
BEFORE UPDATE OF claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_token_hash ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
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

CREATE TRIGGER tasks_requester_pair_insert BEFORE INSERT ON tasks
WHEN NEW.requester_scope_ref IS NOT NULL AND NEW.requester_principal_ref IS NULL
BEGIN SELECT RAISE(ABORT, 'requester scope requires requester principal'); END;

CREATE TRIGGER tasks_requester_pair_update BEFORE UPDATE OF requester_principal_ref, requester_scope_ref ON tasks
WHEN NEW.requester_scope_ref IS NOT NULL AND NEW.requester_principal_ref IS NULL
BEGIN SELECT RAISE(ABORT, 'requester scope requires requester principal'); END;

CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid = '00000000-0000-4000-8000-000000000001'
  UNION ALL
  SELECT c.uuid, c.id, c.slug, c.title, c.parent_uuid, c.kind, c.sort_index,
         ct.path || '/' || c.slug AS path,
         ct.level + 1 AS level
    FROM containers c
    JOIN container_tree ct ON c.parent_uuid = ct.uuid
)
SELECT uuid, id, slug, title, parent_uuid, kind, sort_index, path, level
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
       t.subtask_owner_uuid,
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
       cp.path || '/' || CASE WHEN t.subtask_owner_uuid IS NOT NULL THEN owner.slug || '/' ELSE '' END || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid
  LEFT JOIN tasks owner ON owner.uuid = t.subtask_owner_uuid;

PRAGMA foreign_key_check;
