package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// CausedByRef is a single resolved caused-by edge target: the causing task's
// UUID plus its friendly ID (kept for event/webhook payloads which speak in
// friendly IDs, never UUIDs).
type CausedByRef struct {
	TaskUUID   string
	FriendlyID string
}

// CausedByUpdate is the replace-the-whole-set instruction for a task's caused_by
// lineage, carried as a non-column value through the task field map. An empty
// Refs slice clears the lineage. Presence of this key (regardless of length)
// means "replace"; absence means "no change".
type CausedByUpdate struct {
	Refs []CausedByRef
}

// FriendlyIDs returns the ordered friendly IDs for event/webhook payloads.
func (u CausedByUpdate) FriendlyIDs() []string {
	ids := make([]string, 0, len(u.Refs))
	for _, r := range u.Refs {
		ids = append(ids, r.FriendlyID)
	}
	return ids
}

// MarshalJSON renders a CausedByUpdate as its ordered friendly-ID array (used by
// the `set --dry-run` field preview); the store consumes the Go value directly.
func (u CausedByUpdate) MarshalJSON() ([]byte, error) {
	return json.Marshal(u.FriendlyIDs())
}

// causedByCausedByField is the sentinel key used to carry a CausedByUpdate inside
// the task UpdateFields map without it being treated as a scalar column.
const causedByFieldKey = "caused_by"

// rowQuerier is satisfied by *db.DB, *sql.DB, and *sql.Tx.
type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// CausedByIDs returns the ordered, de-duplicated friendly IDs of the tasks that
// caused taskUUID, in stable position order. Returns an empty slice when the
// task has no lineage.
func CausedByIDs(q rowQuerier, taskUUID string) ([]string, error) {
	rows, err := q.Query(`
		SELECT t.id
		FROM task_causes tc
		JOIN tasks t ON t.uuid = tc.caused_by_task_uuid
		WHERE tc.task_uuid = ?
		ORDER BY tc.position
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to load caused_by: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan caused_by: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// insertCausedByRows replaces nothing; it inserts the ordered edge rows for a
// task within an existing transaction. Callers clear pre-existing rows first.
func insertCausedByRows(tx *sql.Tx, attr causedByAttribution, taskUUID string, refs []CausedByRef) error {
	for i, ref := range refs {
		if _, err := tx.Exec(`
			INSERT INTO task_causes (
				task_uuid, caused_by_task_uuid, position,
				created_by_principal_ref, created_by_scope_ref
			) VALUES (?, ?, ?, ?, ?)
		`, taskUUID, ref.TaskUUID, i, attr.principalRef, attr.scope); err != nil {
			return fmt.Errorf("failed to insert caused_by edge %s -> %s: %w", taskUUID, ref.FriendlyID, err)
		}
	}
	return nil
}

// causedByAttribution carries the pre-resolved SQL bind values for the
// created_by_* columns on task_causes rows.
type causedByAttribution struct {
	principalRef string
	scope        interface{}
}
