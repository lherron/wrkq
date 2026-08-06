//go:build wrkq_local

package wrkqapi

import "github.com/lherron/wrkq/internal/db"

// isSQLiteBusy reports local SQLite write contention (T-07090). Only a build
// that links the database can observe it.
func isSQLiteBusy(err error) bool { return db.IsBusy(err) }
