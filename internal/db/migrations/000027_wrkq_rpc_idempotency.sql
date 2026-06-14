-- wrkq RPC idempotency ledger.
--
-- Persists, per mutating wrkq.* method namespace, the idempotency key, the
-- canonical request hash, and the exact committed result JSON so a replayed
-- call with the same key + same canonical request hash returns the original
-- result, while the same key + a different canonical request hash is rejected
-- with WRKQ_CONFLICT. This is the wrkq-side analogue of the wrkf transition /
-- run idempotency rows (see 000020/000021).

CREATE TABLE IF NOT EXISTS wrkq_rpc_idempotency (
    namespace       TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    result_json     TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    PRIMARY KEY (namespace, idempotency_key)
);
