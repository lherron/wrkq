package rpccli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestActorFlagReducesFullScopeRef(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")
	if err := cmd.ParseFlags([]string{"--principal-ref", "agent:cody:project:wrkq:task:T-05397", "--as", "agent:cody"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	got, err := actorFlag(cmd)
	if err != nil {
		t.Fatalf("actorFlag failed: %v", err)
	}
	if got != "agent:cody" {
		t.Fatalf("actorFlag = %q, want agent:cody", got)
	}
}

func TestActorFlagRejectsConflictingResolvedAgents(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")
	if err := cmd.ParseFlags([]string{"--principal-ref", "agent:cody:project:wrkq", "--as", "agent:clod"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	_, err := actorFlag(cmd)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if got := err.Error(); !strings.Contains(got, "agent:cody") || !strings.Contains(got, "agent:clod") || !strings.Contains(got, "same agent") {
		t.Fatalf("error %q does not explain the resolved-principal conflict", got)
	}
}
