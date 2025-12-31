package cli

import (
	"testing"

	"github.com/lherron/wrkq/internal/config"
)

func TestApplyProjectRootHelpers(t *testing.T) {
	cfg := &config.Config{ProjectRoot: "demo"}

	t.Run("applyProjectRootToPath", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{name: "empty-no-default", input: "", defaultToRoot: false, want: ""},
			{name: "empty-default", input: "", defaultToRoot: true, want: "demo"},
			{name: "relative", input: "inbox", defaultToRoot: false, want: "demo/inbox"},
			{name: "already-prefixed", input: "demo/inbox", defaultToRoot: false, want: "demo/inbox"},
			{name: "trim-slashes", input: "/demo/inbox/", defaultToRoot: false, want: "demo/inbox"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToPath(cfg, tc.input, tc.defaultToRoot)
				if got != tc.want {
					t.Fatalf("expected %q, got %q", tc.want, got)
				}
			})
		}
	})

	t.Run("applyProjectRootToSelector", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{name: "friendly-id", input: "T-00001", defaultToRoot: false, want: "T-00001"},
			{name: "uuid", input: "00000000-0000-0000-0000-000000000001", defaultToRoot: false, want: "00000000-0000-0000-0000-000000000001"},
			{name: "typed-friendly-id", input: "t:T-00001", defaultToRoot: false, want: "t:T-00001"},
			{name: "typed-path", input: "t:inbox/task", defaultToRoot: false, want: "t:demo/inbox/task"},
			{name: "path", input: "inbox/task", defaultToRoot: false, want: "demo/inbox/task"},
			{name: "already-prefixed", input: "demo/inbox/task", defaultToRoot: false, want: "demo/inbox/task"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToSelector(cfg, tc.input, tc.defaultToRoot)
				if got != tc.want {
					t.Fatalf("expected %q, got %q", tc.want, got)
				}
			})
		}
	})

	t.Run("applyProjectRootToPaths", func(t *testing.T) {
		cases := []struct {
			name          string
			input         []string
			defaultToRoot bool
			want          []string
		}{
			{name: "none-no-default", input: nil, defaultToRoot: false, want: nil},
			{name: "none-default", input: nil, defaultToRoot: true, want: []string{"demo"}},
			{name: "single", input: []string{"inbox"}, defaultToRoot: false, want: []string{"demo/inbox"}},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToPaths(cfg, tc.input, tc.defaultToRoot)
				if len(got) != len(tc.want) {
					t.Fatalf("expected %d entries, got %d", len(tc.want), len(got))
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("expected %q, got %q", tc.want[i], got[i])
					}
				}
			})
		}
	})
}
