//go:build wrkq_local

package projectroot

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
)

// ResolveProjectFlag resolves --project against durable rows, which is server
// work. The caller host owns only project-root ARGUMENT scoping (T-07090).
// ResolveProjectFlag resolves a --project selector (path, slug, or ID) to a
// canonical project path, used to OVERRIDE the configured project root. This is
// the one piece that needs database access; it stays neutral (depends only on
// selectors + the db handle), so rpccli can reuse it without importing an
// administrative or daemon adapter.
func ResolveProjectFlag(database *db.DB, projectSelector string) (string, error) {
	selector := strings.TrimSpace(projectSelector)
	if selector == "" {
		return "", nil
	}

	projectUUID, _, err := selectors.ResolveContainer(database, selector)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project %q: %w", selector, err)
	}

	var projectPath string
	if err := database.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
		return "", fmt.Errorf("failed to resolve project path: %w", err)
	}

	return strings.Trim(projectPath, "/"), nil
}
