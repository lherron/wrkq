package scope

import (
	"fmt"
	"os"
	"strings"
)

// Source identifies where the resolved scope value came from.
const (
	SourceOverride         = "override"
	SourceAgentScopeRefEnv = "AGENT_SCOPE_REF"
	SourceScopeRefEnv      = "ASP_SCOPE_REF"
	SourceHandleEnv        = "ASP_HANDLE"
	SourceComposedEnv      = "ASP_AGENT_ID+ASP_PROJECT"
)

// DiagnosticLevel is the severity of a diagnostic emitted during resolution.
type DiagnosticLevel string

const (
	DiagWarn  DiagnosticLevel = "warn"
	DiagInfo  DiagnosticLevel = "info"
	DiagError DiagnosticLevel = "error"
)

// Diagnostic carries a soft warning, info note, or error encountered during
// scope resolution. Resolve returns diagnostics alongside the resolved scope
// so callers can decide whether to surface them.
type Diagnostic struct {
	Level   DiagnosticLevel `json:"level"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
}

// ResolvedScope is the outcome of Resolve. Optional fields are empty strings
// when the input did not specify them.
//
// Raw is the literal input that produced the resolution (the override flag
// value or env value). Source identifies which input was selected. Kind /
// AgentID / ProjectID / TaskID / RoleName describe the parsed input (before
// project-kind normalization). CanonicalRef is always the post-normalization
// canonical scopeRef used by v1 callers (task/role segments dropped).
type ResolvedScope struct {
	Raw          string    `json:"raw"`
	Source       string    `json:"source"`
	Kind         ScopeKind `json:"kind"`
	AgentID      string    `json:"agent_id"`
	ProjectID    string    `json:"project_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	RoleName     string    `json:"role_name,omitempty"`
	CanonicalRef string    `json:"canonical_ref"`
}

// EnvSnapshot captures the env vars we inspect for resolution. Useful for
// `wrkq agent-context` introspection.
type EnvSnapshot struct {
	AgentScopeRef string `json:"AGENT_SCOPE_REF,omitempty"`
	ASPScopeRef   string `json:"ASP_SCOPE_REF,omitempty"`
	ASPHandle     string `json:"ASP_HANDLE,omitempty"`
	ASPAgentID    string `json:"ASP_AGENT_ID,omitempty"`
	ASPProject    string `json:"ASP_PROJECT,omitempty"`
}

// ReadEnv reads the env vars we care about into an EnvSnapshot.
func ReadEnv() EnvSnapshot {
	return EnvSnapshot{
		AgentScopeRef: os.Getenv("AGENT_SCOPE_REF"),
		ASPScopeRef:   os.Getenv("ASP_SCOPE_REF"),
		ASPHandle:     os.Getenv("ASP_HANDLE"),
		ASPAgentID:    os.Getenv("ASP_AGENT_ID"),
		ASPProject:    os.Getenv("ASP_PROJECT"),
	}
}

// looksLikeScopeRef reports whether s appears to be in canonical scopeRef form.
// Heuristic: starts with "agent:". (The handle grammar permits "agent" as an
// agentId, but a handle like "alice@demo" never starts with "agent:".)
func looksLikeScopeRef(s string) bool {
	return strings.HasPrefix(s, "agent:")
}

// parseScopeInput auto-detects whether s is a scopeRef or scopeHandle and
// parses accordingly. ScopeRef takes precedence when both shapes are valid
// (a handle like "alice@demo" never starts with "agent:" so the prefix test
// reliably distinguishes them).
func parseScopeInput(s string) (ParsedScopeRef, error) {
	if looksLikeScopeRef(s) {
		return ParseScopeRef(s)
	}
	return ParseScopeHandle(s)
}

// correctiveExample is the user-facing example string used in resolution
// errors and the agent-context CLI.
const correctiveExample = `set AGENT_SCOPE_REF, ASP_SCOPE_REF, ASP_HANDLE, or both ASP_AGENT_ID and ASP_PROJECT.
Examples:
  AGENT_SCOPE_REF=agent:cody:project:wrkq
  ASP_SCOPE_REF=agent:cody:project:wrkq
  ASP_HANDLE=cody@wrkq
  ASP_AGENT_ID=cody ASP_PROJECT=wrkq
Or pass --scope cody@wrkq.`

// Resolve resolves the active scope using --override → AGENT_SCOPE_REF →
// ASP_SCOPE_REF → ASP_HANDLE → ASP_AGENT_ID+ASP_PROJECT, normalizes to project
// kind for v1, and returns diagnostics on disagreement between env aliases.
//
// When override is non-empty, env vars are ignored entirely (still surfaced
// for introspection callers via ReadEnv).
//
// Returns an error when nothing resolves; the error message includes the
// corrective example.
func Resolve(override string) (ResolvedScope, []Diagnostic, error) {
	var diags []Diagnostic
	env := ReadEnv()

	// 1. --override wins.
	if override != "" {
		parsed, err := parseScopeInput(override)
		if err != nil {
			return ResolvedScope{}, diags, fmt.Errorf("invalid --scope %q: %w", override, err)
		}
		return finalize(parsed, override, SourceOverride, diags), diags, nil
	}

	// 2. AGENT_SCOPE_REF is the canonical runtime scope env. It wins over
	//    ASP_SCOPE_REF; emit a diagnostic when both parse but disagree.
	if env.AgentScopeRef != "" {
		parsed, err := ParseScopeRef(env.AgentScopeRef)
		if err != nil {
			return ResolvedScope{}, diags, fmt.Errorf("invalid AGENT_SCOPE_REF %q: %w", env.AgentScopeRef, err)
		}
		if env.ASPScopeRef != "" {
			aspParsed, aspErr := ParseScopeRef(env.ASPScopeRef)
			if aspErr != nil {
				diags = append(diags, Diagnostic{
					Level:   DiagWarn,
					Code:    "ASP_SCOPE_REF_INVALID",
					Message: fmt.Sprintf("ASP_SCOPE_REF %q failed to parse: %v; using AGENT_SCOPE_REF", env.ASPScopeRef, aspErr),
				})
			} else if !sameAgentProjectTaskRole(parsed, aspParsed) {
				diags = append(diags, Diagnostic{
					Level: DiagWarn,
					Code:  "AGENT_SCOPE_REF_ASP_SCOPE_REF_DISAGREE",
					Message: fmt.Sprintf(
						"AGENT_SCOPE_REF (%s) and ASP_SCOPE_REF (%s) disagree; preferring AGENT_SCOPE_REF",
						parsed.ScopeRef, aspParsed.ScopeRef,
					),
				})
			}
		}
		return finalize(parsed, env.AgentScopeRef, SourceAgentScopeRefEnv, diags), diags, nil
	}

	// 3. ASP_SCOPE_REF wins over ASP_HANDLE; emit a diagnostic if they
	//    disagree.
	if env.ASPScopeRef != "" {
		parsed, err := ParseScopeRef(env.ASPScopeRef)
		if err != nil {
			return ResolvedScope{}, diags, fmt.Errorf("invalid ASP_SCOPE_REF %q: %w", env.ASPScopeRef, err)
		}

		if env.ASPHandle != "" {
			handleParsed, hErr := ParseScopeHandle(env.ASPHandle)
			if hErr != nil {
				diags = append(diags, Diagnostic{
					Level:   DiagWarn,
					Code:    "ASP_HANDLE_INVALID",
					Message: fmt.Sprintf("ASP_HANDLE %q failed to parse: %v; using ASP_SCOPE_REF", env.ASPHandle, hErr),
				})
			} else if !sameAgentProjectTaskRole(parsed, handleParsed) {
				diags = append(diags, Diagnostic{
					Level: DiagWarn,
					Code:  "ASP_SCOPE_REF_HANDLE_DISAGREE",
					Message: fmt.Sprintf(
						"ASP_SCOPE_REF (%s) and ASP_HANDLE (%s) disagree; preferring ASP_SCOPE_REF",
						parsed.ScopeRef, handleParsed.ScopeRef,
					),
				})
			}
		}

		return finalize(parsed, env.ASPScopeRef, SourceScopeRefEnv, diags), diags, nil
	}

	// 4. ASP_HANDLE next.
	if env.ASPHandle != "" {
		parsed, err := ParseScopeHandle(env.ASPHandle)
		if err != nil {
			return ResolvedScope{}, diags, fmt.Errorf("invalid ASP_HANDLE %q: %w", env.ASPHandle, err)
		}
		return finalize(parsed, env.ASPHandle, SourceHandleEnv, diags), diags, nil
	}

	// 5. ASP_AGENT_ID + ASP_PROJECT composed.
	if env.ASPAgentID != "" && env.ASPProject != "" {
		composed := fmt.Sprintf("%s@%s", env.ASPAgentID, env.ASPProject)
		parsed, err := ParseScopeHandle(composed)
		if err != nil {
			return ResolvedScope{}, diags, fmt.Errorf("invalid ASP_AGENT_ID/ASP_PROJECT combination %q: %w", composed, err)
		}
		return finalize(parsed, composed, SourceComposedEnv, diags), diags, nil
	}

	if env.ASPAgentID != "" && env.ASPProject == "" {
		return ResolvedScope{}, diags, fmt.Errorf(
			"ASP_AGENT_ID is set but ASP_PROJECT is empty; %s",
			correctiveExample,
		)
	}
	if env.ASPProject != "" && env.ASPAgentID == "" {
		return ResolvedScope{}, diags, fmt.Errorf(
			"ASP_PROJECT is set but ASP_AGENT_ID is empty; %s",
			correctiveExample,
		)
	}

	return ResolvedScope{}, diags, fmt.Errorf(
		"no scope resolvable: --scope, AGENT_SCOPE_REF, ASP_SCOPE_REF, ASP_HANDLE, or ASP_AGENT_ID+ASP_PROJECT must be set.\n%s",
		correctiveExample,
	)
}

// FullRef returns the complete canonical scopeRef for the resolved scope,
// preserving the task and role segments. Unlike CanonicalRef (which normalizes
// to project kind and drops task/role for v1), FullRef is suitable for durable
// attribution such as a task's created_by_scope_ref. Returns "" when no agent
// is resolved.
func (r ResolvedScope) FullRef() string {
	if r.AgentID == "" {
		return ""
	}
	ref := "agent:" + r.AgentID
	if r.ProjectID != "" {
		ref += ":project:" + r.ProjectID
	}
	if r.TaskID != "" {
		ref += ":task:" + r.TaskID
	}
	if r.RoleName != "" {
		ref += ":role:" + r.RoleName
	}
	return ref
}

// finalize builds a ResolvedScope from a parsed input, normalizing to project
// kind for v1 (task/role dropped from the canonical ref but preserved in the
// pre-normalization fields for diagnostics).
func finalize(parsed ParsedScopeRef, raw, source string, _ []Diagnostic) ResolvedScope {
	canonical := normalizeToProject(parsed)
	return ResolvedScope{
		Raw:          raw,
		Source:       source,
		Kind:         parsed.Kind,
		AgentID:      parsed.AgentID,
		ProjectID:    parsed.ProjectID,
		TaskID:       parsed.TaskID,
		RoleName:     parsed.RoleName,
		CanonicalRef: canonical,
	}
}

// normalizeToProject returns the v1 canonical scopeRef:
//   - agent-only inputs stay agent-only
//   - everything else collapses to agent:<id>:project:<id> (task/role dropped)
func normalizeToProject(p ParsedScopeRef) string {
	if p.ProjectID == "" {
		return "agent:" + p.AgentID
	}
	return "agent:" + p.AgentID + ":project:" + p.ProjectID
}

// sameAgentProjectTaskRole reports whether two parsed scope refs describe the
// same agent/project/task/role tuple (i.e. before normalization).
func sameAgentProjectTaskRole(a, b ParsedScopeRef) bool {
	return a.AgentID == b.AgentID &&
		a.ProjectID == b.ProjectID &&
		a.TaskID == b.TaskID &&
		a.RoleName == b.RoleName
}

// RuntimeIdentity is a minimal description of the agent running the current
// process. EnforceSelfScope uses it to confirm that a resolved scope refers to
// the same agent.
type RuntimeIdentity struct {
	AgentID string
}

// EnforceSelfScope returns an error if resolved.AgentID disagrees with the
// runtime's known AgentID. It is a no-op when the runtime AgentID is unknown
// (empty string), so callers can opt-out gracefully when running outside an
// agent runtime.
//
// Intended for handoff-create style flows that should refuse to write a
// handoff targeting another agent.
func EnforceSelfScope(resolved ResolvedScope, runtime RuntimeIdentity) error {
	if runtime.AgentID == "" {
		return nil
	}
	if resolved.AgentID == "" {
		return fmt.Errorf("resolved scope has no agentId; cannot enforce self-scope against runtime agent %q", runtime.AgentID)
	}
	if resolved.AgentID != runtime.AgentID {
		return fmt.Errorf("scope agentId %q does not match runtime agentId %q; refusing cross-agent operation", resolved.AgentID, runtime.AgentID)
	}
	return nil
}
