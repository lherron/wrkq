package id

import (
	"testing"
)

func TestFormatFunctions(t *testing.T) {
	tests := []struct {
		name string
		fn   func(int) string
		seq  int
		want string
	}{
		{
			name: "FormatActor with seq 1",
			fn:   FormatActor,
			seq:  1,
			want: "A-00001",
		},
		{
			name: "FormatActor with seq 12345",
			fn:   FormatActor,
			seq:  12345,
			want: "A-12345",
		},
		{
			name: "FormatContainer with seq 1",
			fn:   FormatContainer,
			seq:  1,
			want: "P-00001",
		},
		{
			name: "FormatContainer with seq 99999",
			fn:   FormatContainer,
			seq:  99999,
			want: "P-99999",
		},
		{
			name: "FormatTask with seq 1",
			fn:   FormatTask,
			seq:  1,
			want: "T-00001",
		},
		{
			name: "FormatTask with seq 42",
			fn:   FormatTask,
			seq:  42,
			want: "T-00042",
		},
		{
			name: "FormatComment with seq 1",
			fn:   FormatComment,
			seq:  1,
			want: "C-00001",
		},
		{
			name: "FormatAttachment with seq 1",
			fn:   FormatAttachment,
			seq:  1,
			want: "ATT-00001",
		},
		{
			name: "FormatAttachment with seq 123",
			fn:   FormatAttachment,
			seq:  123,
			want: "ATT-00123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn(tt.seq)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantType    Type
		wantSeq     int
		wantErr     bool
	}{
		// Valid IDs
		{
			name:     "actor ID",
			input:    "A-00001",
			wantType: TypeActor,
			wantSeq:  1,
		},
		{
			name:     "actor ID with large seq",
			input:    "A-12345",
			wantType: TypeActor,
			wantSeq:  12345,
		},
		{
			name:     "container ID",
			input:    "P-00001",
			wantType: TypeContainer,
			wantSeq:  1,
		},
		{
			name:     "task ID",
			input:    "T-00001",
			wantType: TypeTask,
			wantSeq:  1,
		},
		{
			name:     "task ID with seq 42",
			input:    "T-00042",
			wantType: TypeTask,
			wantSeq:  42,
		},
		{
			name:     "comment ID",
			input:    "C-00001",
			wantType: TypeComment,
			wantSeq:  1,
		},
		{
			name:     "attachment ID",
			input:    "ATT-00001",
			wantType: TypeAttachment,
			wantSeq:  1,
		},
		{
			name:     "attachment ID with seq 123",
			input:    "ATT-00123",
			wantType: TypeAttachment,
			wantSeq:  123,
		},
		{
			name:     "with whitespace",
			input:    "  T-00001  ",
			wantType: TypeTask,
			wantSeq:  1,
		},

		// Invalid IDs
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "wrong format",
			input:   "INVALID",
			wantErr: true,
		},
		{
			name:    "missing hyphen",
			input:   "T00001",
			wantErr: true,
		},
		{
			name:    "wrong number of digits",
			input:   "T-001",
			wantErr: true,
		},
		{
			name:    "wrong number of digits (too many)",
			input:   "T-000001",
			wantErr: true,
		},
		{
			name:    "lowercase prefix",
			input:   "t-00001",
			wantErr: true,
		},
		{
			name:    "non-numeric sequence",
			input:   "T-ABCDE",
			wantErr: true,
		},
		{
			name:    "unknown prefix",
			input:   "X-00001",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotSeq, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("Parse() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Parse() unexpected error: %v", err)
				return
			}
			if gotType != tt.wantType {
				t.Errorf("Parse() type = %v, want %v", gotType, tt.wantType)
			}
			if gotSeq != tt.wantSeq {
				t.Errorf("Parse() seq = %v, want %v", gotSeq, tt.wantSeq)
			}
		})
	}
}

func TestIsUUID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid UUID v4",
			input: "123e4567-e89b-12d3-a456-426614174000",
			want:  true,
		},
		{
			name:  "valid UUID uppercase",
			input: "123E4567-E89B-12D3-A456-426614174000",
			want:  true,
		},
		{
			name:  "invalid - missing hyphens",
			input: "123e4567e89b12d3a456426614174000",
			want:  false,
		},
		{
			name:  "invalid - wrong format",
			input: "not-a-uuid",
			want:  false,
		},
		{
			name:  "invalid - too short",
			input: "123e4567-e89b-12d3",
			want:  false,
		},
		{
			name:  "invalid - too long",
			input: "123e4567-e89b-12d3-a456-426614174000-extra",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUUID(tt.input)
			if got != tt.want {
				t.Errorf("IsUUID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsFriendlyID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "actor ID", input: "A-00001", want: true},
		{name: "container ID", input: "P-00001", want: true},
		{name: "task ID", input: "T-00001", want: true},
		{name: "comment ID", input: "C-00001", want: true},
		{name: "attachment ID", input: "ATT-00001", want: true},
		{name: "invalid format", input: "INVALID", want: false},
		{name: "UUID", input: "123e4567-e89b-12d3-a456-426614174000", want: false},
		{name: "empty", input: "", want: false},
		{name: "lowercase", input: "t-00001", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsFriendlyID(tt.input)
			if got != tt.want {
				t.Errorf("IsFriendlyID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Property-based tests
func TestIDFormatParseRoundtrip(t *testing.T) {
	tests := []struct {
		name     string
		formatFn func(int) string
		idType   Type
		seqs     []int
	}{
		{
			name:     "actor IDs",
			formatFn: FormatActor,
			idType:   TypeActor,
			seqs:     []int{1, 42, 100, 12345, 99999},
		},
		{
			name:     "container IDs",
			formatFn: FormatContainer,
			idType:   TypeContainer,
			seqs:     []int{1, 42, 100, 12345, 99999},
		},
		{
			name:     "task IDs",
			formatFn: FormatTask,
			idType:   TypeTask,
			seqs:     []int{1, 42, 100, 12345, 99999},
		},
		{
			name:     "comment IDs",
			formatFn: FormatComment,
			idType:   TypeComment,
			seqs:     []int{1, 42, 100, 12345, 99999},
		},
		{
			name:     "attachment IDs",
			formatFn: FormatAttachment,
			idType:   TypeAttachment,
			seqs:     []int{1, 42, 100, 12345, 99999},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, seq := range tt.seqs {
				// Format -> Parse -> should get same type and seq
				formatted := tt.formatFn(seq)
				gotType, gotSeq, err := Parse(formatted)
				if err != nil {
					t.Errorf("Parse(%q) error: %v", formatted, err)
					continue
				}
				if gotType != tt.idType {
					t.Errorf("Parse(%q) type = %v, want %v", formatted, gotType, tt.idType)
				}
				if gotSeq != seq {
					t.Errorf("Parse(%q) seq = %v, want %v", formatted, gotSeq, seq)
				}
			}
		})
	}
}

// Benchmark tests
func BenchmarkFormatTask(b *testing.B) {
	for i := 0; i < b.N; i++ {
		FormatTask(12345)
	}
}

func BenchmarkParse(b *testing.B) {
	id := "T-12345"
	for i := 0; i < b.N; i++ {
		Parse(id)
	}
}

func BenchmarkIsUUID(b *testing.B) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	for i := 0; i < b.N; i++ {
		IsUUID(uuid)
	}
}

func BenchmarkIsFriendlyID(b *testing.B) {
	id := "T-12345"
	for i := 0; i < b.N; i++ {
		IsFriendlyID(id)
	}
}
