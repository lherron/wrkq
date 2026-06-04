-- Migration: add structured routing facts to workflow evidence

ALTER TABLE workflow_evidence
ADD COLUMN facts_json TEXT
CHECK (
  facts_json IS NULL OR
  (json_valid(facts_json) AND json_type(facts_json) = 'object')
);
