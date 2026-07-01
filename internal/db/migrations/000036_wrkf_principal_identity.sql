-- Migration: wrkf workflow identity actor -> principal_ref (T-05372).
--
-- Data-preserving / additive only. Adds canonical principal_ref identity
-- columns alongside the existing actor-named columns on wrkf-owned workflow
-- tables and backfills them from the legacy actor values. The old actor
-- columns are intentionally LEFT IN PLACE as orphaned compatibility storage;
-- their destructive DROP is consolidated into the parent T-04317 migration.
--
-- Backfill rule (per non-empty actor value):
--   * already a prefixed principal ref (contains ':') -> preserved verbatim
--     (e.g. agent:clod, human:lance, system:wrkf, user:lance).
--   * resolves in the pre-existing actors table (by slug or id) -> derived as
--     'agent:' || actors.slug (normalizes an actor id/uuid to its slug).
--   * bare slug-shaped token ([a-z0-9_-]+ with no other resolution) -> derived
--     directly as 'agent:' || token (the token IS the agent slug; an actors row
--     is NOT required — historical wrkf rows carry bare agent slugs such as
--     'smokey'/'observer' that were never minted as actor rows).
--   * otherwise un-derivable (e.g. an uppercase A-* id or uuid with no actors
--     row, or a value with whitespace/punctuation) -> principal_ref stays NULL
--     and the guard below aborts the migration loudly with table/row/value
--     diagnostics. No 'agent:unknown' / empty / system fallback is written for
--     ambiguous rows.

-- 1. Add principal identity columns alongside the legacy actor columns.
ALTER TABLE workflow_events ADD COLUMN principal_ref TEXT;
ALTER TABLE workflow_role_bindings ADD COLUMN principal_ref TEXT;
ALTER TABLE workflow_runs ADD COLUMN principal_ref TEXT;
ALTER TABLE workflow_check_runs ADD COLUMN principal_ref TEXT;
ALTER TABLE workflow_evidence ADD COLUMN principal_ref TEXT;
ALTER TABLE workflow_obligations ADD COLUMN owner_principal_ref TEXT;
ALTER TABLE workflow_obligations ADD COLUMN obligee_principal_ref TEXT;
ALTER TABLE workflow_obligations ADD COLUMN waive_principal_ref TEXT;
ALTER TABLE workflow_obligations ADD COLUMN resolved_by_principal_ref TEXT;
ALTER TABLE workflow_templates ADD COLUMN installed_by_principal_ref TEXT;

-- 2. Backfill principal columns from legacy actor values.
--    (Derivation rule per column value is described in the header.)
UPDATE workflow_events
   SET principal_ref = CASE
     WHEN actor IS NULL OR actor = '' THEN NULL
     WHEN instr(actor, ':') > 0 THEN actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_events.actor OR a.id = workflow_events.actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_events.actor OR a.id = workflow_events.actor LIMIT 1)
     WHEN actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || actor
     ELSE NULL
   END
 WHERE actor IS NOT NULL AND actor <> '';

UPDATE workflow_role_bindings
   SET principal_ref = CASE
     WHEN actor IS NULL OR actor = '' THEN NULL
     WHEN instr(actor, ':') > 0 THEN actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_role_bindings.actor OR a.id = workflow_role_bindings.actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_role_bindings.actor OR a.id = workflow_role_bindings.actor LIMIT 1)
     WHEN actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || actor
     ELSE NULL
   END;

UPDATE workflow_runs
   SET principal_ref = CASE
     WHEN actor IS NULL OR actor = '' THEN NULL
     WHEN instr(actor, ':') > 0 THEN actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_runs.actor OR a.id = workflow_runs.actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_runs.actor OR a.id = workflow_runs.actor LIMIT 1)
     WHEN actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || actor
     ELSE NULL
   END;

UPDATE workflow_check_runs
   SET principal_ref = CASE
     WHEN actor IS NULL OR actor = '' THEN NULL
     WHEN instr(actor, ':') > 0 THEN actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_check_runs.actor OR a.id = workflow_check_runs.actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_check_runs.actor OR a.id = workflow_check_runs.actor LIMIT 1)
     WHEN actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || actor
     ELSE NULL
   END
 WHERE actor IS NOT NULL AND actor <> '';

UPDATE workflow_evidence
   SET principal_ref = CASE
     WHEN actor IS NULL OR actor = '' THEN NULL
     WHEN instr(actor, ':') > 0 THEN actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_evidence.actor OR a.id = workflow_evidence.actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_evidence.actor OR a.id = workflow_evidence.actor LIMIT 1)
     WHEN actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || actor
     ELSE NULL
   END
 WHERE actor IS NOT NULL AND actor <> '';

UPDATE workflow_obligations
   SET owner_principal_ref = CASE
     WHEN owner_actor IS NULL OR owner_actor = '' THEN NULL
     WHEN instr(owner_actor, ':') > 0 THEN owner_actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.owner_actor OR a.id = workflow_obligations.owner_actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.owner_actor OR a.id = workflow_obligations.owner_actor LIMIT 1)
     WHEN owner_actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || owner_actor
     ELSE NULL
   END,
       obligee_principal_ref = CASE
     WHEN obligee_actor IS NULL OR obligee_actor = '' THEN NULL
     WHEN instr(obligee_actor, ':') > 0 THEN obligee_actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.obligee_actor OR a.id = workflow_obligations.obligee_actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.obligee_actor OR a.id = workflow_obligations.obligee_actor LIMIT 1)
     WHEN obligee_actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || obligee_actor
     ELSE NULL
   END,
       waive_principal_ref = CASE
     WHEN waive_actor IS NULL OR waive_actor = '' THEN NULL
     WHEN instr(waive_actor, ':') > 0 THEN waive_actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.waive_actor OR a.id = workflow_obligations.waive_actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.waive_actor OR a.id = workflow_obligations.waive_actor LIMIT 1)
     WHEN waive_actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || waive_actor
     ELSE NULL
   END,
       resolved_by_principal_ref = CASE
     WHEN resolved_by_actor IS NULL OR resolved_by_actor = '' THEN NULL
     WHEN instr(resolved_by_actor, ':') > 0 THEN resolved_by_actor
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.resolved_by_actor OR a.id = workflow_obligations.resolved_by_actor LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_obligations.resolved_by_actor OR a.id = workflow_obligations.resolved_by_actor LIMIT 1)
     WHEN resolved_by_actor NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || resolved_by_actor
     ELSE NULL
   END;

UPDATE workflow_templates
   SET installed_by_principal_ref = CASE
     WHEN installed_by IS NULL OR installed_by = '' THEN NULL
     WHEN instr(installed_by, ':') > 0 THEN installed_by
     WHEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_templates.installed_by OR a.id = workflow_templates.installed_by LIMIT 1) IS NOT NULL
       THEN (SELECT 'agent:' || a.slug FROM actors a WHERE a.slug = workflow_templates.installed_by OR a.id = workflow_templates.installed_by LIMIT 1)
     WHEN installed_by NOT GLOB '*[^a-z0-9_-]*' THEN 'agent:' || installed_by
     ELSE NULL
   END
 WHERE installed_by IS NOT NULL AND installed_by <> '';

-- 3. Loud-failure guard: abort the migration (with table/row/value in the
-- message) if any non-empty legacy actor value could not be derived to a
-- principal_ref. Implemented via a temp trigger so RAISE(ABORT, ...) can embed
-- the offending value.
CREATE TEMP TABLE _t05372_guard (msg TEXT);
CREATE TEMP TRIGGER _t05372_guard_abort BEFORE INSERT ON _t05372_guard
BEGIN
  SELECT RAISE(ABORT, 'T-05372: un-derivable wrkf workflow identity (no agent:<slug> for legacy actor): ' || NEW.msg);
END;

INSERT INTO _t05372_guard (msg)
SELECT 'workflow_events.id=' || id || ' actor=' || actor
  FROM workflow_events
 WHERE actor IS NOT NULL AND actor <> '' AND principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_role_bindings(instance_id=' || instance_id || ',role=' || role || ').actor=' || actor
  FROM workflow_role_bindings
 WHERE actor IS NOT NULL AND actor <> '' AND principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_runs.id=' || id || ' actor=' || actor
  FROM workflow_runs
 WHERE actor IS NOT NULL AND actor <> '' AND principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_check_runs.id=' || id || ' actor=' || actor
  FROM workflow_check_runs
 WHERE actor IS NOT NULL AND actor <> '' AND principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_evidence.id=' || id || ' actor=' || actor
  FROM workflow_evidence
 WHERE actor IS NOT NULL AND actor <> '' AND principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_obligations.id=' || id || ' owner_actor=' || owner_actor
  FROM workflow_obligations
 WHERE owner_actor IS NOT NULL AND owner_actor <> '' AND owner_principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_obligations.id=' || id || ' obligee_actor=' || obligee_actor
  FROM workflow_obligations
 WHERE obligee_actor IS NOT NULL AND obligee_actor <> '' AND obligee_principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_obligations.id=' || id || ' waive_actor=' || waive_actor
  FROM workflow_obligations
 WHERE waive_actor IS NOT NULL AND waive_actor <> '' AND waive_principal_ref IS NULL;
INSERT INTO _t05372_guard (msg)
SELECT 'workflow_obligations.id=' || id || ' resolved_by_actor=' || resolved_by_actor
  FROM workflow_obligations
 WHERE resolved_by_actor IS NOT NULL AND resolved_by_actor <> '' AND resolved_by_principal_ref IS NULL;

DROP TRIGGER _t05372_guard_abort;
DROP TABLE _t05372_guard;

-- 4. Live uniqueness/lookup index on (instance_id, role, principal_ref). The
-- legacy (instance_id, role, actor) PRIMARY KEY remains inert until T-04317.
CREATE UNIQUE INDEX IF NOT EXISTS workflow_role_bindings_principal_unique
  ON workflow_role_bindings(instance_id, role, principal_ref)
  WHERE principal_ref IS NOT NULL;

CREATE INDEX IF NOT EXISTS workflow_runs_principal_idx
  ON workflow_runs(instance_id, principal_ref);
