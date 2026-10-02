-- Seeks for the inbox's addressee and sender listings (T-09997). Every inbox
-- read lists envelopes by party (to/from, scope or principal) and state;
-- a scope-less inbox lists by to_principal_ref and from_principal_ref
-- instead. Without these each listing scanned the whole envelopes table, and
-- under memory pressure one inbox paged the full table for minutes.
CREATE INDEX envelopes_to_scope_state_idx ON envelopes(to_scope_ref, state);
CREATE INDEX envelopes_to_principal_state_idx ON envelopes(to_principal_ref, state);
CREATE INDEX envelopes_from_scope_state_idx ON envelopes(from_scope_ref, state);
CREATE INDEX envelopes_from_principal_state_idx ON envelopes(from_principal_ref, state);
