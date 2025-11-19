package paths

import (
	"path/filepath"
	"strings"
)

// MatchGlob checks if a path matches a glob pattern
// Supports *, ?, and ** patterns
func MatchGlob(pattern, path string) bool {
	// Handle ** patterns for recursive matching
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(pattern, path)
	}

	// Use filepath.Match for simple patterns
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	return matched
}

// matchDoubleStarPattern handles ** recursive patterns
func matchDoubleStarPattern(pattern, path string) bool {
	patternParts := SplitPath(pattern)
	pathParts := SplitPath(path)

	return matchParts(patternParts, pathParts)
}

func matchParts(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}

	if len(pathParts) == 0 {
		// Check if remaining pattern parts are all **
		for _, p := range patternParts {
			if p != "**" {
				return false
			}
		}
		return true
	}

	pattern := patternParts[0]
	path := pathParts[0]

	if pattern == "**" {
		// ** can match zero or more path segments
		// Try matching with consuming path or skipping **
		return matchParts(patternParts[1:], pathParts) || // Skip **
			matchParts(patternParts, pathParts[1:]) // Consume path segment
	}

	// Check if current segments match
	matched, err := filepath.Match(pattern, path)
	if err != nil || !matched {
		return false
	}

	return matchParts(patternParts[1:], pathParts[1:])
}

// IsGlobPattern checks if a string contains glob characters
func IsGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// GlobToSQLPattern converts a shell-style glob pattern to SQLite GLOB pattern
// SQLite GLOB is case-sensitive and uses * and ? wildcards
func GlobToSQLPattern(pattern string) string {
	// SQLite GLOB already uses the same syntax as shell globs
	// Just return the pattern as-is
	return pattern
}
