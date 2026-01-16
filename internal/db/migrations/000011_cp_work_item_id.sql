-- Migration: Add cp_work_item_id for Control Plane WorkItem binding
-- This stable pointer links WRKQ tasks to control-plane WorkItems for multi-agent coordination.
-- Unlike cp_run_id which changes per run, cp_work_item_id remains stable across multiple runs.

ALTER TABLE tasks ADD COLUMN cp_work_item_id TEXT;

CREATE INDEX tasks_cp_work_item_id_idx ON tasks(cp_work_item_id);

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
