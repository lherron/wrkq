package domain

import (
	"errors"
	"testing"
	"time"
)

func TestValidateState(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		wantErr bool
	}{
		{name: "open", state: "open", wantErr: false},
		{name: "in_progress", state: "in_progress", wantErr: false},
		{name: "completed", state: "completed", wantErr: false},
		{name: "archived", state: "archived", wantErr: false},
		{name: "invalid", state: "invalid", wantErr: true},
		{name: "empty", state: "", wantErr: true},
		{name: "uppercase", state: "OPEN", wantErr: true},
		{name: "mixed case", state: "Open", wantErr: true},
		{name: "uppercase in_progress", state: "IN_PROGRESS", wantErr: true},
		{name: "mixed case in_progress", state: "In_Progress", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateState(tt.state)
			if tt.wantErr && err == nil {
				t.Error("ValidateState() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateState() unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePriority(t *testing.T) {
	tests := []struct {
		name     string
		priority int
		wantErr  bool
	}{
		{name: "priority 1", priority: 1, wantErr: false},
		{name: "priority 2", priority: 2, wantErr: false},
		{name: "priority 3", priority: 3, wantErr: false},
		{name: "priority 4", priority: 4, wantErr: false},
		{name: "too low", priority: 0, wantErr: true},
		{name: "negative", priority: -1, wantErr: true},
		{name: "too high", priority: 5, wantErr: true},
		{name: "way too high", priority: 100, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePriority(tt.priority)
			if tt.wantErr && err == nil {
				t.Error("ValidatePriority() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidatePriority() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateActorRole(t *testing.T) {
	tests := []struct {
		name    string
		role    string
		wantErr bool
	}{
		{name: "human", role: "human", wantErr: false},
		{name: "agent", role: "agent", wantErr: false},
		{name: "system", role: "system", wantErr: false},
		{name: "invalid", role: "invalid", wantErr: true},
		{name: "empty", role: "", wantErr: true},
		{name: "uppercase", role: "HUMAN", wantErr: true},
		{name: "mixed case", role: "Human", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateActorRole(tt.role)
			if tt.wantErr && err == nil {
				t.Error("ValidateActorRole() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateActorRole() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateResourceType(t *testing.T) {
	tests := []struct {
		name    string
		resType string
		wantErr bool
	}{
		{name: "task", resType: "task", wantErr: false},
		{name: "container", resType: "container", wantErr: false},
		{name: "attachment", resType: "attachment", wantErr: false},
		{name: "actor", resType: "actor", wantErr: false},
		{name: "config", resType: "config", wantErr: false},
		{name: "system", resType: "system", wantErr: false},
		{name: "invalid", resType: "invalid", wantErr: true},
		{name: "empty", resType: "", wantErr: true},
		{name: "uppercase", resType: "TASK", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceType(tt.resType)
			if tt.wantErr && err == nil {
				t.Error("ValidateResourceType() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateResourceType() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid RFC3339",
			input:   "2025-01-15T10:30:00Z",
			wantErr: false,
		},
		{
			name:    "valid RFC3339 with timezone",
			input:   "2025-01-15T10:30:00-05:00",
			wantErr: false,
		},
		{
			name:    "valid RFC3339 with milliseconds",
			input:   "2025-01-15T10:30:00.123Z",
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "2025-01-15 10:30:00",
			wantErr: true,
		},
		{
			name:    "invalid date",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedTime, err := ValidateTimestamp(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("ValidateTimestamp() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("ValidateTimestamp() unexpected error: %v", err)
				return
			}
			// Verify the parsed time is reasonable
			if parsedTime.IsZero() {
				t.Error("ValidateTimestamp() returned zero time")
			}
		})
	}
}

func TestCheckETag(t *testing.T) {
	tests := []struct {
		name     string
		expected int64
		actual   int64
		wantErr  bool
	}{
		{
			name:     "matching etags",
			expected: 123,
			actual:   123,
			wantErr:  false,
		},
		{
			name:     "different etags",
			expected: 123,
			actual:   456,
			wantErr:  true,
		},
		{
			name:     "zero etags matching",
			expected: 0,
			actual:   0,
			wantErr:  false,
		},
		{
			name:     "zero vs non-zero",
			expected: 0,
			actual:   1,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckETag(tt.expected, tt.actual)
			if tt.wantErr {
				if err == nil {
					t.Error("CheckETag() expected error, got nil")
					return
				}
				// Verify it's the correct error type
				var etagErr *ETagMismatchError
				if !errors.As(err, &etagErr) {
					t.Errorf("CheckETag() error type = %T, want *ETagMismatchError", err)
					return
				}
				if etagErr.Expected != tt.expected {
					t.Errorf("ETagMismatchError.Expected = %d, want %d", etagErr.Expected, tt.expected)
				}
				if etagErr.Actual != tt.actual {
					t.Errorf("ETagMismatchError.Actual = %d, want %d", etagErr.Actual, tt.actual)
				}
			} else {
				if err != nil {
					t.Errorf("CheckETag() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestETagMismatchError(t *testing.T) {
	err := &ETagMismatchError{
		Expected: 123,
		Actual:   456,
	}

	want := "etag mismatch: expected 123, got 456"
	if got := err.Error(); got != want {
		t.Errorf("ETagMismatchError.Error() = %q, want %q", got, want)
	}
}

// Benchmark tests
func BenchmarkValidateState(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidateState("open")
	}
}

func BenchmarkValidatePriority(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidatePriority(2)
	}
}

func BenchmarkValidateTimestamp(b *testing.B) {
	timestamp := time.Now().Format(time.RFC3339)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ValidateTimestamp(timestamp)
	}
}

func BenchmarkCheckETag(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CheckETag(123, 123)
	}
}
