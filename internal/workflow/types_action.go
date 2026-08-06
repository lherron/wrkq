package workflow

// ActionWorkflowRef identifies the workflow template backing an action run.
type ActionWorkflowRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    string `json:"hash,omitempty"`
}

// ActionRun is the canonical semantic view of one action run. ActionRunID and
// RunID are the same durable workflow_runs.id; both names are preserved so
// callers can use existing wrkf run language.
type ActionRun struct {
	ActionRunID        string            `json:"actionRunId"`
	RunID              string            `json:"runId"`
	Task               string            `json:"task"`
	InstanceID         string            `json:"instanceId"`
	Workflow           ActionWorkflowRef `json:"workflow"`
	Action             string            `json:"action"`
	Role               string            `json:"role"`
	PrincipalRef       string            `json:"principal_ref,omitempty"`
	Lane               string            `json:"lane,omitempty"`
	DeliveryRef        string            `json:"deliveryRef,omitempty"`
	ExternalRunRef     string            `json:"externalRunRef,omitempty"`
	Status             string            `json:"status"`
	StartedAt          string            `json:"startedAt"`
	CompletedAt        string            `json:"completedAt,omitempty"`
	TerminalResult     string            `json:"terminalResult,omitempty"`
	LeaseOwner         string            `json:"leaseOwner,omitempty"`
	LeaseToken         string            `json:"leaseToken,omitempty"`
	LeaseExpiresAt     string            `json:"leaseExpiresAt,omitempty"`
	HeartbeatAt        string            `json:"heartbeatAt,omitempty"`
	EvidenceIDs        []string          `json:"evidenceIds,omitempty"`
	EvidenceKinds      []string          `json:"evidenceKinds,omitempty"`
	TransitionEventIDs []string          `json:"transitionEventIds,omitempty"`
}

// ActionEvidenceInput is the optional evidence recorded by complete/fail.
type ActionEvidenceInput struct {
	Kind           string
	Ref            string
	Summary        string
	Facts          string
	Data           string
	ContentHash    string
	IdempotencyKey string
}

// StartActionParams drives StartAction.
type StartActionParams struct {
	Task           string
	InstanceID     string
	Workflow       string
	Action         string
	Role           string
	PrincipalRef   string
	Lane           string
	DeliveryRef    string
	ExternalRunRef string
	IdempotencyKey string
	LeaseOwner     string
	LeaseMs        int64
}

// BindActionExternalParams drives BindActionExternal.
type BindActionExternalParams struct {
	ActionRunID    string
	ExternalRunRef string
	DeliveryRef    string
	Lane           string
	IdempotencyKey string
}

// TransitionMode selects how CompleteAction handles the workflow transition.
type TransitionMode int

// CompleteActionParams drives CompleteAction.
type CompleteActionParams struct {
	ActionRunID              string
	LeaseToken               string
	Evidence                 *ActionEvidenceInput
	TransitionMode           TransitionMode
	TransitionID             string
	TransitionIdempotencyKey string
	RunSummary               string
}

// FailActionParams drives FailAction.
type FailActionParams struct {
	ActionRunID string
	LeaseToken  string
	Summary     string
	Evidence    *ActionEvidenceInput
}

type HeartbeatActionParams struct {
	ActionRunID string
	LeaseToken  string
	LeaseMs     int64
}

type ClaimActionParams struct {
	Task             string             `json:"task,omitempty"`
	InstanceID       string             `json:"instanceId,omitempty"`
	Prefer           ActionClaimPrefer  `json:"prefer,omitempty"`
	RunnerID         string             `json:"runnerId"`
	AgentRef         string             `json:"agentRef"`
	ScopeRef         string             `json:"scopeRef,omitempty"`
	Capabilities     []RunnerCapability `json:"capabilities,omitempty"`
	LeaseMs          int64              `json:"leaseMs"`
	WorkspaceRoot    string             `json:"workspaceRoot,omitempty"`
	IdempotencyKey   string             `json:"idempotencyKey,omitempty"`
	PriorRun         *string            `json:"priorRun"`
	PriorRunProvided bool               `json:"-"`
}

type ActionClaimPrefer struct {
	InstanceID        string `json:"instanceId,omitempty"`
	SemanticActionKey string `json:"semanticActionKey,omitempty"`
	Action            string `json:"action,omitempty"`
}

type RunnerCapability struct {
	HandlerContract   string   `json:"handlerContract,omitempty"`
	HandlerID         string   `json:"handlerId,omitempty"`
	HandlerVersion    string   `json:"handlerVersion,omitempty"`
	Actions           []string `json:"actions,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	SideEffectClasses []string `json:"sideEffectClasses,omitempty"`
	WorkspaceModes    []string `json:"workspaceModes,omitempty"`
}

type ClaimActionResult struct {
	Binding *FencedRunBinding `json:"binding,omitempty"`
}

type ActionClaimEvidenceRecord struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Summary    string `json:"summary,omitempty"`
	ProducedAt string `json:"producedAt"`
}

type ActionClaimPredecessor struct {
	RunID             string                      `json:"runId"`
	Owner             string                      `json:"owner,omitempty"`
	ClaimedAt         string                      `json:"claimedAt"`
	HeartbeatAt       string                      `json:"heartbeatAt,omitempty"`
	ExpiresAt         string                      `json:"expiresAt,omitempty"`
	SettleStatus      string                      `json:"settleStatus"`
	Settled           bool                        `json:"settled"`
	TerminalResult    string                      `json:"terminalResult,omitempty"`
	SideEffectClasses []string                    `json:"sideEffectClasses"`
	ExternalRunRef    string                      `json:"externalRunRef,omitempty"`
	WorkspaceRef      string                      `json:"workspaceRef,omitempty"`
	EvidenceWritten   []ActionClaimEvidenceRecord `json:"evidenceWritten"`
}

type SettleActionParams struct {
	ActionRunID     string               `json:"actionRunId,omitempty"`
	RunID           string               `json:"runId,omitempty"`
	OwnerToken      string               `json:"ownerToken,omitempty"`
	OwnerGeneration int64                `json:"ownerGeneration,omitempty"`
	Result          string               `json:"result"`
	Evidence        *ActionEvidenceInput `json:"evidence,omitempty"`
	TransitionMode  TransitionMode       `json:"-"`
	TransitionID    string               `json:"-"`
	TerminalSummary string               `json:"terminalSummary,omitempty"`
}

type SettleActionResult struct {
	Run         WorkflowRunAttempt     `json:"run"`
	Evidence    *Evidence              `json:"evidence,omitempty"`
	Transition  map[string]interface{} `json:"transition,omitempty"`
	Effects     []Effect               `json:"effects,omitempty"`
	Obligations []Obligation           `json:"obligations,omitempty"`
}

type FencedRunBinding struct {
	Run       WorkflowRunAttempt `json:"run"`
	Task      ActionTaskBinding  `json:"task"`
	Instance  Instance           `json:"instance"`
	Authority ActionRunAuthority `json:"authority"`
}

type WorkflowRunAttempt struct {
	ID                string               `json:"id"`
	InstanceID        string               `json:"instanceId"`
	SemanticActionKey string               `json:"semanticActionKey"`
	Action            string               `json:"action"`
	Role              string               `json:"role"`
	Attempt           int64                `json:"attempt"`
	Status            string               `json:"status"`
	AgentRef          string               `json:"agentRef,omitempty"`
	ScopeRef          string               `json:"scopeRef,omitempty"`
	HandlerContract   string               `json:"handlerContract,omitempty"`
	HandlerID         string               `json:"handlerId,omitempty"`
	HandlerVersion    string               `json:"handlerVersion,omitempty"`
	ExternalRunRef    string               `json:"externalRunRef,omitempty"`
	WorkspaceRef      string               `json:"workspaceRef,omitempty"`
	Source            *ActionSourceBinding `json:"source,omitempty"`
	StartedAt         string               `json:"startedAt"`
	CompletedAt       string               `json:"completedAt,omitempty"`
	TerminalSummary   string               `json:"terminalSummary,omitempty"`
	PredecessorRunID  string               `json:"predecessorRunId,omitempty"`
}

type ActionTaskBinding struct {
	UUID string `json:"uuid"`
	Ref  string `json:"ref"`
	Path string `json:"path,omitempty"`
}

type ActionRunAuthority struct {
	RunnerID        string `json:"runnerId"`
	OwnerToken      string `json:"ownerToken"`
	OwnerGeneration int64  `json:"ownerGeneration"`
	ClaimedAt       string `json:"claimedAt"`
	HeartbeatAt     string `json:"heartbeatAt,omitempty"`
	LeaseExpiresAt  string `json:"leaseExpiresAt"`
}

// ActionCompleteResult is the committed result of CompleteAction.
type ActionCompleteResult struct {
	Run        *ActionRun             `json:"run"`
	Evidence   *Evidence              `json:"evidence,omitempty"`
	Transition map[string]interface{} `json:"transition,omitempty"`
}

// ListActionsParams drives ListActions.
type ListActionsParams struct {
	Task                   string
	InstanceID             string
	IncludeClosedInstances bool
	Status                 string
	Action                 string
	Limit                  int
}

type claimedRun struct {
	ID                    string
	InstanceID            string
	SemanticActionKey     string
	Action                string
	Role                  string
	Attempt               int64
	Status                string
	AgentRef              string
	ScopeRef              string
	HandlerContract       string
	HandlerID             string
	HandlerVersion        string
	ExternalRunRef        string
	WorkspaceRef          string
	SourceRunID           string
	SourceEvidenceID      string
	SourceIdentity        string
	StartedAt             string
	CompletedAt           string
	TerminalSummary       string
	LeaseOwner            string
	LeaseToken            string
	LeaseExpiresAt        string
	HeartbeatAt           string
	OwnerGeneration       int64
	SupersededByRunID     string
	SideEffectClassesJSON string
	PredecessorRunID      string
}

type actionRunViewOptions struct {
	includeLeaseToken bool
}
