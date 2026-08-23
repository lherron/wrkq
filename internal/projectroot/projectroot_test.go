package projectroot

import (
	"testing"

	"github.com/lherron/wrkq/internal/config"
)

// TestTransform is the single, shared proof of the project-root transform that
// all production rpccli commands apply. Keeping it here (rather
// than duplicated per surface) is what enforces "one implementation, one test" —
// the invariant daedalus required for path-taking commands.
func TestTransform(t *testing.T) {
	cfg := &config.Config{ProjectRoot: "demo"}

	t.Run("ApplyToPath", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{"empty-no-default", "", false, ""},
			{"empty-default", "", true, "demo"},
			{"relative", "inbox", false, "demo/inbox"},
			{"already-rooted", "demo/inbox", false, "demo/inbox"},
			{"already-rooted-exact", "demo", false, "demo"},
			{"trim-slashes", "/demo/inbox/", false, "demo/inbox"},
			{"nested-relative", "inbox/sub", false, "demo/inbox/sub"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := ApplyToPath(cfg, tc.input, tc.defaultToRoot); got != tc.want {
					t.Fatalf("ApplyToPath(%q,%v): want %q, got %q", tc.input, tc.defaultToRoot, tc.want, got)
				}
			})
		}
	})

	t.Run("ApplyToSelector", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{"empty-default", "", true, "demo"},
			{"empty-no-default", "", false, ""},
			{"friendly-task-id", "T-00001", false, "T-00001"},
			{"friendly-container-id", "P-00001", false, "P-00001"},
			{"friendly-promise-id", "PR-00001", false, "PR-00001"},
			{"uuid", "00000000-0000-0000-0000-000000000001", false, "00000000-0000-0000-0000-000000000001"},
			{"bare-sequence", "1454", false, "1454"},
			{"typed-friendly-id", "t:T-00001", false, "t:T-00001"},
			{"typed-container-friendly-id", "c:P-00001", false, "c:P-00001"},
			{"typed-bare-sequence", "t:1454", false, "t:1454"},
			{"typed-uuid", "t:00000000-0000-0000-0000-000000000001", false, "t:00000000-0000-0000-0000-000000000001"},
			{"typed-path", "t:inbox/task", false, "t:demo/inbox/task"},
			{"typed-container-path", "c:area/sub", false, "c:demo/area/sub"},
			{"typed-empty-default", "t:", true, "t:demo"},
			{"typed-empty-no-default", "t:", false, "t:"},
			{"path", "inbox/task", false, "demo/inbox/task"},
			{"already-rooted", "demo/inbox/task", false, "demo/inbox/task"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				if got := ApplyToSelector(cfg, tc.input, tc.defaultToRoot); got != tc.want {
					t.Fatalf("ApplyToSelector(%q,%v): want %q, got %q", tc.input, tc.defaultToRoot, tc.want, got)
				}
			})
		}
	})

	t.Run("ApplyToPaths", func(t *testing.T) {
		cases := []struct {
			name          string
			input         []string
			defaultToRoot bool
			want          []string
		}{
			{"none-no-default", nil, false, nil},
			{"none-default", nil, true, []string{"demo"}},
			{"single", []string{"inbox"}, false, []string{"demo/inbox"}},
			{"multi", []string{"inbox", "T-00001"}, false, []string{"demo/inbox", "T-00001"}},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := ApplyToPaths(cfg, tc.input, tc.defaultToRoot)
				if len(got) != len(tc.want) {
					t.Fatalf("len: want %d (%v), got %d (%v)", len(tc.want), tc.want, len(got), got)
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("[%d]: want %q, got %q", i, tc.want[i], got[i])
					}
				}
			})
		}
	})

	t.Run("no-root-is-identity", func(t *testing.T) {
		// With no configured root, every transform is the trimmed identity — this
		// is why the hermetic parity harness (no root) proves command bytes.
		empty := &config.Config{}
		if got := ApplyToPath(empty, "inbox", true); got != "inbox" {
			t.Fatalf("ApplyToPath no-root: want %q, got %q", "inbox", got)
		}
		if got := ApplyToSelector(empty, "t:inbox/x", false); got != "t:inbox/x" {
			t.Fatalf("ApplyToSelector no-root: want %q, got %q", "t:inbox/x", got)
		}
		if got := Normalize(empty); got != "" {
			t.Fatalf("Normalize no-root: want %q, got %q", "", got)
		}
		if got := Normalize(nil); got != "" {
			t.Fatalf("Normalize nil: want %q, got %q", "", got)
		}
	})
}
