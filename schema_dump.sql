CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		);
CREATE TABLE actor_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE sqlite_sequence(name,seq);
CREATE TABLE container_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE attachment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE handoff_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE comment_sequences (
    name TEXT PRIMARY KEY,
    value INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS "actors" (
  uuid TEXT NOT NULL PRIMARY KEY
        DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id   TEXT UNIQUE,
  slug TEXT NOT NULL UNIQUE
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  display_name TEXT,
  role TEXT NOT NULL CHECK (role IN ('human','agent','system')),
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX actors_slug_unique ON actors(slug);
CREATE TRIGGER actors_ai_friendly
AFTER INSERT ON actors
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO actor_seq (id) VALUES (NULL);
  UPDATE actors
     SET id = 'A-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER actors_au_touch
AFTER UPDATE ON actors
BEGIN
  UPDATE actors SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE IF NOT EXISTS "containers" (
  uuid TEXT NOT NULL PRIMARY KEY
        DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id   TEXT UNIQUE,
  parent_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  etag INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
, kind TEXT NOT NULL DEFAULT 'project', sort_index INTEGER NOT NULL DEFAULT 0, section_uuid TEXT REFERENCES sections(uuid) ON DELETE SET NULL, webhook_urls TEXT);
CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE TRIGGER containers_ai_friendly
AFTER INSERT ON containers
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO container_seq (id) VALUES (NULL);
  UPDATE containers
     SET id = 'P-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER containers_au_touch
AFTER UPDATE ON containers
BEGIN
  UPDATE containers SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE IF NOT EXISTS "attachments" (
  uuid TEXT NOT NULL PRIMARY KEY
        DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id   TEXT UNIQUE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  filename  TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  mime_type TEXT,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum   TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL
);
CREATE UNIQUE INDEX attachments_task_filename_unique
  ON attachments(task_uuid, filename);
CREATE UNIQUE INDEX attachments_relpath_unique
  ON attachments(relative_path);
CREATE INDEX attachments_task_idx ON attachments(task_uuid);
CREATE TRIGGER attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq (id) VALUES (NULL);
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    task_uuid TEXT NOT NULL,
    actor_uuid TEXT NOT NULL,
    body TEXT NOT NULL,
    meta TEXT,
    etag INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT,
    deleted_at TEXT,
    deleted_by_actor_uuid TEXT,

    FOREIGN KEY (task_uuid) REFERENCES tasks(uuid) ON DELETE CASCADE,
    FOREIGN KEY (actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,
    FOREIGN KEY (deleted_by_actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,

    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);
CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at);
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at);
CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment','handoff')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT
);
CREATE INDEX event_log_resource_idx ON event_log(resource_type, resource_uuid, id DESC);
CREATE TABLE handoffs (
  uuid TEXT PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  scope_ref TEXT NOT NULL,
  scope_kind TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  agent_actor_uuid TEXT,
  project_container_uuid TEXT,
  created_by_agent_id TEXT NOT NULL,
  created_by_actor_uuid TEXT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','acknowledged')),
  idempotency_key TEXT,
  acknowledged_at TEXT,
  acknowledged_by_agent_id TEXT,
  acknowledged_by_actor_uuid TEXT,
  acknowledgement_note TEXT,
  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  FOREIGN KEY (agent_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,
  FOREIGN KEY (project_container_uuid) REFERENCES containers(uuid) ON DELETE SET NULL,
  FOREIGN KEY (created_by_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,
  FOREIGN KEY (acknowledged_by_actor_uuid) REFERENCES actors(uuid) ON DELETE SET NULL,

  CHECK (length(id) > 0),
  CHECK (length(title) > 0),
  CHECK (length(body) > 0),
  CHECK (etag >= 1),
  CHECK (scope_kind IN ('agent','project','project-role','project-task','project-task-role'))
);
CREATE INDEX handoffs_scope_status_idx ON handoffs(scope_ref, status, created_at);
CREATE INDEX handoffs_agent_idx ON handoffs(agent_id, project_id, status);
CREATE INDEX handoffs_updated_idx ON handoffs(updated_at);
CREATE UNIQUE INDEX handoffs_idempotency_idx ON handoffs(scope_ref, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE TABLE section_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, or misc');
END;
CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'feature', 'area', 'misc')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, feature, area, or misc');
END;
CREATE TABLE sections (
  uuid TEXT NOT NULL PRIMARY KEY
        DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id TEXT UNIQUE,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE CASCADE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  order_index INTEGER NOT NULL DEFAULT 0,
  role TEXT NOT NULL DEFAULT 'ready',
  is_default INTEGER NOT NULL DEFAULT 0,
  wip_limit INTEGER,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  UNIQUE(project_uuid, slug)
);
CREATE TRIGGER sections_role_check_insert
BEFORE INSERT ON sections
WHEN NEW.role NOT IN ('backlog', 'ready', 'active', 'review', 'done')
BEGIN
  SELECT RAISE(ABORT, 'Invalid section role: must be backlog, ready, active, review, or done');
END;
CREATE TRIGGER sections_role_check_update
BEFORE UPDATE OF role ON sections
WHEN NEW.role NOT IN ('backlog', 'ready', 'active', 'review', 'done')
BEGIN
  SELECT RAISE(ABORT, 'Invalid section role: must be backlog, ready, active, review, or done');
END;
CREATE TRIGGER sections_ai_friendly
AFTER INSERT ON sections
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO section_seq (id) VALUES (NULL);
  UPDATE sections
     SET id = 'S-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER sections_au_touch
AFTER UPDATE ON sections
BEGIN
  UPDATE sections SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE INDEX sections_project_idx ON sections(project_uuid);
CREATE INDEX sections_role_idx ON sections(role);
CREATE INDEX containers_section_idx ON containers(section_uuid) WHERE section_uuid IS NOT NULL;
CREATE TABLE task_relations (
  from_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  to_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  PRIMARY KEY (from_task_uuid, to_task_uuid, kind)
);
CREATE TRIGGER task_relations_kind_check
BEFORE INSERT ON task_relations
WHEN NEW.kind NOT IN ('blocks', 'relates_to', 'duplicates')
BEGIN
  SELECT RAISE(ABORT, 'Invalid relation kind: must be blocks, relates_to, or duplicates');
END;
CREATE TRIGGER task_relations_no_self
BEFORE INSERT ON task_relations
WHEN NEW.from_task_uuid = NEW.to_task_uuid
BEGIN
  SELECT RAISE(ABORT, 'Task cannot have a relation to itself');
END;
CREATE INDEX task_relations_to_idx ON task_relations(to_task_uuid);
CREATE INDEX task_relations_from_idx ON task_relations(from_task_uuid);
CREATE INDEX task_relations_kind_idx ON task_relations(kind);
CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid IS NULL
  UNION ALL
  SELECT c.uuid, c.id, c.slug, c.title, c.parent_uuid, c.kind, c.section_uuid, c.sort_index,
         ct.path || '/' || c.slug AS path,
         ct.level + 1 AS level
    FROM containers c
    JOIN container_tree ct ON c.parent_uuid = ct.uuid
)
SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level
  FROM container_tree
/* v_container_paths(uuid,id,slug,title,parent_uuid,kind,section_uuid,sort_index,path,level) */;
CREATE TABLE IF NOT EXISTS "tasks" (
  uuid TEXT PRIMARY KEY
       DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id   TEXT UNIQUE,
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  -- Extended state enum: added 'idea' before 'draft'
  state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  parent_task_uuid TEXT REFERENCES "tasks"(uuid) ON DELETE CASCADE,
  assignee_actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  requested_by_project_id TEXT,
  assigned_project_id TEXT,
  acknowledged_at TEXT,
  resolution TEXT,
  cp_project_id TEXT,
  cp_run_id TEXT,
  cp_session_id TEXT,
  sdk_session_id TEXT,
  run_status TEXT CHECK (run_status IN ('queued','running','completed','failed','cancelled','timed_out')),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,
  meta     TEXT,
  description TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  deleted_at   TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
, cp_work_item_id TEXT, specification TEXT NOT NULL DEFAULT '', workflow_preset TEXT, preset_version INTEGER, phase TEXT, risk_class TEXT
  CHECK (risk_class IS NULL OR risk_class IN ('low','medium','high')), created_by_scope_ref TEXT);
CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);
CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);
CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_uuid) WHERE parent_task_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_idx ON tasks(assignee_actor_uuid) WHERE assignee_actor_uuid IS NOT NULL;
CREATE INDEX tasks_kind_idx ON tasks(kind);
CREATE INDEX tasks_deleted_at_idx ON tasks(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);
CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);
CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;
CREATE INDEX tasks_cp_run_id_idx ON tasks(cp_run_id);
CREATE INDEX tasks_cp_session_id_idx ON tasks(cp_session_id);
CREATE TRIGGER tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq (id) VALUES (NULL);
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER tasks_au_etag
AFTER UPDATE ON tasks
FOR EACH ROW
BEGIN
  UPDATE tasks SET etag = OLD.etag + 1 WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER tasks_au_deleted_at
AFTER UPDATE OF state ON tasks
WHEN NEW.state = 'deleted' AND OLD.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
  WHERE rowid = NEW.rowid AND deleted_at IS NULL;
END;
CREATE TRIGGER tasks_au_undelete
AFTER UPDATE OF state ON tasks
WHEN OLD.state = 'deleted' AND NEW.state != 'deleted'
BEGIN
  UPDATE tasks SET deleted_at = NULL WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER tasks_au_state_consistency
AFTER UPDATE OF state ON tasks
BEGIN
  -- Set completed_at on transition to completed (if not already set)
  UPDATE tasks
     SET completed_at = COALESCE(NEW.completed_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'completed'
     AND NEW.completed_at IS NULL;

  -- Set archived_at on transition to archived (if not already set)
  UPDATE tasks
     SET archived_at = COALESCE(NEW.archived_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'archived'
     AND NEW.archived_at IS NULL;
END;
CREATE INDEX tasks_cp_work_item_id_idx ON tasks(cp_work_item_id);
CREATE TRIGGER tasks_bi_workflow_consistency
BEFORE INSERT ON tasks
WHEN NOT (
  (NEW.workflow_preset IS NULL AND NEW.preset_version IS NULL AND NEW.phase IS NULL) OR
  (NEW.workflow_preset IS NOT NULL AND NEW.preset_version IS NOT NULL AND NEW.phase IS NOT NULL)
)
BEGIN
  SELECT RAISE(ABORT, 'workflow_preset, preset_version, and phase must all be NULL or all be set');
END;
CREATE TRIGGER tasks_bu_workflow_consistency
BEFORE UPDATE OF workflow_preset, preset_version, phase ON tasks
WHEN NOT (
  (NEW.workflow_preset IS NULL AND NEW.preset_version IS NULL AND NEW.phase IS NULL) OR
  (NEW.workflow_preset IS NOT NULL AND NEW.preset_version IS NOT NULL AND NEW.phase IS NOT NULL)
)
BEGIN
  SELECT RAISE(ABORT, 'workflow_preset, preset_version, and phase must all be NULL or all be set');
END;
CREATE TABLE evidence_item_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_transition_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_role_assignments (
  uuid TEXT NOT NULL PRIMARY KEY
       DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('triager','owner','implementer','tester','reviewer','release_manager')),
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  assigned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(task_uuid, role)
);
CREATE INDEX task_role_assignments_task_idx ON task_role_assignments(task_uuid);
CREATE TABLE evidence_items (
  uuid TEXT NOT NULL PRIMARY KEY
       DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id TEXT UNIQUE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  content_hash TEXT,
  produced_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  produced_by_role TEXT NOT NULL,
  build_id TEXT,
  build_version TEXT,
  build_env TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);
CREATE INDEX evidence_items_task_produced_at_idx ON evidence_items(task_uuid, produced_at);
CREATE INDEX evidence_items_task_kind_idx ON evidence_items(task_uuid, kind);
CREATE TRIGGER evidence_items_ai_friendly
AFTER INSERT ON evidence_items
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO evidence_item_seq (id) VALUES (NULL);
  UPDATE evidence_items
     SET id = 'EV-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE task_transitions (
  uuid TEXT NOT NULL PRIMARY KEY
       DEFAULT (
          lower(
            hex(randomblob(4)) || '-' ||
            hex(randomblob(2)) || '-' ||
            '4' || substr(hex(randomblob(2)),2) || '-' ||
            substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
            hex(randomblob(6))
          )
        ),
  id TEXT UNIQUE,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  from_phase TEXT,
  to_phase TEXT NOT NULL,
  from_lifecycle_state TEXT,
  to_lifecycle_state TEXT,
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  actor_role TEXT NOT NULL,
  evidence_item_uuids TEXT,
  transitioned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);
CREATE INDEX task_transitions_task_transitioned_at_idx ON task_transitions(task_uuid, transitioned_at);
CREATE TRIGGER task_transitions_ai_friendly
AFTER INSERT ON task_transitions
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_transition_seq (id) VALUES (NULL);
  UPDATE task_transitions
     SET id = 'TR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE VIEW v_task_paths AS
SELECT t.uuid,
       t.id,
       t.slug,
       t.title,
       t.state,
       t.priority,
       t.kind,
       t.parent_task_uuid,
       t.assignee_actor_uuid,
       t.requested_by_project_id,
       t.assigned_project_id,
       t.acknowledged_at,
       t.resolution,
       t.cp_project_id,
       t.cp_work_item_id,
       t.cp_run_id,
       t.cp_session_id,
       t.sdk_session_id,
       t.run_status,
       t.workflow_preset,
       t.preset_version,
       t.phase,
       t.risk_class,
       t.start_at,
       t.due_at,
       t.labels,
       t.meta,
       t.etag,
       t.created_at,
       t.updated_at,
       t.completed_at,
       t.archived_at,
       t.deleted_at,
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid
/* v_task_paths(uuid,id,slug,title,state,priority,kind,parent_task_uuid,assignee_actor_uuid,requested_by_project_id,assigned_project_id,acknowledged_at,resolution,cp_project_id,cp_work_item_id,cp_run_id,cp_session_id,sdk_session_id,run_status,workflow_preset,preset_version,phase,risk_class,start_at,due_at,labels,meta,etag,created_at,updated_at,completed_at,archived_at,deleted_at,project_uuid,path) */;
CREATE TABLE workflow_templates (
  id TEXT NOT NULL,
  version TEXT NOT NULL,
  hash TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  installed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  installed_by TEXT,
  PRIMARY KEY (id, version)
);
CREATE TABLE workflow_instances (
  id TEXT PRIMARY KEY,
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  task_ref TEXT NOT NULL,
  project_id TEXT,
  template_id TEXT NOT NULL,
  template_version TEXT NOT NULL,
  template_hash TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('open', 'active', 'waiting', 'closed')),
  phase TEXT,
  outcome TEXT,
  revision INTEGER NOT NULL DEFAULT 0,
  context_hash TEXT NOT NULL,
  task_doc_etag TEXT NOT NULL,
  task_doc_hash TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  closed_at TEXT,
  FOREIGN KEY (template_id, template_version) REFERENCES workflow_templates(id, version)
);
CREATE UNIQUE INDEX workflow_instances_one_active_per_task
  ON workflow_instances(task_uuid)
  WHERE status != 'closed';
CREATE TABLE workflow_events (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  schema_version TEXT NOT NULL,
  type TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT,
  causation_id TEXT,
  correlation_id TEXT,
  observed_revision INTEGER,
  next_revision INTEGER NOT NULL,
  task_doc_etag TEXT,
  task_doc_hash TEXT,
  context_hash TEXT,
  idempotency_key TEXT,
  result TEXT,
  rejection_code TEXT,
  payload_json TEXT NOT NULL,
  prev_event_hash TEXT,
  event_hash TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(instance_id, seq),
  UNIQUE(instance_id, idempotency_key)
);
CREATE TABLE workflow_role_bindings (
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  actor TEXT NOT NULL,
  delivery_ref TEXT,
  lane TEXT,
  binding_mode TEXT NOT NULL CHECK (binding_mode IN ('required', 'optional', 'auto')),
  bound_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (instance_id, role, actor)
);
CREATE TABLE workflow_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_check_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_evidence_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_obligation_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_effect_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_runs (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  actor TEXT NOT NULL,
  delivery_ref TEXT,
  lane TEXT,
  external_run_ref TEXT,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  terminal_result TEXT
);
CREATE TABLE workflow_check_runs (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  transition_id TEXT NOT NULL,
  check_id TEXT NOT NULL,
  hook_id TEXT,
  input_hash TEXT NOT NULL,
  exit_code INTEGER,
  verdict TEXT NOT NULL CHECK (verdict IN ('pass', 'fail', 'block', 'error', 'inconclusive')),
  outcome TEXT,
  code TEXT,
  summary TEXT,
  facts_json TEXT,
  actor TEXT,
  role TEXT,
  run_id TEXT REFERENCES workflow_runs(id),
  started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT
);
CREATE TABLE workflow_evidence (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  summary TEXT,
  facts_json TEXT CHECK (
  facts_json IS NULL OR
  (json_valid(facts_json) AND json_type(facts_json) = 'object')
),
  data_json TEXT,
  source_json TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT REFERENCES workflow_runs(id),
  task_etag_at_production TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE TABLE workflow_check_run_evidence (
  check_run_id TEXT NOT NULL REFERENCES workflow_check_runs(id) ON DELETE CASCADE,
  evidence_id TEXT NOT NULL REFERENCES workflow_evidence(id) ON DELETE CASCADE,
  PRIMARY KEY (check_run_id, evidence_id)
);
CREATE TABLE workflow_obligations (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  owner_role TEXT,
  owner_actor TEXT,
  blocking INTEGER NOT NULL CHECK (blocking IN (0, 1)),
  status TEXT NOT NULL CHECK (status IN ('open', 'satisfied', 'waived', 'cancelled')),
  reason TEXT,
  satisfied_by_evidence_id TEXT REFERENCES workflow_evidence(id),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE TABLE workflow_effects (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'leased', 'delivered', 'failed', 'cancelled', 'unsupported')),
  idempotency_key TEXT NOT NULL UNIQUE,
  attempts INTEGER NOT NULL DEFAULT 0,
  leased_by TEXT,
  leased_until TEXT,
  delivered_at TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
