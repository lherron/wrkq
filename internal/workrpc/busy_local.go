//go:build wrkq_local

package workrpc

import "github.com/lherron/wrkq/internal/db"

// isSQLiteBusy reports local SQLite write contention. Only a build that links
// the database can observe it.
func isSQLiteBusy(err error) bool { return db.IsBusy(err) }
