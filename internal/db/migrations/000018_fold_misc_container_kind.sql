-- Migration: fold misc container kind into directory
--
-- `misc` never became a useful semantic label. Normalize any existing rows to
-- directory, then remove misc from the DB-level whitelist.

UPDATE containers
   SET kind = 'directory'
 WHERE kind = 'misc';

DROP TRIGGER IF EXISTS containers_kind_check_insert;
DROP TRIGGER IF EXISTS containers_kind_check_update;

CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, or area');
END;

CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, or area');
END;
