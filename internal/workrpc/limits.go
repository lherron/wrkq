package workrpc

import "time"

const (
	// HTTPResponseTimeout is the canonical daemon response deadline.
	HTTPResponseTimeout = 30 * time.Second
	// RemoteHookTimeoutCeiling leaves response headroom for persistence and
	// JSON-RPC framing after a remote hook process exits.
	RemoteHookTimeoutCeiling = 25 * time.Second
)
