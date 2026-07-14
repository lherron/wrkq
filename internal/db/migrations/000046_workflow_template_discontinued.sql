-- Template-version lifecycle metadata is operator-managed. Installing a newer
-- version does not implicitly discontinue any existing version.
ALTER TABLE workflow_templates ADD COLUMN discontinued_at TEXT;
ALTER TABLE workflow_templates ADD COLUMN discontinued_by TEXT;
