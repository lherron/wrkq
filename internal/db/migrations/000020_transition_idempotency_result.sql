-- Store transition idempotency request hashes and exact committed replay results.

ALTER TABLE workflow_events ADD COLUMN request_hash TEXT;
ALTER TABLE workflow_events ADD COLUMN result_json TEXT;
