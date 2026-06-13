package attribution

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
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
		{name: "full scope rejected", value: "agent:clod:project:wrkq", wantErr: true},
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
	t.Setenv(ActorEnv, "compat-actor")
	t.Setenv(ActorIDEnv, "A-99999")

	cmd := &cobra.Command{}
	cmd.Flags().String("as", "", "principal")
	if err := cmd.ParseFlags([]string{"--as", "flag-principal"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	attr, err := Resolve(ResolveOptions{
		Command: cmd,
		ResolvedScope: &scope.ResolvedScope{
			AgentID:   "scope-agent",
			ProjectID: "wrkq",
		},
		Config: &config.Config{DefaultActor: "config-actor"},
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
	t.Setenv(ActorEnv, "actor-env")

	attr, err := Resolve(ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:principal-env" {
		t.Fatalf("PrincipalRef = %q, want agent:principal-env", attr.PrincipalRef)
	}
}

func TestResolveScopeOutranksConfigDefaultActor(t *testing.T) {
	attr, err := Resolve(ResolveOptions{
		ResolvedScope: &scope.ResolvedScope{
			AgentID:   "scope-agent",
			ProjectID: "wrkq",
		},
		Config: &config.Config{DefaultActor: "config-actor"},
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

func TestResolveActorIDBestEffortTranslation(t *testing.T) {
	database := openAttributionTestDB(t)
	_, err := database.Exec(`INSERT INTO actors (id, slug, role) VALUES ('A-00001', 'legacy-cody', 'agent')`)
	if err != nil {
		t.Fatalf("insert actor: %v", err)
	}

	t.Setenv(ActorIDEnv, "A-00001")

	attr, err := Resolve(ResolveOptions{DB: database.DB})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:legacy-cody" {
		t.Fatalf("PrincipalRef = %q, want agent:legacy-cody", attr.PrincipalRef)
	}
	if attr.LegacyActorUUID == nil {
		t.Fatal("LegacyActorUUID should be populated from display/cache row")
	}
	if attr.LegacyActorID != "A-00001" {
		t.Fatalf("LegacyActorID = %q, want A-00001", attr.LegacyActorID)
	}
}

func TestResolveActorIDMissingRowFallsBackToVerbatim(t *testing.T) {
	database := openAttributionTestDB(t)
	t.Setenv(ActorIDEnv, "A-12345")

	attr, err := Resolve(ResolveOptions{DB: database.DB})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if attr.PrincipalRef != "agent:A-12345" {
		t.Fatalf("PrincipalRef = %q, want agent:A-12345", attr.PrincipalRef)
	}
	if attr.LegacyActorUUID != nil {
		t.Fatalf("LegacyActorUUID = %q, want nil", *attr.LegacyActorUUID)
	}
}

func TestResolveNoInputFailsWithPrincipalHint(t *testing.T) {
	_, err := Resolve(ResolveOptions{})
	if err == nil {
		t.Fatal("expected no-input error")
	}
	if got := err.Error(); !containsAll(got, "no principal configured", PrincipalEnv, ActorEnv, ActorIDEnv) {
		t.Fatalf("error %q does not name accepted inputs", got)
	}
}

func openAttributionTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return database
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
