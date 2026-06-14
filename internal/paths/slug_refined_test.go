package paths

import (
	"testing"
)

// TestNewSlug_Rejects verifies that NewSlug returns an error for any input that is
// not already a well-formed slug. Unlike NormalizeSlug, NewSlug does NOT normalise
// (no lowercasing, no space→hyphen). It validates and wraps — or rejects.
//
// Target API: NewSlug(string) (Slug, error)
// A Slug value is always valid; it cannot be constructed from an invalid string.
func TestNewSlug_Rejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"uppercase", "UPPER"},
		{"mixed case", "Bad-Slug"},
		{"spaces and caps", "Bad Slug"},
		{"spaces only", "bad slug"},
		{"underscores", "bad_slug"},
		{"starts with hyphen", "-bad"},
		{"special chars", "bad@slug"},
		{"pure hyphen", "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewSlug(tc.input)
			if err == nil {
				t.Errorf("NewSlug(%q) expected error, got nil", tc.input)
			}
		})
	}
}

// TestNewSlug_Accepts verifies that valid slug strings produce a Slug whose
// underlying string value equals the input verbatim (no mutation).
func TestNewSlug_Accepts(t *testing.T) {
	cases := []string{
		"valid-slug-1",
		"abc",
		"hello-world",
		"123",
		"a",
		"my-task-slug",
		"agent-enablement",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			s, err := NewSlug(input)
			if err != nil {
				t.Fatalf("NewSlug(%q) unexpected error: %v", input, err)
			}
			if string(s) != input {
				t.Errorf("string(NewSlug(%q)) = %q, want %q (NewSlug must not normalise)", input, string(s), input)
			}
			// Type proof: paths.Slug is a named type, not an alias.
			// This call only compiles if s is assignable to paths.Slug.
			requireSlug(s)
		})
	}
}

func requireSlug(Slug) {}
