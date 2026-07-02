package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

// resetAgentContextFlags clears package-level flag state between tests since
// rootCmd is process-global.
func resetAgentContextFlags() {
	agentContextScope = ""
	agentContextJSON = false
	agentContextHuman = false
}

// clearScopeEnvT clears scope env vars for the test's lifetime.
func clearScopeEnvT(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AGENT_SCOPE_REF", "ASP_SCOPE_REF", "ASP_HANDLE", "ASP_AGENT_ID", "ASP_PROJECT"} {
		t.Setenv(k, "")
	}
}

// runAgentContextForTest invokes runAgentContext directly without going
// through cobra's RunE / os.Exit path. It returns the decoded output struct.
func runAgentContextForTest(t *testing.T) agentContextOutput {
	t.Helper()
	resetAgentContextFlags()
	// Force JSON for assertion stability.
	agentContextJSON = true

	cmd := &cobra.Command{}
	cmd.PersistentFlags().String("db", "", "")
	_ = cmd.ParseFlags(nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	// Build the output by calling the underlying primitives directly so we
	// avoid the os.Exit branch in runAgentContext.
	env := scope.ReadEnv()
	resolved, diags, resolveErr := scope.Resolve(agentContextScope)
	lookups, dbPath, dbReachable, dbErr := tryDBLookups(cmd, resolved, resolveErr)

	o := agentContextOutput{
		Env:         env,
		Diagnostics: diags,
		DBPath:      dbPath,
		DBReachable: dbReachable,
	}
	if dbErr != "" {
		o.DBError = dbErr
	}
	if agentContextScope != "" {
		o.OverrideFlag = agentContextScope
	}
	if resolveErr != nil {
		o.Error = resolveErr.Error()
	} else {
		r := resolved
		o.Scope = &r
		o.Lookups = lookups
	}
	return o
}

func TestAgentContext_ResolvesScopeRefEnv(t *testing.T) {
	clearScopeEnvT(t)
	t.Setenv("ASP_SCOPE_REF", "agent:clod:project:wrkq:task:primary")
	t.Setenv("ASP_HANDLE", "clod@wrkq:primary")

	o := runAgentContextForTest(t)
	if o.Error != "" {
		t.Fatalf("unexpected error: %s", o.Error)
	}
	if o.Scope == nil {
		t.Fatalf("expected resolved scope")
	}
	if o.Scope.Source != scope.SourceScopeRefEnv {
		t.Errorf("Source=%q, want %q", o.Scope.Source, scope.SourceScopeRefEnv)
	}
	if o.Scope.CanonicalRef != "agent:clod:project:wrkq" {
		t.Errorf("CanonicalRef=%q, want agent:clod:project:wrkq", o.Scope.CanonicalRef)
	}
	if o.Env.ASPScopeRef != "agent:clod:project:wrkq:task:primary" {
		t.Errorf("env not echoed correctly: %+v", o.Env)
	}
}

func TestAgentContext_ResolvesAgentScopeRefEnv(t *testing.T) {
	clearScopeEnvT(t)
	t.Setenv("AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423")
	t.Setenv("ASP_SCOPE_REF", "agent:clod:project:wrkq")

	o := runAgentContextForTest(t)
	if o.Error != "" {
		t.Fatalf("unexpected error: %s", o.Error)
	}
	if o.Scope == nil {
		t.Fatalf("expected resolved scope")
	}
	if o.Scope.Source != scope.SourceAgentScopeRefEnv {
		t.Errorf("Source=%q, want %q", o.Scope.Source, scope.SourceAgentScopeRefEnv)
	}
	if o.Scope.AgentID != "cody" {
		t.Errorf("AgentID=%q, want cody", o.Scope.AgentID)
	}
	if o.Env.AgentScopeRef != "agent:cody:project:wrkq:task:T-05423" {
		t.Errorf("env not echoed correctly: %+v", o.Env)
	}
}

func TestAgentContext_OverrideHandle(t *testing.T) {
	clearScopeEnvT(t)
	// Even with env set, --scope wins.
	t.Setenv("ASP_SCOPE_REF", "agent:clod:project:wrkq")
	resetAgentContextFlags()
	agentContextScope = "cody@wrkq"
	agentContextJSON = true

	env := scope.ReadEnv()
	resolved, diags, err := scope.Resolve(agentContextScope)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if resolved.Source != scope.SourceOverride {
		t.Errorf("Source=%q, want %q", resolved.Source, scope.SourceOverride)
	}
	if resolved.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", resolved.CanonicalRef)
	}
	_ = env
	_ = diags
}

func TestAgentContext_UnresolvableNoEnv(t *testing.T) {
	clearScopeEnvT(t)
	o := runAgentContextForTest(t)
	if o.Error == "" {
		t.Fatalf("expected error when no env is set")
	}
	if o.Scope != nil {
		t.Errorf("expected no Scope when unresolvable, got %+v", o.Scope)
	}
	if !strings.Contains(o.Error, "ASP_SCOPE_REF") {
		t.Errorf("error should mention ASP_SCOPE_REF: %s", o.Error)
	}
}

func TestAgentContext_DBNotPresent(t *testing.T) {
	clearScopeEnvT(t)
	t.Setenv("ASP_SCOPE_REF", "agent:cody:project:wrkq")
	// Force DB path to a non-existent file.
	tmp := t.TempDir()
	t.Setenv("WRKQ_DB_PATH", tmp+"/does-not-exist.db")

	o := runAgentContextForTest(t)
	if o.Error != "" {
		t.Fatalf("scope resolution should succeed even without DB; got error %s", o.Error)
	}
	if o.Scope == nil {
		t.Fatalf("expected resolved scope without DB")
	}
	if o.DBReachable {
		t.Errorf("expected DB unreachable when file is missing")
	}
}

func TestAgentContext_JSONIsValid(t *testing.T) {
	clearScopeEnvT(t)
	t.Setenv("ASP_SCOPE_REF", "agent:cody:project:wrkq")

	o := runAgentContextForTest(t)
	if o.Scope == nil {
		t.Fatalf("expected scope")
	}

	// Confirm the struct marshals to JSON without error.
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"canonical_ref":"agent:cody:project:wrkq"`)) {
		t.Errorf("missing canonical_ref in JSON: %s", string(b))
	}
}

func TestAgentContext_DisagreementDiagnostic(t *testing.T) {
	clearScopeEnvT(t)
	t.Setenv("ASP_SCOPE_REF", "agent:clod:project:wrkq")
	t.Setenv("ASP_HANDLE", "cody@wrkq")
	o := runAgentContextForTest(t)
	if o.Scope == nil {
		t.Fatalf("expected resolved scope, got %+v", o)
	}
	if o.Scope.AgentID != "clod" {
		t.Errorf("expected scopeRef to win (agent_id=clod), got %q", o.Scope.AgentID)
	}
	if len(o.Diagnostics) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	found := false
	for _, d := range o.Diagnostics {
		if d.Code == "ASP_SCOPE_REF_HANDLE_DISAGREE" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ASP_SCOPE_REF_HANDLE_DISAGREE diagnostic, got %v", o.Diagnostics)
	}
}
