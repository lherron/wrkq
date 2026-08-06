//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
)

// RelationListView is the server-owned COMPATIBILITY list projection for
// `wrkq relation ls`. It returns the legacy relation rows (outgoing then
// incoming, each ordered by kind,id) using the same CatViewRelation shape as
// catView. The RPC CLI renders these as an indented JSON array (--json), NDJSON
// (non-TTY default), or a table (TTY). No cursor: a task's relation set is bounded.
func (a *API) RelationListView(ctx context.Context, p RelationListViewParams) ([]CatViewRelation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	rels, err := catViewRelations(ctx, tx, taskUUID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}

	return rels, nil
}
