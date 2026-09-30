-- Named subtasks: stable local identity and cascade-enforced owner residency.
ALTER TABLE tasks ADD COLUMN subtask_owner_uuid TEXT REFERENCES tasks(uuid) ON DELETE RESTRICT;
DROP INDEX tasks_unique_slug_in_container;
CREATE UNIQUE INDEX tasks_unique_slug_in_container ON tasks(project_uuid, slug) WHERE subtask_owner_uuid IS NULL;
CREATE UNIQUE INDEX tasks_unique_slug_under_owner ON tasks(subtask_owner_uuid, slug) WHERE subtask_owner_uuid IS NOT NULL;

CREATE TRIGGER tasks_bi_subtask
BEFORE INSERT ON tasks WHEN NEW.subtask_owner_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask owner must be an ordinary task') WHERE NOT EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.subtask_owner_uuid AND subtask_owner_uuid IS NULL);
 SELECT RAISE(ABORT, 'subtask residency must equal owner residency') WHERE NEW.project_uuid IS NOT (SELECT project_uuid FROM tasks WHERE uuid=NEW.subtask_owner_uuid);
 SELECT RAISE(ABORT, 'subtask cannot have parent or campaign') WHERE NEW.parent_task_uuid IS NOT NULL OR NEW.campaign_uuid IS NOT NULL;
 SELECT RAISE(ABORT, 'invalid subtask identity') WHERE length(NEW.slug) NOT BETWEEN 1 AND 56 OR NEW.slug NOT GLOB '[a-z]*' OR NEW.slug GLOB '*[^a-z0-9-]*' OR NEW.slug GLOB '*-' OR NEW.id IS NOT ((SELECT id FROM tasks WHERE uuid=NEW.subtask_owner_uuid)||'.'||NEW.slug);
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
CREATE TRIGGER tasks_au_subtask_residency
AFTER UPDATE OF project_uuid ON tasks WHEN NEW.subtask_owner_uuid IS NULL AND NEW.project_uuid IS NOT OLD.project_uuid
BEGIN
 UPDATE tasks SET project_uuid=NEW.project_uuid WHERE subtask_owner_uuid=NEW.uuid;
END;
CREATE TRIGGER tasks_bi_child_owner
BEFORE INSERT ON tasks WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask cannot own child tasks') WHERE EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.parent_task_uuid AND subtask_owner_uuid IS NOT NULL);
END;
CREATE TRIGGER tasks_bu_child_owner
BEFORE UPDATE OF parent_task_uuid ON tasks WHEN NEW.parent_task_uuid IS NOT NULL
BEGIN
 SELECT RAISE(ABORT, 'subtask cannot own child tasks') WHERE EXISTS (SELECT 1 FROM tasks WHERE uuid=NEW.parent_task_uuid AND subtask_owner_uuid IS NOT NULL);
END;
CREATE TRIGGER tasks_bi_slug_disjoint
BEFORE INSERT ON tasks WHEN NEW.subtask_owner_uuid IS NULL
BEGIN
 SELECT RAISE(ABORT, 'task slug conflicts with sibling container') WHERE EXISTS (SELECT 1 FROM containers WHERE parent_uuid=NEW.project_uuid AND slug=NEW.slug);
END;
CREATE TRIGGER tasks_bu_slug_disjoint
BEFORE UPDATE OF slug, project_uuid ON tasks WHEN NEW.subtask_owner_uuid IS NULL
BEGIN
 SELECT RAISE(ABORT, 'task slug conflicts with sibling container') WHERE EXISTS (SELECT 1 FROM containers WHERE parent_uuid=NEW.project_uuid AND slug=NEW.slug);
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

DROP VIEW v_task_paths;
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
