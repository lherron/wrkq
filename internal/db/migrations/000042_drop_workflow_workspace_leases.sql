-- Purge the workspace lock table (T-06267).
-- The engine no longer serializes physical worktree access. Which directory a
-- seat runs in is dispatch policy and belongs to the loop; a single loop
-- serializes its own checkouts. `workspaceRef` survives only as an opaque
-- reported fact on run records — recorded, never interpreted by the engine.
DROP TABLE IF EXISTS workflow_workspace_leases;
