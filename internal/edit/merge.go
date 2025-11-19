package edit

import (
	"fmt"
	"strings"
)

// MergeResult represents the outcome of a 3-way merge
type MergeResult struct {
	Merged    *TaskDocument
	Conflicts []string
	HasConflict bool
}

// TaskDocument represents a task with all editable fields
type TaskDocument struct {
	Title    string
	State    string
	Priority int
	DueAt    string
	Body     string
}

// Merge3Way performs a 3-way merge of task documents
// base: original version when editing started
// current: current version in database
// edited: user's edited version
func Merge3Way(base, current, edited *TaskDocument) *MergeResult {
	result := &MergeResult{
		Merged:    &TaskDocument{},
		Conflicts: []string{},
	}

	// Merge title
	result.Merged.Title = mergeField(
		"title",
		base.Title,
		current.Title,
		edited.Title,
		result,
	)

	// Merge state
	result.Merged.State = mergeField(
		"state",
		base.State,
		current.State,
		edited.State,
		result,
	)

	// Merge priority (convert to string for mergeField)
	basePrio := fmt.Sprintf("%d", base.Priority)
	currentPrio := fmt.Sprintf("%d", current.Priority)
	editedPrio := fmt.Sprintf("%d", edited.Priority)
	mergedPrio := mergeField("priority", basePrio, currentPrio, editedPrio, result)
	fmt.Sscanf(mergedPrio, "%d", &result.Merged.Priority)

	// Merge due_at
	result.Merged.DueAt = mergeField(
		"due_at",
		base.DueAt,
		current.DueAt,
		edited.DueAt,
		result,
	)

	// Merge body (line-by-line)
	result.Merged.Body = mergeBody(
		base.Body,
		current.Body,
		edited.Body,
		result,
	)

	return result
}

// mergeField performs 3-way merge on a single field
func mergeField(name, base, current, edited string, result *MergeResult) string {
	// If all same, no change
	if base == current && current == edited {
		return current
	}

	// If base == current, use edited (user made a change)
	if base == current {
		return edited
	}

	// If base == edited, use current (someone else made a change)
	if base == edited {
		return current
	}

	// If current == edited, both made same change
	if current == edited {
		return current
	}

	// Otherwise, we have a conflict
	result.HasConflict = true
	result.Conflicts = append(result.Conflicts, fmt.Sprintf(
		"Field %s: base=%q, current=%q, edited=%q",
		name, base, current, edited,
	))

	// Return edited version (user's choice)
	return edited
}

// mergeBody performs line-by-line 3-way merge on body text
func mergeBody(base, current, edited string, result *MergeResult) string {
	// Simple line-by-line comparison
	baseLines := strings.Split(base, "\n")
	currentLines := strings.Split(current, "\n")
	editedLines := strings.Split(edited, "\n")

	// If all identical, return as-is
	if base == current && current == edited {
		return current
	}

	// If base == current, user made changes, use edited
	if base == current {
		return edited
	}

	// If base == edited, someone else made changes, use current
	if base == edited {
		return current
	}

	// If current == edited, both made same changes
	if current == edited {
		return current
	}

	// Complex diff - for now, mark as conflict and return edited with markers
	result.HasConflict = true
	result.Conflicts = append(result.Conflicts, fmt.Sprintf(
		"Body has conflicting changes:\n  Base lines: %d\n  Current lines: %d\n  Edited lines: %d",
		len(baseLines), len(currentLines), len(editedLines),
	))

	// Return body with conflict markers
	var merged strings.Builder
	merged.WriteString("<<<<<<< CURRENT (in database)\n")
	merged.WriteString(current)
	merged.WriteString("\n=======\n")
	merged.WriteString(edited)
	merged.WriteString("\n>>>>>>> EDITED (your changes)\n")

	return merged.String()
}

// AutoResolve attempts to automatically resolve simple conflicts
func (r *MergeResult) AutoResolve() bool {
	// For now, we don't auto-resolve any conflicts
	// In the future, we could implement smarter merge strategies
	return false
}

// FormatConflicts returns a human-readable description of conflicts
func (r *MergeResult) FormatConflicts() string {
	if !r.HasConflict {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Merge conflicts detected:\n\n")
	for i, conflict := range r.Conflicts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, conflict))
	}
	sb.WriteString("\nPlease resolve conflicts manually and try again.\n")
	return sb.String()
}
