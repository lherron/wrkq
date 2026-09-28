-- Type-first seeks for the two merged project-timeline sources. The time
-- column makes bounded sparse reads stop at their server-time window edges.
CREATE INDEX event_log_type_time_idx
  ON event_log(event_type, timestamp, id);

CREATE INDEX project_events_type_time_idx
  ON project_events(type, created_at, id);

-- Unfiltered bounded reads seek both window edges without walking ID history.
CREATE INDEX event_log_time_id_idx ON event_log(timestamp, id);
CREATE INDEX project_events_time_id_idx ON project_events(created_at, id);
