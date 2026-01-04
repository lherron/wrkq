-- Migration: Async run linkage (CP ↔ wrkq)
-- Adds cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status
-- for tracking Control Plane async run status on tasks

ALTER TABLE tasks ADD COLUMN cp_project_id TEXT;
ALTER TABLE tasks ADD COLUMN cp_run_id TEXT;
ALTER TABLE tasks ADD COLUMN cp_session_id TEXT;
ALTER TABLE tasks ADD COLUMN sdk_session_id TEXT;
ALTER TABLE tasks ADD COLUMN run_status TEXT CHECK (run_status IN ('queued','running','completed','failed','cancelled','timed_out'));

CREATE INDEX tasks_cp_run_id_idx ON tasks(cp_run_id);
CREATE INDEX tasks_cp_session_id_idx ON tasks(cp_session_id);

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
       t.cp_run_id,
       t.cp_session_id,
       t.sdk_session_id,
       t.run_status,
       t.start_at,
       t.due_at,
       t.labels,
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
