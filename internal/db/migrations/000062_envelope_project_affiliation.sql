-- T-08656: immutable, non-owning endpoint-project stamps for ad-hoc messages.
-- Existing envelopes deliberately remain NULL: current scope slugs cannot safely
-- establish historical project identity.

ALTER TABLE envelopes ADD COLUMN from_project_uuid TEXT;
ALTER TABLE envelopes ADD COLUMN to_project_uuid TEXT;

CREATE INDEX envelopes_group_project_affiliation_idx
  ON envelopes(group_id, to_project_uuid)
  WHERE to_project_uuid IS NOT NULL;
