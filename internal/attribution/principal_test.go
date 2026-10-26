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
		{name: "task-depth scope reduces", value: "agent:cody:project:wrkq:task:T-05397", want: "agent:cody"},
		{name: "role-depth scope reduces", value: "agent:cody:project:wrkq:task:T-05397:role:implementer", want: "agent:cody"},
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

// TestPrincipalResolvesFromHighestRankedSource is the principal precedence
// ladder: --principal-ref, --as, the product principal env, the resolved
// scope, then config default_principal_ref. Each row is one rung winning over
// every lower rung that is also set; ScopeRef always comes from the resolved
// scope, whichever rung supplied the principal.
//
// rewrite-held (§1): the caller-visible form of this table is
// `wrkq whoami --json` argv (principal + scope_ref per row, with
// --as/--principal-ref flags, WRKQ_PRINCIPAL_REF, AGENT_SCOPE_REF and a config
// default_principal_ref). That command lives in internal/rpccli, which this
// package cannot import, so the ladder stays here until that surface table
// exists.
func TestPrincipalResolvesFromHighestRankedSource(t *testing.T) {
	projectScope := &scope.ResolvedScope{AgentID: "scope-agent", ProjectID: "wrkq"}
	taskScope := &scope.ResolvedScope{AgentID: "scope-agent", ProjectID: "wrkq", TaskID: "T-05423"}
	cfg := &config.Config{DefaultPrincipalRef: "agent:config-principal"}

	tests := []struct {
		name          string
		envNames      []string // nil means Resolve (WRKQ_PRINCIPAL_REF)
		env           map[string]string
		flags         []string
		scope         *scope.ResolvedScope
		wantPrincipal string
		wantScopeRef  string
	}{
		{
			name: "--as beats env, scope and config; legacy actor env ignored",
			env: map[string]string{
				PrincipalEnv:    "agent:env-principal",
				"WRKQ_ACTOR":    "compat-actor",
				"WRKQ_ACTOR_ID": "A-99999",
			},
			flags:         []string{"--as", "agent:flag-principal"},
			scope:         projectScope,
			wantPrincipal: "agent:flag-principal",
			wantScopeRef:  "agent:scope-agent:project:wrkq",
		},
		{
			name:          "--principal-ref full scope reduces and beats product env",
			envNames:      []string{"WRKF_PRINCIPAL_REF"},
			env:           map[string]string{"WRKF_PRINCIPAL_REF": "agent:env-principal"},
			flags:         []string{"--principal-ref", "agent:flag-principal:project:wrkq"},
			scope:         projectScope,
			wantPrincipal: "agent:flag-principal",
			wantScopeRef:  "agent:scope-agent:project:wrkq",
		},
		{
			name:          "product env beats scope and config; scope ref keeps task depth",
			envNames:      []string{"WRKF_PRINCIPAL_REF"},
			env:           map[string]string{"WRKF_PRINCIPAL_REF": "agent:env-principal", "WRKQ_ACTOR": "actor-env"},
			scope:         taskScope,
			wantPrincipal: "agent:env-principal",
			wantScopeRef:  "agent:scope-agent:project:wrkq:task:T-05423",
		},
		{
			name:          "scope agent beats config default",
			scope:         taskScope,
			wantPrincipal: "agent:scope-agent",
			wantScopeRef:  "agent:scope-agent:project:wrkq:task:T-05423",
		},
		{
			name:          "config default when nothing else is set",
			wantPrincipal: "agent:config-principal",
			wantScopeRef:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cmd := &cobra.Command{}
			cmd.Flags().String("principal-ref", "", "principal")
			cmd.Flags().String("as", "", "principal")
			if err := cmd.ParseFlags(tt.flags); err != nil {
				t.Fatalf("ParseFlags failed: %v", err)
			}
			opts := ResolveOptions{Command: cmd, ResolvedScope: tt.scope, Config: cfg}

			var attr Attribution
			var err error
			if tt.envNames == nil {
				attr, err = Resolve(opts)
			} else {
				attr, err = ResolveWithPrincipalEnvs(opts, tt.envNames...)
			}
			if err != nil {
				t.Fatalf("resolve failed: %v", err)
			}
			if attr.PrincipalRef != tt.wantPrincipal {
				t.Fatalf("PrincipalRef = %q, want %q", attr.PrincipalRef, tt.wantPrincipal)
			}
			if attr.ScopeRef != tt.wantScopeRef {
				t.Fatalf("ScopeRef = %q, want %q", attr.ScopeRef, tt.wantScopeRef)
			}
		})
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

func TestLegacyActorEnvAloneLeavesNoPrincipalConfigured(t *testing.T) {
	t.Setenv("WRKQ_ACTOR", "legacy-actor")
	t.Setenv("WRKQ_ACTOR_ID", "A-00001")

	_, err := Resolve(ResolveOptions{Config: &config.Config{}})
	if !IsNoPrincipalConfigured(err) {
		t.Fatalf("legacy-only actor sources must leave no principal configured, got %v", err)
	}
}

func TestBareAsIsRejectedWithAgentPrefixHint(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--as", "calchas"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}
	_, err := Resolve(ResolveOptions{Command: cmd})
	if err == nil {
		t.Fatal("expected bare --as to be rejected")
	}
	if got := err.Error(); !containsAll(got, "missing agent: prefix", "use agent:calchas") {
		t.Fatalf("bare --as error %q does not give the agent: prefix hint", got)
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
