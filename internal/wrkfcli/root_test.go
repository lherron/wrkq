package wrkfcli

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

// The caller principal resolves flag, then WRKF_PRINCIPAL_REF, then the runtime
// scope ref (reduced to its agent), then the config default_principal_ref.
func TestWrkfCallerPrincipalPrecedenceFlagThenEnvThenScopeThenConfig(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     []string
		env       map[string]string
		configRef string
		want      string
	}{
		{
			name:  "--principal-ref beats WRKF_PRINCIPAL_REF and AGENT_SCOPE_REF",
			flags: []string{"--principal-ref", "agent:flag-principal"},
			env:   map[string]string{"WRKF_PRINCIPAL_REF": "agent:env-principal", "AGENT_SCOPE_REF": "agent:cody:project:wrkq"},
			want:  "agent:flag-principal",
		},
		{
			name: "WRKF_PRINCIPAL_REF beats AGENT_SCOPE_REF",
			env:  map[string]string{"WRKF_PRINCIPAL_REF": "agent:env-principal", "AGENT_SCOPE_REF": "agent:cody:project:wrkq"},
			want: "agent:env-principal",
		},
		{
			name: "AGENT_SCOPE_REF reduces to its agent principal",
			env:  map[string]string{"AGENT_SCOPE_REF": "agent:cody:project:wrkq:task:T-05423"},
			want: "agent:cody",
		},
		{
			name:      "config default_principal_ref when nothing else is set",
			configRef: "agent:config-principal",
			want:      "agent:config-principal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearWrkfAuthorityEnv(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			got, err := wrkfPrincipalDefault(testPrincipalCmd(t, tc.flags), &config.Config{DefaultPrincipalRef: tc.configRef})
			if err != nil {
				t.Fatalf("wrkfPrincipalDefault failed: %v", err)
			}
			if got != tc.want {
				t.Fatalf("wrkfPrincipalDefault = %q, want %q", got, tc.want)
			}
		})
	}
}

// Legacy actor variables are ignored, not honored or validated: a caller that
// sets only them gets the "required" refusal, not a principal.
func TestLegacyActorEnvAloneIsIgnoredAndWrkfRefusesWithoutPrincipal(t *testing.T) {
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
			res := runWrkf(t, "", []string{tc.key + "=" + tc.value}, "hook", "list", "--json")
			requireWrkfRefusal(t, res, "workflow caller principal is required; set --principal-ref agent:<id> or WRKF_PRINCIPAL_REF=agent:<id>")
		})
	}
}

func TestMalformedWrkfPrincipalRefIsRefusedAsInvalid(t *testing.T) {
	for _, value := range []string{
		"legacy-slug",
		"A-00001",
		"8f7ef6e9-42ff-4fc0-bd9d-f111bbb61f51",
		"system:wrkf",
	} {
		t.Run(value, func(t *testing.T) {
			res := runWrkf(t, "", []string{"WRKF_PRINCIPAL_REF=" + value}, "hook", "list", "--json")
			requireWrkfRefusal(t, res, "invalid workflow caller principal; set --principal-ref agent:<id> or WRKF_PRINCIPAL_REF=agent:<id>")
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
