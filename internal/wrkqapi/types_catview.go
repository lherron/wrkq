package wrkqapi

import "encoding/json"

// TaskCatViewParams selects the task to project and whether to include comments.
type TaskCatViewParams struct {
	Task            string `json:"task"`
	IncludeComments *bool  `json:"includeComments,omitempty"` // default true
}

// WrkqTaskCatView is the server-owned COMPATIBILITY read model for `wrkq cat`.
// It is NOT the canonical task resource (that is WrkqTask / wrkq.task.show). Its
// shape — snake_case names, legacy time formats, nested comments/relations/
// blockers — exactly reproduces a single legacy `wrkq cat --json` object so the
// RPC CLI can render byte-identical output. Do not add fields here unless they
// are needed to reproduce a current `cat` invariant (T-05090 ruling).
//
// artifact_dir is a CANONICAL-HOST/server-local path hint, not a guarantee that
// the path exists on a remote caller's filesystem.
type WrkqTaskCatView struct {
	ID                    string            `json:"id"`
	UUID                  string            `json:"uuid"`
	Path                  string            `json:"path"`
	ArtifactDir           string            `json:"artifact_dir"`
	ProjectID             string            `json:"project_id"`
	ProjectUUID           string            `json:"project_uuid"`
	RequestedByProjectID  *string           `json:"requested_by_project_id,omitempty"`
	AssignedProjectID     *string           `json:"assigned_project_id,omitempty"`
	Slug                  string            `json:"slug"`
	Title                 string            `json:"title"`
	State                 string            `json:"state"`
	Priority              int               `json:"priority"`
	Kind                  string            `json:"kind"`
	ParentTaskID          *string           `json:"parent_task_id,omitempty"`
	ParentTaskUUID        *string           `json:"parent_task_uuid,omitempty"`
	AssigneeSlug          *string           `json:"assignee,omitempty"`
	AssigneeUUID          *string           `json:"assignee_uuid,omitempty"`
	AssigneePrincipalRef  *string           `json:"assignee_principal_ref,omitempty"`
	ClaimedBy             *string           `json:"claimed_by,omitempty"`
	ClaimedScope          *string           `json:"claimed_scope,omitempty"`
	ClaimedNode           *string           `json:"claimed_node,omitempty"`
	ClaimedAt             *string           `json:"claimed_at,omitempty"`
	ClaimGeneration       int64             `json:"claim_generation,omitempty"`
	StartAt               *string           `json:"start_at,omitempty"`
	DueAt                 *string           `json:"due_at,omitempty"`
	Labels                *string           `json:"labels,omitempty"`
	Meta                  json.RawMessage   `json:"meta"`
	Description           string            `json:"description"`
	Specification         string            `json:"specification"`
	Outcome               *string           `json:"outcome,omitempty"`
	AcknowledgedAt        *string           `json:"acknowledged_at,omitempty"`
	Resolution            *string           `json:"resolution,omitempty"`
	Etag                  int64             `json:"etag"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
	CompletedAt           *string           `json:"completed_at,omitempty"`
	ArchivedAt            *string           `json:"archived_at,omitempty"`
	CreatedBy             string            `json:"created_by"`
	CreatedByPrincipalRef string            `json:"created_by_principal_ref,omitempty"`
	CreatedByScopeRef     *string           `json:"created_by_scope_ref,omitempty"`
	UpdatedBy             string            `json:"updated_by"`
	UpdatedByPrincipalRef string            `json:"updated_by_principal_ref,omitempty"`
	CausedBy              []string          `json:"caused_by"`
	BlockedBy             []CatViewBlocker  `json:"blocked_by,omitempty"`
	Comments              []CatViewComment  `json:"comments,omitempty"`
	Relations             []CatViewRelation `json:"relations,omitempty"`
	Promises              []WrkqPromise     `json:"promises"`
}

type CatViewComment struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Body         string `json:"body"`
	PrincipalRef string `json:"principal_ref,omitempty"`
}

type CatViewRelation struct {
	Direction   string `json:"direction"`
	Kind        string `json:"kind"`
	TaskID      string `json:"task_id"`
	TaskUUID    string `json:"task_uuid"`
	TaskSlug    string `json:"task_slug"`
	TaskTitle   string `json:"task_title"`
	CreatedAt   string `json:"created_at"`
	CreatedByID string `json:"created_by_id"`
}

type CatViewBlocker struct {
	ID    string `json:"id"`
	State string `json:"state"`
}
