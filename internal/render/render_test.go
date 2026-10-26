package render

import (
	"strings"
	"testing"
)

// RenderList terminates every item, including the last. Separator-style output
// silently cost `--output raw` its final record: a `while read` loop dropped it
// and `wc -l` undercounted by one.
func TestRenderListTerminatesEveryItem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []string
		want  string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"R-00001"}, "R-00001\n"},
		{"multiple", []string{"R-00001", "R-00002"}, "R-00001\nR-00002\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := NewRenderer(&buf, Options{}).RenderList(tc.items); err != nil {
				t.Fatalf("RenderList failed: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("RenderList = %q, want %q", got, tc.want)
			}
		})
	}
}
