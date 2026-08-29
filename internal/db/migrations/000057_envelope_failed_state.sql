-- wrkq:foreign-keys-off
-- rev 5.1 binds an obligation to the runtime that received it. Rounds and
-- dead-lettering are gone; unsuccessful terminal rows are failed{reason}.

CREATE TABLE envelopes_new (
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
  group_id TEXT,
  from_principal_ref TEXT NOT NULL,
  from_scope_ref TEXT,
  to_scope_ref TEXT,
  to_principal_ref TEXT,
  obligation TEXT NOT NULL
    CHECK (obligation IN ('reply_required', 'fyi', 'none')),
  body TEXT NOT NULL CHECK (length(trim(body)) > 0),
  task_uuid TEXT REFERENCES tasks(uuid) ON DELETE SET NULL,
  state TEXT NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'presented', 'acked', 'deferred', 'failed')),
  failure_reason TEXT
    CHECK (failure_reason IN ('runtime_terminated', 'ignored', 'undeliverable', 'legacy')),
  retry_at TEXT,
  defer_reason TEXT,
  terminal_actor TEXT,
  terminal_at TEXT,
  materialization_intent TEXT,
  respond_to_principal_ref TEXT,
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
  ),
  CHECK (
    (state = 'failed' AND failure_reason IS NOT NULL)
    OR (state <> 'failed' AND failure_reason IS NULL)
  )
);

INSERT INTO envelopes_new (
  uuid, id, room_uuid, group_id, from_principal_ref, from_scope_ref,
  to_scope_ref, to_principal_ref, obligation, body, task_uuid, state,
  failure_reason, retry_at, defer_reason, terminal_actor, terminal_at,
  materialization_intent, respond_to_principal_ref, retry_promise_uuid,
  idempotency_key, meta, etag, created_at, updated_at,
  created_by_principal_ref, created_by_scope_ref,
  updated_by_principal_ref, updated_by_scope_ref
)
SELECT
  uuid, id, room_uuid, group_id, from_principal_ref, from_scope_ref,
  to_scope_ref, to_principal_ref, obligation, body, task_uuid,
  CASE state WHEN 'dead' THEN 'failed' ELSE state END,
  CASE state WHEN 'dead' THEN 'legacy' ELSE NULL END,
  retry_at, defer_reason, terminal_actor, terminal_at,
  materialization_intent, respond_to_principal_ref, retry_promise_uuid,
  idempotency_key, meta, etag, created_at, updated_at,
  created_by_principal_ref, created_by_scope_ref,
  updated_by_principal_ref, updated_by_scope_ref
FROM envelopes;

DROP TABLE envelopes;
ALTER TABLE envelopes_new RENAME TO envelopes;

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

PRAGMA foreign_key_check;
