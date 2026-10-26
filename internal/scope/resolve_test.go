package scope

import (
	"strings"
	"testing"
)

// clearScopeEnv unsets every env var Resolve cares about for the lifetime of
// the test.
func clearScopeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AGENT_SCOPE_REF", "ASP_SCOPE_REF", "ASP_HANDLE", "ASP_AGENT_ID", "ASP_PROJECT"} {
		t.Setenv(k, "")
	}
}

// TestScopeComesFromHighestRankedSource is the scope precedence ladder: --scope
// override, AGENT_SCOPE_REF, ASP_SCOPE_REF, ASP_HANDLE, then ASP_AGENT_ID +
// ASP_PROJECT. Each row is one rung (or one refusal) and asserts the winning
// source, the canonical (project-depth) ref, the full ref kept for
// attribution, and the exact disagreement diagnostics.
//
// rewrite-held (§1): the caller-visible form is `wrkq agent-context --json`
// argv, one row per case below, asserting source, canonical_ref and
// diagnostics (agent_context.go already emits all three) with the same env
// and --scope inputs, and the refusal rows asserting the error text. That
// table belongs in internal/rpccli, which this package cannot import, so the
// ladder stays here until it exists.
func TestScopeComesFromHighestRankedSource(t *testing.T) {
	tests := []struct {
		name          string
		override      string
		env           map[string]string
		wantSource    string
		wantAgent     string
		wantCanonical string
		wantFullRef   string    // "" means same as wantCanonical
		wantTaskID    string    // checked when non-empty
		wantKind      ScopeKind // checked when non-empty
		wantDiags     []string  // exact diagnostic codes, in order
		wantErr       []string  // substrings the refusal must name; non-nil means a refusal row
	}{
		{
			name:          "override scope ref",
			override:      "agent:cody:project:wrkq",
			wantSource:    SourceOverride,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
		},
		{
			name:          "override handle",
			override:      "cody@wrkq",
			wantSource:    SourceOverride,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
		},
		{
			name:          "override at task depth canonicalizes to project but keeps task and kind",
			override:      "agent:clod:project:wrkq:task:primary",
			wantSource:    SourceOverride,
			wantAgent:     "clod",
			wantCanonical: "agent:clod:project:wrkq",
			wantFullRef:   "agent:clod:project:wrkq:task:primary",
			wantTaskID:    "primary",
			wantKind:      KindProjectTask,
		},
		{
			name:     "invalid override is refused",
			override: "has space",
			wantErr:  []string{"invalid --scope", "has space"},
		},
		{
			name:          "override beats AGENT_SCOPE_REF",
			override:      "agent:clod:project:wrkq",
			env:           map[string]string{"AGENT_SCOPE_REF": "agent:cody:project:wrkq"},
			wantSource:    SourceOverride,
			wantAgent:     "clod",
			wantCanonical: "agent:clod:project:wrkq",
		},
		{
			name:          "AGENT_SCOPE_REF at role depth",
			env:           map[string]string{"AGENT_SCOPE_REF": "agent:cody:project:wrkq:task:T-05423:role:implementer"},
			wantSource:    SourceAgentScopeRefEnv,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
			wantFullRef:   "agent:cody:project:wrkq:task:T-05423:role:implementer",
		},
		{
			name: "AGENT_SCOPE_REF beats a disagreeing ASP_SCOPE_REF with a diagnostic",
			env: map[string]string{
				"AGENT_SCOPE_REF": "agent:cody:project:wrkq",
				"ASP_SCOPE_REF":   "agent:clod:project:wrkq",
			},
			wantSource:    SourceAgentScopeRefEnv,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
			wantDiags:     []string{"AGENT_SCOPE_REF_ASP_SCOPE_REF_DISAGREE"},
		},
		{
			name:    "invalid AGENT_SCOPE_REF is refused naming the env var",
			env:     map[string]string{"AGENT_SCOPE_REF": "bad scope"},
			wantErr: []string{"AGENT_SCOPE_REF"},
		},
		{
			name:          "ASP_SCOPE_REF",
			env:           map[string]string{"ASP_SCOPE_REF": "agent:clod:project:wrkq:task:primary"},
			wantSource:    SourceScopeRefEnv,
			wantAgent:     "clod",
			wantCanonical: "agent:clod:project:wrkq",
			wantFullRef:   "agent:clod:project:wrkq:task:primary",
		},
		{
			name: "ASP_SCOPE_REF and an agreeing ASP_HANDLE give no diagnostic",
			env: map[string]string{
				"ASP_SCOPE_REF": "agent:clod:project:wrkq:task:primary",
				"ASP_HANDLE":    "clod@wrkq:primary",
			},
			wantSource:    SourceScopeRefEnv,
			wantAgent:     "clod",
			wantCanonical: "agent:clod:project:wrkq",
			wantFullRef:   "agent:clod:project:wrkq:task:primary",
		},
		{
			name: "ASP_SCOPE_REF beats a disagreeing ASP_HANDLE with a diagnostic",
			env: map[string]string{
				"ASP_SCOPE_REF": "agent:clod:project:wrkq",
				"ASP_HANDLE":    "cody@wrkq",
			},
			wantSource:    SourceScopeRefEnv,
			wantAgent:     "clod",
			wantCanonical: "agent:clod:project:wrkq",
			wantDiags:     []string{"ASP_SCOPE_REF_HANDLE_DISAGREE"},
		},
		{
			name:          "ASP_HANDLE",
			env:           map[string]string{"ASP_HANDLE": "cody@wrkq:t1"},
			wantSource:    SourceHandleEnv,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
			wantFullRef:   "agent:cody:project:wrkq:task:t1",
		},
		{
			name:          "ASP_AGENT_ID plus ASP_PROJECT",
			env:           map[string]string{"ASP_AGENT_ID": "cody", "ASP_PROJECT": "wrkq"},
			wantSource:    SourceComposedEnv,
			wantAgent:     "cody",
			wantCanonical: "agent:cody:project:wrkq",
		},
		{
			name:    "nothing set is refused naming ASP_SCOPE_REF and --scope",
			wantErr: []string{"ASP_SCOPE_REF", "--scope"},
		},
		{
			name:    "ASP_AGENT_ID without ASP_PROJECT is refused naming ASP_PROJECT",
			env:     map[string]string{"ASP_AGENT_ID": "cody"},
			wantErr: []string{"ASP_PROJECT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearScopeEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			r, diags, err := Resolve(tt.override)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected refusal, resolved %+v", r)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q should name %q", err.Error(), want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve error: %v", err)
			}
			if r.Source != tt.wantSource {
				t.Errorf("Source=%q, want %q", r.Source, tt.wantSource)
			}
			if r.AgentID != tt.wantAgent {
				t.Errorf("AgentID=%q, want %q", r.AgentID, tt.wantAgent)
			}
			if r.CanonicalRef != tt.wantCanonical {
				t.Errorf("CanonicalRef=%q, want %q", r.CanonicalRef, tt.wantCanonical)
			}
			wantFull := tt.wantFullRef
			if wantFull == "" {
				wantFull = tt.wantCanonical
			}
			if got := r.FullRef(); got != wantFull {
				t.Errorf("FullRef()=%q, want %q", got, wantFull)
			}
			if tt.wantTaskID != "" && r.TaskID != tt.wantTaskID {
				t.Errorf("TaskID=%q, want %q", r.TaskID, tt.wantTaskID)
			}
			if tt.wantKind != "" && r.Kind != tt.wantKind {
				t.Errorf("Kind=%q, want %q", r.Kind, tt.wantKind)
			}
			var codes []string
			for _, d := range diags {
				codes = append(codes, d.Code)
			}
			if strings.Join(codes, ",") != strings.Join(tt.wantDiags, ",") {
				t.Errorf("diagnostics=%v, want %v", codes, tt.wantDiags)
			}
		})
	}
}
