-- Record the full praesidium scopeRef of the agent that created a task, so
-- created_by attribution carries the scope (agent + project + task), not just
-- the agent slug. Nullable: human/CLI creations without a resolvable scope
-- leave it NULL and fall back to the actor slug.
ALTER TABLE tasks ADD COLUMN created_by_scope_ref TEXT;
