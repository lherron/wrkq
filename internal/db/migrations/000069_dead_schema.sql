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
