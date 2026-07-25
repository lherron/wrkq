package wrkfcli

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

func TestWrkfPrincipalDefaultUsesAgentScopeRef(t *testing.T) {
	clearWrkfAuthorityEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423")

	got, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{})
	if err != nil {
		t.Fatalf("wrkfPrincipalDefault failed: %v", err)
	}
	if got != "agent:cody" {
		t.Fatalf("wrkfPrincipalDefault = %q, want agent:cody", got)
	}
}

func TestWrkfPrincipalDefaultPrincipalEnvOverridesAgentScopeRef(t *testing.T) {
	clearWrkfAuthorityEnv(t)
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:env-principal")
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq")

	got, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{})
	if err != nil {
		t.Fatalf("wrkfPrincipalDefault failed: %v", err)
	}
	if got != "agent:env-principal" {
		t.Fatalf("wrkfPrincipalDefault = %q, want agent:env-principal", got)
	}
}

func TestWrkfPrincipalDefaultExplicitFlagOverridesAgentScopeRef(t *testing.T) {
	clearWrkfAuthorityEnv(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq")

	got, err := wrkfPrincipalDefault(
		testPrincipalCmd(t, []string{"--principal-ref", "agent:flag-principal"}),
		&config.Config{},
	)
	if err != nil {
		t.Fatalf("wrkfPrincipalDefault failed: %v", err)
	}
	if got != "agent:flag-principal" {
		t.Fatalf("wrkfPrincipalDefault = %q, want agent:flag-principal", got)
	}
}

func TestWrkfPrincipalDefaultUsesCanonicalConfigAndIgnoresLegacyDefaultActor(t *testing.T) {
	t.Run("canonical default_principal_ref", func(t *testing.T) {
		clearWrkfAuthorityEnv(t)
		got, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{
			DefaultPrincipalRef: "agent:config-principal",
			DefaultActor:        "legacy-config-actor",
		})
		if err != nil {
			t.Fatalf("wrkfPrincipalDefault failed: %v", err)
		}
		if got != "agent:config-principal" {
			t.Fatalf("wrkfPrincipalDefault = %q, want agent:config-principal", got)
		}
	})

	t.Run("legacy default_actor only", func(t *testing.T) {
		clearWrkfAuthorityEnv(t)
		_, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{
			DefaultActor: "legacy-config-actor",
		})
		assertPrincipalReplacementDiagnostic(t, err)
	})
}

func TestWrkfPrincipalDefaultLegacyEnvOnlyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "WRKF_ACTOR bare slug", key: "WRKF_ACTOR", value: "legacy-workflow-actor"},
		{name: "WRKF_ACTOR canonical-shaped", key: "WRKF_ACTOR", value: "agent:legacy"},
		{name: "WRKQ_ACTOR", key: "WRKQ_ACTOR", value: "agent:legacy"},
		{name: "WRKQ_ACTOR_ID", key: "WRKQ_ACTOR_ID", value: "A-00001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearWrkfAuthorityEnv(t)
			t.Setenv(tc.key, tc.value)
			_, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{})
			assertPrincipalReplacementDiagnostic(t, err)
		})
	}
}

func TestWrkfPrincipalDefaultRejectsInvalidCanonicalInputs(t *testing.T) {
	for _, value := range []string{
		"legacy-slug",
		"A-00001",
		"8f7ef6e9-42ff-4fc0-bd9d-f111bbb61f51",
		"system:wrkf",
	} {
		t.Run(value, func(t *testing.T) {
			clearWrkfAuthorityEnv(t)
			t.Setenv("WRKF_PRINCIPAL_REF", value)
			_, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{})
			assertPrincipalReplacementDiagnostic(t, err)
		})
	}
}

func TestWrkfPrincipalDefaultMissingFailsClosed(t *testing.T) {
	clearWrkfAuthorityEnv(t)
	_, err := wrkfPrincipalDefault(testPrincipalCmd(t, nil), &config.Config{})
	assertPrincipalReplacementDiagnostic(t, err)
}

func assertPrincipalReplacementDiagnostic(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected principal resolution failure")
	}
	for _, want := range []string{"--principal-ref", "WRKF_PRINCIPAL_REF"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %s", err, want)
		}
	}
}

func testPrincipalCmd(t *testing.T, args []string) *cobra.Command {
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

func clearWrkfAuthorityEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WRKF_PRINCIPAL_REF",
		"WRKF_ACTOR",
		"WRKQ_ACTOR",
		"WRKQ_ACTOR_ID",
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
