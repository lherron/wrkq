package id

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTask(t *testing.T) {
	for _, tt := range []struct{ input, owner, slug string }{
		{"T-12345", "T-12345", ""},
		{"T-12345.a", "T-12345", "a"},
		{"T-12345.architecture-diagram", "T-12345", "architecture-diagram"},
		{"T-12345." + strings.Repeat("a", 56), "T-12345", strings.Repeat("a", 56)},
	} {
		owner, slug, err := ParseTask(tt.input)
		if err != nil || owner != tt.owner || slug != tt.slug {
			t.Fatalf("ParseTask(%q) = %q, %q, %v", tt.input, owner, slug, err)
		}
		if !IsTask(tt.input) {
			t.Fatalf("IsTask(%q) = false", tt.input)
		}
		if IsOrdinaryTask(tt.input) != (tt.slug == "") {
			t.Fatalf("ordinary classification: %q", tt.input)
		}
	}
	for _, input := range []string{"T-12345.2", "T-12345.a.b", "T-12345.slug-", "T-12345.Next", "T-12345.", "T-12345.-a", "T-12345.a_", "T-123456", "t-12345", " T-12345", "T-12345." + strings.Repeat("a", 57)} {
		if _, _, err := ParseTask(input); err == nil || IsTask(input) {
			t.Errorf("accepted invalid task %q", input)
		}
	}
}

func TestFindTaskIDs(t *testing.T) {
	for _, tt := range []struct {
		body string
		want []string
	}{
		{"see T-12345.", []string{"T-12345"}},
		{"T-12345.Next", []string{"T-12345"}},
		{"see T-12345.render-preview and T-12345.a", []string{"T-12345.render-preview", "T-12345.a"}},
		{"XT-12345 T-123456 T-12345_suffix", []string{}},
		{"T-12345." + strings.Repeat("a", 56), []string{"T-12345." + strings.Repeat("a", 56)}},
		{"T-12345." + strings.Repeat("a", 57), []string{}},
	} {
		if got := FindTaskIDs(tt.body); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("FindTaskIDs(%q) = %v, want %v", tt.body, got, tt.want)
		}
	}
}
