package workrpc

import "time"

const (
	// HTTPResponseTimeout is the canonical daemon response deadline.
	HTTPResponseTimeout = 30 * time.Second
	// RemoteHookTimeoutCeiling leaves response headroom for persistence and
	// JSON-RPC framing after a remote hook process exits.
	RemoteHookTimeoutCeiling = 25 * time.Second
)

const (
	// ReadDeadline bounds a pure read on the daemon (T-09997). It sits under
	// HTTPResponseTimeout so the typed WORKRPC_TIMEOUT reaches the caller
	// before the HTTP write deadline closes the connection.
	ReadDeadline = 20 * time.Second
	// MaxAbandonedReads caps reads still running past their deadline; beyond
	// it new bounded reads are refused at once with WORKRPC_TIMEOUT.
	MaxAbandonedReads = 16
)
