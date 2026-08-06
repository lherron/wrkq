package wrkqapi

import "time"

// WrkqHandoff is the stable handoff resource DTO. Its field ORDER + json tags
// reproduce the legacy `handoffJSON` (internal/rpccli/handoff.go) EXACTLY so
// the mirror can render byte-identical output: snake_case keys, legacy struct
// order, pointer/omitempty parity. This is the WrkqHandoff fingerprint pinned by
// TestHandoffDTOFingerprint — any drift is a PROTOCOL CONTRACT change.
type WrkqHandoff struct {
	UUID                       string     `json:"uuid"`
	ID                         string     `json:"id"`
	ScopeRef                   string     `json:"scope_ref"`
	ScopeKind                  string     `json:"scope_kind"`
	AgentID                    string     `json:"agent_id"`
	ProjectID                  string     `json:"project_id"`
	AgentPrincipalRef          *string    `json:"agent_principal_ref,omitempty"`
	ProjectContainerUUID       *string    `json:"project_container_uuid"`
	CreatedByAgentID           string     `json:"created_by_agent_id"`
	CreatedByPrincipalRef      string     `json:"created_by_principal_ref,omitempty"`
	Title                      string     `json:"title"`
	Body                       string     `json:"body"`
	Status                     string     `json:"status"`
	IdempotencyKey             *string    `json:"idempotency_key"`
	AcknowledgedAt             *time.Time `json:"acknowledged_at"`
	AcknowledgedByAgentID      *string    `json:"acknowledged_by_agent_id"`
	AcknowledgedByPrincipalRef *string    `json:"acknowledged_by_principal_ref,omitempty"`
	AcknowledgementNote        *string    `json:"acknowledgement_note"`
	Meta                       *string    `json:"meta"`
	ETag                       int64      `json:"etag"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// WrkqHandoffCreateResult is the wrkq.handoff.create envelope: the created (or
// idempotently replayed) handoff + the replay flag.
type WrkqHandoffCreateResult struct {
	Handoff          WrkqHandoff `json:"handoff"`
	IdempotentReplay bool        `json:"idempotentReplay"`
}

// WrkqHandoffListResult is the wrkq.handoff.listView envelope. nextCursor is the
// opaque pagination token (empty when the page is the last).
type WrkqHandoffListResult struct {
	Items      []WrkqHandoff `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// WrkqHandoffSearchResult is the server-owned compatibility envelope for
// `wrkq handoff search`. The mirror combines these handoffs with caller-side
// scope diagnostics and renders the legacy handoffSearchOutput shape.
type WrkqHandoffSearchResult struct {
	Handoffs        []WrkqHandoff `json:"handoffs"`
	NextCursor      *string       `json:"next_cursor"`
	Truncated       bool          `json:"truncated"`
	Stale           bool          `json:"stale,omitempty"`
	StaleEventCount int64         `json:"stale_event_count,omitempty"`
	IndexWarning    string        `json:"index_warning,omitempty"`
}

// HandoffCreateParams carries the CALLER-resolved effective scope/actor for a
// handoff create. Scope is caller-owned but NOT project-root: the mirror resolves
// --scope / agent-runtime env via scope.Resolve and enforces self-scope BEFORE
// submitting. The SERVER receives EXPLICIT effective fields and MUST NOT read
// ASP_SCOPE_REF / ASP_HANDLE / ASP_AGENT_ID / ASP_PROJECT.
type HandoffCreateParams struct {
	ScopeRef       string  `json:"scopeRef"`
	AgentID        string  `json:"agentId"`
	ProjectID      string  `json:"projectId"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	Meta           *string `json:"meta,omitempty"`
	IdempotencyKey *string `json:"idempotencyKey,omitempty"`
	// ActorAgentID/PrincipalRef are the caller-resolved create attribution. When
	// empty they default to the scope agent (legacy: created_by == scope agent).
	ActorAgentID string `json:"actorAgentId,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	DryRun       bool   `json:"dryRun,omitempty"`
}

// HandoffGetParams selects a single handoff by friendly ID or UUID.
type HandoffGetParams struct {
	Handoff string `json:"handoff"`
}

// HandoffListViewParams is the caller-scoped handoff list request. scopeRef is
// the CALLER-resolved canonical project scope; the server never derives it from
// env. status is pending|acknowledged|all (default pending).
type HandoffListViewParams struct {
	ScopeRef string `json:"scopeRef"`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

// HandoffSearchViewParams is caller-scoped handoff search. Scope is resolved by
// the CLI mirror and passed explicitly; cursor is the legacy base64 offset token.
type HandoffSearchViewParams struct {
	Query    string `json:"query"`
	ScopeRef string `json:"scopeRef"`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

// HandoffAcknowledgeParams carries the caller-resolved acting identity for an
// acknowledge. The server owns the etag CAS + the handoff.acknowledged event.
type HandoffAcknowledgeParams struct {
	Handoff      string  `json:"handoff"`
	Note         *string `json:"note,omitempty"`
	ActorAgentID string  `json:"actorAgentId"`
	PrincipalRef string  `json:"principalRef,omitempty"`
	ScopeRef     string  `json:"scopeRef,omitempty"`
	DryRun       bool    `json:"dryRun,omitempty"`
	IfMatch      int64   `json:"ifMatch,omitempty"`
}
