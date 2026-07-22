package wrkfcli

import "testing"

func TestInstanceCancelCommandExposesSelectorsCASAndExplanation(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"instance", "cancel"})
	if err != nil || cmd == nil || cmd.Name() != "cancel" {
		t.Fatalf("wrkf instance cancel command missing: command=%v err=%v", cmd, err)
	}
	for _, flag := range []string{"instance", "expect-revision", "explanation"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("wrkf instance cancel missing --%s", flag)
		}
	}
	if err := cmd.Args(cmd, []string{"T-00001"}); err != nil {
		t.Fatalf("wrkf instance cancel TASK grammar rejected one task selector: %v", err)
	}
}
