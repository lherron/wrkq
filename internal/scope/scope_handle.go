package scope

import (
	"fmt"
	"strings"
)

// ValidateScopeHandle validates a scope handle string against the canonical
// shorthand grammar:
//
//	<agentId> ["@" <projectId> [":" <taskId>] ["/" <roleName>]]
func ValidateScopeHandle(handle string) ValidationResult {
	if len(handle) == 0 {
		return ValidationResult{OK: false, Error: "ScopeHandle must not be empty"}
	}

	// Split off role: only valid after "@", using the first "/" in the project
	// portion.
	main := handle
	var roleName string
	hasRole := false

	atIdx := strings.Index(handle, "@")
	if atIdx != -1 {
		afterAt := handle[atIdx+1:]
		slashIdx := strings.Index(afterAt, "/")
		if slashIdx != -1 {
			roleName = afterAt[slashIdx+1:]
			hasRole = true
			main = handle[:atIdx+1+slashIdx]
		}
	}

	// Now parse main: agentId ["@" projectId [":" taskId]]
	var agentID, projectID, taskID string
	hasProject := false
	hasTask := false

	if atIdx == -1 {
		agentID = main
	} else {
		agentID = main[:atIdx]
		projectPart := main[atIdx+1:]
		hasProject = true

		colonIdx := strings.Index(projectPart, ":")
		if colonIdx == -1 {
			projectID = projectPart
		} else {
			projectID = projectPart[:colonIdx]
			taskID = projectPart[colonIdx+1:]
			hasTask = true
		}
	}

	if e := validateToken(agentID, "agentId"); e != "" {
		return ValidationResult{OK: false, Error: e}
	}
	if hasProject {
		if e := validateToken(projectID, "projectId"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}
	}
	if hasTask {
		if e := validateToken(taskID, "taskId"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}
	}
	if hasRole {
		if e := validateToken(roleName, "roleName"); e != "" {
			return ValidationResult{OK: false, Error: e}
		}
	}

	return ValidationResult{OK: true}
}

// ParseScopeHandle parses a scope handle string into a ParsedScopeRef.
// Returns an error if the handle is invalid.
func ParseScopeHandle(handle string) (ParsedScopeRef, error) {
	v := ValidateScopeHandle(handle)
	if !v.OK {
		return ParsedScopeRef{}, fmt.Errorf("invalid ScopeHandle %q: %s", handle, v.Error)
	}

	// Split off role
	main := handle
	var roleName string
	hasRole := false

	atIdx := strings.Index(handle, "@")
	if atIdx != -1 {
		afterAt := handle[atIdx+1:]
		slashIdx := strings.Index(afterAt, "/")
		if slashIdx != -1 {
			roleName = afterAt[slashIdx+1:]
			hasRole = true
			main = handle[:atIdx+1+slashIdx]
		}
	}

	// Parse main: agentId ["@" projectId [":" taskId]]
	var agentID, projectID, taskID string

	if atIdx == -1 {
		agentID = main
	} else {
		agentID = main[:atIdx]
		projectPart := main[atIdx+1:]
		colonIdx := strings.Index(projectPart, ":")
		if colonIdx == -1 {
			projectID = projectPart
		} else {
			projectID = projectPart[:colonIdx]
			taskID = projectPart[colonIdx+1:]
		}
	}

	scopeRef := "agent:" + agentID
	if projectID != "" {
		scopeRef += ":project:" + projectID
	}
	if taskID != "" {
		scopeRef += ":task:" + taskID
	}
	if hasRole {
		scopeRef += ":role:" + roleName
	}

	return ParseScopeRef(scopeRef)
}

// FormatScopeHandle formats a ParsedScopeRef into its shorthand handle form.
// Exact inverse of ParseScopeHandle for well-formed inputs.
func FormatScopeHandle(p ParsedScopeRef) string {
	handle := p.AgentID
	if p.ProjectID != "" {
		handle += "@" + p.ProjectID
	}
	if p.TaskID != "" {
		handle += ":" + p.TaskID
	}
	if p.RoleName != "" {
		handle += "/" + p.RoleName
	}
	return handle
}
