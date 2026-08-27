-- Durable agent collaboration owned by wrkq: rooms, envelopes, members, and the
-- presentation receipts that join wrkq's collaboration ledger to HRC's
-- execution world. HRC identifiers are stored as OPAQUE STRINGS only; wrkq
-- never interprets them and never imports hrc. (Spec T-07612 rev 2 §3.)

CREATE TABLE room_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);

CREATE TABLE envelope_seq (
  id INTEGER PRIMARY KEY AUTOINCREMENT
);

-- A room is a durable conversation keyed by a WORK IDENTITY. Derived rooms
-- (campaign/task/project) carry no friendly id: their key IS the work id, so
-- `wrkq monitor watch T-07613` shows task state and the conversation on one
-- selector. Ad-hoc rooms have no work identity and mint R-xxxxx instead.
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

-- One room per work identity. Strict campaign coalesce (spec §4 rule 2) is
-- enforced in the routing resolver; these indexes make the storage side
-- incapable of holding a second room for the same anchor.
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

-- One object for chat and obligation. EN-xxxxx is an INTERNAL row id: shown by
-- inbox/show/log, never in the injected presentation. (`EV-` is already owned
-- by evidence_items; mable erratum on T-07612 rev 2, 2026-08-27.)
--
-- Exactly ONE addressee per envelope. `--to a,b` fans out to one envelope per
-- addressee sharing group_id, so every lifecycle field is per envelope and one
-- recipient's reply/defer/rounds/dead never disposes another's obligation.
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
  urgent INTEGER NOT NULL DEFAULT 0 CHECK (urgent IN (0, 1)),
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
-- The kicker wake set and the stop-hook predicate: pending/presented
-- reply_required envelopes by addressee scope.
CREATE INDEX envelopes_obligation_idx
  ON envelopes(to_scope_ref, state)
  WHERE obligation = 'reply_required';
CREATE INDEX envelopes_retry_idx
  ON envelopes(retry_at) WHERE state = 'deferred' AND retry_at IS NOT NULL;
-- The idempotency key belongs to the SAY, so every envelope the say fanned out
-- to carries it and consumers can correlate a dual-written row on any addressee.
-- The uniqueness guard is therefore per (key, addressee): a retried say collides
-- on its rows and the whole transaction rolls back rather than half-writing a
-- group. COALESCE keeps the guard live for an addressee-less log entry, whose
-- NULL addressee SQLite would otherwise treat as distinct on every retry.
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

-- Membership is identity + attendance, never delivery and never an ACL. A
-- member is a scope (agent@project:task) or, for principals with no HRC scope
-- (humans), the bare principal (agent:lance).
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

-- The join between wrkq's collaboration world and HRC's execution world:
-- "gen 49 of clod@hrc-runtime:primary was presented envelope X". HRC writes
-- this at presentation and keeps no durable copy. Every HRC identifier here is
-- an opaque string to wrkq.
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
);

CREATE INDEX envelope_presentations_envelope_idx
  ON envelope_presentations(envelope_uuid, presented_at);
CREATE INDEX envelope_presentations_attendance_idx
  ON envelope_presentations(room_uuid, member_ref, presented_at DESC);
-- Presentation is at-least-once by design (T-06810 residual), but one
-- driveAttemptId presents an envelope exactly once.
CREATE UNIQUE INDEX envelope_presentations_attempt_idx
  ON envelope_presentations(envelope_uuid, drive_attempt_id)
  WHERE drive_attempt_id IS NOT NULL;

-- Widen the append-only event resource vocabulary for room.* / envelope.* /
-- member.* without losing historic identities or principal/scope attribution.
-- member.* events are logged against their ROOM: membership has no independent
-- addressable identity, and `wrkq monitor watch <room>` must show joins.
DROP INDEX IF EXISTS event_log_resource_idx;
DROP INDEX IF EXISTS event_log_principal_idx;
DROP INDEX IF EXISTS event_log_scope_idx;

ALTER TABLE event_log RENAME TO event_log_old;

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

INSERT INTO event_log (
  id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag,
  payload, principal_ref, scope_ref
)
SELECT
  id, timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag,
  payload, principal_ref, scope_ref
FROM event_log_old;

DROP TABLE event_log_old;

CREATE INDEX event_log_resource_idx
  ON event_log(resource_type, resource_uuid, id DESC);
CREATE INDEX event_log_principal_idx
  ON event_log(principal_ref, id DESC)
  WHERE principal_ref IS NOT NULL;
CREATE INDEX event_log_scope_idx
  ON event_log(scope_ref, id DESC)
  WHERE scope_ref IS NOT NULL;
