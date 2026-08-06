//go:build !wrkq_local

package wrkqapi

// isSQLiteBusy is always false in the portable build: a remote client holds no
// local SQLite handle, so no error it constructs is contention.
func isSQLiteBusy(error) bool { return false }
