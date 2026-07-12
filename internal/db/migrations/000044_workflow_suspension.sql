-- Suspension as a first-class condition on a workflow instance (T-06260).
-- A running instance carries a nil suspension; a suspended instance carries a
-- single active suspension record (unique id, template-declared reason code,
-- timestamp, and a pointer to the causing outcome/event). Status/phase/outcome
-- are untouched by park — a suspended instance is still "in" its phase.
-- There is no stack: exactly one active suspension, enforced by the engine at
-- the suspend door and by the suspended-write gate on both commit paths.
ALTER TABLE workflow_instances ADD COLUMN suspension_id TEXT;
ALTER TABLE workflow_instances ADD COLUMN suspension_reason TEXT;
ALTER TABLE workflow_instances ADD COLUMN suspension_at TEXT;
ALTER TABLE workflow_instances ADD COLUMN suspension_cause_ref TEXT;

-- Monotonic source for suspension ids.
CREATE TABLE workflow_suspension_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
