-- wrkc has ONE delivery class (T-07612 rev 4). `urgent` was a delivery intent
-- stored for HRC to actuate; nothing waits for idle any more, so nothing reads
-- it. Drop the column rather than leave a dead field on the wire.
ALTER TABLE envelopes DROP COLUMN urgent;
