package selectors

import (
	"strings"
)

// topLevelClause selects top-level project containers (direct children of the
// singleton root). It replaces the legacy `parent_uuid IS NULL` test now that the
// root is the only null-parent row. kind='root' is the authoritative root locator.
const topLevelClause = "parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')"

// PathResolution contains the result of resolving a container path
type PathResolution struct {
	UUID       string  // UUID of the resolved container (empty for root)
	FriendlyID string  // Friendly ID (e.g., P-00001)
	ParentUUID *string // Parent container UUID (nil for root containers)
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
