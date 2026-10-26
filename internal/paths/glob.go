package paths

// GlobToSQLPattern converts a shell-style glob pattern to SQLite GLOB pattern
// SQLite GLOB is case-sensitive and uses * and ? wildcards
func GlobToSQLPattern(pattern string) string {
	// SQLite GLOB already uses the same syntax as shell globs
	// Just return the pattern as-is
	return pattern
}
