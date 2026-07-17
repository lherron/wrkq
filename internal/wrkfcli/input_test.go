package wrkfcli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestActionSettleSummaryReadsStdinVerbatim(t *testing.T) {
	cmd := actionSettleCmd()
	const summary = "first verdict\nsecond line\n"
	cmd.SetIn(strings.NewReader(summary))

	if err := cmd.Flags().Set("summary", "-"); err != nil {
		t.Fatalf("set --summary: %v", err)
	}
	if err := resolveStdinTextFlags(cmd); err != nil {
		t.Fatalf("resolve --summary stdin: %v", err)
	}

	got, err := cmd.Flags().GetString("summary")
	if err != nil {
		t.Fatalf("get --summary: %v", err)
	}
	if got != summary {
		t.Fatalf("summary = %q, want verbatim %q", got, summary)
	}
}

func TestActionSettleRejectsMultipleStdinTextFlags(t *testing.T) {
	cmd := actionSettleCmd()
	cmd.SetIn(strings.NewReader("one input"))
	for _, name := range []string{"summary", "terminal-summary"} {
		if err := cmd.Flags().Set(name, "-"); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}

	err := resolveStdinTextFlags(cmd)
	if err == nil {
		t.Fatal("expected stdin claim conflict")
	}
	if !strings.Contains(err.Error(), "stdin already claimed by --summary") {
		t.Fatalf("error = %q, want first claimant", err)
	}
}

func TestWithAppResolvesStdinTextFlagsBeforeCommand(t *testing.T) {
	var summary string
	cmd := &cobra.Command{
		Use: "test",
		RunE: withApp(false, func(_ *app, _ *cobra.Command, _ []string) error {
			if summary != "through withApp\n" {
				t.Fatalf("summary = %q, want stdin text", summary)
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&summary, "summary", "", "")
	cmd.SetArgs([]string{"--summary", "-"})
	cmd.SetIn(strings.NewReader("through withApp\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
