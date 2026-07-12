-- Action claim succession: explicit predecessor CAS and durable lineage.
ALTER TABLE workflow_runs ADD COLUMN predecessor_run_id TEXT REFERENCES workflow_runs(id);
ALTER TABLE workflow_runs ADD COLUMN superseded_by_run_id TEXT REFERENCES workflow_runs(id);
ALTER TABLE workflow_runs ADD COLUMN side_effect_classes_json TEXT;

CREATE INDEX workflow_runs_predecessor_idx
  ON workflow_runs(predecessor_run_id)
  WHERE predecessor_run_id IS NOT NULL;
