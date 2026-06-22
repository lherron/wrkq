// Package projectroot holds the neutral project-root path/selector transform that
// both the legacy CLI (internal/cli) and the RPC-backed mirror (internal/rpccli)
// apply to raw user arguments BEFORE they become RPC params or store lookups.
//
// Project-root scoping is CLI caller semantics, not a server read-model concern:
// the RPC methods receive already-scoped selectors/paths and never read
// WRKQ_PROJECT_ROOT / ASP_PROJECT / --project. Keeping the transform here (rather
// than duplicated in cli and rpccli) is what prevents the two surfaces from
// drifting — there is exactly one implementation, exercised by one test suite.
package projectroot

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
)

// Normalize returns the configured project root, trimmed of surrounding
// whitespace and slashes. Empty config or empty root yields "".
func Normalize(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	root := strings.TrimSpace(cfg.ProjectRoot)
	root = strings.Trim(root, "/")
	return root
}

// ApplyToPath prefixes a single path with the project root. defaultToRoot makes
// an empty input resolve to the root itself (used by commands whose no-arg form
// targets the current project, e.g. ls/tree).
func ApplyToPath(cfg *config.Config, path string, defaultToRoot bool) string {
	root := Normalize(cfg)
	trimmed := strings.TrimSpace(path)
	if root == "" {
		return trimmed
	}
	if trimmed == "" {
		if defaultToRoot {
			return root
		}
		return trimmed
	}
	return applyToken(root, trimmed)
}

// ApplyToSelector prefixes a selector with the project root, preserving the
// `t:` / `c:` typed-selector prefixes and exempting friendly IDs, UUIDs, and bare
// sequence numbers (which are not path segments).
func ApplyToSelector(cfg *config.Config, selector string, defaultToRoot bool) string {
	root := Normalize(cfg)
	trimmed := strings.TrimSpace(selector)
	if root == "" {
		return trimmed
	}
	if trimmed == "" {
		if defaultToRoot {
			return root
		}
		return trimmed
	}

	if strings.HasPrefix(trimmed, "t:") || strings.HasPrefix(trimmed, "c:") {
		prefix := trimmed[:2]
		token := strings.TrimSpace(trimmed[2:])
		if token == "" {
			if defaultToRoot {
				return prefix + root
			}
			return trimmed
		}
		return prefix + applyToken(root, token)
	}

	return applyToken(root, trimmed)
}

// ApplyToPaths prefixes every path with the project root. With defaultToRoot,
// an empty input list resolves to [root] (the no-arg "current project" form).
func ApplyToPaths(cfg *config.Config, pathsIn []string, defaultToRoot bool) []string {
	if len(pathsIn) == 0 {
		root := Normalize(cfg)
		if defaultToRoot && root != "" {
			return []string{root}
		}
		return pathsIn
	}
	out := make([]string, 0, len(pathsIn))
	for _, path := range pathsIn {
		out = append(out, ApplyToPath(cfg, path, false))
	}
	return out
}

// applyToken applies the root prefix to a single non-empty token, exempting
// friendly IDs, UUIDs, and bare sequence numbers, and avoiding double-prefixing
// an already-rooted token.
func applyToken(root, token string) string {
	if token == "" {
		return token
	}

	if id.IsFriendlyID(token) || id.IsUUID(token) {
		return token
	}

	// A bare sequence number ("1454") is an ID-like task shorthand, not a
	// path segment, so leave it untouched here; ResolveTask expands it.
	if _, ok := id.ExpandTaskID(token); ok {
		return token
	}

	normalized := strings.Trim(token, "/")
	if normalized == "" {
		return normalized
	}
	if normalized == root || strings.HasPrefix(normalized, root+"/") {
		return normalized
	}
	return paths.JoinPath(root, normalized)
}

// ResolveProjectFlag resolves a --project selector (path, slug, or ID) to a
// canonical project path, used to OVERRIDE the configured project root. This is
// the one piece that needs database access; it stays neutral (depends only on
// selectors + the db handle), so the mirror can reuse it without importing
// internal/cli.
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
