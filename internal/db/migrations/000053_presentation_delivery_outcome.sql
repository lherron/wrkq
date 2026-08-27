-- How a presentation was DELIVERED, as HRC classified it (T-07638).
--
-- One more opaque HRC identifier on the receipt: wrkq stores and returns it and
-- never interprets it, exactly like node/runtime_id/generation beside it. The
-- classes HRC writes today are admitted_into_active_turn,
-- presented_to_live_harness, started_fresh_turn, and kicker; wrkq does not
-- enforce that set, so HRC can add one without a migration.
ALTER TABLE envelope_presentations ADD COLUMN delivery_outcome TEXT;
