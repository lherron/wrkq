ALTER TABLE workflow_runs ADD COLUMN semantic_action_key TEXT;
ALTER TABLE workflow_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN agent_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN scope_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_contract TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_id TEXT;
ALTER TABLE workflow_runs ADD COLUMN handler_version TEXT;
ALTER TABLE workflow_runs ADD COLUMN workspace_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN source_run_id TEXT REFERENCES workflow_runs(id);
ALTER TABLE workflow_runs ADD COLUMN source_evidence_id TEXT REFERENCES workflow_evidence(id);
ALTER TABLE workflow_runs ADD COLUMN source_commit_sha TEXT;
ALTER TABLE workflow_runs ADD COLUMN owner_generation INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX workflow_runs_active_semantic_key_unique
  ON workflow_runs(instance_id, semantic_action_key)
  WHERE semantic_action_key IS NOT NULL
    AND status = 'active';

CREATE INDEX workflow_runs_source_binding_idx
  ON workflow_runs(instance_id, source_run_id, source_evidence_id)
  WHERE source_run_id IS NOT NULL;
