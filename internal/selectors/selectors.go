package selectors

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
)

// PathResolution contains the result of resolving a container path
type PathResolution struct {
	UUID       string  // UUID of the resolved container (empty for root)
	FriendlyID string  // Friendly ID (e.g., P-00001)
	ParentUUID *string // Parent container UUID (nil for root containers)
}

// WalkContainerPath walks through container path segments and returns the final container.
// This is the canonical helper for container path traversal - use it instead of
// duplicating the SQL pattern in command implementations.
//
// Returns (uuid, friendlyID, error). For empty paths, returns ("", "", nil) indicating root.
func WalkContainerPath(database *db.DB, path string) (string, string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		// Empty path means root
		return "", "", nil
	}

	var currentUUID *string
	var friendlyID string

	for i, segment := range segments {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}

		query := `SELECT uuid, id FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if currentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *currentUUID)
		}

		var uuid string
		err = database.QueryRow(query, args...).Scan(&uuid, &friendlyID)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", "", fmt.Errorf("container not found: %s", paths.JoinPath(segments[:i+1]...))
			}
			return "", "", fmt.Errorf("database error: %w", err)
		}
		currentUUID = &uuid
	}

	return *currentUUID, friendlyID, nil
}

// ResolveParentContainer resolves all but the last segment of a path.
// Returns (parentUUID, normalizedFinalSlug, parentFriendlyID, error).
// parentUUID will be nil if the path has only one segment (root level).
//
// This is useful for commands like touch/mkdir that need to find the parent
// container before creating a new resource.
func ResolveParentContainer(database *db.DB, path string) (*string, string, string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return nil, "", "", fmt.Errorf("invalid path: empty")
	}

	// Normalize the final slug
	finalSlug, err := paths.NormalizeSlug(segments[len(segments)-1])
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid slug %q: %w", segments[len(segments)-1], err)
	}

	// If single segment, return nil parent (root level)
	if len(segments) == 1 {
		return nil, finalSlug, "", nil
	}

	// Walk to parent container
	parentPath := paths.JoinPath(segments[:len(segments)-1]...)
	uuid, friendlyID, err := WalkContainerPath(database, parentPath)
	if err != nil {
		return nil, "", "", err
	}

	return &uuid, finalSlug, friendlyID, nil
}

// WalkContainerSegment resolves a single container segment relative to a parent.
// parentUUID should be nil for root-level containers.
// Returns (uuid, friendlyID, error).
func WalkContainerSegment(database *db.DB, slug string, parentUUID *string) (string, string, error) {
	normalizedSlug, err := paths.NormalizeSlug(slug)
	if err != nil {
		return "", "", fmt.Errorf("invalid slug %q: %w", slug, err)
	}

	query := `SELECT uuid, id FROM containers WHERE slug = ? AND `
	args := []interface{}{normalizedSlug}
	if parentUUID == nil {
		query += `parent_uuid IS NULL`
	} else {
		query += `parent_uuid = ?`
		args = append(args, *parentUUID)
	}

	var uuid, friendlyID string
	err = database.QueryRow(query, args...).Scan(&uuid, &friendlyID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", fmt.Errorf("container not found: %s", normalizedSlug)
		}
		return "", "", fmt.Errorf("database error: %w", err)
	}

	return uuid, friendlyID, nil
}

// LookupContainerSegment checks if a container exists without returning an error for not found.
// Returns (uuid, friendlyID, exists). Use this when you want to check existence before creating.
func LookupContainerSegment(database *db.DB, slug string, parentUUID *string) (string, string, bool) {
	normalizedSlug, err := paths.NormalizeSlug(slug)
	if err != nil {
		return "", "", false
	}

	query := `SELECT uuid, id FROM containers WHERE slug = ? AND `
	args := []interface{}{normalizedSlug}
	if parentUUID == nil {
		query += `parent_uuid IS NULL`
	} else {
		query += `parent_uuid = ?`
		args = append(args, *parentUUID)
	}

	var uuid, friendlyID string
	err = database.QueryRow(query, args...).Scan(&uuid, &friendlyID)
	if err != nil {
		return "", "", false
	}

	return uuid, friendlyID, true
}

// ResolveTaskByPath resolves a task by its container path + task slug.
// The path should be in format "container/subcontainer/task-slug".
// Returns (uuid, friendlyID, error).
func ResolveTaskByPath(database *db.DB, path string) (string, string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: empty")
	}

	// Get parent container
	var parentUUID *string
	if len(segments) > 1 {
		parentPath := paths.JoinPath(segments[:len(segments)-1]...)
		uuid, _, err := WalkContainerPath(database, parentPath)
		if err != nil {
			return "", "", err
		}
		parentUUID = &uuid
	}

	// Normalize task slug
	taskSlug := segments[len(segments)-1]
	normalizedSlug, err := paths.NormalizeSlug(taskSlug)
	if err != nil {
		return "", "", fmt.Errorf("invalid task slug %q: %w", taskSlug, err)
	}

	// Find task
	var taskUUID, friendlyID string
	if parentUUID == nil {
		// Try to find in any root container
		err = database.QueryRow(`
			SELECT uuid, id FROM tasks WHERE slug = ? AND project_uuid IN (
				SELECT uuid FROM containers WHERE parent_uuid IS NULL
			) LIMIT 1
		`, normalizedSlug).Scan(&taskUUID, &friendlyID)
	} else {
		err = database.QueryRow(`
			SELECT uuid, id FROM tasks WHERE slug = ? AND project_uuid = ?
		`, normalizedSlug, *parentUUID).Scan(&taskUUID, &friendlyID)
	}

	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", fmt.Errorf("task not found: %s", path)
		}
		return "", "", fmt.Errorf("database error: %w", err)
	}

	return taskUUID, friendlyID, nil
}

// Type represents the type of resource being selected
type Type string

const (
	TypeTask      Type = "task"
	TypeContainer Type = "container"
	TypeComment   Type = "comment"
	TypeAuto      Type = "auto" // Auto-detect based on selector
)

// Selector represents a parsed typed selector
type Selector struct {
	Type  Type
	Token string // The part after the prefix (e.g., "T-00123" from "t:T-00123")
}

// Parse parses a selector string and returns the type and token
// Supports: t:<token>, c:<token>, or plain <token> (auto-detect)
func Parse(selector string) Selector {
	// Check for typed prefix
	if strings.HasPrefix(selector, "t:") {
		return Selector{
			Type:  TypeTask,
			Token: strings.TrimPrefix(selector, "t:"),
		}
	}

	if strings.HasPrefix(selector, "c:") {
		return Selector{
			Type:  TypeComment,
			Token: strings.TrimPrefix(selector, "c:"),
		}
	}

	// No prefix - auto-detect
	return Selector{
		Type:  TypeAuto,
		Token: selector,
	}
}

// ResolveTask resolves a task selector to its UUID.
// Supports friendly IDs (T-00001), UUIDs, and paths (container/task-slug).
// Returns (uuid, friendlyID, error).
func ResolveTask(database *db.DB, selector string) (string, string, error) {
	parsed := Parse(selector)

	// Validate type
	if parsed.Type != TypeTask && parsed.Type != TypeAuto {
		return "", "", fmt.Errorf("expected task selector (t:), got %s selector", parsed.Type)
	}

	token := parsed.Token

	// Try as friendly ID
	if strings.HasPrefix(token, "T-") {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", token).Scan(&uuid)
		if err == nil {
			return uuid, token, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("task not found: %s", token)
	}

	// Try as UUID
	if len(token) == 36 && strings.Count(token, "-") == 4 {
		var uuid, friendlyID string
		err := database.QueryRow("SELECT uuid, id FROM tasks WHERE uuid = ?", token).Scan(&uuid, &friendlyID)
		if err == nil {
			return uuid, friendlyID, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("task not found: %s", token)
	}

	// Try as path - use the shared helper
	return ResolveTaskByPath(database, token)
}

// ResolveContainer resolves a container selector to its UUID.
// Supports friendly IDs (P-00001), UUIDs, and paths (project/subproject).
// Returns (uuid, friendlyID, error).
func ResolveContainer(database *db.DB, selector string) (string, string, error) {
	parsed := Parse(selector)

	// Validate type (currently containers don't have a c: prefix, that's for comments)
	// We support auto-detect mode for backward compatibility
	if parsed.Type == TypeComment {
		return "", "", fmt.Errorf("expected container selector, got comment selector (c:)")
	}
	if parsed.Type == TypeTask {
		return "", "", fmt.Errorf("expected container selector, got task selector (t:)")
	}

	token := parsed.Token

	// Try as friendly ID
	if strings.HasPrefix(token, "P-") {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM containers WHERE id = ?", token).Scan(&uuid)
		if err == nil {
			return uuid, token, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("container not found: %s", token)
	}

	// Try as UUID
	if len(token) == 36 && strings.Count(token, "-") == 4 {
		var uuid, friendlyID string
		err := database.QueryRow("SELECT uuid, id FROM containers WHERE uuid = ?", token).Scan(&uuid, &friendlyID)
		if err == nil {
			return uuid, friendlyID, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("container not found: %s", token)
	}

	// Try as path - use the shared helper
	segments := paths.SplitPath(token)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: %s", token)
	}

	return WalkContainerPath(database, token)
}

// ResolveComment resolves a comment selector to its UUID
// Returns (uuid, friendlyID, error)
func ResolveComment(database *db.DB, selector string) (string, string, error) {
	parsed := Parse(selector)

	// Validate type
	if parsed.Type != TypeComment && parsed.Type != TypeAuto {
		return "", "", fmt.Errorf("expected comment selector (c:), got %s selector", parsed.Type)
	}

	token := parsed.Token

	// Try as friendly ID
	if strings.HasPrefix(token, "C-") {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM comments WHERE id = ? AND deleted_at IS NULL", token).Scan(&uuid)
		if err == nil {
			return uuid, token, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("comment not found: %s", token)
	}

	// Try as UUID
	if len(token) == 36 && strings.Count(token, "-") == 4 {
		var uuid, friendlyID string
		err := database.QueryRow("SELECT uuid, id FROM comments WHERE uuid = ? AND deleted_at IS NULL", token).Scan(&uuid, &friendlyID)
		if err == nil {
			return uuid, friendlyID, nil
		}
		if err != sql.ErrNoRows {
			return "", "", fmt.Errorf("database error: %w", err)
		}
		return "", "", fmt.Errorf("comment not found: %s", token)
	}

	return "", "", fmt.Errorf("invalid comment selector: %s (expected C-00001 or UUID)", token)
}
