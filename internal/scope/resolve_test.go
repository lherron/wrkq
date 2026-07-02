package scope

import (
	"strings"
	"testing"
)

// setEnv sets an env var for the test and restores its previous value when
// the test ends.
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
}

// clearScopeEnv unsets every env var Resolve cares about for the lifetime of
// the test.
func clearScopeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AGENT_SCOPE_REF", "ASP_SCOPE_REF", "ASP_HANDLE", "ASP_AGENT_ID", "ASP_PROJECT"} {
		t.Setenv(k, "")
	}
}

func TestResolve_OverrideScopeRef(t *testing.T) {
	clearScopeEnv(t)
	r, diags, err := Resolve("agent:cody:project:wrkq")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
	if r.Source != SourceOverride {
		t.Errorf("Source=%q, want %q", r.Source, SourceOverride)
	}
	if r.AgentID != "cody" || r.ProjectID != "wrkq" {
		t.Errorf("unexpected scope: %+v", r)
	}
	if r.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
}

func TestResolve_OverrideHandle(t *testing.T) {
	clearScopeEnv(t)
	r, _, err := Resolve("cody@wrkq")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceOverride {
		t.Errorf("Source=%q", r.Source)
	}
	if r.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
}

func TestResolve_OverrideNormalizesTaskToProject(t *testing.T) {
	clearScopeEnv(t)
	r, _, err := Resolve("agent:clod:project:wrkq:task:primary")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.CanonicalRef != "agent:clod:project:wrkq" {
		t.Errorf("CanonicalRef=%q, want agent:clod:project:wrkq", r.CanonicalRef)
	}
	if r.TaskID != "primary" {
		t.Errorf("TaskID lost in pre-normalization fields: %+v", r)
	}
	if r.Kind != KindProjectTask {
		t.Errorf("Kind=%q, want %q (pre-normalization should preserve original kind)", r.Kind, KindProjectTask)
	}
}

func TestResolvedScope_FullRefPreservesTaskAndRole(t *testing.T) {
	clearScopeEnv(t)
	r, _, err := Resolve("agent:clod:project:wrkq:task:primary")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	// CanonicalRef drops the task for v1; FullRef must preserve it for
	// durable attribution (created_by_scope_ref).
	if got, want := r.FullRef(), "agent:clod:project:wrkq:task:primary"; got != want {
		t.Errorf("FullRef()=%q, want %q", got, want)
	}
}

func TestResolvedScope_FullRefEmptyWhenNoAgent(t *testing.T) {
	var r ResolvedScope
	if got := r.FullRef(); got != "" {
		t.Errorf("FullRef() on zero value = %q, want empty", got)
	}
}

func TestResolve_OverrideInvalid(t *testing.T) {
	clearScopeEnv(t)
	_, _, err := Resolve("has space")
	if err == nil {
		t.Fatalf("expected error for invalid override")
	}
}

func TestResolve_ScopeRefEnv(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_SCOPE_REF", "agent:clod:project:wrkq:task:primary")
	r, diags, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceScopeRefEnv {
		t.Errorf("Source=%q, want %q", r.Source, SourceScopeRefEnv)
	}
	if r.CanonicalRef != "agent:clod:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestResolve_AgentScopeRefEnv(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "AGENT_SCOPE_REF", "agent:cody:project:wrkq:task:T-05423:role:implementer")
	r, diags, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceAgentScopeRefEnv {
		t.Errorf("Source=%q, want %q", r.Source, SourceAgentScopeRefEnv)
	}
	if r.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
	if got, want := r.FullRef(), "agent:cody:project:wrkq:task:T-05423:role:implementer"; got != want {
		t.Errorf("FullRef()=%q, want %q", got, want)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestResolve_OverrideBeatsAgentScopeRef(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "AGENT_SCOPE_REF", "agent:cody:project:wrkq")
	r, _, err := Resolve("agent:clod:project:wrkq")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceOverride {
		t.Errorf("Source=%q, want %q", r.Source, SourceOverride)
	}
	if r.AgentID != "clod" {
		t.Errorf("AgentID=%q, want clod", r.AgentID)
	}
}

func TestResolve_AgentScopeRefBeatsASPScopeRefWithDiagnostic(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "AGENT_SCOPE_REF", "agent:cody:project:wrkq")
	setEnv(t, "ASP_SCOPE_REF", "agent:clod:project:wrkq")
	r, diags, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceAgentScopeRefEnv {
		t.Errorf("Source=%q, want %q", r.Source, SourceAgentScopeRefEnv)
	}
	if r.AgentID != "cody" {
		t.Errorf("AgentID=%q, want cody", r.AgentID)
	}
	found := false
	for _, d := range diags {
		if d.Code == "AGENT_SCOPE_REF_ASP_SCOPE_REF_DISAGREE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected AGENT_SCOPE_REF_ASP_SCOPE_REF_DISAGREE diagnostic, got %v", diags)
	}
}

func TestResolve_InvalidAgentScopeRefNamesEnv(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "AGENT_SCOPE_REF", "bad scope")
	_, _, err := Resolve("")
	if err == nil {
		t.Fatal("expected invalid AGENT_SCOPE_REF error")
	}
	if !strings.Contains(err.Error(), "AGENT_SCOPE_REF") {
		t.Fatalf("error should mention AGENT_SCOPE_REF: %v", err)
	}
}

func TestResolve_HandleEnv(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_HANDLE", "cody@wrkq:t1")
	r, _, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceHandleEnv {
		t.Errorf("Source=%q", r.Source)
	}
	if r.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
}

func TestResolve_ComposedEnv(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_AGENT_ID", "cody")
	setEnv(t, "ASP_PROJECT", "wrkq")
	r, _, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceComposedEnv {
		t.Errorf("Source=%q", r.Source)
	}
	if r.CanonicalRef != "agent:cody:project:wrkq" {
		t.Errorf("CanonicalRef=%q", r.CanonicalRef)
	}
}

func TestResolve_ScopeRefBeatsHandle_NoDiagWhenAgree(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_SCOPE_REF", "agent:clod:project:wrkq:task:primary")
	setEnv(t, "ASP_HANDLE", "clod@wrkq:primary")
	_, diags, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics when scopeRef and handle agree, got %v", diags)
	}
}

func TestResolve_ScopeRefHandleDisagree(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_SCOPE_REF", "agent:clod:project:wrkq")
	setEnv(t, "ASP_HANDLE", "cody@wrkq")
	r, diags, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if r.Source != SourceScopeRefEnv {
		t.Errorf("expected source=%q, got %q", SourceScopeRefEnv, r.Source)
	}
	if r.AgentID != "clod" {
		t.Errorf("AgentID=%q, want clod (scopeRef should win)", r.AgentID)
	}
	if len(diags) == 0 {
		t.Fatalf("expected a disagreement diagnostic")
	}
	found := false
	for _, d := range diags {
		if d.Code == "ASP_SCOPE_REF_HANDLE_DISAGREE" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ASP_SCOPE_REF_HANDLE_DISAGREE diagnostic, got %v", diags)
	}
}

func TestResolve_NoEnv_Error(t *testing.T) {
	clearScopeEnv(t)
	_, _, err := Resolve("")
	if err == nil {
		t.Fatalf("expected error when nothing resolves")
	}
	if !strings.Contains(err.Error(), "ASP_SCOPE_REF") {
		t.Errorf("error should mention ASP_SCOPE_REF: %v", err)
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Errorf("error should mention --scope: %v", err)
	}
}

func TestResolve_PartialComposedEnv_Error(t *testing.T) {
	clearScopeEnv(t)
	setEnv(t, "ASP_AGENT_ID", "cody")
	_, _, err := Resolve("")
	if err == nil {
		t.Fatalf("expected error when only ASP_AGENT_ID is set")
	}
	if !strings.Contains(err.Error(), "ASP_PROJECT") {
		t.Errorf("error should mention ASP_PROJECT: %v", err)
	}
}

func TestEnforceSelfScope(t *testing.T) {
	resolved := ResolvedScope{AgentID: "cody"}

	if err := EnforceSelfScope(resolved, RuntimeIdentity{AgentID: ""}); err != nil {
		t.Errorf("expected nil error when runtime is empty, got %v", err)
	}
	if err := EnforceSelfScope(resolved, RuntimeIdentity{AgentID: "cody"}); err != nil {
		t.Errorf("expected nil error when agent matches, got %v", err)
	}
	if err := EnforceSelfScope(resolved, RuntimeIdentity{AgentID: "clod"}); err == nil {
		t.Errorf("expected error when agent disagrees")
	}
}
