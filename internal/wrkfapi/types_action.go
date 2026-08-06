package wrkfapi

import (
	"encoding/json"

	"github.com/lherron/wrkq/internal/workflow"
)

type ActionRun = workflow.ActionRun
type ActionNextParams = workflow.ActionNextParams
type ActionNextResult = workflow.ActionNextResult
type ActionClaimParams = workflow.ClaimActionParams
type ActionClaimResult = workflow.ClaimActionResult
type ActionClaimPredecessor = workflow.ActionClaimPredecessor

type ActionEvidenceParams struct {
	Kind           string          `json:"kind,omitempty"`
	Ref            string          `json:"ref,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Facts          json.RawMessage `json:"facts,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	ContentHash    string          `json:"contentHash,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

type ActionStartParams struct {
	Task           string          `json:"task,omitempty"`
	InstanceID     string          `json:"instanceId,omitempty"`
	Workflow       string          `json:"workflow,omitempty"`
	Action         string          `json:"action"`
	Role           string          `json:"role,omitempty"`
	PrincipalRef   string          `json:"principal_ref,omitempty"`
	Lane           string          `json:"lane,omitempty"`
	DeliveryRef    json.RawMessage `json:"deliveryRef,omitempty"`
	ExternalRunRef string          `json:"externalRunRef,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	LeaseOwner     string          `json:"leaseOwner,omitempty"`
	LeaseMs        int64           `json:"leaseMs,omitempty"`
}

type ActionBindExternalParams struct {
	ActionRunID    string          `json:"actionRunId"`
	ExternalRunRef string          `json:"externalRunRef"`
	DeliveryRef    json.RawMessage `json:"deliveryRef,omitempty"`
	Lane           string          `json:"lane,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

type ActionCompleteParams struct {
	ActionRunID              string                `json:"actionRunId"`
	LeaseToken               string                `json:"leaseToken,omitempty"`
	Evidence                 *ActionEvidenceParams `json:"evidence,omitempty"`
	Transition               json.RawMessage       `json:"transition,omitempty"`
	TransitionIdempotencyKey string                `json:"transitionIdempotencyKey,omitempty"`
	RunSummary               string                `json:"runSummary,omitempty"`
}

type ActionSettleParams struct {
	ActionRunID     string                `json:"actionRunId,omitempty"`
	RunID           string                `json:"runId,omitempty"`
	OwnerToken      string                `json:"ownerToken,omitempty"`
	OwnerGeneration int64                 `json:"ownerGeneration,omitempty"`
	Result          string                `json:"result"`
	Evidence        *ActionEvidenceParams `json:"evidence,omitempty"`
	Transition      json.RawMessage       `json:"transition,omitempty"`
	TerminalSummary string                `json:"terminalSummary,omitempty"`
}

type ActionFailParams struct {
	ActionRunID string                `json:"actionRunId"`
	LeaseToken  string                `json:"leaseToken,omitempty"`
	Summary     string                `json:"summary"`
	Evidence    *ActionEvidenceParams `json:"evidence,omitempty"`
}

type ActionHeartbeatParams struct {
	ActionRunID string `json:"actionRunId"`
	LeaseToken  string `json:"leaseToken"`
	LeaseMs     int64  `json:"leaseMs,omitempty"`
}

type ActionShowParams struct {
	ActionRunID string `json:"actionRunId"`
}

type ActionListParams struct {
	Task                   string `json:"task,omitempty"`
	InstanceID             string `json:"instanceId,omitempty"`
	IncludeClosedInstances bool   `json:"includeClosedInstances,omitempty"`
	Status                 string `json:"status,omitempty"`
	Action                 string `json:"action,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	Cursor                 string `json:"cursor,omitempty"`
}

type ActionListResult struct {
	Items []workflow.ActionRun `json:"items"`
}

type ActionCompleteResult struct {
	Run        *workflow.ActionRun `json:"run"`
	Evidence   *workflow.Evidence  `json:"evidence,omitempty"`
	Transition *TransitionResult   `json:"transition,omitempty"`
}

type ActionSettleResult struct {
	Run         workflow.WorkflowRunAttempt `json:"run"`
	Evidence    *workflow.Evidence          `json:"evidence,omitempty"`
	Transition  *TransitionResult           `json:"transition,omitempty"`
	Effects     []workflow.Effect           `json:"effects,omitempty"`
	Obligations []workflow.Obligation       `json:"obligations,omitempty"`
}

// DefaultActionWorkflowRef projects the workflow producer's default into
// transport adapters without duplicating ownership of the selected version.
func DefaultActionWorkflowRef() string {
	return workflow.DefaultActionWorkflowTemplateRef
}
