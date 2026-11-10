package scope

import (
	"fmt"
	"strings"
)

// directiveSeparator opens a provisioning directive block ("+key=value"). It is
// outside the token charset; only ResolveQualifiedInput accepts (and strips) it.
const directiveSeparator = "+"

// scopeFields is a scope decomposed into its tokens, before validation or ref
// assembly. Empty means absent; the has* flags record a separator that was
// present with an empty token, which validation then refuses.
type scopeFields struct {
	agentID, projectID, taskID, roleName string
	hasProject, hasTask, hasRole         bool
}

// splitHandle splits a scope handle into its tokens without validating them:
//
//	<agentId> [":" <taskId>]                              (no "@": project from context)
//	<agentId> "@" <projectId> [":" <taskId>] ["/" <roleName>]
//
// Without "@" a "/" is not parsed and lands in a token, which validation refuses.
func splitHandle(handle string) scopeFields {
	atIdx := strings.Index(handle, "@")
	if atIdx == -1 {
		agentID, taskID, hasTask := strings.Cut(handle, ":")
		return scopeFields{agentID: agentID, taskID: taskID, hasTask: hasTask}
	}

	f := scopeFields{agentID: handle[:atIdx], hasProject: true}
	projectPart := handle[atIdx+1:]
	if before, role, ok := strings.Cut(projectPart, "/"); ok {
		projectPart, f.roleName, f.hasRole = before, role, true
	}
	f.projectID, f.taskID, f.hasTask = strings.Cut(projectPart, ":")
	return f
}

// ValidateScopeHandle validates a scope handle string against the canonical
// shorthand grammar (see splitHandle). It validates the tokens only: the
// project-deferred "<agentId>:<taskId>" form passes here and is refused by
// ParseScopeHandle, because a task with no project is not a scope ref.
func ValidateScopeHandle(handle string) ValidationResult {
	if len(handle) == 0 {
		return invalid(CodeEmpty, "ScopeHandle must not be empty")
	}
	if strings.Contains(handle, directiveSeparator) {
		return invalid(CodeDirectiveNotAllowed,
			`provisioning directives ("+key=value") are not part of a ScopeHandle`)
	}

	f := splitHandle(handle)
	if r, ok := tokenResult(f.agentID, "agentId"); !ok {
		return r
	}
	if f.hasProject {
		if r, ok := tokenResult(f.projectID, "projectId"); !ok {
			return r
		}
	}
	if f.hasTask {
		if r, ok := tokenResult(f.taskID, "taskId"); !ok {
			return r
		}
	}
	if f.hasRole {
		if r, ok := tokenResult(f.roleName, "roleName"); !ok {
			return r
		}
	}
	return ValidationResult{OK: true}
}

// ParseScopeHandle parses a scope handle string into a ParsedScopeRef. It does
// not default: "alice@demo" is agent:alice:project:demo, and "alice:t1" (task,
// project from context) is refused. ResolveQualifiedInput does the defaulting.
func ParseScopeHandle(handle string) (ParsedScopeRef, error) {
	v := ValidateScopeHandle(handle)
	if !v.OK {
		return ParsedScopeRef{}, &GrammarError{Code: v.Code, Message: fmt.Sprintf("invalid ScopeHandle %q: %s", handle, v.Error)}
	}
	return ParseScopeRef(fieldsRef(splitHandle(handle)))
}

// f2ref assembles the scope ref string for a split handle.
func fieldsRef(f scopeFields) string {
	return FormatScopeRef(ParsedScopeRef{AgentID: f.agentID, ProjectID: f.projectID, TaskID: f.taskID, RoleName: f.roleName})
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
