-- Campaign portfolio substrate: draft lifecycle and container labels.
--
-- SQLite cannot widen an inline column CHECK directly. Add the replacement
-- column, copy the values, then drop/rename it so foreign keys and the rest of
-- the container table remain untouched.

ALTER TABLE containers ADD COLUMN labels TEXT;

ALTER TABLE containers ADD COLUMN campaign_state_v2 TEXT
  CHECK (campaign_state_v2 IN ('draft','active','completed','cancelled'));
UPDATE containers SET campaign_state_v2 = campaign_state;
ALTER TABLE containers DROP COLUMN campaign_state;
ALTER TABLE containers RENAME COLUMN campaign_state_v2 TO campaign_state;
