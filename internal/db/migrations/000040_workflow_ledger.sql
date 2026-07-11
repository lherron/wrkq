-- Immutable, instance-scoped forensic ledger (T-06032).
CREATE TABLE ledger_entry (
  uuid TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts TEXT NOT NULL,
  kind TEXT NOT NULL,
  about_principal_ref TEXT NOT NULL,
  written_by TEXT NOT NULL,
  body_json TEXT NOT NULL,
  UNIQUE(instance_id, seq)
);

-- Per-task replay is ordered by the frozen (instance_id, seq) tuple.
CREATE INDEX ledger_entry_task_replay_idx
  ON ledger_entry(task_id, instance_id, seq);

-- Cross-instance projections paginate by (ts, instance_id, seq).
CREATE INDEX ledger_entry_projection_idx
  ON ledger_entry(about_principal_ref, kind, ts, instance_id, seq);

-- The absence of update/delete verbs is intentional, but not sufficient:
-- SQLite itself refuses mutation of a forensic ledger row.
CREATE TRIGGER ledger_entry_no_update
BEFORE UPDATE ON ledger_entry
BEGIN
  SELECT RAISE(ABORT, 'ledger entries are append-only');
END;

CREATE TRIGGER ledger_entry_no_delete
BEFORE DELETE ON ledger_entry
BEGIN
  SELECT RAISE(ABORT, 'ledger entries are append-only');
END;
