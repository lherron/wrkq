// Package causedby parses and resolves caused_by causal-lineage input shared by
// the legacy CLI, the RPC server, and any other surface. caused_by references
// are friendly task IDs matching ^T-[0-9]{5}$ that must resolve to existing
// tasks; the resolved set is ordered (first-seen) and de-duplicated.
package causedby

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

var friendlyIDPattern = regexp.MustCompile(`^T-[0-9]{5}$`)

// Resolve parses a comma-separated caused_by input string, validating each token
// against the friendly-ID format, resolving it to a task UUID, de-duplicating
// while preserving first-seen order. selfFriendlyID, when non-empty, rejects
// self-causation. An input that yields no tokens (empty / whitespace / commas)
// returns an empty slice — callers interpret that as an explicit clear.
func Resolve(database *db.DB, input, selfFriendlyID string) ([]store.CausedByRef, error) {
	return ResolveTokens(database, strings.Split(input, ","), selfFriendlyID)
}

// ResolveTokens is Resolve over a pre-split token list (used by the RPC server,
// which receives a JSON array rather than a comma string).
func ResolveTokens(database *db.DB, tokens []string, selfFriendlyID string) ([]store.CausedByRef, error) {
	refs := []store.CausedByRef{}
	seen := make(map[string]bool, len(tokens))
	for _, raw := range tokens {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		if !friendlyIDPattern.MatchString(token) {
			return nil, fmt.Errorf("invalid caused-by reference %q: must be a task ID like T-00001", token)
		}
		if selfFriendlyID != "" && token == selfFriendlyID {
			return nil, fmt.Errorf("a task cannot be caused by itself: %s", token)
		}
		if seen[token] {
			continue
		}
		var uuid string
		err := database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", token).Scan(&uuid)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("caused-by task not found: %s", token)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to resolve caused-by task %s: %w", token, err)
		}
		seen[token] = true
		refs = append(refs, store.CausedByRef{TaskUUID: uuid, FriendlyID: token})
	}
	return refs, nil
}
