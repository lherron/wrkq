package cursor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Cursor represents a pagination cursor with sort fields and last seen values
type Cursor struct {
	SortFields []string      `json:"sort_fields"`
	LastValues []interface{} `json:"last_values"`
	LastID     string        `json:"last_id"`
}

// Encode serializes the cursor to an opaque base64 string
func (c *Cursor) Encode() (string, error) {
	if len(c.SortFields) != len(c.LastValues) {
		return "", fmt.Errorf("sort fields and last values length mismatch")
	}

	jsonData, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cursor: %w", err)
	}

	encoded := base64.URLEncoding.EncodeToString(jsonData)
	return encoded, nil
}

// Decode deserializes a cursor from an opaque base64 string
func Decode(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, fmt.Errorf("empty cursor string")
	}

	jsonData, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}

	var c Cursor
	if err := json.Unmarshal(jsonData, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor format: %w", err)
	}

	// Validate cursor structure
	if len(c.SortFields) == 0 {
		return nil, fmt.Errorf("cursor missing sort fields")
	}
	if len(c.SortFields) != len(c.LastValues) {
		return nil, fmt.Errorf("cursor sort fields and values length mismatch")
	}
	if c.LastID == "" {
		return nil, fmt.Errorf("cursor missing last ID")
	}

	return &c, nil
}

// BuildWhereClause constructs a SQL WHERE clause for cursor-based pagination
// Returns the WHERE clause string and slice of parameters
// For ORDER BY a DESC, b DESC, id DESC, generates:
//   WHERE (a < ?) OR (a = ? AND b < ?) OR (a = ? AND b = ? AND id < ?)
func (c *Cursor) BuildWhereClause(descending []bool) (string, []interface{}, error) {
	if len(c.SortFields) != len(descending) {
		return "", nil, fmt.Errorf("sort fields and descending flags length mismatch")
	}

	var params []interface{}
	var orConditions []string

	// Build OR conditions for each level
	// Level i: equality on fields 0..i-1, comparison on field i
	for i := 0; i < len(c.SortFields); i++ {
		var andParts []string

		// Add equality conditions for all previous fields
		for j := 0; j < i; j++ {
			andParts = append(andParts, fmt.Sprintf("%s = ?", c.SortFields[j]))
			params = append(params, c.LastValues[j])
		}

		// Add comparison for current field
		op := ">"
		if descending[i] {
			op = "<"
		}
		andParts = append(andParts, fmt.Sprintf("%s %s ?", c.SortFields[i], op))
		params = append(params, c.LastValues[i])

		// Combine with AND
		if len(andParts) == 1 {
			orConditions = append(orConditions, andParts[0])
		} else {
			condition := "("
			for idx, part := range andParts {
				if idx > 0 {
					condition += " AND "
				}
				condition += part
			}
			condition += ")"
			orConditions = append(orConditions, condition)
		}
	}

	// Add final tie-breaker with ID (always include)
	var andParts []string
	for j := 0; j < len(c.SortFields); j++ {
		andParts = append(andParts, fmt.Sprintf("%s = ?", c.SortFields[j]))
		params = append(params, c.LastValues[j])
	}

	// ID tie-breaker uses same direction as last sort field
	op := ">"
	if len(descending) > 0 && descending[len(descending)-1] {
		op = "<"
	}
	andParts = append(andParts, fmt.Sprintf("id %s ?", op))
	params = append(params, c.LastID)

	condition := "("
	for idx, part := range andParts {
		if idx > 0 {
			condition += " AND "
		}
		condition += part
	}
	condition += ")"
	orConditions = append(orConditions, condition)

	// Combine all conditions with OR
	whereClause := "("
	for idx, condition := range orConditions {
		if idx > 0 {
			whereClause += " OR "
		}
		whereClause += condition
	}
	whereClause += ")"

	return whereClause, params, nil
}

// NewCursor creates a new cursor from the last row values
func NewCursor(sortFields []string, lastValues []interface{}, lastID string) (*Cursor, error) {
	if len(sortFields) != len(lastValues) {
		return nil, fmt.Errorf("sort fields and last values length mismatch")
	}
	if lastID == "" {
		return nil, fmt.Errorf("last ID required")
	}

	return &Cursor{
		SortFields: sortFields,
		LastValues: lastValues,
		LastID:     lastID,
	}, nil
}
