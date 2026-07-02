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

func TestActorFlagUsesAgentScopeRefWhenFlagsAbsent(t *testing.T) {
	clearActorFlagEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423")
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")

	got, err := actorFlag(cmd)
	if err != nil {
		t.Fatalf("actorFlag failed: %v", err)
	}
	if got != "agent:cody" {
		t.Fatalf("actorFlag = %q, want agent:cody", got)
	}
}

func TestActorFlagExplicitFlagOverridesAgentScopeRef(t *testing.T) {
	clearActorFlagEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq")
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")
	if err := cmd.ParseFlags([]string{"--as", "agent:flag-principal"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	got, err := actorFlag(cmd)
	if err != nil {
		t.Fatalf("actorFlag failed: %v", err)
	}
	if got != "agent:flag-principal" {
		t.Fatalf("actorFlag = %q, want agent:flag-principal", got)
	}
}

func TestLaunchPrincipalRefUsesAgentScopeRef(t *testing.T) {
	clearActorFlagEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423")
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")

	got, err := launchPrincipalRef(cmd)
	if err != nil {
		t.Fatalf("launchPrincipalRef failed: %v", err)
	}
	if got != "agent:cody" {
		t.Fatalf("launchPrincipalRef = %q, want agent:cody", got)
	}
}

func clearActorFlagEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WRKQ_PRINCIPAL_REF",
		"AGENT_SCOPE_REF",
		"ASP_SCOPE_REF",
		"ASP_HANDLE",
		"ASP_AGENT_ID",
		"ASP_PROJECT",
	} {
		t.Setenv(key, "")
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
