package paths

import (
	"strings"
	"testing"
)

func TestNormalizeSlug(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		// Valid inputs
		{
			name:  "simple lowercase",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "uppercase to lowercase",
			input: "Hello",
			want:  "hello",
		},
		{
			name:  "with numbers",
			input: "hello123",
			want:  "hello123",
		},
		{
			name:  "with hyphens",
			input: "hello-world",
			want:  "hello-world",
		},
		{
			name:  "spaces to hyphens",
			input: "hello world",
			want:  "hello-world",
		},
		{
			name:  "underscores to hyphens",
			input: "hello_world",
			want:  "hello-world",
		},
		{
			name:  "mixed case and spaces",
			input: "Hello World Test",
			want:  "hello-world-test",
		},
		{
			name:  "removes invalid characters",
			input: "hello@world!",
			want:  "helloworld",
		},
		{
			name:  "leading/trailing hyphens removed",
			input: "-hello-",
			want:  "hello",
		},
		{
			name:  "multiple spaces",
			input: "hello   world",
			want:  "hello---world",
		},
		{
			name:  "starts with number",
			input: "123hello",
			want:  "123hello",
		},

		// Invalid inputs
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only special characters",
			input:   "@@@",
			wantErr: true,
		},
		{
			name:    "only hyphens",
			input:   "---",
			wantErr: true,
		},
		{
			name:    "starts with hyphen only",
			input:   "-",
			wantErr: true,
		},
		{
			name:    "too long",
			input:   strings.Repeat("a", 256),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSlug(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NormalizeSlug() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("NormalizeSlug() unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeSlug() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid
		{name: "simple lowercase", input: "hello", wantErr: false},
		{name: "with numbers", input: "hello123", wantErr: false},
		{name: "with hyphens", input: "hello-world", wantErr: false},
		{name: "starts with number", input: "123hello", wantErr: false},
		{name: "single char", input: "a", wantErr: false},
		{name: "max length", input: strings.Repeat("a", 255), wantErr: false},
		{name: "ends with hyphen", input: "hello-", wantErr: false}, // allowed by regex pattern

		// Invalid
		{name: "empty", input: "", wantErr: true},
		{name: "uppercase", input: "Hello", wantErr: true},
		{name: "spaces", input: "hello world", wantErr: true},
		{name: "underscores", input: "hello_world", wantErr: true},
		{name: "special chars", input: "hello@world", wantErr: true},
		{name: "starts with hyphen", input: "-hello", wantErr: true},
		{name: "too long", input: strings.Repeat("a", 256), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSlug(tt.input)
			if tt.wantErr && err == nil {
				t.Error("ValidateSlug() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateSlug() unexpected error: %v", err)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple path",
			input: "a/b/c",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "single segment",
			input: "hello",
			want:  []string{"hello"},
		},
		{
			name:  "with leading slash",
			input: "/a/b/c",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "with trailing slash",
			input: "a/b/c/",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "with leading and trailing slash",
			input: "/a/b/c/",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "just slashes",
			input: "///",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitPath(tt.input)
			if !stringSliceEqual(got, tt.want) {
				t.Errorf("SplitPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJoinPath(t *testing.T) {
	tests := []struct {
		name     string
		segments []string
		want     string
	}{
		{
			name:     "simple segments",
			segments: []string{"a", "b", "c"},
			want:     "a/b/c",
		},
		{
			name:     "single segment",
			segments: []string{"hello"},
			want:     "hello",
		},
		{
			name:     "empty slice",
			segments: []string{},
			want:     "",
		},
		{
			name:     "with empty strings",
			segments: []string{"a", "", "c"},
			want:     "a//c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JoinPath(tt.segments...)
			if got != tt.want {
				t.Errorf("JoinPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Property-based tests for slug normalization
func TestSlugNormalizationProperties(t *testing.T) {
	t.Run("idempotent normalization", func(t *testing.T) {
		inputs := []string{
			"hello",
			"Hello World",
			"test_case",
			"mix123-ABC",
		}

		for _, input := range inputs {
			normalized1, err1 := NormalizeSlug(input)
			if err1 != nil {
				continue // Skip invalid inputs
			}

			// Normalizing twice should give the same result
			normalized2, err2 := NormalizeSlug(normalized1)
			if err2 != nil {
				t.Errorf("Second normalization failed for %q: %v", normalized1, err2)
			}

			if normalized1 != normalized2 {
				t.Errorf("Normalization not idempotent: %q -> %q -> %q", input, normalized1, normalized2)
			}
		}
	})

	t.Run("normalized slug is always valid", func(t *testing.T) {
		inputs := []string{
			"hello",
			"Hello World",
			"test_case",
			"mix123-ABC",
			"UPPERCASE",
		}

		for _, input := range inputs {
			normalized, err := NormalizeSlug(input)
			if err != nil {
				continue // Skip invalid inputs
			}

			// Every normalized slug should pass validation
			if err := ValidateSlug(normalized); err != nil {
				t.Errorf("Normalized slug %q failed validation: %v", normalized, err)
			}
		}
	})

	t.Run("always lowercase", func(t *testing.T) {
		inputs := []string{
			"HELLO",
			"Hello",
			"hElLo",
			"HELLO-WORLD",
		}

		for _, input := range inputs {
			normalized, err := NormalizeSlug(input)
			if err != nil {
				continue
			}

			if normalized != strings.ToLower(normalized) {
				t.Errorf("Normalized slug %q is not lowercase", normalized)
			}
		}
	})
}

// Helper function
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
