// Package taskfamily defines task selection for event reads. Ordinary owners
// include all named subtasks; a named subtask remains an exact selection.
package taskfamily

// Filter returns a SQL predicate with two bind slots for the selected task UUID.
// expression is a source-owned SQL expression, never caller input. The ownership
// guard prevents a subtask from owning subtasks, so the same predicate is exact
// for subtask selectors without a separate membership query.
func Filter(expression string) string {
	return "(" + expression + " = ? OR " + expression + " IN (SELECT uuid FROM tasks WHERE subtask_owner_uuid = ?))"
}
