-- migration: add comments table
-- M3: Comments & Collaboration

-- Sequence counter for friendly comment IDs (C-00001, C-00002, etc.)
CREATE TABLE IF NOT EXISTS comment_sequences (
    name TEXT PRIMARY KEY,
    value INTEGER NOT NULL DEFAULT 0
);

INSERT INTO comment_sequences (name, value) VALUES ('next_comment', 0);

-- Drop old comments table if it exists (from earlier prototype)
DROP TABLE IF EXISTS comments;
DROP TRIGGER IF EXISTS comments_ai_friendly;
DROP TABLE IF EXISTS comment_seq;

-- Comments table: append-only notes attached to tasks
CREATE TABLE comments (
    uuid TEXT PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,  -- friendly ID like C-00001
    task_uuid TEXT NOT NULL,
    actor_uuid TEXT NOT NULL,
    body TEXT NOT NULL,        -- Markdown content
    meta TEXT,                 -- JSON optional metadata for agents/tools
    etag INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT,           -- nullable; reserved for future editable comments
    deleted_at TEXT,           -- nullable; soft delete timestamp
    deleted_by_actor_uuid TEXT,  -- nullable; actor who soft-deleted this comment

    FOREIGN KEY (task_uuid) REFERENCES tasks(uuid) ON DELETE CASCADE,
    FOREIGN KEY (actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,
    FOREIGN KEY (deleted_by_actor_uuid) REFERENCES actors(uuid) ON DELETE RESTRICT,

    CHECK (length(id) > 0),
    CHECK (length(body) > 0),
    CHECK (etag >= 1)
);

-- Indexes for efficient comment queries

-- Primary query: list comments for a task, ordered by creation time
CREATE INDEX idx_comments_task_created ON comments(task_uuid, created_at);

-- Query: find comments by actor (for future tooling)
CREATE INDEX idx_comments_actor_created ON comments(actor_uuid, created_at);

-- Event log integration: extend resource_type to include 'comment'
-- We need to drop and recreate the event_log table with the updated constraint
-- First, save existing events
CREATE TEMP TABLE event_log_backup AS SELECT * FROM event_log;

-- Drop the old table
DROP TABLE event_log;

-- Recreate with updated resource_type constraint
CREATE TABLE event_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  actor_uuid    TEXT,
  resource_type TEXT CHECK (resource_type IN ('task','container','attachment','actor','config','system','comment')),
  resource_uuid TEXT,
  event_type    TEXT NOT NULL,
  etag          INTEGER,
  payload       TEXT
);

CREATE INDEX event_log_resource_idx ON event_log(resource_type, resource_uuid, id DESC);

-- Restore backed-up events
INSERT INTO event_log SELECT * FROM event_log_backup;

-- Drop the temp table
DROP TABLE event_log_backup;

-- Event types will be: comment.created, comment.deleted, comment.purged
