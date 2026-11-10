// Package scope is wrkq's Go implementation of the canonical agent-scope
// grammar (agent-spaces contracts/agent-scope), checked against the shared
// conformance fixture (contracts/agent-scope/conformance/scope-cases.json,
// copied to testdata/ by `just sync-scope-fixture`), plus Resolve, wrkq's own
// env-aware caller-scope policy built on top of the strict parsers.
//
// Declared fixture operations (the list in conformance_test.go):
//
//	scopeRef.parse       ParseScopeRef
//	scopeRef.format      FormatScopeRef
//	scopeHandle.parse    ParseScopeHandle
//	scopeHandle.format   FormatScopeHandle
//	laneRef.normalize    NormalizeLaneRef
//	sessionHandle.parse  ParseSessionHandle
//	sessionRef.parse     ParseSessionRef
//	input.resolve        ResolveQualifiedInput
//
// Not declared: sessionRef.format (wrkq formats no session ref) and
// identity.project (wrkq has no lenient parser).
//
// Reference grammar:
//
//	scopeRef long form:   agent:<agentId>[:project:<projectId>[:task:<taskId>][:role:<roleName>]]
//	scopeHandle short:    <agentId>[:<taskId>] | <agentId>@<projectId>[:<taskId>][/<roleName>]
//	sessionHandle:        <scopeHandle>[~<laneId>]
//	sessionRef:           <scopeRef>/lane:<laneId>
//	laneRef:              main | lane:<laneId>
//
// Token identifiers must match [A-Za-z0-9._-]+ and be 1..64 characters, counted
// in UTF-16 units as the TypeScript grammar counts them. Every refusal is a
// *GrammarError carrying one of the fixture's refusal codes (CodeOf).
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
