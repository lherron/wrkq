package rpccli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A repeated replace-style flag refuses at parse time instead of keeping the
// last value (T-08044: `--labels ops --labels awaiting-activation` dropped ops).
func TestRepeatedReplaceFlagRefuses(t *testing.T) {
	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		want string
	}{
		{"set labels", newSetCmd, []string{"--labels", "a", "--labels", "b"}, `--labels a,b or --labels '["a","b"]'`},
		{"set title", newSetCmd, []string{"--title", "x", "--title", "y"}, "--title given more than once"},
		{"set priority", newSetCmd, []string{"--priority", "1", "--priority", "2"}, "--priority given more than once"},
		{"set -d shorthand", newSetCmd, []string{"-d", "x", "--description", "y"}, "--description given more than once"},
		{"touch caused-by", newTouchCmd, []string{"--caused-by", "T-1", "--caused-by", "T-2"}, "--caused-by T-00012,T-00034"},
		{"touch meta", newTouchCmd, []string{"--meta", "{}", "--meta", "null"}, "--meta given more than once"},
		{"mkdir kind", newMkdirCmd, []string{"--kind", "area", "--kind", "feature"}, "--kind given more than once"},
		{"restore labels", newRestoreCmd, []string{"--labels", "a", "--labels", "b"}, "--labels given more than once"},
		{"campaign edit labels", newCampaignEditCmd, []string{"--labels", "a", "--labels", "b"}, "--labels given more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cmd().ParseFlags(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseFlags(%v) = %v, want error containing %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestSingleReplaceFlagStillParses(t *testing.T) {
	cmd := newSetCmd()
	if err := cmd.ParseFlags([]string{"--labels", "ops,awaiting-activation", "--dry-run", "--dry-run"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got, _ := cmd.Flags().GetString("labels"); got != "ops,awaiting-activation" {
		t.Fatalf("labels = %q", got)
	}
	if !cmd.Flags().Changed("labels") {
		t.Fatal("labels not marked changed")
	}
}
