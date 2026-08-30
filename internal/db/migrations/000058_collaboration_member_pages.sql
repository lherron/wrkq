-- One durable identity fences collaboration cursors across database replacement.
-- Normal process restart preserves this row; reinitializing the database mints
-- a new value when migrations are applied to the replacement.
CREATE TABLE collaboration_ledger_meta (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  incarnation TEXT NOT NULL UNIQUE
);

INSERT INTO collaboration_ledger_meta (singleton, incarnation)
VALUES (1, lower(hex(randomblob(16))));

-- EN-xxxxx's numeric suffix is already the collaboration ledger sequence.
-- Index the numeric expression so cross-room before/after pages retain numeric
-- ordering after EN-99999 without adding a second sequence authority.
CREATE INDEX envelopes_message_seq_idx
  ON envelopes(CAST(SUBSTR(id, 4) AS INTEGER));

-- One exact active-member lookup per sequence-index candidate. Both the stored
-- member address and its normalized principal participate so a malformed or
-- stale alias cannot widen a member page.
CREATE INDEX room_members_observation_idx
  ON room_members(member_ref, member_principal_ref, room_uuid)
  WHERE left_at IS NULL;
