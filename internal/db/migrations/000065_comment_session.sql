-- A session qualifies the writer scope. Existing comments retain NULL attribution.
ALTER TABLE comments ADD COLUMN created_by_host_session_id TEXT
  CHECK (created_by_host_session_id IS NULL OR created_by_host_session_id GLOB 'hsid-*');
ALTER TABLE comments ADD COLUMN created_by_generation INTEGER
  CHECK (created_by_generation IS NULL OR created_by_generation >= 1);
-- SQLite cannot add a table CHECK without rebuilding comments. Enforce pairing
-- for all writes, including raw SQL, with insert/update triggers.
CREATE TRIGGER comments_session_pair_insert BEFORE INSERT ON comments
WHEN (NEW.created_by_host_session_id IS NULL) != (NEW.created_by_generation IS NULL)
BEGIN SELECT RAISE(ABORT, 'comment session fields must be paired'); END;
CREATE TRIGGER comments_session_pair_update BEFORE UPDATE ON comments
WHEN (NEW.created_by_host_session_id IS NULL) != (NEW.created_by_generation IS NULL)
BEGIN SELECT RAISE(ABORT, 'comment session fields must be paired'); END;
