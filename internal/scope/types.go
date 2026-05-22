// Package scope provides a Go port of the canonical agent-scope grammar
// (originally in TypeScript at ~/praesidium/agent-spaces/packages/agent-scope)
// plus an env-aware Resolve helper used by wrkq commands.
//
// Reference grammar:
//
//	scopeRef long form:   agent:<agentId>[:project:<projectId>[:task:<taskId>][:role:<roleName>]]
//	scopeHandle short:    <agentId>[@<projectId>[:<taskId>][/<roleName>]]
//
// Token identifiers must match [A-Za-z0-9._-]+ and be 1..64 characters.
package scope

import "regexp"

// TokenPattern is the regex for valid identifier tokens.
// Mirrors the TypeScript canonical: ^[A-Za-z0-9._-]+$.
var TokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Token length bounds.
const (
	TokenMinLength = 1
	TokenMaxLength = 64
)

// ScopeKind enumerates the valid scope shapes.
type ScopeKind string

const (
	KindAgent           ScopeKind = "agent"
	KindProject         ScopeKind = "project"
	KindProjectRole     ScopeKind = "project-role"
	KindProjectTask     ScopeKind = "project-task"
	KindProjectTaskRole ScopeKind = "project-task-role"
)

// ParsedScopeRef is a structured representation of a canonical scopeRef.
// Optional segments are empty strings when absent (zero-value safe).
type ParsedScopeRef struct {
	Kind      ScopeKind
	AgentID   string
	ProjectID string // empty when Kind == KindAgent
	TaskID    string // empty unless Kind is project-task or project-task-role
	RoleName  string // empty unless Kind is project-role or project-task-role
	ScopeRef  string // canonical scopeRef string
}
