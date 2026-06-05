-- Harden workflow run idempotency and external run binding.

ALTER TABLE workflow_runs ADD COLUMN idempotency_key TEXT;
ALTER TABLE workflow_runs ADD COLUMN request_hash TEXT;

CREATE TEMP TABLE wrkq_external_run_ref_dupes_guard (
  ok INTEGER NOT NULL CHECK (ok = 1)
);

INSERT INTO wrkq_external_run_ref_dupes_guard(ok)
SELECT 0
WHERE EXISTS (
  SELECT 1
  FROM workflow_runs
  WHERE external_run_ref IS NOT NULL AND external_run_ref <> ''
  GROUP BY external_run_ref
  HAVING COUNT(*) > 1
);

DROP TABLE wrkq_external_run_ref_dupes_guard;

CREATE UNIQUE INDEX workflow_runs_instance_idempotency_key_unique
ON workflow_runs(instance_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX workflow_runs_external_run_ref_unique
ON workflow_runs(external_run_ref)
WHERE external_run_ref IS NOT NULL AND external_run_ref <> '';
