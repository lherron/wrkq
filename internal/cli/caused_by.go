package cli

import (
	"github.com/lherron/wrkq/internal/causedby"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// causedByUnset is the sentinel default for `--caused-by` on commands that need
// to distinguish "flag omitted" (no change) from "flag set to empty" (explicit
// clear). It cannot collide with any real friendly-ID input. Commands reset
// their global back to this sentinel after running so the shared rootCmd used in
// tests never leaks a stale value into another command invocation.
const causedByUnset = "\x00wrkq-caused-by-unset"

// resolveCausedBy parses + resolves a caused-by input string into ordered,
// de-duplicated store edge refs. selfFriendlyID rejects self-causation.
func resolveCausedBy(database *db.DB, input, selfFriendlyID string) ([]store.CausedByRef, error) {
	return causedby.Resolve(database, input, selfFriendlyID)
}
