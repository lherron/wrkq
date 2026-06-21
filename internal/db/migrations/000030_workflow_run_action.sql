-- Add a nullable semantic-action label to workflow_runs.
--
-- A wrkf "action" (triage/implement/review/verify/...) is a low-ceremony
-- composition over an existing workflow_runs row — not a new ledger table. The
-- action label is the only piece of an action that the run table cannot already
-- represent, so it is stored here as a single nullable column. Existing
-- low-level wrkf.run.* writers leave it NULL.

ALTER TABLE workflow_runs ADD COLUMN action TEXT;

CREATE INDEX workflow_runs_instance_action_idx
  ON workflow_runs(instance_id, action)
  WHERE action IS NOT NULL;
