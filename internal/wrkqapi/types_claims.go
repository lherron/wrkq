//go:build wrkq_local

package wrkqapi

import "database/sql"

type taskClaimRow struct {
	taskID         string
	state          string
	claimedBy      sql.NullString
	claimedScope   sql.NullString
	claimedNode    sql.NullString
	claimedAt      sql.NullString
	generation     int64
	claimTokenHash sql.NullString
	etag           int64
}

type claimRowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}