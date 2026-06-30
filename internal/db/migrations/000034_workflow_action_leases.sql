ALTER TABLE workflow_runs ADD COLUMN lease_owner TEXT;
ALTER TABLE workflow_runs ADD COLUMN lease_token TEXT;
ALTER TABLE workflow_runs ADD COLUMN lease_expires_at TEXT;
ALTER TABLE workflow_runs ADD COLUMN heartbeat_at TEXT;

CREATE INDEX workflow_runs_action_reap_idx
  ON workflow_runs(status, action, lease_expires_at)
  WHERE action IS NOT NULL;
