package wrkfcli

import (
	"strings"
	"testing"
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

// Only one text flag may read stdin; the refusal names the first claimant and
// is reached before any database or transport work.
func TestTwoDashTextFlagsRefuseNamingFirstStdinClaimant(t *testing.T) {
	res := runWrkf(t, "one input", nil, "action", "settle", "R-1", "--summary", "-", "--terminal-summary", "-")
	requireWrkfRefusal(t, res, "stdin already claimed by --summary")
}
