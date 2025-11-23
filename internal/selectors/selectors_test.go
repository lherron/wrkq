package selectors

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input       string
		expectType  Type
		expectToken string
	}{
		{"t:T-00123", TypeTask, "T-00123"},
		{"t:portal/auth/login", TypeTask, "portal/auth/login"},
		{"t:uuid-here", TypeTask, "uuid-here"},
		{"c:C-00012", TypeComment, "C-00012"},
		{"c:uuid-here", TypeComment, "uuid-here"},
		{"T-00123", TypeAuto, "T-00123"},
		{"portal/auth/login", TypeAuto, "portal/auth/login"},
		{"P-00001", TypeAuto, "P-00001"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			sel := Parse(tt.input)
			if sel.Type != tt.expectType {
				t.Errorf("Parse(%q).Type = %v, want %v", tt.input, sel.Type, tt.expectType)
			}
			if sel.Token != tt.expectToken {
				t.Errorf("Parse(%q).Token = %q, want %q", tt.input, sel.Token, tt.expectToken)
			}
		})
	}
}

func TestParseEdgeCases(t *testing.T) {
	tests := []struct {
		input       string
		expectType  Type
		expectToken string
	}{
		{"t:", TypeTask, ""},
		{"c:", TypeComment, ""},
		{"", TypeAuto, ""},
		{"t:t:nested", TypeTask, "t:nested"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			sel := Parse(tt.input)
			if sel.Type != tt.expectType {
				t.Errorf("Parse(%q).Type = %v, want %v", tt.input, sel.Type, tt.expectType)
			}
			if sel.Token != tt.expectToken {
				t.Errorf("Parse(%q).Token = %q, want %q", tt.input, sel.Token, tt.expectToken)
			}
		})
	}
}
