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
