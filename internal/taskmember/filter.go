// Package taskmember defines ordinary container task membership. Named subtasks
// inherit residency but appear only in explicitly subtask-aware reads.
package taskmember

// Filter returns the shared SQL membership predicate. alias is a source-owned
// table alias, never caller input. includeSubtasks names an explicit opt-in.
func Filter(alias string, includeSubtasks bool) string {
	if includeSubtasks {
		return "1 = 1"
	}
	if alias != "" {
		alias += "."
	}
	return alias + "subtask_owner_uuid IS NULL"
}
