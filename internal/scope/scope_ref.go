package scope

import (
	"fmt"
	"strings"
)

// ValidationResult is the validation outcome for a scopeRef or scopeHandle.
type ValidationResult struct {
	OK    bool
	Error string
}

func validateToken(value, label string) string {
	n := len(value)
	if n < TokenMinLength || n > TokenMaxLength {
		return fmt.Sprintf("%s must be %d..%d characters, got %d", label, TokenMinLength, TokenMaxLength, n)
	}
	if !TokenPattern.MatchString(value) {
		return fmt.Sprintf("%s contains invalid characters: must match [A-Za-z0-9._-]+", label)
	}
	return ""
}

// ValidateScopeRef validates a scopeRef string against the canonical grammar.
// On failure, OK is false and Error is a descriptive message.
func ValidateScopeRef(scopeRef string) ValidationResult {
	parts := strings.Split(scopeRef, ":")

	// Must start with "agent:<agentId>"
	if len(parts) < 2 || parts[0] != "agent" {
		return ValidationResult{OK: false, Error: `ScopeRef must start with "agent:<agentId>"`}
	}

	agentID := parts[1]
	if e := validateToken(agentID, "agentId"); e != "" {
		return ValidationResult{OK: false, Error: e}
	}

	// agent:<agentId> — valid
	if len(parts) == 2 {
		return ValidationResult{OK: true}
	}

	// Must continue with "project:<projectId>"
	if len(parts) < 4 || parts[2] != "project" {
		return ValidationResult{OK: false, Error: `After "agent:<agentId>", expected "project:<projectId>"`}
	}

	projectID := parts[3]
	if e := validateToken(projectID, "projectId"); e != "" {
		return ValidationResult{OK: false, Error: e}
	}

	// agent:<agentId>:project:<projectId> — valid
	if len(parts) == 4 {
		return ValidationResult{OK: true}
	}

	// Next segment must be "role" or "task"
	nextKey := parts[4]
	if nextKey == "role" {
		if len(parts) != 6 {
			return ValidationResult{OK: false, Error: `Expected exactly "role:<roleName>" after project segment`}
		}
		roleName := parts[5]
		if e := validateToken(roleName, "roleName"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}
		return ValidationResult{OK: true}
	}

	if nextKey == "task" {
		if len(parts) < 6 {
			return ValidationResult{OK: false, Error: `Expected "task:<taskId>" after project segment`}
		}
		taskID := parts[5]
		if e := validateToken(taskID, "taskId"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}

		// agent:<agentId>:project:<projectId>:task:<taskId> — valid
		if len(parts) == 6 {
			return ValidationResult{OK: true}
		}

		if len(parts) != 8 || parts[6] != "role" {
			return ValidationResult{OK: false, Error: `After "task:<taskId>", expected "role:<roleName>"`}
		}
		roleName := parts[7]
		if e := validateToken(roleName, "roleName"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}
		return ValidationResult{OK: true}
	}

	return ValidationResult{OK: false, Error: fmt.Sprintf(`Unexpected segment %q after project; expected "role" or "task"`, nextKey)}
}

// ParseScopeRef parses a canonical scopeRef into a ParsedScopeRef.
// Returns an error if the scopeRef is invalid.
func ParseScopeRef(scopeRef string) (ParsedScopeRef, error) {
	v := ValidateScopeRef(scopeRef)
	if !v.OK {
		return ParsedScopeRef{}, fmt.Errorf("invalid ScopeRef %q: %s", scopeRef, v.Error)
	}

	parts := strings.Split(scopeRef, ":")
	agentID := parts[1]

	if len(parts) == 2 {
		return ParsedScopeRef{Kind: KindAgent, AgentID: agentID, ScopeRef: scopeRef}, nil
	}

	projectID := parts[3]
	if len(parts) == 4 {
		return ParsedScopeRef{
			Kind:      KindProject,
			AgentID:   agentID,
			ProjectID: projectID,
			ScopeRef:  scopeRef,
		}, nil
	}

	nextKey := parts[4]
	if nextKey == "role" {
		roleName := parts[5]
		return ParsedScopeRef{
			Kind:      KindProjectRole,
			AgentID:   agentID,
			ProjectID: projectID,
			RoleName:  roleName,
			ScopeRef:  scopeRef,
		}, nil
	}

	// nextKey == "task"
	taskID := parts[5]
	if len(parts) == 6 {
		return ParsedScopeRef{
			Kind:      KindProjectTask,
			AgentID:   agentID,
			ProjectID: projectID,
			TaskID:    taskID,
			ScopeRef:  scopeRef,
		}, nil
	}

	roleName := parts[7]
	return ParsedScopeRef{
		Kind:      KindProjectTaskRole,
		AgentID:   agentID,
		ProjectID: projectID,
		TaskID:    taskID,
		RoleName:  roleName,
		ScopeRef:  scopeRef,
	}, nil
}

// FormatScopeRef formats a ParsedScopeRef back into canonical string form.
// Optional segments are emitted only when non-empty.
func FormatScopeRef(p ParsedScopeRef) string {
	ref := "agent:" + p.AgentID
	if p.ProjectID != "" {
		ref += ":project:" + p.ProjectID
	}
	if p.TaskID != "" {
		ref += ":task:" + p.TaskID
	}
	if p.RoleName != "" {
		ref += ":role:" + p.RoleName
	}
	return ref
}

// AncestorScopeRefs returns the ancestor scopeRefs for a given scopeRef,
// least-specific to most-specific.
func AncestorScopeRefs(scopeRef string) ([]string, error) {
	p, err := ParseScopeRef(scopeRef)
	if err != nil {
		return nil, err
	}

	ancestors := []string{"agent:" + p.AgentID}
	if p.ProjectID != "" {
		ancestors = append(ancestors, "agent:"+p.AgentID+":project:"+p.ProjectID)
	}
	if p.TaskID != "" {
		ancestors = append(ancestors,
			"agent:"+p.AgentID+":project:"+p.ProjectID+":task:"+p.TaskID)
	}
	if p.RoleName != "" {
		// The full ref with role is always the most specific.
		ancestors = append(ancestors, scopeRef)
	}
	return ancestors, nil
}
