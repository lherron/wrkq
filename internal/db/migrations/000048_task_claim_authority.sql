-- Durable, generation-fenced task holdership for cross-node coordinators.
-- A released task retains claim_generation so authority can never regress.

ALTER TABLE tasks ADD COLUMN claimed_by_principal_ref TEXT
  CHECK (claimed_by_principal_ref IS NULL OR (
    claimed_by_principal_ref LIKE 'agent:%'
    AND length(substr(claimed_by_principal_ref, 7)) BETWEEN 1 AND 64
    AND instr(substr(claimed_by_principal_ref, 7), ':') = 0
    AND substr(claimed_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
  ));
ALTER TABLE tasks ADD COLUMN claimed_scope_ref TEXT;
ALTER TABLE tasks ADD COLUMN claimed_node TEXT;
ALTER TABLE tasks ADD COLUMN claimed_at TEXT;
ALTER TABLE tasks ADD COLUMN claim_generation INTEGER NOT NULL DEFAULT 0
  CHECK (claim_generation >= 0);
-- Only the digest is durable; the bearer claim token is returned once.
ALTER TABLE tasks ADD COLUMN claim_token_hash TEXT;

CREATE INDEX tasks_claimed_by_idx ON tasks(claimed_by_principal_ref)
  WHERE claimed_by_principal_ref IS NOT NULL;
CREATE INDEX tasks_claimed_node_idx ON tasks(claimed_node)
  WHERE claimed_node IS NOT NULL;

-- Holdership is projected as one durable tuple. Partial tuples are never
-- meaningful and would make fencing decisions ambiguous.
CREATE TRIGGER tasks_claim_tuple_insert
BEFORE INSERT ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
END;

CREATE TRIGGER tasks_claim_tuple_update
BEFORE UPDATE OF claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_token_hash ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
END;

CREATE TRIGGER tasks_claim_generation_no_regression
BEFORE UPDATE OF claim_generation ON tasks
WHEN NEW.claim_generation < OLD.claim_generation
BEGIN
  SELECT RAISE(ABORT, 'task claim generation cannot regress');
END;
