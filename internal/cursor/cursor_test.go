package cursor

import (
	"strings"
	"testing"
)

func TestCursorEncodeDecode(t *testing.T) {
	tests := []struct {
		name   string
		cursor *Cursor
	}{
		{
			name: "single field",
			cursor: &Cursor{
				SortFields: []string{"updated_at"},
				LastValues: []interface{}{"2025-11-19T10:00:00Z"},
				LastID:     "T-00123",
			},
		},
		{
			name: "multiple fields",
			cursor: &Cursor{
				SortFields: []string{"updated_at", "priority"},
				LastValues: []interface{}{"2025-11-19T10:00:00Z", float64(1)},
				LastID:     "T-00456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			encoded, err := tt.cursor.Encode()
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			// Should be non-empty
			if encoded == "" {
				t.Error("Encoded cursor is empty")
			}

			// Decode
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			// Compare fields
			if len(decoded.SortFields) != len(tt.cursor.SortFields) {
				t.Errorf("SortFields length mismatch: got %d, want %d",
					len(decoded.SortFields), len(tt.cursor.SortFields))
			}
			for i := range decoded.SortFields {
				if decoded.SortFields[i] != tt.cursor.SortFields[i] {
					t.Errorf("SortFields[%d] mismatch: got %s, want %s",
						i, decoded.SortFields[i], tt.cursor.SortFields[i])
				}
			}

			if decoded.LastID != tt.cursor.LastID {
				t.Errorf("LastID mismatch: got %s, want %s",
					decoded.LastID, tt.cursor.LastID)
			}
		})
	}
}

func TestDecodeInvalid(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		wantErr string
	}{
		{
			name:    "empty string",
			encoded: "",
			wantErr: "empty cursor",
		},
		{
			name:    "invalid base64",
			encoded: "not-valid-base64!!!",
			wantErr: "invalid cursor encoding",
		},
		{
			name:    "invalid json",
			encoded: "bm90LWpzb24=", // "not-json" in base64
			wantErr: "invalid cursor format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.encoded)
			if err == nil {
				t.Error("Expected error but got none")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Error message mismatch: got %q, want substring %q",
					err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildWhereClause(t *testing.T) {
	tests := []struct {
		name       string
		cursor     *Cursor
		descending []bool
		wantSQL    string
		wantParams int
	}{
		{
			name: "single field DESC",
			cursor: &Cursor{
				SortFields: []string{"updated_at"},
				LastValues: []interface{}{"2025-11-19T10:00:00Z"},
				LastID:     "T-00123",
			},
			descending: []bool{true},
			wantSQL:    "updated_at < ?",
			wantParams: 3, // updated_at value, updated_at value again for ID tie-breaker, lastID
		},
		{
			name: "single field ASC",
			cursor: &Cursor{
				SortFields: []string{"updated_at"},
				LastValues: []interface{}{"2025-11-19T10:00:00Z"},
				LastID:     "T-00123",
			},
			descending: []bool{false},
			wantSQL:    "updated_at > ?",
			wantParams: 3,
		},
		{
			name: "multiple fields DESC",
			cursor: &Cursor{
				SortFields: []string{"updated_at", "priority"},
				LastValues: []interface{}{"2025-11-19T10:00:00Z", float64(1)},
				LastID:     "T-00123",
			},
			descending: []bool{true, true},
			wantSQL:    "updated_at < ?",
			wantParams: 6, // 1 + 2 + 3 params from the three OR conditions
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, params, err := tt.cursor.BuildWhereClause(tt.descending)
			if err != nil {
				t.Fatalf("BuildWhereClause failed: %v", err)
			}

			if sql == "" {
				t.Error("Generated SQL is empty")
			}

			// Check that the SQL contains the expected pattern
			if !strings.Contains(sql, tt.wantSQL) {
				t.Errorf("SQL doesn't contain expected pattern: got %q, want substring %q",
					sql, tt.wantSQL)
			}

			if len(params) != tt.wantParams {
				t.Errorf("Parameter count mismatch: got %d, want %d",
					len(params), tt.wantParams)
			}

			// Verify SQL has proper structure
			if !strings.Contains(sql, "(") || !strings.Contains(sql, ")") {
				t.Error("SQL doesn't have proper parentheses")
			}
			if !strings.Contains(sql, " OR ") {
				t.Error("SQL doesn't contain OR conditions")
			}
		})
	}
}

func TestNewCursor(t *testing.T) {
	tests := []struct {
		name       string
		sortFields []string
		lastValues []interface{}
		lastID     string
		wantErr    bool
	}{
		{
			name:       "valid cursor",
			sortFields: []string{"updated_at"},
			lastValues: []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "T-00123",
			wantErr:    false,
		},
		{
			name:       "mismatched lengths",
			sortFields: []string{"updated_at", "priority"},
			lastValues: []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "T-00123",
			wantErr:    true,
		},
		{
			name:       "empty lastID",
			sortFields: []string{"updated_at"},
			lastValues: []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor, err := NewCursor(tt.sortFields, tt.lastValues, tt.lastID)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if cursor == nil {
					t.Error("Cursor is nil")
				}
			}
		})
	}
}

func TestApply(t *testing.T) {
	tests := []struct {
		name           string
		cursorStr      string
		opts           ApplyOptions
		wantOrderBy    string
		wantLimit      string
		wantWhereEmpty bool
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:      "no cursor single field ASC",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"slug"},
				Descending: []bool{false},
				IDField:    "id",
				Limit:      50,
			},
			wantOrderBy:    "ORDER BY slug ASC, id ASC",
			wantLimit:      "LIMIT ?",
			wantWhereEmpty: true,
			wantErr:        false,
		},
		{
			name:      "no cursor single field DESC",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"updated_at"},
				Descending: []bool{true},
				IDField:    "id",
				Limit:      25,
			},
			wantOrderBy:    "ORDER BY updated_at DESC, id DESC",
			wantLimit:      "LIMIT ?",
			wantWhereEmpty: true,
			wantErr:        false,
		},
		{
			name:      "no cursor multiple fields",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"priority", "updated_at"},
				Descending: []bool{true, true},
				IDField:    "id",
				Limit:      100,
			},
			wantOrderBy:    "ORDER BY priority DESC, updated_at DESC, id DESC",
			wantLimit:      "LIMIT ?",
			wantWhereEmpty: true,
			wantErr:        false,
		},
		{
			name:      "no limit",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"slug"},
				Descending: []bool{false},
				IDField:    "id",
				Limit:      0,
			},
			wantOrderBy:    "ORDER BY slug ASC, id ASC",
			wantLimit:      "",
			wantWhereEmpty: true,
			wantErr:        false,
		},
		{
			name:      "with cursor",
			cursorStr: func() string {
				c, _ := NewCursor([]string{"updated_at"}, []interface{}{"2025-11-19T10:00:00Z"}, "T-00123")
				encoded, _ := c.Encode()
				return encoded
			}(),
			opts: ApplyOptions{
				SortFields: []string{"updated_at"},
				Descending: []bool{true},
				IDField:    "id",
				Limit:      50,
			},
			wantOrderBy:    "ORDER BY updated_at DESC, id DESC",
			wantLimit:      "LIMIT ?",
			wantWhereEmpty: false,
			wantErr:        false,
		},
		{
			name:      "default id field",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"created_at"},
				Descending: []bool{false},
				Limit:      10,
			},
			wantOrderBy:    "ORDER BY created_at ASC, id ASC",
			wantLimit:      "LIMIT ?",
			wantWhereEmpty: true,
			wantErr:        false,
		},
		{
			name:      "no sort fields",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{},
				Descending: []bool{},
				Limit:      10,
			},
			wantErr:    true,
			wantErrMsg: "at least one sort field is required",
		},
		{
			name:      "mismatched sort and descending",
			cursorStr: "",
			opts: ApplyOptions{
				SortFields: []string{"slug", "id"},
				Descending: []bool{false},
				Limit:      10,
			},
			wantErr:    true,
			wantErrMsg: "sort fields and descending flags length mismatch",
		},
		{
			name:      "invalid cursor",
			cursorStr: "invalid-cursor",
			opts: ApplyOptions{
				SortFields: []string{"slug"},
				Descending: []bool{false},
				Limit:      10,
			},
			wantErr:    true,
			wantErrMsg: "invalid cursor",
		},
		{
			name: "cursor field mismatch",
			cursorStr: func() string {
				c, _ := NewCursor([]string{"priority"}, []interface{}{1}, "T-00123")
				encoded, _ := c.Encode()
				return encoded
			}(),
			opts: ApplyOptions{
				SortFields: []string{"slug"},
				Descending: []bool{false},
				Limit:      10,
			},
			wantErr:    true,
			wantErrMsg: "cursor sort field mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Apply(tt.cursorStr, tt.opts)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("Error message mismatch: got %q, want substring %q", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.OrderByClause != tt.wantOrderBy {
				t.Errorf("OrderByClause mismatch: got %q, want %q", result.OrderByClause, tt.wantOrderBy)
			}

			if result.LimitClause != tt.wantLimit {
				t.Errorf("LimitClause mismatch: got %q, want %q", result.LimitClause, tt.wantLimit)
			}

			if tt.wantWhereEmpty && result.WhereClause != "" {
				t.Errorf("Expected empty WhereClause but got %q", result.WhereClause)
			}

			if !tt.wantWhereEmpty && result.WhereClause == "" {
				t.Error("Expected non-empty WhereClause but got empty")
			}

			// Check limit param
			if tt.wantLimit != "" && result.LimitParam == nil {
				t.Error("Expected LimitParam but got nil")
			}
			if tt.wantLimit == "" && result.LimitParam != nil {
				t.Error("Expected nil LimitParam but got value")
			}

			// Verify limit is limit+1 (for detecting more results)
			if result.LimitParam != nil && tt.opts.Limit > 0 {
				if *result.LimitParam != tt.opts.Limit+1 {
					t.Errorf("LimitParam should be limit+1: got %d, want %d", *result.LimitParam, tt.opts.Limit+1)
				}
			}
		})
	}
}

func TestApplyWithCursorParams(t *testing.T) {
	// Test that cursor params are correctly generated for SQL
	c, _ := NewCursor([]string{"updated_at"}, []interface{}{"2025-11-19T10:00:00Z"}, "T-00123")
	encoded, _ := c.Encode()

	result, err := Apply(encoded, ApplyOptions{
		SortFields: []string{"updated_at"},
		Descending: []bool{true},
		IDField:    "id",
		Limit:      50,
	})

	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Should have WHERE clause with params
	if result.WhereClause == "" {
		t.Error("Expected WHERE clause but got empty")
	}

	if len(result.Params) == 0 {
		t.Error("Expected params but got empty slice")
	}

	// WHERE clause should reference the sort field
	if !strings.Contains(result.WhereClause, "updated_at") {
		t.Errorf("WHERE clause should contain 'updated_at': %s", result.WhereClause)
	}
}

func TestApplyWithSQLFields(t *testing.T) {
	// Test that SQLFields are used for table-qualified names
	c, _ := NewCursor([]string{"id"}, []interface{}{int64(100)}, "100")
	encoded, _ := c.Encode()

	result, err := Apply(encoded, ApplyOptions{
		SortFields: []string{"id"},
		SQLFields:  []string{"e.id"},
		Descending: []bool{true},
		IDField:    "e.id",
		Limit:      50,
	})

	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// ORDER BY should use SQLFields
	if !strings.Contains(result.OrderByClause, "e.id") {
		t.Errorf("ORDER BY should contain 'e.id': %s", result.OrderByClause)
	}

	// WHERE clause should use SQLFields
	if !strings.Contains(result.WhereClause, "e.id") {
		t.Errorf("WHERE clause should contain 'e.id': %s", result.WhereClause)
	}
}

func TestApplyWithSQLFieldsMismatch(t *testing.T) {
	// Test that mismatched SQLFields and SortFields length returns error
	_, err := Apply("", ApplyOptions{
		SortFields: []string{"id", "updated_at"},
		SQLFields:  []string{"e.id"},
		Descending: []bool{true, true},
		Limit:      50,
	})

	if err == nil {
		t.Error("Expected error for mismatched SQLFields length")
	}
	if !strings.Contains(err.Error(), "SQL fields and sort fields length mismatch") {
		t.Errorf("Wrong error message: %v", err)
	}
}

func TestBuildNextCursor(t *testing.T) {
	tests := []struct {
		name       string
		sortFields []string
		values     []interface{}
		lastID     string
		wantErr    bool
	}{
		{
			name:       "valid single field",
			sortFields: []string{"updated_at"},
			values:     []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "T-00123",
			wantErr:    false,
		},
		{
			name:       "valid multiple fields",
			sortFields: []string{"priority", "updated_at"},
			values:     []interface{}{1, "2025-11-19T10:00:00Z"},
			lastID:     "T-00456",
			wantErr:    false,
		},
		{
			name:       "mismatched lengths",
			sortFields: []string{"updated_at", "priority"},
			values:     []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "T-00123",
			wantErr:    true,
		},
		{
			name:       "empty lastID",
			sortFields: []string{"updated_at"},
			values:     []interface{}{"2025-11-19T10:00:00Z"},
			lastID:     "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := BuildNextCursor(tt.sortFields, tt.values, tt.lastID)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if encoded == "" {
				t.Error("Expected non-empty encoded cursor")
			}

			// Verify roundtrip
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Failed to decode: %v", err)
			}

			if decoded.LastID != tt.lastID {
				t.Errorf("LastID mismatch: got %q, want %q", decoded.LastID, tt.lastID)
			}

			if len(decoded.SortFields) != len(tt.sortFields) {
				t.Errorf("SortFields length mismatch: got %d, want %d",
					len(decoded.SortFields), len(tt.sortFields))
			}
		})
	}
}
