-- The broker input that accepted a presentation (T-07673).
--
-- This is opaque HRC execution-world join data. A nullable additive column
-- preserves every existing receipt without inventing a backfill value.
ALTER TABLE envelope_presentations ADD COLUMN input_id TEXT;
