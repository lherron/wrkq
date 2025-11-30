package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/snapshot"
)

// SummarizeOptions configures patch summarize behavior.
type SummarizeOptions struct {
	// PatchPath is the patch file to summarize
	PatchPath string
	// BasePath is an optional base snapshot for enriched context
	BasePath string
	// Format is the output format: text, markdown, or json
	Format string
}

// SummarizeResult contains the summarize output.
type SummarizeResult struct {
	// Summary is the formatted summary string (for text/markdown)
	Summary string `json:"summary,omitempty"`
	// Counts by entity type
	Counts EntityCounts `json:"counts"`
	// Details lists individual operations
	Details []OpDetail `json:"details,omitempty"`
}

// EntityCounts tracks operation counts by entity type.
type EntityCounts struct {
	Tasks      OpCounts `json:"tasks,omitempty"`
	Containers OpCounts `json:"containers,omitempty"`
	Actors     OpCounts `json:"actors,omitempty"`
	Comments   OpCounts `json:"comments,omitempty"`
}

// OpCounts tracks counts by operation type.
type OpCounts struct {
	Add     int `json:"add,omitempty"`
	Replace int `json:"replace,omitempty"`
	Remove  int `json:"remove,omitempty"`
}

// OpDetail describes a single operation.
type OpDetail struct {
	Entity    string `json:"entity"`
	Op        string `json:"op"`
	UUID      string `json:"uuid"`
	ID        string `json:"id,omitempty"`
	Path      string `json:"path,omitempty"`
	Title     string `json:"title,omitempty"`
	Field     string `json:"field,omitempty"`
	OldValue  string `json:"old_value,omitempty"`
	NewValue  string `json:"new_value,omitempty"`
}

// Summarize generates a human-friendly summary of a patch.
func Summarize(opts SummarizeOptions) (*SummarizeResult, error) {
	// Load patch
	p, err := LoadPatch(opts.PatchPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load patch: %w", err)
	}

	// Optionally load base snapshot for context
	var base *snapshot.Snapshot
	if opts.BasePath != "" {
		baseData, err := os.ReadFile(opts.BasePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read base snapshot: %w", err)
		}
		base = &snapshot.Snapshot{}
		if err := json.Unmarshal(baseData, base); err != nil {
			return nil, fmt.Errorf("failed to parse base snapshot: %w", err)
		}
	}

	// Process operations
	counts := EntityCounts{}
	var details []OpDetail

	for _, op := range p {
		detail := processOperation(op, base)
		if detail != nil {
			details = append(details, *detail)
			updateCounts(&counts, detail.Entity, op.Op)
		}
	}

	// Sort details for deterministic output
	sort.Slice(details, func(i, j int) bool {
		if details[i].Entity != details[j].Entity {
			return details[i].Entity < details[j].Entity
		}
		if details[i].Op != details[j].Op {
			return details[i].Op < details[j].Op
		}
		return details[i].UUID < details[j].UUID
	})

	result := &SummarizeResult{
		Counts:  counts,
		Details: details,
	}

	// Format output
	switch opts.Format {
	case "json":
		// JSON output handled by caller
	case "markdown":
		result.Summary = formatMarkdown(counts, details)
	default: // text
		result.Summary = formatText(counts)
	}

	return result, nil
}

// processOperation extracts details from a single operation.
func processOperation(op Operation, base *snapshot.Snapshot) *OpDetail {
	// Parse path: /entity_type/uuid or /entity_type/uuid/field
	parts := strings.Split(strings.TrimPrefix(op.Path, "/"), "/")
	if len(parts) < 2 {
		return nil
	}

	entityType := parts[0]
	uuid := unescapeJSONPointer(parts[1])

	// Map entity type to singular form
	entity := singularEntity(entityType)
	if entity == "" {
		return nil
	}

	detail := &OpDetail{
		Entity: entity,
		Op:     op.Op,
		UUID:   uuid,
	}

	// Handle field-level operations
	if len(parts) >= 3 {
		detail.Field = unescapeJSONPointer(parts[2])
	}

	// Enrich from base snapshot
	if base != nil {
		enrichFromBase(detail, entityType, uuid, base)
	}

	// Extract info from operation value
	enrichFromValue(detail, op)

	return detail
}

// singularEntity converts plural entity type to singular.
func singularEntity(plural string) string {
	switch plural {
	case "tasks":
		return "task"
	case "containers":
		return "container"
	case "actors":
		return "actor"
	case "comments":
		return "comment"
	default:
		return ""
	}
}

// enrichFromBase adds context from the base snapshot.
func enrichFromBase(detail *OpDetail, entityType, uuid string, base *snapshot.Snapshot) {
	switch entityType {
	case "tasks":
		if task, ok := base.Tasks[uuid]; ok {
			detail.ID = task.ID
			detail.Title = task.Title
			if container, ok := base.Containers[task.ProjectUUID]; ok {
				detail.Path = container.Slug + "/" + task.Slug
			} else {
				detail.Path = task.Slug
			}
		}
	case "containers":
		if container, ok := base.Containers[uuid]; ok {
			detail.ID = container.ID
			detail.Title = container.Title
			detail.Path = buildContainerPath(uuid, base)
		}
	case "actors":
		if actor, ok := base.Actors[uuid]; ok {
			detail.ID = actor.ID
			detail.Title = actor.DisplayName
		}
	case "comments":
		if comment, ok := base.Comments[uuid]; ok {
			detail.ID = comment.ID
			// Try to get task context
			if task, ok := base.Tasks[comment.TaskUUID]; ok {
				detail.Path = "on " + task.ID
			}
		}
	}
}

// buildContainerPath builds a full path for a container by its UUID.
func buildContainerPath(uuid string, base *snapshot.Snapshot) string {
	container, ok := base.Containers[uuid]
	if !ok {
		return ""
	}

	if container.ParentUUID == "" {
		return container.Slug
	}

	parentPath := buildContainerPath(container.ParentUUID, base)
	if parentPath == "" {
		return container.Slug
	}
	return parentPath + "/" + container.Slug
}

// enrichFromValue extracts info from the operation value.
func enrichFromValue(detail *OpDetail, op Operation) {
	if op.Value == nil {
		return
	}

	switch v := op.Value.(type) {
	case map[string]interface{}:
		// Full entity add/replace
		if id, ok := v["id"].(string); ok && detail.ID == "" {
			detail.ID = id
		}
		if title, ok := v["title"].(string); ok && detail.Title == "" {
			detail.Title = title
		}
		if slug, ok := v["slug"].(string); ok && detail.Path == "" {
			detail.Path = slug
		}
		if displayName, ok := v["display_name"].(string); ok && detail.Title == "" {
			detail.Title = displayName
		}

		// For replace operations on specific fields
		if detail.Field != "" {
			if state, ok := v["state"].(string); ok && detail.Field == "state" {
				detail.NewValue = state
			}
		}
	case string:
		// Field-level replacement
		if detail.Field != "" {
			detail.NewValue = v
		}
	}
}

// updateCounts increments the appropriate counter.
func updateCounts(counts *EntityCounts, entity, op string) {
	var opCounts *OpCounts
	switch entity {
	case "task":
		opCounts = &counts.Tasks
	case "container":
		opCounts = &counts.Containers
	case "actor":
		opCounts = &counts.Actors
	case "comment":
		opCounts = &counts.Comments
	default:
		return
	}

	switch op {
	case "add":
		opCounts.Add++
	case "replace":
		opCounts.Replace++
	case "remove":
		opCounts.Remove++
	}
}

// formatText generates a text summary.
func formatText(counts EntityCounts) string {
	var parts []string

	if total := counts.Tasks.Add + counts.Tasks.Replace + counts.Tasks.Remove; total > 0 {
		parts = append(parts, formatEntityText("task", counts.Tasks))
	}
	if total := counts.Containers.Add + counts.Containers.Replace + counts.Containers.Remove; total > 0 {
		parts = append(parts, formatEntityText("container", counts.Containers))
	}
	if total := counts.Actors.Add + counts.Actors.Replace + counts.Actors.Remove; total > 0 {
		parts = append(parts, formatEntityText("actor", counts.Actors))
	}
	if total := counts.Comments.Add + counts.Comments.Replace + counts.Comments.Remove; total > 0 {
		parts = append(parts, formatEntityText("comment", counts.Comments))
	}

	if len(parts) == 0 {
		return "No changes."
	}

	return strings.Join(parts, ", ") + "."
}

// formatEntityText formats counts for a single entity type.
func formatEntityText(entity string, counts OpCounts) string {
	var ops []string

	if counts.Add > 0 {
		ops = append(ops, fmt.Sprintf("%d %s added", counts.Add, pluralize(entity, counts.Add)))
	}
	if counts.Replace > 0 {
		ops = append(ops, fmt.Sprintf("%d %s updated", counts.Replace, pluralize(entity, counts.Replace)))
	}
	if counts.Remove > 0 {
		ops = append(ops, fmt.Sprintf("%d %s removed", counts.Remove, pluralize(entity, counts.Remove)))
	}

	return strings.Join(ops, ", ")
}

// pluralize returns singular or plural form.
func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

// formatMarkdown generates a markdown summary with table.
func formatMarkdown(counts EntityCounts, details []OpDetail) string {
	var sb strings.Builder

	// Summary line
	sb.WriteString("## Summary\n\n")
	sb.WriteString(formatText(counts))
	sb.WriteString("\n\n")

	if len(details) == 0 {
		return sb.String()
	}

	// Details table
	sb.WriteString("## Details\n\n")
	sb.WriteString("| Entity | Op | ID | Path / Title |\n")
	sb.WriteString("|--------|----|----|-------------|\n")

	for _, d := range details {
		entity := d.Entity
		op := d.Op
		id := d.ID
		if id == "" {
			id = d.UUID[:8] + "..."
		}

		pathOrTitle := d.Path
		if pathOrTitle == "" {
			pathOrTitle = d.Title
		}
		if pathOrTitle == "" {
			pathOrTitle = "-"
		}

		// For field-level changes, show the field
		if d.Field != "" && d.NewValue != "" {
			pathOrTitle = fmt.Sprintf("%s: `%s`", d.Field, d.NewValue)
		}

		// Escape pipe characters in table
		pathOrTitle = strings.ReplaceAll(pathOrTitle, "|", "\\|")

		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", entity, op, id, pathOrTitle))
	}

	return sb.String()
}
