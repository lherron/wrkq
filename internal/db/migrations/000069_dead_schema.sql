-- T-10158: owner-approved clear drops and handoff sequence repair.
DROP TABLE workflow_check_run_evidence;
ALTER TABLE rooms DROP COLUMN reopened_at;
ALTER TABLE task_causes DROP COLUMN created_by_actor_uuid;
ALTER TABLE workflow_events DROP COLUMN causation_id;
ALTER TABLE workflow_events DROP COLUMN correlation_id;
DROP INDEX rooms_adhoc_idle_idx;
DROP INDEX tasks_cp_run_id_idx;
DROP INDEX tasks_cp_session_id_idx;
DROP INDEX tasks_cp_work_item_id_idx;
DROP INDEX idx_comments_actor_created;
DROP INDEX tasks_assignee_idx;

-- sqlite_sequence has no unique constraint on name. Preserve the largest
-- allocated identity before removing duplicate rows, including uneven rows.
UPDATE sqlite_sequence
SET seq = (SELECT MAX(seq) FROM sqlite_sequence WHERE name = 'handoff_seq')
WHERE rowid = (SELECT MIN(rowid) FROM sqlite_sequence WHERE name = 'handoff_seq');
DELETE FROM sqlite_sequence
WHERE name = 'handoff_seq'
AND rowid != (SELECT MIN(rowid) FROM sqlite_sequence WHERE name = 'handoff_seq');

-- Approved contract removals (one protocol identity change).
DROP VIEW v_task_paths;
DROP VIEW v_container_paths;
DROP INDEX containers_section_idx;
ALTER TABLE containers DROP COLUMN section_uuid;
DROP TABLE sections;
DROP TABLE section_seq;
ALTER TABLE workflow_runs DROP COLUMN handler_id;
ALTER TABLE workflow_runs DROP COLUMN handler_version;
ALTER TABLE workflow_check_runs DROP COLUMN run_id;
ALTER TABLE workflow_events DROP COLUMN rejection_code;

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
