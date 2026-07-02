package attribution

import (
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

func TestValidatePrincipalRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{name: "canonical", ref: "agent:clod"},
		{name: "token chars", ref: "agent:clod_1.2-3"},
		{name: "bare slug", ref: "clod", wantErr: true},
		{name: "full scope", ref: "agent:clod:project:wrkq", wantErr: true},
		{name: "empty", ref: "", wantErr: true},
		{name: "control", ref: "agent:clod\nx", wantErr: true},
		{name: "over length", ref: "agent:abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrincipalRef(tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePrincipalRef(%q) err = %v, wantErr %v", tt.ref, err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeCompat(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "bare slug", value: "clod", want: "agent:clod"},
		{name: "canonical", value: "agent:clod", want: "agent:clod"},
		{name: "full scope reduces", value: "agent:clod:project:wrkq", want: "agent:clod"},
		{name: "invalid bare rejected", value: "clod/bad", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeCompat(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeCompat(%q) err = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeCompat(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestNormalizeCanonicalReducesFullScopeRef(t *testing.T) {
	for _, input := range []string{
		"agent:cody:project:wrkq",
		"agent:cody:project:wrkq:task:T-05397",
		"agent:cody:project:wrkq:task:T-05397:role:implementer",
	} {
		got, err := NormalizeCanonical(input)
		if err != nil {
			t.Fatalf("NormalizeCanonical(%q) failed: %v", input, err)
		}
		if got != "agent:cody" {
			t.Fatalf("NormalizeCanonical(%q) = %q, want agent:cody", input, got)
		}
	}
}

func TestDeriveFromScope(t *testing.T) {
	got, err := DeriveFromScope(scope.ResolvedScope{
		AgentID:   "cody",
		ProjectID: "wrkq",
		TaskID:    "primary",
	})
	if err != nil {
		t.Fatalf("DeriveFromScope failed: %v", err)
	}
	if got != "agent:cody" {
		t.Fatalf("DeriveFromScope = %q, want agent:cody", got)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Setenv(PrincipalEnv, "agent:env-principal")
	t.Setenv("WRKQ_ACTOR", "compat-actor")
	t.Setenv("WRKQ_ACTOR_ID", "A-99999")

	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "principal")
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--as", "agent:flag-principal"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	attr, err := Resolve(ResolveOptions{
		Command: cmd,
		ResolvedScope: &scope.ResolvedScope{
			AgentID:   "scope-agent",
			ProjectID: "wrkq",
		},
		Config: &config.Config{DefaultPrincipalRef: "agent:config-principal"},
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:flag-principal" {
		t.Fatalf("PrincipalRef = %q, want agent:flag-principal", attr.PrincipalRef)
	}
	if attr.ScopeRef != "agent:scope-agent:project:wrkq" {
		t.Fatalf("ScopeRef = %q, want full scope", attr.ScopeRef)
	}
}

func TestResolvePrincipalEnvOutranksActorEnv(t *testing.T) {
	t.Setenv(PrincipalEnv, "agent:principal-env")
	t.Setenv("WRKQ_ACTOR", "actor-env")

	attr, err := Resolve(ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:principal-env" {
		t.Fatalf("PrincipalRef = %q, want agent:principal-env", attr.PrincipalRef)
	}
}

func TestResolveScopeOutranksConfigDefaultPrincipal(t *testing.T) {
	attr, err := Resolve(ResolveOptions{
		ResolvedScope: &scope.ResolvedScope{
			AgentID:   "scope-agent",
			ProjectID: "wrkq",
		},
		Config: &config.Config{DefaultPrincipalRef: "agent:config-principal"},
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:scope-agent" {
		t.Fatalf("PrincipalRef = %q, want agent:scope-agent", attr.PrincipalRef)
	}
	if attr.ScopeRef != "agent:scope-agent:project:wrkq" {
		t.Fatalf("ScopeRef = %q, want full scope", attr.ScopeRef)
	}
}

func TestResolveWithPrincipalEnvsProductEnvOutranksScope(t *testing.T) {
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:env-principal")

	attr, err := ResolveWithPrincipalEnvs(ResolveOptions{
		ResolvedScope: &scope.ResolvedScope{
			AgentID:   "scope-agent",
			ProjectID: "wrkq",
			TaskID:    "T-05423",
		},
		Config: &config.Config{DefaultPrincipalRef: "agent:config-principal"},
	}, "WRKF_PRINCIPAL_REF")
	if err != nil {
		t.Fatalf("ResolveWithPrincipalEnvs failed: %v", err)
	}
	if attr.PrincipalRef != "agent:env-principal" {
		t.Fatalf("PrincipalRef = %q, want agent:env-principal", attr.PrincipalRef)
	}
	if attr.ScopeRef != "agent:scope-agent:project:wrkq:task:T-05423" {
		t.Fatalf("ScopeRef = %q, want full scope", attr.ScopeRef)
	}
}

func TestResolveWithPrincipalEnvsScopeOutranksConfig(t *testing.T) {
	attr, err := ResolveWithPrincipalEnvs(ResolveOptions{
		ResolvedScope: &scope.ResolvedScope{AgentID: "cody", ProjectID: "wrkq"},
		Config:        &config.Config{DefaultPrincipalRef: "agent:config-principal"},
	}, "WRKF_PRINCIPAL_REF")
	if err != nil {
		t.Fatalf("ResolveWithPrincipalEnvs failed: %v", err)
	}
	if attr.PrincipalRef != "agent:cody" {
		t.Fatalf("PrincipalRef = %q, want agent:cody", attr.PrincipalRef)
	}
}

func TestResolveWithPrincipalEnvsFlagsOutrankProductEnv(t *testing.T) {
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:env-principal")
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "principal")
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--principal-ref", "agent:flag-principal:project:wrkq"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	attr, err := ResolveWithPrincipalEnvs(ResolveOptions{
		Command:       cmd,
		ResolvedScope: &scope.ResolvedScope{AgentID: "scope-agent", ProjectID: "wrkq"},
	}, "WRKF_PRINCIPAL_REF")
	if err != nil {
		t.Fatalf("ResolveWithPrincipalEnvs failed: %v", err)
	}
	if attr.PrincipalRef != "agent:flag-principal" {
		t.Fatalf("PrincipalRef = %q, want agent:flag-principal", attr.PrincipalRef)
	}
}

func TestResolveNoInputFailsWithPrincipalHint(t *testing.T) {
	_, err := Resolve(ResolveOptions{})
	if err == nil {
		t.Fatal("expected no-input error")
	}
	if got := err.Error(); !containsAll(got, "no principal configured", PrincipalEnv, "default_principal_ref") {
		t.Fatalf("error %q does not name accepted inputs", got)
	}
}

func TestResolveIgnoresLegacyActorSources(t *testing.T) {
	t.Setenv("WRKQ_ACTOR", "legacy-actor")
	t.Setenv("WRKQ_ACTOR_ID", "A-00001")

	_, err := Resolve(ResolveOptions{Config: &config.Config{DefaultActor: "legacy-config"}})
	if err == nil {
		t.Fatal("expected legacy-only actor sources to be ignored")
	}
}

func TestResolveRejectsBareAs(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--as", "calchas"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}
	if _, err := Resolve(ResolveOptions{Command: cmd}); err == nil {
		t.Fatal("expected bare --as to be rejected")
	}
}

func TestResolveAllowsFullScopeFlagsWhenTheyResolveToSameAgent(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "principal")
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--principal-ref", "agent:cody:project:wrkq:task:T-1", "--as", "agent:cody"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}
	attr, err := Resolve(ResolveOptions{Command: cmd})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:cody" {
		t.Fatalf("PrincipalRef = %q, want agent:cody", attr.PrincipalRef)
	}
}

func TestResolveRejectsConflictingPrincipalFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("principal-ref", "", "principal")
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--principal-ref", "agent:cody:project:wrkq", "--as", "agent:clod"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}
	_, err := Resolve(ResolveOptions{Command: cmd})
	if err == nil {
		t.Fatal("expected conflicting principal flags to be rejected")
	}
	if got := err.Error(); !containsAll(got, "resolves to agent:cody", "resolves to agent:clod", "same agent") {
		t.Fatalf("error %q does not explain the conflict", got)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !stringsContains(s, needle) {
			return false
		}
	}
	return true
}

func stringsContains(s, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(needle); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
