-- Action lease expiry is evaluated while claiming or heartbeating a run; no
-- query scans workflow runs for expired leases to reap them.
DROP INDEX IF EXISTS workflow_runs_action_reap_idx;
