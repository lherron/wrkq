-- Independent extension records; their fences never mutate the subject.
CREATE TABLE ext (
  uuid TEXT PRIMARY KEY,
  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
  container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
  room_uuid TEXT REFERENCES rooms(uuid) ON DELETE CASCADE,
  ns TEXT NOT NULL,
  kind TEXT NOT NULL,
  id TEXT NOT NULL,
  body TEXT NOT NULL CHECK (json_valid(body) AND json_type(body) = 'object' AND length(body) <= 65536),
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  updated_by_principal_ref TEXT,
  updated_by_scope_ref TEXT,
  CHECK ((task_uuid IS NOT NULL) + (container_uuid IS NOT NULL) + (room_uuid IS NOT NULL) = 1)
);
CREATE UNIQUE INDEX ext_key ON ext (coalesce(task_uuid, container_uuid, room_uuid), ns, kind, id);
CREATE INDEX ext_by_ns ON ext (ns, kind, updated_at);
