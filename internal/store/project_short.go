package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/domain"
)

// ProjectShortConflict names the project holding a reserved slug or short.
type ProjectShortConflict struct {
	Field  string
	Value  string
	Holder string
}

func (e *ProjectShortConflict) Error() string {
	return fmt.Sprintf("project %s %q is reserved by project %q", e.Field, e.Value, e.Holder)
}

// checkProjectSlugAgainstShort runs inside the same immediate write transaction as
// the mutation. Existing slug-vs-slug collisions keep their usual handling.
func checkProjectSlugAgainstShort(tx *sql.Tx, rootUUID, slug string) error {
	var holder string
	switch err := tx.QueryRow(`SELECT slug FROM containers WHERE parent_uuid = ? AND short = ?`, rootUUID, slug).Scan(&holder); err {
	case sql.ErrNoRows:
		return nil
	case nil:
		return &ProjectShortConflict{Field: "slug", Value: slug, Holder: holder}
	default:
		return err
	}
}

func validateProjectShortUpdate(tx *sql.Tx, rootUUID, uuid string, fields map[string]any) error {
	if raw, changed := fields["short"]; changed && raw != nil {
		short, ok := raw.(string)
		if !ok {
			return fmt.Errorf("project short must be a string")
		}
		if err := domain.ValidateProjectShort(short); err != nil {
			return err
		}
		// Include the current slug to reject self-maps, but permit reapplying
		// the existing short to its holder.
		var holder string
		err := tx.QueryRow(`
			SELECT slug FROM containers WHERE parent_uuid = ?
			AND (slug = ? OR (short = ? AND uuid <> ?))
		`, rootUUID, short, short, uuid).Scan(&holder)
		switch err {
		case nil:
			return &ProjectShortConflict{Field: "short", Value: short, Holder: holder}
		case sql.ErrNoRows:
		default:
			return err
		}
	}
	if slug, ok := fields["slug"].(string); ok {
		return checkProjectSlugAgainstShort(tx, rootUUID, slug)
	}
	return nil
}
