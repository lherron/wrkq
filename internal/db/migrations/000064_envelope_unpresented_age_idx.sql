-- Bounds the undelivered-envelope age scan (T-09261) to live mail. Every
-- authoritative observation, including each ordinary monitor eventsView page,
-- asks which pending/deferred envelopes have passed created_at + 24h; without
-- this the OR of explicit and implicit deadlines scans the whole table.
CREATE INDEX envelopes_unpresented_age_idx
  ON envelopes(created_at) WHERE state IN ('pending', 'deferred');
