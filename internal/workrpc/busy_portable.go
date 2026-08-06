//go:build !wrkq_local

package workrpc

// isSQLiteBusy is always false in the portable build: a remote client never
// holds a local SQLite handle, and the active locator invariant requires that
// network/auth/handshake failures are reported as transport errors rather than
// as SQLite contention such as WRKQ_DB_BUSY.
func isSQLiteBusy(error) bool { return false }
