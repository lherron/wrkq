package selectors

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
)

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

// ResolveTask resolves a task selector to its UUID
// Returns (uuid, friendlyID, error)
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

	// Try as path
	segments := paths.SplitPath(token)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: %s", token)
	}

	// Navigate to parent container
	var parentUUID *string
	for i, segment := range segments[:len(segments)-1] {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}

		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if parentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *parentUUID)
		}

		var uuid string
		err = database.QueryRow(query, args...).Scan(&uuid)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", "", fmt.Errorf("container not found: %s", paths.JoinPath(segments[:i+1]...))
			}
			return "", "", fmt.Errorf("database error: %w", err)
		}
		parentUUID = &uuid
	}

	// Get final segment as task
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
			return "", "", fmt.Errorf("task not found: %s", token)
		}
		return "", "", fmt.Errorf("database error: %w", err)
	}

	return taskUUID, friendlyID, nil
}

// ResolveContainer resolves a container selector to its UUID
// Returns (uuid, friendlyID, error)
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

	// Try as path
	segments := paths.SplitPath(token)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: %s", token)
	}

	// Navigate through container hierarchy
	var currentUUID *string
	for i, segment := range segments {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}

		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if currentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *currentUUID)
		}

		var uuid string
		err = database.QueryRow(query, args...).Scan(&uuid)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", "", fmt.Errorf("container not found: %s", paths.JoinPath(segments[:i+1]...))
			}
			return "", "", fmt.Errorf("database error: %w", err)
		}
		currentUUID = &uuid
	}

	// Get friendly ID for the final container
	var friendlyID string
	err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", *currentUUID).Scan(&friendlyID)
	if err != nil {
		return "", "", fmt.Errorf("database error: %w", err)
	}

	return *currentUUID, friendlyID, nil
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
