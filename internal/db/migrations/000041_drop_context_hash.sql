-- Purge contextHash from the wrkf engine (T-06264).
-- Revision is the sole CAS token; the stored hash only ever changed when the
-- revision changed, so it gated nothing ExpectRevision does not, and nothing
-- read it back. Drop the columns from both instance and event tables.
ALTER TABLE workflow_instances DROP COLUMN context_hash;
ALTER TABLE workflow_events DROP COLUMN context_hash;
