CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		);
CREATE TABLE actor_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE container_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE attachment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
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
CREATE TABLE section_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
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
  project_uuid TEXT NOT NULL REFERENCES "containers_old"(uuid) ON DELETE CASCADE,
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
CREATE TABLE evidence_item_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE task_transition_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_templates (
  id TEXT NOT NULL,
  version TEXT NOT NULL,
  hash TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  installed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  installed_by TEXT, hook_catalog_json TEXT CHECK (hook_catalog_json IS NULL OR json_valid(hook_catalog_json)), hook_catalog_hash TEXT, installed_by_principal_ref TEXT, discontinued_at TEXT, discontinued_by TEXT,
  PRIMARY KEY (id, version)
);
CREATE TABLE workflow_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_check_run_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_evidence_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_obligation_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_effect_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE workflow_event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE handoff_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE containers (
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
  created_by_actor_uuid TEXT,
  updated_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  updated_by_principal_ref TEXT
    CHECK (updated_by_principal_ref IS NULL OR (
      updated_by_principal_ref LIKE 'agent:%'
      AND length(substr(updated_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(updated_by_principal_ref, 7), ':') = 0
      AND substr(updated_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
  updated_by_scope_ref TEXT,
  kind TEXT NOT NULL DEFAULT 'directory',
  sort_index INTEGER NOT NULL DEFAULT 0,
  section_uuid TEXT REFERENCES sections(uuid) ON DELETE SET NULL,
  webhook_urls TEXT
, root TEXT, specification TEXT, labels TEXT, campaign_state TEXT
  CHECK (campaign_state IN ('draft','active','completed','cancelled')));
CREATE UNIQUE INDEX containers_unique_slug_in_parent
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE INDEX containers_section_idx ON containers(section_uuid) WHERE section_uuid IS NOT NULL;
CREATE UNIQUE INDEX containers_single_root ON containers(kind) WHERE kind = 'root';
CREATE INDEX containers_created_by_principal_idx ON containers(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
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
CREATE TRIGGER containers_kind_check_insert
BEFORE INSERT ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or root');
END;
CREATE TRIGGER containers_kind_check_update
BEFORE UPDATE OF kind ON containers
WHEN NEW.kind NOT IN ('project', 'directory', 'feature', 'area', 'root')
BEGIN
  SELECT RAISE(ABORT, 'Invalid container kind: must be project, directory, feature, area, or root');
END;
CREATE TRIGGER containers_parent_kind_consistency_insert
BEFORE INSERT ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant: root=>null parent, project=>parent=root, other=>non-root non-null parent');
END;
CREATE TRIGGER containers_parent_kind_consistency_update
BEFORE UPDATE OF kind, parent_uuid ON containers
WHEN NOT (
  (NEW.kind = 'root'    AND NEW.parent_uuid IS NULL) OR
  (NEW.kind = 'project' AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid = '00000000-0000-4000-8000-000000000001') OR
  (NEW.kind NOT IN ('root', 'project') AND NEW.parent_uuid IS NOT NULL
        AND NEW.parent_uuid <> '00000000-0000-4000-8000-000000000001')
)
BEGIN
  SELECT RAISE(ABORT, 'container parent/kind invariant violated');
END;
CREATE TRIGGER containers_root_immutable_update
BEFORE UPDATE OF parent_uuid, slug, kind, archived_at ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container is immutable (parent_uuid, slug, kind, archived_at)');
END;
CREATE TRIGGER containers_root_no_delete
BEFORE DELETE ON containers
WHEN OLD.kind = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root container cannot be deleted');
END;
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
  task_doc_etag TEXT NOT NULL,
  task_doc_hash TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  closed_at TEXT, suspension_id TEXT, suspension_reason TEXT, suspension_at TEXT, suspension_cause_ref TEXT,
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
  idempotency_key TEXT,
  result TEXT,
  rejection_code TEXT,
  payload_json TEXT NOT NULL,
  prev_event_hash TEXT,
  event_hash TEXT,
  request_hash TEXT,
  result_json TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')), principal_ref TEXT,
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
  bound_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')), principal_ref TEXT,
  PRIMARY KEY (instance_id, role, actor)
);
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
  terminal_result TEXT,
  idempotency_key TEXT,
  request_hash TEXT
, action TEXT, lease_owner TEXT, lease_token TEXT, lease_expires_at TEXT, heartbeat_at TEXT, principal_ref TEXT, semantic_action_key TEXT, attempt INTEGER NOT NULL DEFAULT 1, agent_ref TEXT, scope_ref TEXT, handler_contract TEXT, handler_id TEXT, handler_version TEXT, workspace_ref TEXT, source_run_id TEXT REFERENCES workflow_runs(id), source_evidence_id TEXT REFERENCES workflow_evidence(id), source_commit_sha TEXT, owner_generation INTEGER NOT NULL DEFAULT 0, source_identity TEXT, predecessor_run_id TEXT REFERENCES workflow_runs(id), superseded_by_run_id TEXT REFERENCES workflow_runs(id), side_effect_classes_json TEXT);
CREATE UNIQUE INDEX workflow_runs_instance_idempotency_key_unique
ON workflow_runs(instance_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX workflow_runs_external_run_ref_unique
ON workflow_runs(external_run_ref)
WHERE external_run_ref IS NOT NULL AND external_run_ref <> '';
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
, principal_ref TEXT);
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
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  obligee_role TEXT,
  obligee_actor TEXT,
  waive_role TEXT,
  waive_actor TEXT,
  no_self_waive INTEGER NOT NULL DEFAULT 1 CHECK (no_self_waive IN (0, 1)),
  resolved_by_actor TEXT,
  resolved_by_role TEXT,
  resolved_at TEXT
, owner_principal_ref TEXT, obligee_principal_ref TEXT, waive_principal_ref TEXT, resolved_by_principal_ref TEXT);
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
  lease_token TEXT,
  sequence INTEGER,
  semantic_key TEXT,
  receipt_json TEXT CHECK (receipt_json IS NULL OR json_valid(receipt_json)),
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE UNIQUE INDEX workflow_effects_instance_sequence_unique
ON workflow_effects(instance_id, sequence)
WHERE sequence IS NOT NULL;
CREATE UNIQUE INDEX workflow_effects_instance_semantic_key_unique
ON workflow_effects(instance_id, semantic_key)
WHERE semantic_key IS NOT NULL AND semantic_key <> '';
CREATE TABLE attachments (
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
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT
);
CREATE UNIQUE INDEX attachments_task_filename_unique
  ON attachments(task_uuid, filename);
CREATE UNIQUE INDEX attachments_relpath_unique
  ON attachments(relative_path);
CREATE INDEX attachments_task_idx ON attachments(task_uuid);
CREATE INDEX attachments_created_by_principal_idx ON attachments(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
CREATE TRIGGER attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq (id) VALUES (NULL);
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE task_relations (
  from_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  to_task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  meta TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
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
CREATE INDEX task_relations_created_by_principal_idx ON task_relations(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
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
  actor_uuid TEXT,
  principal_ref TEXT
    CHECK (principal_ref IS NULL OR (
      principal_ref LIKE 'agent:%'
      AND length(substr(principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(principal_ref, 7), ':') = 0
      AND substr(principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  assigned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(task_uuid, role)
);
CREATE INDEX task_role_assignments_task_idx ON task_role_assignments(task_uuid);
CREATE INDEX task_role_assignments_principal_idx ON task_role_assignments(principal_ref)
  WHERE principal_ref IS NOT NULL;
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
  produced_by_actor_uuid TEXT,
  produced_by_principal_ref TEXT
    CHECK (produced_by_principal_ref IS NULL OR (
      produced_by_principal_ref LIKE 'agent:%'
      AND length(substr(produced_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(produced_by_principal_ref, 7), ':') = 0
      AND substr(produced_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  produced_by_role TEXT NOT NULL,
  build_id TEXT,
  build_version TEXT,
  build_env TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);
CREATE INDEX evidence_items_task_produced_at_idx ON evidence_items(task_uuid, produced_at);
CREATE INDEX evidence_items_task_kind_idx ON evidence_items(task_uuid, kind);
CREATE INDEX evidence_items_produced_by_principal_idx ON evidence_items(produced_by_principal_ref)
  WHERE produced_by_principal_ref IS NOT NULL;
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
  actor_uuid TEXT,
  principal_ref TEXT
    CHECK (principal_ref IS NULL OR (
      principal_ref LIKE 'agent:%'
      AND length(substr(principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(principal_ref, 7), ':') = 0
      AND substr(principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  actor_role TEXT NOT NULL,
  evidence_item_uuids TEXT,
  transitioned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta TEXT
);
CREATE INDEX task_transitions_task_transitioned_at_idx ON task_transitions(task_uuid, transitioned_at);
CREATE INDEX task_transitions_principal_idx ON task_transitions(principal_ref)
  WHERE principal_ref IS NOT NULL;
CREATE TRIGGER task_transitions_ai_friendly
AFTER INSERT ON task_transitions
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_transition_seq (id) VALUES (NULL);
  UPDATE task_transitions
     SET id = 'TR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE handoffs (
  uuid TEXT PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  scope_ref TEXT NOT NULL,
  scope_kind TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  agent_actor_uuid TEXT,
  agent_principal_ref TEXT,
  project_container_uuid TEXT,
  created_by_agent_id TEXT NOT NULL,
  created_by_actor_uuid TEXT,
  created_by_principal_ref TEXT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','acknowledged')),
  idempotency_key TEXT,
  acknowledged_at TEXT,
  acknowledged_by_agent_id TEXT,
  acknowledged_by_actor_uuid TEXT,
  acknowledged_by_principal_ref TEXT,
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
CREATE INDEX handoffs_agent_principal_idx ON handoffs(agent_principal_ref)
  WHERE agent_principal_ref IS NOT NULL;
CREATE INDEX handoffs_created_by_principal_idx ON handoffs(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
CREATE VIEW v_container_paths AS
WITH RECURSIVE container_tree(uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, path, level) AS (
  SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, slug AS path, 0 AS level
    FROM containers
   WHERE parent_uuid = '00000000-0000-4000-8000-000000000001'
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
       t.assignee_principal_ref,
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
       t.created_by_principal_ref,
       t.updated_by_principal_ref,
       t.deleted_by_principal_ref,
       t.created_by_scope_ref,
       t.updated_by_scope_ref,
       t.deleted_by_scope_ref,
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid
/* v_task_paths(uuid,id,slug,title,state,priority,kind,parent_task_uuid,assignee_actor_uuid,assignee_principal_ref,requested_by_project_id,assigned_project_id,acknowledged_at,resolution,cp_project_id,cp_work_item_id,cp_run_id,cp_session_id,sdk_session_id,run_status,workflow_preset,preset_version,phase,risk_class,start_at,due_at,labels,meta,etag,created_at,updated_at,completed_at,archived_at,deleted_at,created_by_principal_ref,updated_by_principal_ref,deleted_by_principal_ref,created_by_scope_ref,updated_by_scope_ref,deleted_by_scope_ref,project_uuid,path) */;
CREATE TABLE wrkq_rpc_idempotency (
    namespace       TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    result_json     TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY (namespace, idempotency_key)
);
CREATE TABLE IF NOT EXISTS "workflow_evidence" (
  id TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  summary TEXT,
  data_json TEXT,
  source_json TEXT NOT NULL,
  actor TEXT,
  role TEXT,
  run_id TEXT,
  task_etag_at_production TEXT,
  produced_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  facts_json TEXT CHECK (
    facts_json IS NULL OR
    (json_valid(facts_json) AND json_type(facts_json) = 'object')
  ),
  task_hash_at_production TEXT
, principal_ref TEXT);
CREATE TABLE workflow_evidence_idempotency (
  instance_id     TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  idempotency_key TEXT NOT NULL,
  request_hash    TEXT NOT NULL,
  result_json     TEXT NOT NULL,
  evidence_id     TEXT NOT NULL REFERENCES workflow_evidence(id) ON DELETE CASCADE,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (instance_id, idempotency_key)
);
CREATE INDEX workflow_events_type_created_id_idx
  ON workflow_events(type, created_at, id);
CREATE INDEX workflow_events_transition_from_to_created_idx
  ON workflow_events(
    type,
    json_extract(payload_json, '$.from.phase'),
    json_extract(payload_json, '$.to.phase'),
    created_at,
    id
  )
  WHERE type = 'workflow.transitioned';
CREATE INDEX workflow_role_bindings_role_instance_idx
  ON workflow_role_bindings(role, instance_id);
CREATE INDEX workflow_runs_instance_action_idx
  ON workflow_runs(instance_id, action)
  WHERE action IS NOT NULL;
CREATE TABLE tasks (
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
  state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task','subtask','spike','bug','chore')),
  parent_task_uuid TEXT REFERENCES tasks(uuid),
  assignee_actor_uuid TEXT,
  assignee_principal_ref TEXT
    CHECK (assignee_principal_ref IS NULL OR (
      assignee_principal_ref LIKE 'agent:%'
      AND length(substr(assignee_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(assignee_principal_ref, 7), ':') = 0
      AND substr(assignee_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
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
  deleted_by_principal_ref TEXT
    CHECK (deleted_by_principal_ref IS NULL OR (
      deleted_by_principal_ref LIKE 'agent:%'
      AND length(substr(deleted_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(deleted_by_principal_ref, 7), ':') = 0
      AND substr(deleted_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_actor_uuid TEXT,
  updated_by_actor_uuid TEXT,
  created_by_principal_ref TEXT
    CHECK (created_by_principal_ref IS NULL OR (
      created_by_principal_ref LIKE 'agent:%'
      AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(created_by_principal_ref, 7), ':') = 0
      AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  updated_by_principal_ref TEXT
    CHECK (updated_by_principal_ref IS NULL OR (
      updated_by_principal_ref LIKE 'agent:%'
      AND length(substr(updated_by_principal_ref, 7)) BETWEEN 1 AND 64
      AND instr(substr(updated_by_principal_ref, 7), ':') = 0
      AND substr(updated_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
    )),
  created_by_scope_ref TEXT,
  updated_by_scope_ref TEXT,
  deleted_by_scope_ref TEXT,
  cp_work_item_id TEXT,
  specification TEXT NOT NULL DEFAULT '',
  workflow_preset TEXT,
  preset_version INTEGER,
  phase TEXT,
  risk_class TEXT CHECK (risk_class IS NULL OR risk_class IN ('low','medium','high')), claimed_by_principal_ref TEXT
  CHECK (claimed_by_principal_ref IS NULL OR (
    claimed_by_principal_ref LIKE 'agent:%'
    AND length(substr(claimed_by_principal_ref, 7)) BETWEEN 1 AND 64
    AND instr(substr(claimed_by_principal_ref, 7), ':') = 0
    AND substr(claimed_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
  )), claimed_scope_ref TEXT, claimed_node TEXT, claimed_at TEXT, claim_generation INTEGER NOT NULL DEFAULT 0
  CHECK (claim_generation >= 0), claim_token_hash TEXT, outcome TEXT, campaign_uuid TEXT
  REFERENCES containers(uuid) ON DELETE RESTRICT,
  CHECK (
    (workflow_preset IS NULL AND preset_version IS NULL AND phase IS NULL) OR
    (workflow_preset IS NOT NULL AND preset_version IS NOT NULL AND phase IS NOT NULL)
  )
);
CREATE UNIQUE INDEX tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);
CREATE INDEX tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX tasks_slug_idx      ON tasks(slug);
CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_uuid) WHERE parent_task_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_idx ON tasks(assignee_actor_uuid) WHERE assignee_actor_uuid IS NOT NULL;
CREATE INDEX tasks_assignee_principal_idx ON tasks(assignee_principal_ref) WHERE assignee_principal_ref IS NOT NULL;
CREATE INDEX tasks_kind_idx ON tasks(kind);
CREATE INDEX tasks_deleted_at_idx ON tasks(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX tasks_requested_by_idx ON tasks(requested_by_project_id);
CREATE INDEX tasks_assigned_idx ON tasks(assigned_project_id);
CREATE INDEX tasks_ack_pending_idx ON tasks(requested_by_project_id, state, acknowledged_at)
  WHERE acknowledged_at IS NULL;
CREATE INDEX tasks_cp_run_id_idx ON tasks(cp_run_id);
CREATE INDEX tasks_cp_session_id_idx ON tasks(cp_session_id);
CREATE INDEX tasks_cp_work_item_id_idx ON tasks(cp_work_item_id);
CREATE INDEX tasks_created_by_principal_idx ON tasks(created_by_principal_ref)
  WHERE created_by_principal_ref IS NOT NULL;
CREATE INDEX tasks_updated_by_principal_idx ON tasks(updated_by_principal_ref)
  WHERE updated_by_principal_ref IS NOT NULL;
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
  UPDATE tasks
     SET deleted_at = NULL,
         deleted_by_principal_ref = NULL,
         deleted_by_scope_ref = NULL
   WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;
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
CREATE TRIGGER tasks_not_under_root_insert
BEFORE INSERT ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be created directly under the root container');
END;
CREATE TRIGGER tasks_not_under_root_update
BEFORE UPDATE OF project_uuid ON tasks
WHEN NEW.project_uuid = '00000000-0000-4000-8000-000000000001'
BEGIN
  SELECT RAISE(ABORT, 'tasks cannot be moved directly under the root container');
END;
CREATE TRIGGER tasks_au_state_consistency
AFTER UPDATE OF state ON tasks
BEGIN
  UPDATE tasks
     SET completed_at = COALESCE(NEW.completed_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'completed'
     AND NEW.completed_at IS NULL;

  UPDATE tasks
     SET completed_at = NULL
   WHERE rowid = NEW.rowid
     AND OLD.state = 'completed'
     AND NEW.state != 'completed';

  UPDATE tasks
     SET archived_at = COALESCE(NEW.archived_at, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
   WHERE rowid = NEW.rowid
     AND NEW.state = 'archived'
     AND NEW.archived_at IS NULL;
END;
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
CREATE UNIQUE INDEX workflow_role_bindings_principal_unique
  ON workflow_role_bindings(instance_id, role, principal_ref)
  WHERE principal_ref IS NOT NULL;
CREATE INDEX workflow_runs_principal_idx
  ON workflow_runs(instance_id, principal_ref);
CREATE UNIQUE INDEX workflow_runs_active_semantic_key_unique
  ON workflow_runs(instance_id, semantic_action_key)
  WHERE semantic_action_key IS NOT NULL
    AND status = 'active';
CREATE INDEX workflow_runs_source_binding_idx
  ON workflow_runs(instance_id, source_run_id, source_evidence_id)
  WHERE source_run_id IS NOT NULL;
CREATE TABLE ledger_entry (
  uuid TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts TEXT NOT NULL,
  kind TEXT NOT NULL,
  about_principal_ref TEXT NOT NULL,
  written_by TEXT NOT NULL,
  body_json TEXT NOT NULL,
  UNIQUE(instance_id, seq)
);
CREATE INDEX ledger_entry_task_replay_idx
  ON ledger_entry(task_id, instance_id, seq);
CREATE INDEX ledger_entry_projection_idx
  ON ledger_entry(about_principal_ref, kind, ts, instance_id, seq);
CREATE TRIGGER ledger_entry_no_update
BEFORE UPDATE ON ledger_entry
BEGIN
  SELECT RAISE(ABORT, 'ledger entries are append-only');
END;
CREATE TRIGGER ledger_entry_no_delete
BEFORE DELETE ON ledger_entry
BEGIN
  SELECT RAISE(ABORT, 'ledger entries are append-only');
END;
CREATE INDEX workflow_runs_predecessor_idx
  ON workflow_runs(predecessor_run_id)
  WHERE predecessor_run_id IS NOT NULL;
CREATE TABLE workflow_suspension_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE INDEX tasks_claimed_by_idx ON tasks(claimed_by_principal_ref)
  WHERE claimed_by_principal_ref IS NOT NULL;
CREATE INDEX tasks_claimed_node_idx ON tasks(claimed_node)
  WHERE claimed_node IS NOT NULL;
CREATE TRIGGER tasks_claim_tuple_insert
BEFORE INSERT ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
END;
CREATE TRIGGER tasks_claim_tuple_update
BEFORE UPDATE OF claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_token_hash ON tasks
WHEN (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_scope_ref IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_node IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claimed_at IS NULL)
  OR (NEW.claimed_by_principal_ref IS NULL) != (NEW.claim_token_hash IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'task claim tuple must be wholly present or absent');
END;
CREATE TRIGGER tasks_claim_generation_no_regression
BEFORE UPDATE OF claim_generation ON tasks
WHEN NEW.claim_generation < OLD.claim_generation
BEGIN
  SELECT RAISE(ABORT, 'task claim generation cannot regress');
END;
CREATE INDEX tasks_campaign_idx ON tasks(campaign_uuid);
CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
    container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
    kind TEXT CHECK (kind IS NULL OR kind IN ('blocker','decision','postmortem','digest')),
    actor_uuid TEXT,
    created_by_principal_ref TEXT
      CHECK (created_by_principal_ref IS NULL OR (
        created_by_principal_ref LIKE 'agent:%'
        AND length(substr(created_by_principal_ref, 7)) BETWEEN 1 AND 64
        AND instr(substr(created_by_principal_ref, 7), ':') = 0
        AND substr(created_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
      )),
    created_by_scope_ref TEXT,
    body TEXT NOT NULL,
    meta TEXT,
    etag INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT,
    deleted_at TEXT,
    deleted_by_actor_uuid TEXT,
    deleted_by_principal_ref TEXT
      CHECK (deleted_by_principal_ref IS NULL OR (
        deleted_by_principal_ref LIKE 'agent:%'
        AND length(substr(deleted_by_principal_ref, 7)) BETWEEN 1 AND 64
        AND instr(substr(deleted_by_principal_ref, 7), ':') = 0
        AND substr(deleted_by_principal_ref, 7) NOT GLOB '*[^A-Za-z0-9._-]*'
      )),
    deleted_by_scope_ref TEXT,

    CHECK (
      (task_uuid IS NOT NULL AND container_uuid IS NULL) OR
      (task_uuid IS NULL AND container_uuid IS NOT NULL)
    ),
    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);
CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at, id);
CREATE INDEX idx_comments_container_created ON comments(container_uuid, created_at, id);
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at) WHERE actor_uuid IS NOT NULL;
CREATE INDEX idx_comments_principal_created ON comments(created_by_principal_ref, created_at)
  WHERE created_by_principal_ref IS NOT NULL;
CREATE TRIGGER comments_ai_touch_task
AFTER INSERT ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;
CREATE TRIGGER comments_au_touch_task
AFTER UPDATE ON comments
WHEN NEW.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.task_uuid;
END;
CREATE TRIGGER comments_ad_touch_task
AFTER DELETE ON comments
WHEN OLD.task_uuid IS NOT NULL
BEGIN
  UPDATE tasks
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.task_uuid;
END;
CREATE TRIGGER comments_ai_touch_container
AFTER INSERT ON comments
WHEN NEW.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.container_uuid;
END;
CREATE TRIGGER comments_au_touch_container
AFTER UPDATE ON comments
WHEN NEW.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(NEW.deleted_by_actor_uuid, NEW.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(NEW.deleted_by_principal_ref, NEW.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(NEW.deleted_by_scope_ref, NEW.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = NEW.container_uuid;
END;
CREATE TRIGGER comments_ad_touch_container
AFTER DELETE ON comments
WHEN OLD.container_uuid IS NOT NULL
BEGIN
  UPDATE containers
     SET updated_by_actor_uuid = COALESCE(OLD.actor_uuid, updated_by_actor_uuid),
         updated_by_principal_ref = COALESCE(OLD.created_by_principal_ref, updated_by_principal_ref),
         updated_by_scope_ref = COALESCE(OLD.created_by_scope_ref, updated_by_scope_ref)
   WHERE uuid = OLD.container_uuid;
END;
CREATE TABLE promise_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);
CREATE TABLE promises (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  id TEXT UNIQUE,

  owner_principal_ref TEXT NOT NULL,

  subject TEXT NOT NULL CHECK (length(trim(subject)) > 0),
  review_question TEXT,

  subject_task_uuid TEXT
    REFERENCES tasks(uuid) ON DELETE SET NULL,
  subject_container_uuid TEXT
    REFERENCES containers(uuid) ON DELETE SET NULL,

  review_at TEXT NOT NULL
    CHECK (review_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]Z'),

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'resolved', 'abandoned')),
  closed_at TEXT,

  last_reviewed_at TEXT,
  last_review_note TEXT,

  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),

  created_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  updated_by_principal_ref TEXT NOT NULL,
  updated_by_scope_ref TEXT,

  CHECK (
    subject_task_uuid IS NULL
    OR subject_container_uuid IS NULL
  ),

  CHECK (
    (state = 'open' AND closed_at IS NULL)
    OR
    (state IN ('resolved', 'abandoned') AND closed_at IS NOT NULL)
  )
);
CREATE INDEX promises_owner_ready_idx
  ON promises(owner_principal_ref, review_at)
  WHERE state = 'open';
CREATE INDEX promises_task_idx
  ON promises(subject_task_uuid)
  WHERE subject_task_uuid IS NOT NULL;
CREATE INDEX promises_container_idx
  ON promises(subject_container_uuid)
  WHERE subject_container_uuid IS NOT NULL;
CREATE TRIGGER promises_ai_friendly
AFTER INSERT ON promises
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO promise_seq (id) VALUES (NULL);
  UPDATE promises
     SET id = 'PR-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE room_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);
CREATE TABLE envelope_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);
CREATE TABLE rooms (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  id TEXT UNIQUE,

  kind TEXT NOT NULL CHECK (kind IN ('campaign', 'task', 'project', 'adhoc')),

  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
  container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,

  subject TEXT,

  state TEXT NOT NULL DEFAULT 'open'
    CHECK (state IN ('open', 'closed', 'archived')),
  closed_at TEXT,
  -- An explicit reopen overrides DERIVED closure (a task room whose task went
  -- terminal, a campaign room whose campaign closed) until an explicit close.
  reopened_at TEXT,

  last_activity_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  opened_by_principal_ref TEXT NOT NULL,
  opened_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),

  created_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  updated_by_principal_ref TEXT NOT NULL,
  updated_by_scope_ref TEXT,

  -- Exactly one work anchor for derived kinds; none for ad-hoc.
  CHECK (
    (kind = 'task'     AND task_uuid IS NOT NULL AND container_uuid IS NULL)
    OR
    (kind IN ('campaign', 'project')
                       AND container_uuid IS NOT NULL AND task_uuid IS NULL)
    OR
    (kind = 'adhoc'    AND task_uuid IS NULL AND container_uuid IS NULL)
  ),

  CHECK (kind = 'adhoc' OR subject IS NULL),

  CHECK (
    (state = 'open' AND closed_at IS NULL)
    OR
    (state IN ('closed', 'archived') AND closed_at IS NOT NULL)
  )
);
CREATE UNIQUE INDEX rooms_task_idx
  ON rooms(task_uuid) WHERE task_uuid IS NOT NULL;
CREATE UNIQUE INDEX rooms_container_idx
  ON rooms(container_uuid) WHERE container_uuid IS NOT NULL;
CREATE INDEX rooms_adhoc_idle_idx
  ON rooms(last_activity_at) WHERE kind = 'adhoc' AND state = 'open';
CREATE TRIGGER rooms_ai_friendly
AFTER INSERT ON rooms
WHEN NEW.kind = 'adhoc' AND (NEW.id IS NULL OR NEW.id = '')
BEGIN
  INSERT INTO room_seq (id) VALUES (NULL);
  UPDATE rooms
     SET id = 'R-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE envelopes (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  id TEXT UNIQUE,

  room_uuid TEXT NOT NULL REFERENCES rooms(uuid) ON DELETE CASCADE,
  -- Shared by the envelopes one `say` fanned out to; equals the envelope's own
  -- id for a single addressee. Recipients never see the group.
  group_id TEXT,

  from_principal_ref TEXT NOT NULL,
  from_scope_ref TEXT,

  -- Addressee: a scope handle (agent@project:task) for scoped members, or NULL
  -- with to_principal_ref set for scope-less principals (humans). Both NULL for
  -- obligation 'none' (a log entry).
  to_scope_ref TEXT,
  to_principal_ref TEXT,

  obligation TEXT NOT NULL
    CHECK (obligation IN ('reply_required', 'fyi', 'none')),

  body TEXT NOT NULL CHECK (length(trim(body)) > 0),

  -- Set when the say routed via a task, even into a campaign room.
  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE SET NULL,

  state TEXT NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'presented', 'acked', 'deferred', 'dead')),
  round_count INTEGER NOT NULL DEFAULT 0 CHECK (round_count >= 0),
  retry_at TEXT,
  defer_reason TEXT,
  terminal_actor TEXT,
  terminal_at TEXT,

  -- Delivery intent HRC actuates; wrkq stores it and does not interpret it.
  materialization_intent TEXT,
  respond_to_principal_ref TEXT,

  -- Promise backing `defer --retry-after`.
  retry_promise_uuid TEXT REFERENCES promises(uuid) ON DELETE SET NULL,

  idempotency_key TEXT,
  meta TEXT,
  etag INTEGER NOT NULL DEFAULT 1 CHECK (etag >= 1),

  created_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),

  created_by_principal_ref TEXT NOT NULL,
  created_by_scope_ref TEXT,
  updated_by_principal_ref TEXT NOT NULL,
  updated_by_scope_ref TEXT,

  -- An addressee is required by every firing obligation and forbidden without.
  CHECK (
    (obligation = 'none'
       AND to_scope_ref IS NULL AND to_principal_ref IS NULL)
    OR
    (obligation IN ('reply_required', 'fyi')
       AND to_principal_ref IS NOT NULL)
  ),

  CHECK (
    (state = 'deferred' AND defer_reason IS NOT NULL)
    OR state <> 'deferred'
  )
);
CREATE INDEX envelopes_room_idx ON envelopes(room_uuid, id);
CREATE INDEX envelopes_group_idx ON envelopes(group_id) WHERE group_id IS NOT NULL;
CREATE INDEX envelopes_task_idx ON envelopes(task_uuid) WHERE task_uuid IS NOT NULL;
CREATE INDEX envelopes_obligation_idx
  ON envelopes(to_scope_ref, state)
  WHERE obligation = 'reply_required';
CREATE INDEX envelopes_retry_idx
  ON envelopes(retry_at) WHERE state = 'deferred' AND retry_at IS NOT NULL;
CREATE UNIQUE INDEX envelopes_idempotency_idx
  ON envelopes(idempotency_key, COALESCE(to_principal_ref, ''))
  WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER envelopes_ai_friendly
AFTER INSERT ON envelopes
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO envelope_seq (id) VALUES (NULL);
  UPDATE envelopes
     SET id = 'EN-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;
CREATE TABLE room_members (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  room_uuid TEXT NOT NULL REFERENCES rooms(uuid) ON DELETE CASCADE,

  -- The member address: a scope handle, or the bare principal when scope-less.
  member_ref TEXT NOT NULL,
  member_principal_ref TEXT NOT NULL,
  scoped INTEGER NOT NULL DEFAULT 1 CHECK (scoped IN (0, 1)),

  source TEXT NOT NULL CHECK (source IN ('spoke', 'addressed', 'joined')),

  joined_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  left_at TEXT,

  UNIQUE (room_uuid, member_ref)
);
CREATE INDEX room_members_ref_idx ON room_members(member_ref) WHERE left_at IS NULL;
CREATE INDEX room_members_active_idx ON room_members(room_uuid) WHERE left_at IS NULL;
CREATE TABLE envelope_presentations (
  uuid TEXT PRIMARY KEY
       DEFAULT (
         lower(
           hex(randomblob(4)) || '-' ||
           hex(randomblob(2)) || '-' ||
           '4' || substr(hex(randomblob(2)),2) || '-' ||
           substr('89ab', abs(random()) % 4 + 1, 1) ||
             substr(hex(randomblob(2)),2) || '-' ||
           hex(randomblob(6))
         )
       ),
  envelope_uuid TEXT NOT NULL REFERENCES envelopes(uuid) ON DELETE CASCADE,
  -- Denormalized so attendance per (room, member) is one index seek.
  room_uuid TEXT NOT NULL REFERENCES rooms(uuid) ON DELETE CASCADE,
  member_ref TEXT NOT NULL,

  node TEXT,
  runtime_id TEXT,
  host_session_id TEXT,
  generation TEXT,
  run_id TEXT,
  drive_attempt_id TEXT,

  presented_at TEXT NOT NULL
    DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  presented_by_principal_ref TEXT NOT NULL
, delivery_outcome TEXT);
CREATE INDEX envelope_presentations_envelope_idx
  ON envelope_presentations(envelope_uuid, presented_at);
CREATE INDEX envelope_presentations_attendance_idx
  ON envelope_presentations(room_uuid, member_ref, presented_at DESC);
CREATE UNIQUE INDEX envelope_presentations_attempt_idx
  ON envelope_presentations(envelope_uuid, drive_attempt_id)
  WHERE drive_attempt_id IS NOT NULL;
CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment','handoff','promise','room','envelope')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT,
  principal_ref TEXT,
  scope_ref     TEXT
);
CREATE INDEX event_log_resource_idx
  ON event_log(resource_type, resource_uuid, id DESC);
CREATE INDEX event_log_principal_idx
  ON event_log(principal_ref, id DESC)
  WHERE principal_ref IS NOT NULL;
CREATE INDEX event_log_scope_idx
  ON event_log(scope_ref, id DESC)
  WHERE scope_ref IS NOT NULL;
