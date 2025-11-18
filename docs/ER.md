## Data model (ER overview)

```
Actor (uuid, id=A-xxxxx, slug, role, meta, created_at, updated_at)
  ├─< created_by_actor_uuid
  └─< updated_by_actor_uuid

Container (uuid, id=P-xxxxx, slug, title, parent_uuid?, etag, created/updated/archived_at,
           created_by_actor_uuid, updated_by_actor_uuid)
  ├─ parent_uuid → Container.uuid   (hierarchy: project/subproject/…)
  └─ unique slug among siblings (root vs child handled with partial unique indexes)

Task (uuid, id=T-xxxxx, slug, title, project_uuid, state, priority, start_at, due_at,
      labels (JSON), body, etag, created/updated/completed/archived_at,
      created_by_actor_uuid, updated_by_actor_uuid)
  ├─ project_uuid → Container.uuid
  └─ unique (project_uuid, slug)

Comment (uuid, id=C-xxxxx, task_uuid, actor_uuid, body, created_at)
  ├─ task_uuid → Task.uuid
  └─ actor_uuid → Actor.uuid

Attachment (uuid, id=ATT-xxxxx, task_uuid, filename, relative_path, mime_type, size_bytes,
           checksum?, created_at, created_by_actor_uuid)
  ├─ task_uuid → Task.uuid
  ├─ unique (task_uuid, filename)
  └─ unique relative_path (under attach_dir/tasks/<task_uuid>/...)

EventLog (id AUTOINCR, timestamp, actor_uuid?, resource_type, resource_uuid?, event_type,
          etag?, payload JSON)
  ├─ actor_uuid → Actor.uuid (nullable for system)
  └─ append‑only (trigger prevents UPDATE/DELETE)

Views
  - v_container_paths: canonical container path via recursive CTE
  - v_task_paths: container path + task slug
```

**Concurrency & attribution**
- `etag INTEGER` on containers/tasks; increment in app layer (safe for `--if-match` compares).
- All mutating commands must set `created_by_actor_uuid`/`updated_by_actor_uuid`.
- WAL + `busy_timeout` for cooperating CLIs/agents.

**Slug rules (enforced)**
- lower‑case, `[a-z0-9][a-z0-9-]*`, ≤255 chars; unique among siblings.

---

## `db/migrations/0001_init.sql`

> Drop this file under `./db/migrations/0001_init.sql`. Your migrator can run it inside a single transaction. It’s idempotent on re‑apply.

```sql
-- 0001_init.sql — todo CLI (Go + SQLite)
-- Canonical schema. Apply inside one transaction.

-- -----------------------------
-- Pragmas (apply-time)
-- -----------------------------
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA recursive_triggers = OFF;  -- prevents UPDATE-in-trigger from re-firing

-- -----------------------------
-- Sequences for friendly IDs (monotonic, never reused)
-- -----------------------------
CREATE TABLE IF NOT EXISTS actor_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS container_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS task_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS attachment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS comment_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS event_seq(id INTEGER PRIMARY KEY AUTOINCREMENT);

-- Helper: v4 UUID expression (used inline as DEFAULT)
-- lower(
--   hex(randomblob(4)) || '-' ||
--   hex(randomblob(2)) || '-' ||
--   '4' || substr(hex(randomblob(2)),2) || '-' ||
--   substr('89ab', abs(random()) % 4 + 1, 1) || substr(hex(randomblob(2)),2) || '-' ||
--   hex(randomblob(6))
-- )

-- -----------------------------
-- Actors
-- -----------------------------
CREATE TABLE IF NOT EXISTS actors (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. A-00001 (set by trigger)
  slug TEXT NOT NULL UNIQUE
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  display_name TEXT,
  role TEXT NOT NULL CHECK (role IN ('human','agent','system')),
  meta TEXT,  -- JSON (app-level validated)
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TRIGGER IF NOT EXISTS actors_ai_friendly
AFTER INSERT ON actors
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO actor_seq DEFAULT VALUES;
  UPDATE actors
     SET id = 'A-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER IF NOT EXISTS actors_au_touch
AFTER UPDATE ON actors
BEGIN
  UPDATE actors SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

-- -----------------------------
-- Containers (projects/subprojects)
-- -----------------------------
CREATE TABLE IF NOT EXISTS containers (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. P-00001 (set by trigger)
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT,                           -- optional; app may default to slug
  parent_uuid TEXT REFERENCES containers(uuid) ON DELETE RESTRICT,
  etag INTEGER NOT NULL DEFAULT 1,      -- app increments for optimistic concurrency
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  archived_at TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS containers_ai_friendly
AFTER INSERT ON containers
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO container_seq DEFAULT VALUES;
  UPDATE containers
     SET id = 'P-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Sibling-uniqueness (root vs child handled separately)
CREATE UNIQUE INDEX IF NOT EXISTS containers_unique_root_slug
  ON containers(slug) WHERE parent_uuid IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS containers_unique_child_slug
  ON containers(parent_uuid, slug) WHERE parent_uuid IS NOT NULL;

CREATE INDEX IF NOT EXISTS containers_parent_idx ON containers(parent_uuid);
CREATE INDEX IF NOT EXISTS containers_slug_idx   ON containers(slug);

CREATE TRIGGER IF NOT EXISTS containers_au_touch
AFTER UPDATE ON containers
BEGIN
  UPDATE containers SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

-- -----------------------------
-- Tasks
-- -----------------------------
CREATE TABLE IF NOT EXISTS tasks (
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
  id   TEXT NOT NULL UNIQUE,   -- friendly, e.g. T-00001 (set by trigger)
  slug TEXT NOT NULL
       CHECK (slug = lower(slug) AND slug GLOB '[a-z0-9][a-z0-9-]*' AND length(slug) <= 255),
  title TEXT NOT NULL,
  project_uuid TEXT NOT NULL REFERENCES containers(uuid) ON DELETE RESTRICT,
  state TEXT NOT NULL CHECK (state IN ('open','completed','archived')),
  priority INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 4),
  start_at TEXT,
  due_at   TEXT,
  labels   TEXT,        -- JSON array; app validates
  body     TEXT NOT NULL DEFAULT '',
  etag     INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  completed_at TEXT,
  archived_at  TEXT,
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS tasks_ai_friendly
AFTER INSERT ON tasks
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO task_seq DEFAULT VALUES;
  UPDATE tasks
     SET id = 'T-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

-- Uniqueness within container
CREATE UNIQUE INDEX IF NOT EXISTS tasks_unique_slug_in_container
  ON tasks(project_uuid, slug);

CREATE INDEX IF NOT EXISTS tasks_state_due_idx ON tasks(state, due_at);
CREATE INDEX IF NOT EXISTS tasks_updated_idx   ON tasks(updated_at);
CREATE INDEX IF NOT EXISTS tasks_project_idx   ON tasks(project_uuid);
CREATE INDEX IF NOT EXISTS tasks_slug_idx      ON tasks(slug);

-- Touch + state consistency
CREATE TRIGGER IF NOT EXISTS tasks_au_touch
AFTER UPDATE ON tasks
BEGIN
  UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
   WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER IF NOT EXISTS tasks_au_state_consistency
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

-- -----------------------------
-- Comments
-- -----------------------------
CREATE TABLE IF NOT EXISTS comments (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. C-00001 (set by trigger)
  task_uuid  TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TRIGGER IF NOT EXISTS comments_ai_friendly
AFTER INSERT ON comments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO comment_seq DEFAULT VALUES;
  UPDATE comments
     SET id = 'C-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE INDEX IF NOT EXISTS comments_task_idx  ON comments(task_uuid);
CREATE INDEX IF NOT EXISTS comments_actor_idx ON comments(actor_uuid);

-- -----------------------------
-- Attachments (metadata only; bytes live under attach_dir)
-- -----------------------------
CREATE TABLE IF NOT EXISTS attachments (
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
  id   TEXT NOT NULL UNIQUE,  -- friendly, e.g. ATT-00001 (set by trigger)
  task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
  filename  TEXT NOT NULL,
  relative_path TEXT NOT NULL,  -- relative to attach_dir
  mime_type TEXT,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  checksum  TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid) ON DELETE RESTRICT
);

CREATE TRIGGER IF NOT EXISTS attachments_ai_friendly
AFTER INSERT ON attachments
WHEN NEW.id IS NULL OR NEW.id = ''
BEGIN
  INSERT INTO attachment_seq DEFAULT VALUES;
  UPDATE attachments
     SET id = 'ATT-' || printf('%05d', last_insert_rowid())
   WHERE rowid = NEW.rowid;
END;

CREATE UNIQUE INDEX IF NOT EXISTS attachments_task_filename_unique
  ON attachments(task_uuid, filename);

CREATE UNIQUE INDEX IF NOT EXISTS attachments_relpath_unique
  ON attachments(relative_path);

CREATE INDEX IF NOT EXISTS attachments_task_idx ON attachments(task_uuid);

-- -----------------------------
-- Event Log (append-only)
-- -----------------------------
CREATE TABLE IF NOT EXISTS event_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid TEXT REFERENCES actors(uuid) ON DELETE SET NULL,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('task','container','attachment','actor','config','system')),
  resource_uuid TEXT,    -- UUID of target; nullable for 'system'
  event_type TEXT NOT NULL,   -- e.g. task.created, task.updated, task.deleted, task.moved
  etag INTEGER,               -- resulting etag if applicable
  payload TEXT                -- JSON diff or structured blob
);

CREATE TRIGGER IF NOT EXISTS event_log_no_update
BEFORE UPDATE ON event_log
BEGIN
  SELECT RAISE(ABORT, 'event_log is append-only');
END;

CREATE TRIGGER IF NOT EXISTS event_log_no_delete
BEFORE DELETE ON event_log
BEGIN
  SELECT RAISE(ABORT, 'event_log is append-only');
END;

CREATE INDEX IF NOT EXISTS event_log_res_idx   ON event_log(resource_type, resource_uuid);
CREATE INDEX IF NOT EXISTS event_log_ts_idx    ON event_log(timestamp);
CREATE INDEX IF NOT EXISTS event_log_actor_idx ON event_log(actor_uuid);

-- -----------------------------
-- Helper views: canonical paths
-- -----------------------------
CREATE VIEW IF NOT EXISTS v_container_paths AS
WITH RECURSIVE cte(uuid, id, slug, title, parent_uuid, depth, path) AS (
  SELECT uuid, id, slug, COALESCE(title, slug), parent_uuid, 1, slug
    FROM containers
   WHERE parent_uuid IS NULL
  UNION ALL
  SELECT c.uuid, c.id, c.slug, COALESCE(c.title, c.slug), c.parent_uuid,
         cte.depth + 1, cte.path || '/' || c.slug
    FROM containers c
    JOIN cte ON c.parent_uuid = cte.uuid
)
SELECT uuid, id, slug, title, parent_uuid, depth, path FROM cte;

CREATE VIEW IF NOT EXISTS v_task_paths AS
SELECT t.uuid,
       t.id,
       t.slug,
       t.title,
       t.state,
       t.priority,
       t.start_at,
       t.due_at,
       t.labels,
       t.etag,
       t.created_at,
       t.updated_at,
       t.completed_at,
       t.archived_at,
       t.project_uuid,
       cp.path || '/' || t.slug AS path
  FROM tasks t
  JOIN v_container_paths cp ON cp.uuid = t.project_uuid;

-- End 0001_init
```

---

## Implementation notes & edge cases

- **Task archiving**: Your text shows `task.state` includes `'archived'` *and* `rm` mentions `archived_at` for tasks/containers. I included **`tasks.archived_at`** so soft‑delete has a timestamp and `restore` can clear it. Triggers set `archived_at` when `state` becomes `'archived'`.
- **ETag**: Increment in app code inside the same transaction where you enforce `--if-match` (SQL: `UPDATE ... SET ..., etag = etag + 1 WHERE uuid = ? AND etag = ?`). DB doesn’t auto‑increment etags to keep the compare‑and‑swap semantics tight.
- **`updated_at`**: Touch triggers use `AFTER UPDATE` + `recursive_triggers = OFF` to avoid recursion. This writes a second UPDATE per logical update; simple and reliable for SQLite.
- **Friendly IDs**: Generated with `*_seq` tables + `AFTER INSERT` triggers, so you can `INSERT` with a pre‑set `id` if you want (e.g., import).
- **Slug rules**: Enforced via `CHECK` and partial unique indexes for root vs child containers to guarantee sibling uniqueness.
- **Labels/meta JSON**: Kept as `TEXT`. If your build has JSON1, you can additionally add `CHECK (labels IS NULL OR json_valid(labels))` and same for `actors.meta`.
- **Path views**: `v_container_paths` and `v_task_paths` give you canonical `path` strings for `ls/tree/resolve` without recomputing in Go.
- **Event log**: Append‑only enforced by triggers. The CLI should insert an event for each write (with the current actor); you can optionally add a temp table `session_actor(actor_uuid TEXT)` per connection if you ever want to move event writes into DB triggers later.

---

## Suggested `todo init` SQL (executed by the CLI)

After connecting:

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

-- Ensure attach_dir exists (filesystem operation in Go)

-- Seed top-level inbox project and the default human actor as per flags/env.
-- The CLI should INSERT with known slugs and then read back resulting friendly IDs.

-- Example shape (values provided by CLI):
-- INSERT INTO actors(slug, display_name, role) VALUES('local-human','Local Human','human');
-- INSERT INTO containers(slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
--   VALUES('inbox','Inbox',NULL, :actor_uuid, :actor_uuid);
```