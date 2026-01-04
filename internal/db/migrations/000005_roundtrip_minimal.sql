-- Migration: Minimal round-trip routing + acknowledgment fields
-- Adds requested_by_project_id, assigned_project_id, acknowledged_at, resolution
-- Adds indexes and updates v_task_paths view

ALTER TABLE tasks ADD COLUMN requested_by_project_id TEXT;
ALTER TABLE tasks ADD COLUMN assigned_project_id TEXT;
ALTER TABLE tasks ADD COLUMN acknowledged_at TEXT;
ALTER TABLE tasks ADD COLUMN resolution TEXT;

CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);
CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);
CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;

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
