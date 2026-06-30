-- T-04229: first-class caused_by causal lineage between tasks.
-- A dedicated normalized edge table (NOT tasks.meta, NOT task_relations): each row
-- records that task_uuid's delivered defect/rework was caused by
-- caused_by_task_uuid. position preserves the ordered, de-duplicated input set.
-- The caused_by side is ON DELETE RESTRICT so purging a causing task cannot
-- silently erase surviving tasks' lineage; the dependent side is ON DELETE CASCADE.
CREATE TABLE task_causes (
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  caused_by_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE RESTRICT,
  position INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  PRIMARY KEY (task_uuid, caused_by_task_uuid),
  UNIQUE (task_uuid, position),
  CHECK (task_uuid <> caused_by_task_uuid)
);
CREATE INDEX task_causes_caused_by_idx ON task_causes(caused_by_task_uuid, task_uuid);
