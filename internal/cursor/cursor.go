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

// ApplyOptions configures how pagination is applied to a query.
type ApplyOptions struct {
	// SortFields are the column names to sort by (e.g., "updated_at", "id").
	// These are the logical field names stored in the cursor.
	SortFields []string
	// SQLFields are the SQL column expressions for each sort field.
	// If not provided, SortFields are used directly.
	// Use this when you need table-qualified names (e.g., "e.id" instead of "id").
	SQLFields []string
	// Descending specifies sort direction for each field (true = DESC, false = ASC).
	Descending []bool
	// IDField is the column name used as the unique tie-breaker (defaults to "id").
	// This is the SQL expression, not the cursor field name.
	IDField string
	// Limit is the maximum number of rows to return. 0 means no limit.
	Limit int
}

// ApplyResult contains the result of applying pagination to a query.
type ApplyResult struct {
	// WhereClause is the WHERE condition to AND with existing conditions.
	// Empty string if no cursor was provided.
	WhereClause string
	// Params are the placeholder values for the WHERE clause.
	Params []interface{}
	// OrderByClause is the ORDER BY clause (including "ORDER BY" prefix).
	OrderByClause string
	// LimitClause is the LIMIT clause (including "LIMIT" prefix).
	// Empty string if limit is 0.
	LimitClause string
	// LimitParam is the limit value (nil if no limit).
	LimitParam *int
}

// Apply prepares cursor-based pagination components for a SQL query.
// It returns WHERE, ORDER BY, and LIMIT clause fragments that can be appended
// to a base query. The caller is responsible for combining these with their
// existing query structure.
//
// Example usage:
//
//	opts := cursor.ApplyOptions{
//	    SortFields: []string{"updated_at"},
//	    Descending: []bool{true},
//	    IDField:    "id",
//	    Limit:      50,
//	}
//	result, err := cursor.Apply(cursorStr, opts)
//	if err != nil {
//	    return err
//	}
//
//	query := "SELECT * FROM tasks WHERE project_uuid = ?"
//	args := []interface{}{projectUUID}
//
//	if result.WhereClause != "" {
//	    query += " AND " + result.WhereClause
//	    args = append(args, result.Params...)
//	}
//	query += " " + result.OrderByClause
//	if result.LimitClause != "" {
//	    query += " " + result.LimitClause
//	    args = append(args, *result.LimitParam)
//	}
func Apply(cursorStr string, opts ApplyOptions) (*ApplyResult, error) {
	if len(opts.SortFields) == 0 {
		return nil, fmt.Errorf("at least one sort field is required")
	}
	if len(opts.SortFields) != len(opts.Descending) {
		return nil, fmt.Errorf("sort fields and descending flags length mismatch")
	}

	// SQLFields defaults to SortFields if not provided
	sqlFields := opts.SQLFields
	if len(sqlFields) == 0 {
		sqlFields = opts.SortFields
	} else if len(sqlFields) != len(opts.SortFields) {
		return nil, fmt.Errorf("SQL fields and sort fields length mismatch")
	}

	idField := opts.IDField
	if idField == "" {
		idField = "id"
	}

	result := &ApplyResult{}

	// Build ORDER BY clause using SQL field names
	var orderParts []string
	for i, field := range sqlFields {
		dir := "ASC"
		if opts.Descending[i] {
			dir = "DESC"
		}
		orderParts = append(orderParts, fmt.Sprintf("%s %s", field, dir))
	}
	// Add ID as final tie-breaker with same direction as last sort field
	idDir := "ASC"
	if len(opts.Descending) > 0 && opts.Descending[len(opts.Descending)-1] {
		idDir = "DESC"
	}
	orderParts = append(orderParts, fmt.Sprintf("%s %s", idField, idDir))
	result.OrderByClause = "ORDER BY " + joinStrings(orderParts, ", ")

	// Build LIMIT clause
	if opts.Limit > 0 {
		// Request one extra row to detect if there are more results
		limitVal := opts.Limit + 1
		result.LimitClause = "LIMIT ?"
		result.LimitParam = &limitVal
	}

	// Build WHERE clause if cursor provided
	if cursorStr != "" {
		c, err := Decode(cursorStr)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor: %w", err)
		}

		// Validate cursor matches our sort fields (logical names)
		if len(c.SortFields) != len(opts.SortFields) {
			return nil, fmt.Errorf("cursor sort fields don't match query: got %d, want %d",
				len(c.SortFields), len(opts.SortFields))
		}
		for i, field := range c.SortFields {
			if field != opts.SortFields[i] {
				return nil, fmt.Errorf("cursor sort field mismatch at position %d: got %q, want %q",
					i, field, opts.SortFields[i])
			}
		}

		// Build WHERE clause using SQL field names
		whereClause, params, err := buildWhereClauseWithFields(c, sqlFields, idField, opts.Descending)
		if err != nil {
			return nil, fmt.Errorf("failed to build cursor WHERE clause: %w", err)
		}

		result.WhereClause = whereClause
		result.Params = params
	}

	return result, nil
}

// buildWhereClauseWithFields is like BuildWhereClause but uses explicit SQL field names
func buildWhereClauseWithFields(c *Cursor, sqlFields []string, idField string, descending []bool) (string, []interface{}, error) {
	if len(sqlFields) != len(descending) {
		return "", nil, fmt.Errorf("sql fields and descending flags length mismatch")
	}

	var params []interface{}
	var orConditions []string

	// Build OR conditions for each level
	// Level i: equality on fields 0..i-1, comparison on field i
	for i := 0; i < len(sqlFields); i++ {
		var andParts []string

		// Add equality conditions for all previous fields
		for j := 0; j < i; j++ {
			andParts = append(andParts, fmt.Sprintf("%s = ?", sqlFields[j]))
			params = append(params, c.LastValues[j])
		}

		// Add comparison for current field
		op := ">"
		if descending[i] {
			op = "<"
		}
		andParts = append(andParts, fmt.Sprintf("%s %s ?", sqlFields[i], op))
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
	for j := 0; j < len(sqlFields); j++ {
		andParts = append(andParts, fmt.Sprintf("%s = ?", sqlFields[j]))
		params = append(params, c.LastValues[j])
	}

	// ID tie-breaker uses same direction as last sort field
	op := ">"
	if len(descending) > 0 && descending[len(descending)-1] {
		op = "<"
	}
	andParts = append(andParts, fmt.Sprintf("%s %s ?", idField, op))
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

// BuildNextCursor creates an encoded cursor from the last row of results.
// Returns empty string if there are no results.
// The values slice should contain the sort field values followed by the ID value.
func BuildNextCursor(sortFields []string, values []interface{}, lastID string) (string, error) {
	if len(values) != len(sortFields) {
		return "", fmt.Errorf("values and sort fields length mismatch: got %d values, want %d",
			len(values), len(sortFields))
	}
	if lastID == "" {
		return "", fmt.Errorf("last ID required")
	}

	c, err := NewCursor(sortFields, values, lastID)
	if err != nil {
		return "", err
	}

	return c.Encode()
}

// Helper to join strings (avoiding import of strings package)
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
