-- Indexes for replayable workflow event queries across tasks.

CREATE INDEX IF NOT EXISTS workflow_events_type_created_id_idx
  ON workflow_events(type, created_at, id);

CREATE INDEX IF NOT EXISTS workflow_events_transition_from_to_created_idx
  ON workflow_events(
    type,
    json_extract(payload_json, '$.from.phase'),
    json_extract(payload_json, '$.to.phase'),
    created_at,
    id
  )
  WHERE type = 'workflow.transitioned';

CREATE INDEX IF NOT EXISTS workflow_role_bindings_role_instance_idx
  ON workflow_role_bindings(role, instance_id);

