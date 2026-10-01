-- Requester: who asked for this task or subtask (T-09978). Distinct from creator
-- attribution (audit history) and requested_by_project_id (project routing).
-- The scope's agent must name the principal; scope without principal is refused.
ALTER TABLE tasks ADD COLUMN requester_principal_ref TEXT;
ALTER TABLE tasks ADD COLUMN requester_scope_ref TEXT;
CREATE TRIGGER tasks_requester_pair_insert BEFORE INSERT ON tasks
WHEN NEW.requester_scope_ref IS NOT NULL AND NEW.requester_principal_ref IS NULL
BEGIN SELECT RAISE(ABORT, 'requester scope requires requester principal'); END;
CREATE TRIGGER tasks_requester_pair_update BEFORE UPDATE OF requester_principal_ref, requester_scope_ref ON tasks
WHEN NEW.requester_scope_ref IS NOT NULL AND NEW.requester_principal_ref IS NULL
BEGIN SELECT RAISE(ABORT, 'requester scope requires requester principal'); END;
