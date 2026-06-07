-- Harden wrkf evidence/effect/obligation semantics.

ALTER TABLE workflow_templates ADD COLUMN hook_catalog_json TEXT CHECK (hook_catalog_json IS NULL OR json_valid(hook_catalog_json));
ALTER TABLE workflow_templates ADD COLUMN hook_catalog_hash TEXT;

ALTER TABLE workflow_evidence ADD COLUMN task_hash_at_production TEXT;

ALTER TABLE workflow_obligations ADD COLUMN obligee_role TEXT;
ALTER TABLE workflow_obligations ADD COLUMN obligee_actor TEXT;
ALTER TABLE workflow_obligations ADD COLUMN waive_role TEXT;
ALTER TABLE workflow_obligations ADD COLUMN waive_actor TEXT;
ALTER TABLE workflow_obligations ADD COLUMN no_self_waive INTEGER NOT NULL DEFAULT 1 CHECK (no_self_waive IN (0, 1));
ALTER TABLE workflow_obligations ADD COLUMN resolved_by_actor TEXT;
ALTER TABLE workflow_obligations ADD COLUMN resolved_by_role TEXT;
ALTER TABLE workflow_obligations ADD COLUMN resolved_at TEXT;

ALTER TABLE workflow_effects ADD COLUMN sequence INTEGER;
ALTER TABLE workflow_effects ADD COLUMN semantic_key TEXT;
ALTER TABLE workflow_effects ADD COLUMN receipt_json TEXT CHECK (receipt_json IS NULL OR json_valid(receipt_json));

CREATE UNIQUE INDEX workflow_effects_instance_sequence_unique
ON workflow_effects(instance_id, sequence)
WHERE sequence IS NOT NULL;

CREATE UNIQUE INDEX workflow_effects_instance_semantic_key_unique
ON workflow_effects(instance_id, semantic_key)
WHERE semantic_key IS NOT NULL AND semantic_key <> '';
