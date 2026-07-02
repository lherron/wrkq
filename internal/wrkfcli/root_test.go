package wrkfcli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestActorDefaultsUseAgentScopeRefForWorkflowAndWrkqDefaults(t *testing.T) {
	clearWrkfActorEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423")

	workflowActor, wrkqActor, err := actorDefaults(testActorCmd(t, nil))
	if err != nil {
		t.Fatalf("actorDefaults failed: %v", err)
	}
	if workflowActor != "agent:cody" {
		t.Fatalf("workflowActor = %q, want agent:cody", workflowActor)
	}
	if wrkqActor != "agent:cody" {
		t.Fatalf("wrkqActor = %q, want agent:cody", wrkqActor)
	}
}

func TestActorDefaultsPrincipalEnvOverridesAgentScopeRef(t *testing.T) {
	clearWrkfActorEnv(t)
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:env-principal")
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq")

	workflowActor, wrkqActor, err := actorDefaults(testActorCmd(t, nil))
	if err != nil {
		t.Fatalf("actorDefaults failed: %v", err)
	}
	if workflowActor != "agent:env-principal" || wrkqActor != "agent:env-principal" {
		t.Fatalf("actorDefaults = (%q, %q), want env principal for both", workflowActor, wrkqActor)
	}
}

func TestActorDefaultsExplicitFlagOverridesAgentScopeRef(t *testing.T) {
	clearWrkfActorEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq")

	workflowActor, wrkqActor, err := actorDefaults(testActorCmd(t, []string{"--principal-ref", "agent:flag-principal"}))
	if err != nil {
		t.Fatalf("actorDefaults failed: %v", err)
	}
	if workflowActor != "agent:flag-principal" || wrkqActor != "agent:flag-principal" {
		t.Fatalf("actorDefaults = (%q, %q), want flag principal for both", workflowActor, wrkqActor)
	}
}

func TestActorDefaultsLegacyActorDoesNotBecomeWrkqDefault(t *testing.T) {
	clearWrkfActorEnv(t)
	t.Setenv("WRKF_ACTOR", "legacy-workflow-actor")

	workflowActor, wrkqActor, err := actorDefaults(testActorCmd(t, nil))
	if err != nil {
		t.Fatalf("actorDefaults failed: %v", err)
	}
	if workflowActor != "legacy-workflow-actor" {
		t.Fatalf("workflowActor = %q, want legacy-workflow-actor", workflowActor)
	}
	if wrkqActor != "" {
		t.Fatalf("wrkqActor = %q, want empty", wrkqActor)
	}
}

func testActorCmd(t *testing.T, args []string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "")
	cmd.Flags().String("as", "", "")
	if len(args) > 0 {
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("ParseFlags failed: %v", err)
		}
	}
	return cmd
}

func clearWrkfActorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WRKF_PRINCIPAL_REF",
		"WRKF_ACTOR",
		"WRKQ_ACTOR",
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
