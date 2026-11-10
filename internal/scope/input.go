package scope

import (
	"fmt"
	"strings"
)

// DefaultTaskID is the task input resolution fills when a project is present
// and neither the input nor the options name one.
const DefaultTaskID = "primary"

// InputOptions are the per-call defaults for ResolveQualifiedInput. Empty means
// absent. EnvDefaultTask is the caller's ASP_DEFAULT_TASK value for this call;
// ResolveQualifiedInput never reads the process env itself.
type InputOptions struct {
	ProjectID       string // project when the input has none
	TaskID          string // task when the input has none; beats DefaultTaskID
	DefaultTaskID   string // task default; beats EnvDefaultTask
	DefaultRoleName string // role when the input has a task and no role
	EnvDefaultTask  string // trimmed; beats DefaultTaskID's fallback "primary"
}

// ResolvedInput is the outcome of ResolveQualifiedInput.
type ResolvedInput struct {
	Parsed   ParsedScopeRef
	ScopeRef string
	LaneRef  string
	// Directives holds the raw tokens of the input's "+" block, nil without
	// one. They never enter the identity above; wrkq does not interpret them.
	Directives []string
}

// ResolveQualifiedInput is the grammar's input resolution (input.resolve;
// TypeScript resolveQualifiedScopeInput): it accepts a scope ref, a scope
// handle or a session handle, with an optional "+" directive block, and
// qualifies it. A missing project comes from opts.ProjectID; when a project is
// present a missing task comes from opts.TaskID, opts.DefaultTaskID,
// opts.EnvDefaultTask, then DefaultTaskID ("primary"); opts.DefaultRoleName
// applies only when the input carried a task and no role. A task with no
// project is refused (task_requires_project).
//
// It is not Resolve. Resolve is wrkq's caller-scope policy and never adds a
// task; no existing caller uses this function yet.
func ResolveQualifiedInput(input string, opts InputOptions) (ResolvedInput, error) {
	handle, directives := input, []string(nil)
	if before, block, ok := strings.Cut(input, directiveSeparator); ok {
		handle, directives = before, strings.Split(block, directiveSeparator)
	}

	f, lane, err := splitScopeInput(handle)
	if err != nil {
		return ResolvedInput{}, err
	}
	if f.taskID != "" && f.roleName == "" {
		f.roleName = opts.DefaultRoleName
	}
	if f.projectID == "" {
		f.projectID = opts.ProjectID
	}
	if f.taskID == "" && f.projectID != "" {
		f.taskID = firstNonEmpty(opts.TaskID, opts.DefaultTaskID, strings.TrimSpace(opts.EnvDefaultTask), DefaultTaskID)
	}
	if f.taskID != "" && f.projectID == "" {
		return ResolvedInput{}, &GrammarError{
			Code: CodeTaskRequiresProject,
			Message: fmt.Sprintf("invalid scope input %q: task %q requires a project; use %q",
				input, f.taskID, f.agentID+"@<project>:"+f.taskID),
		}
	}

	parsed, err := ParseScopeRef(fieldsRef(f))
	if err != nil {
		return ResolvedInput{}, err
	}
	return ResolvedInput{Parsed: parsed, ScopeRef: parsed.ScopeRef, LaneRef: lane, Directives: directives}, nil
}

// splitScopeInput decomposes a session handle (it has a "~"), a scope ref, or
// a scope handle, in that order, without defaulting. A scope ref wins over the
// project-deferred handle form it also matches ("agent:alice").
func splitScopeInput(input string) (scopeFields, string, error) {
	if strings.Contains(input, "~") {
		session, err := ParseSessionHandle(input)
		if err != nil {
			return scopeFields{}, "", err
		}
		parsed, err := ParseScopeRef(session.ScopeRef)
		if err != nil {
			return scopeFields{}, "", err
		}
		return fieldsOf(parsed), session.LaneRef, nil
	}
	if parsed, err := ParseScopeRef(input); err == nil {
		return fieldsOf(parsed), MainLane, nil
	}
	if ValidateScopeHandle(input).OK {
		return splitHandle(input), MainLane, nil
	}
	code := CodeShapeInvalid
	if input == "" {
		code = CodeEmpty
	}
	return scopeFields{}, "", &GrammarError{Code: code,
		Message: fmt.Sprintf("invalid scope input %q: expected a ScopeHandle, SessionHandle, or ScopeRef", input)}
}

func fieldsOf(p ParsedScopeRef) scopeFields {
	return scopeFields{agentID: p.AgentID, projectID: p.ProjectID, taskID: p.TaskID, roleName: p.RoleName}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
