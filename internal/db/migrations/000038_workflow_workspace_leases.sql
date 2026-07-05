CREATE TABLE workflow_workspace_leases (
  canonical_root TEXT PRIMARY KEY,
  lease_owner TEXT,
  lease_token TEXT,
  owner_generation INTEGER NOT NULL DEFAULT 0,
  lease_expires_at TEXT,
  heartbeat_at TEXT,
  claimed_at TEXT NOT NULL,
  released_at TEXT
);

CREATE INDEX workflow_workspace_leases_active_idx
  ON workflow_workspace_leases(lease_expires_at)
  WHERE lease_token IS NOT NULL;
