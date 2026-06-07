package store

import (
	"database/sql"
	"fmt"
)

// RootChildClause is the SQL predicate that selects top-level project containers
// (direct children of the singleton root). It replaces the legacy
// `parent_uuid IS NULL` test now that the root is the only null-parent row.
// kind='root' is the authoritative way to locate the root, so no UUID is hardcoded.
const RootChildClause = "parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')"

// rootQueryer is the minimal surface needed to resolve the root container.
type rootQueryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

// RootContainerUUID resolves the UUID of the singleton root container by its
// kind. Returns an error if the root is missing (a corrupt/unmigrated database).
func RootContainerUUID(q rootQueryer) (string, error) {
	var uuid string
	err := q.QueryRow("SELECT uuid FROM containers WHERE kind = 'root'").Scan(&uuid)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("root container not found (run `wrkq adm migrate`)")
	}
	if err != nil {
		return "", fmt.Errorf("resolve root container: %w", err)
	}
	return uuid, nil
}

// ProjectParentUUID returns the UUID that every project container must have as
// its parent — i.e. the root container.
func ProjectParentUUID(q rootQueryer) (string, error) {
	return RootContainerUUID(q)
}
