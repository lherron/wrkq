package workflow

import (
	"encoding/json"
	"time"
)

type State struct {
	Status      string `json:"status"`
	Phase       string `json:"phase,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	Description string `json:"description,omitempty"`
}

type Template struct {
	SchemaVersion     string                          `json:"schemaVersion"`
	ID                string                          `json:"id"`
	Version           string                          `json:"version"`
	Kind              string                          `json:"kind"`
	Description       string                          `json:"description,omitempty"`
	Initial           State                           `json:"initial"`
	Roles             map[string]RoleSpec             `json:"roles"`
	States            []State                         `json:"states"`
	EvidenceKinds     map[string]KindSpec             `json:"evidenceKinds,omitempty"`
	ObligationKinds   map[string]KindSpec             `json:"obligationKinds,omitempty"`
	Checks            map[string]CheckSpec            `json:"checks,omitempty"`
	Transitions       []TransitionSpec                `json:"transitions"`
	ExecutableActions map[string]ExecutableActionSpec `json:"executableActions,omitempty"`
	StateHooks        map[string][]HookRef            `json:"stateHooks,omitempty"`
	NextActionModel   map[string]json.RawMessage      `json:"nextActionModel,omitempty"`
	Supervisor        map[string]json.RawMessage      `json:"supervisor,omitempty"`
	Raw               map[string]json.RawMessage      `json:"-"`
}

type ExecutableActionSpec struct {
	ID                 string             `json:"id,omitempty"`
	Description        string             `json:"description,omitempty"`
	From               *State             `json:"from,omitempty"`
	Role               string             `json:"role"`
	Transition         string             `json:"transition"`
	ResultEvidenceKind string             `json:"resultEvidenceKind"`
	HandlerContract    string             `json:"handlerContract,omitempty"`
	WorkClass          string             `json:"workClass,omitempty"`
	RiskClass          string             `json:"riskClass,omitempty"`
	WorkspaceMode      string             `json:"workspaceMode,omitempty"`
	SideEffectClasses  []string           `json:"sideEffectClasses,omitempty"`
	SourceBinding      *SourceBindingSpec `json:"sourceBinding,omitempty"`
	Continuation       *ContinuationSpec  `json:"continuation,omitempty"`
	Rank               int                `json:"rank,omitempty"`
}

type SourceBindingSpec struct {
	Kind          string            `json:"kind"`
	Action        string            `json:"action"`
	RequiredFacts []string          `json:"requiredFacts,omitempty"`
	BindFields    *SourceBindFields `json:"bindFields,omitempty"`
}

type SourceBindFields struct {
	SourceRunID      string `json:"sourceRunId,omitempty"`
	SourceEvidenceID string `json:"sourceEvidenceId,omitempty"`
	CommitSha        string `json:"commitSha,omitempty"`
	ArtifactRef      string `json:"artifactRef,omitempty"`
}

type ContinuationSpec struct {
	Next               string `json:"next"`
	AttentionScope     string `json:"attentionScope,omitempty"`
	RequireExactSource bool   `json:"requireExactSource,omitempty"`
}

type RoleSpec struct {
	Description string   `json:"description,omitempty"`
	Principals  []string `json:"principals,omitempty"`
}

type KindSpec struct {
	Description string         `json:"description,omitempty"`
	Class       string         `json:"class,omitempty"`
	Facts       *FactsContract `json:"facts,omitempty"`
	// ProducibleBy declares which roles may produce this evidence kind
	// (supplied-role conformance, not an authenticated-principal boundary). When
	// empty, all roles are allowed. To allow system-produced evidence, list
	// "system" explicitly — there is no implicit admin bypass.
	ProducibleBy []string `json:"producibleBy,omitempty"`
	// LinkageRefs declares fields inside the evidence `data` document that must
	// resolve to a live evidence id on the same workflow instance.
	LinkageRefs []LinkageRef `json:"linkageRefs,omitempty"`
}

// LinkageRef declares a referential-integrity constraint on an evidence `data`
// field: its string value must resolve to a live evidence id on the same
// instance (optionally of a specific kind).
type LinkageRef struct {
	// Path is a top-level JSON Pointer into the evidence `data` object, e.g.
	// "/basedOnBehaviorNoteId". Only single-segment top-level pointers are
	// supported.
	Path string `json:"path"`
	// ResolvesToKind, when set, requires the referenced evidence to be of this
	// kind.
	ResolvesToKind string `json:"resolvesToKind,omitempty"`
	// Required, when true, rejects an add whose `data` omits this path.
	Required bool `json:"required,omitempty"`
	// Latest, when true, additionally requires the referenced evidence to be
	// the current (latest by production time) evidence of ResolvesToKind on the
	// instance — a superseded earlier id is rejected. Requires ResolvesToKind.
	Latest bool `json:"latest,omitempty"`
}

type FactsContract struct {
	Required   []string                `json:"required,omitempty"`
	Properties map[string]FactProperty `json:"properties,omitempty"`
}

type FactProperty struct {
	Type      string            `json:"type,omitempty"`
	Enum      []json.RawMessage `json:"enum,omitempty"`
	MaxLength int               `json:"maxLength,omitempty"`
	MaxItems  int               `json:"maxItems,omitempty"`
	ItemsType string            `json:"itemsType,omitempty"`
}

type TransitionSpec struct {
	ID               string                `json:"id"`
	Description      string                `json:"description,omitempty"`
	From             State                 `json:"from"`
	By               []string              `json:"by"`
	Responsibility   *ResponsibilitySpec   `json:"responsibility,omitempty"`
	Guards           []Predicate           `json:"guards,omitempty"`
	Requires         []RequirementSpec     `json:"requires,omitempty"`
	Checks           []string              `json:"checks,omitempty"`
	Outcomes         []OutcomeCase         `json:"outcomes"`
	Hooks            map[string][]HookRef  `json:"hooks,omitempty"`
	Postconditions   []Predicate           `json:"postconditions,omitempty"`
	SeparationOfDuty *SeparationOfDutySpec `json:"separationOfDuty,omitempty"`
}

type SeparationOfDutySpec struct {
	DistinctPrincipalFromEvidence  []string                        `json:"distinctPrincipalFromEvidence,omitempty"`
	EvidencePrincipalPairsDistinct []EvidencePrincipalDistinctPair `json:"evidencePrincipalPairsDistinct,omitempty"`
}

type EvidencePrincipalDistinctPair struct {
	LeftKind  string `json:"leftKind"`
	RightKind string `json:"rightKind"`
}

type ResponsibilitySpec struct {
	Role     string `json:"role,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Lane     string `json:"lane,omitempty"`
	Fallback *struct {
		Role string `json:"role"`
		On   string `json:"on"`
	} `json:"fallback,omitempty"`
}

type RequirementSpec struct {
	Evidence   *EvidenceRequirementSpec   `json:"evidence,omitempty"`
	Obligation *ObligationRequirementSpec `json:"obligation,omitempty"`
}

type EvidenceRequirementSpec struct {
	Kind  string                     `json:"kind"`
	Facts map[string]json.RawMessage `json:"facts,omitempty"`
}

type ObligationRequirementSpec struct {
	Kind   string `json:"kind,omitempty"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

type CheckSpec struct {
	Type          string             `json:"type"`
	Name          string             `json:"name,omitempty"`
	Predicate     *Predicate         `json:"predicate,omitempty"`
	HookID        string             `json:"hookId,omitempty"`
	ExitMap       map[string]ExitMap `json:"exitMap,omitempty"`
	Role          string             `json:"role,omitempty"`
	Instruction   string             `json:"instruction,omitempty"`
	EvidenceKind  string             `json:"evidenceKind,omitempty"`
	EvidenceKinds []string           `json:"evidenceKinds,omitempty"`
}

type ExitMap struct {
	Verdict string `json:"verdict"`
	Outcome string `json:"outcome,omitempty"`
}

type OutcomeCase struct {
	ID          string                 `json:"id"`
	Description string                 `json:"description,omitempty"`
	When        Predicate              `json:"when"`
	To          State                  `json:"to"`
	Effects     []EffectSpec           `json:"effects,omitempty"`
	Obligations []ObligationCreateSpec `json:"obligations,omitempty"`
}

type EffectSpec struct {
	Kind        string                 `json:"kind"`
	Role        string                 `json:"role,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	SemanticKey string                 `json:"semanticKey,omitempty"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

type ObligationCreateSpec struct {
	Kind                string `json:"kind"`
	OwnerRole           string `json:"ownerRole,omitempty"`
	OwnerPrincipalRef   string `json:"ownerPrincipalRef,omitempty"`
	ObligeeRole         string `json:"obligeeRole,omitempty"`
	ObligeePrincipalRef string `json:"obligeePrincipalRef,omitempty"`
	WaiveRole           string `json:"waiveRole,omitempty"`
	WaivePrincipalRef   string `json:"waivePrincipalRef,omitempty"`
	NoSelfWaive         *bool  `json:"noSelfWaive,omitempty"`
	Blocking            bool   `json:"blocking"`
	Reason              string `json:"reason,omitempty"`
}

type HookRef struct {
	HookID string `json:"hookId"`
}

type Predicate struct {
	Always           *bool                  `json:"always,omitempty"`
	Otherwise        *bool                  `json:"otherwise,omitempty"`
	All              []Predicate            `json:"all,omitempty"`
	Any              []Predicate            `json:"any,omitempty"`
	Not              *Predicate             `json:"not,omitempty"`
	CheckVerdict     *CheckVerdictPredicate `json:"checkVerdict,omitempty"`
	CheckOutcome     *CheckOutcomePredicate `json:"checkOutcome,omitempty"`
	EvidenceExists   *EvidencePredicate     `json:"evidenceExists,omitempty"`
	ObligationStatus *ObligationPredicate   `json:"obligationStatus,omitempty"`
	FactEquals       *FactEqualsPredicate   `json:"factEquals,omitempty"`
}

type CheckVerdictPredicate struct {
	Check string `json:"check"`
	Is    string `json:"is"`
}

type CheckOutcomePredicate struct {
	Check string `json:"check"`
	Is    string `json:"is"`
}

type EvidencePredicate struct {
	Kind  string                     `json:"kind"`
	Facts map[string]json.RawMessage `json:"facts,omitempty"`
}

type ObligationPredicate struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	Is   string `json:"is"`
}

type FactEqualsPredicate struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

type Instance struct {
	ID              string              `json:"id"`
	TaskUUID        string              `json:"taskUuid,omitempty"`
	TaskRef         string              `json:"taskRef"`
	ProjectID       string              `json:"projectId,omitempty"`
	TemplateID      string              `json:"templateId"`
	TemplateVersion string              `json:"templateVersion"`
	TemplateHash    string              `json:"templateHash"`
	Status          string              `json:"status"`
	Phase           string              `json:"phase,omitempty"`
	Outcome         string              `json:"outcome,omitempty"`
	Revision        int64               `json:"revision"`
	ContextHash     string              `json:"contextHash"`
	TaskDocEtag     string              `json:"taskDocEtag"`
	TaskDocHash     string              `json:"taskDocHash"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
	ClosedAt        string              `json:"closedAt,omitempty"`
	Supersedes      *InstanceLineageRef `json:"supersedes,omitempty"`
	SupersededBy    *InstanceLineageRef `json:"supersededBy,omitempty"`
}

func (i Instance) State() State {
	return State{Status: i.Status, Phase: i.Phase, Outcome: i.Outcome}
}

type InstanceLineageRef struct {
	InstanceID      string `json:"instanceId"`
	TemplateID      string `json:"templateId,omitempty"`
	TemplateVersion string `json:"templateVersion,omitempty"`
	TemplateHash    string `json:"templateHash,omitempty"`
	Revision        int64  `json:"revision"`
	Status          string `json:"status,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
}

type Event struct {
	ID               string          `json:"id"`
	InstanceID       string          `json:"instanceId"`
	Seq              int64           `json:"seq"`
	SchemaVersion    string          `json:"schemaVersion"`
	Type             string          `json:"type"`
	PrincipalRef     string          `json:"principal_ref,omitempty"`
	Role             string          `json:"role,omitempty"`
	RunID            string          `json:"runId,omitempty"`
	ObservedRevision int64           `json:"observedRevision"`
	NextRevision     int64           `json:"nextRevision"`
	TaskDocEtag      string          `json:"taskDocEtag,omitempty"`
	TaskDocHash      string          `json:"taskDocHash,omitempty"`
	ContextHash      string          `json:"contextHash,omitempty"`
	IdempotencyKey   string          `json:"idempotencyKey,omitempty"`
	Result           string          `json:"result,omitempty"`
	RejectionCode    string          `json:"rejectionCode,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	CreatedAt        string          `json:"createdAt"`
}

type Evidence struct {
	ID                   string          `json:"id"`
	InstanceID           string          `json:"instanceId,omitempty"`
	Kind                 string          `json:"kind"`
	Ref                  string          `json:"ref"`
	Summary              string          `json:"summary,omitempty"`
	Facts                json.RawMessage `json:"facts,omitempty"`
	Data                 json.RawMessage `json:"data,omitempty"`
	Source               json.RawMessage `json:"source,omitempty"`
	PrincipalRef         string          `json:"principal_ref,omitempty"`
	Role                 string          `json:"role,omitempty"`
	RunID                string          `json:"runId,omitempty"`
	ContentHash          string          `json:"contentHash,omitempty"`
	Build                *EvidenceBuild  `json:"build,omitempty"`
	TaskEtagAtProduction string          `json:"taskEtagAtProduction,omitempty"`
	TaskHashAtProduction string          `json:"taskHashAtProduction,omitempty"`
	ProducedAt           string          `json:"producedAt"`
}

type EvidenceBuild struct {
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Env     string `json:"env,omitempty"`
}

// EvidenceSchema is the contract for an evidence kind, returned by
// `wrkf evidence schema` so an agent can query requirements before adding (F3).
type EvidenceSchema struct {
	Kind         string         `json:"kind"`
	Description  string         `json:"description,omitempty"`
	Class        string         `json:"class,omitempty"`
	Facts        *FactsContract `json:"facts,omitempty"`
	ProducibleBy []string       `json:"producibleBy,omitempty"`
	LinkageRefs  []LinkageRef   `json:"linkageRefs,omitempty"`
}

type AddEvidenceParams struct {
	TaskSelector   string
	InstanceID     string
	Kind           string
	Ref            string
	Summary        string
	Facts          string
	Data           string
	PrincipalRef   string
	Role           string
	RunID          string
	ContentHash    string
	Build          *EvidenceBuild
	IdempotencyKey string
}

type RoleBinding struct {
	InstanceID   string `json:"instanceId"`
	Role         string `json:"role"`
	PrincipalRef string `json:"principal_ref"`
	DeliveryRef  string `json:"deliveryRef,omitempty"`
	Lane         string `json:"lane,omitempty"`
	BindingMode  string `json:"bindingMode"`
	BoundAt      string `json:"boundAt"`
}

type EventQueryParams struct {
	EventType           string   `json:"eventType,omitempty"`
	Project             string   `json:"project,omitempty"`
	FromPhase           string   `json:"fromPhase,omitempty"`
	ToPhase             string   `json:"toPhase,omitempty"`
	RiskClass           string   `json:"riskClass,omitempty"`
	RiskClasses         []string `json:"riskClasses,omitempty"`
	ExcludeRiskClass    string   `json:"excludeRiskClass,omitempty"`
	ExcludeRiskClasses  []string `json:"excludeRiskClasses,omitempty"`
	BoundRole           string   `json:"boundRole,omitempty"`
	IncludeRoleBindings bool     `json:"includeRoleBindings,omitempty"`
	Limit               int      `json:"limit,omitempty"`
	Cursor              string   `json:"cursor,omitempty"`
}

type EventQueryResult struct {
	Items      []TransitionEvent `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
	HasMore    bool              `json:"hasMore"`
}

type TransitionEvent struct {
	ID                   string          `json:"id"`
	EventType            string          `json:"eventType"`
	InstanceID           string          `json:"instanceId"`
	Seq                  int64           `json:"seq"`
	Task                 EventTaskRef    `json:"task"`
	Transition           string          `json:"transition,omitempty"`
	Outcome              string          `json:"outcome,omitempty"`
	From                 State           `json:"from,omitempty"`
	To                   State           `json:"to,omitempty"`
	FromPhase            string          `json:"fromPhase,omitempty"`
	ToPhase              string          `json:"toPhase,omitempty"`
	TransitionedAt       string          `json:"transitionedAt"`
	PrincipalRef         string          `json:"principal_ref,omitempty"`
	Role                 string          `json:"role,omitempty"`
	MatchingRoleBindings []RoleBinding   `json:"matchingRoleBindings"`
	RoleBindings         []RoleBinding   `json:"roleBindings,omitempty"`
	Payload              json.RawMessage `json:"payload,omitempty"`
}

type EventTaskRef struct {
	UUID        string `json:"uuid"`
	ID          string `json:"id"`
	Slug        string `json:"slug,omitempty"`
	Ref         string `json:"ref,omitempty"`
	ProjectUUID string `json:"projectUuid,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectSlug string `json:"projectSlug,omitempty"`
	RiskClass   string `json:"riskClass,omitempty"`
}

type Obligation struct {
	ID                     string          `json:"id"`
	InstanceID             string          `json:"instanceId,omitempty"`
	Kind                   string          `json:"kind"`
	OwnerRole              string          `json:"ownerRole,omitempty"`
	OwnerPrincipalRef      string          `json:"ownerPrincipalRef,omitempty"`
	ObligeeRole            string          `json:"obligeeRole,omitempty"`
	ObligeePrincipalRef    string          `json:"obligeePrincipalRef,omitempty"`
	WaiveRole              string          `json:"waiveRole,omitempty"`
	WaivePrincipalRef      string          `json:"waivePrincipalRef,omitempty"`
	NoSelfWaive            bool            `json:"noSelfWaive,omitempty"`
	Blocking               bool            `json:"blocking"`
	Status                 string          `json:"status"`
	Reason                 string          `json:"reason,omitempty"`
	Data                   json.RawMessage `json:"data,omitempty"`
	SatisfiedByEvidenceID  string          `json:"satisfiedByEvidenceId,omitempty"`
	ResolvedByPrincipalRef string          `json:"resolvedByPrincipalRef,omitempty"`
	ResolvedByRole         string          `json:"resolvedByRole,omitempty"`
	ResolvedAt             string          `json:"resolvedAt,omitempty"`
	CreatedAt              string          `json:"createdAt"`
	UpdatedAt              string          `json:"updatedAt"`
}

type Effect struct {
	ID             string          `json:"id"`
	InstanceID     string          `json:"instanceId,omitempty"`
	Revision       int64           `json:"revision"`
	Sequence       int64           `json:"sequence,omitempty"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Status         string          `json:"status"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	SemanticKey    string          `json:"semanticKey,omitempty"`
	Attempts       int64           `json:"attempts"`
	LeasedBy       string          `json:"leasedBy,omitempty"`
	LeasedUntil    string          `json:"leasedUntil,omitempty"`
	DeliveredAt    string          `json:"deliveredAt,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
	Receipt        json.RawMessage `json:"receipt,omitempty"`
	CreatedAt      string          `json:"createdAt"`
	UpdatedAt      string          `json:"updatedAt"`
}

type EffectClaim struct {
	Effects        []Effect `json:"effects"`
	LeaseToken     string   `json:"leaseToken"`
	LeaseExpiresAt string   `json:"leaseExpiresAt"`
}

type CheckRun struct {
	ID           string          `json:"id"`
	InstanceID   string          `json:"instanceId,omitempty"`
	TransitionID string          `json:"transitionId"`
	CheckID      string          `json:"checkId"`
	HookID       string          `json:"hookId,omitempty"`
	InputHash    string          `json:"inputHash"`
	ExitCode     *int            `json:"exitCode,omitempty"`
	Verdict      string          `json:"verdict"`
	Outcome      string          `json:"outcome,omitempty"`
	Code         string          `json:"code,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Facts        json.RawMessage `json:"facts,omitempty"`
	PrincipalRef string          `json:"principal_ref,omitempty"`
	Role         string          `json:"role,omitempty"`
	RunID        string          `json:"runId,omitempty"`
	StartedAt    string          `json:"startedAt"`
	CompletedAt  string          `json:"completedAt,omitempty"`
}

type Run struct {
	ID             string `json:"id"`
	InstanceID     string `json:"instanceId,omitempty"`
	Role           string `json:"role"`
	PrincipalRef   string `json:"principal_ref"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	ExternalRunRef string `json:"externalRunRef,omitempty"`
	Action         string `json:"action,omitempty"`
	Status         string `json:"status"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt,omitempty"`
	TerminalResult string `json:"terminalResult,omitempty"`
	LeaseOwner     string `json:"leaseOwner,omitempty"`
	LeaseToken     string `json:"-"`
	LeaseExpiresAt string `json:"leaseExpiresAt,omitempty"`
	HeartbeatAt    string `json:"heartbeatAt,omitempty"`
}

type ActionNextScope struct {
	Project   string   `json:"project,omitempty"`
	Path      string   `json:"path,omitempty"`
	Recursive bool     `json:"recursive,omitempty"`
	Templates []string `json:"templates,omitempty"`
}

type ActionNextFilters struct {
	Actions           []string `json:"actions,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	Statuses          []string `json:"statuses,omitempty"`
	Phases            []string `json:"phases,omitempty"`
	IncludeBlocked    bool     `json:"includeBlocked,omitempty"`
	IncludeActiveRuns bool     `json:"includeActiveRuns,omitempty"`
}

type ActionNextParams struct {
	Task       string            `json:"task,omitempty"`
	InstanceID string            `json:"instanceId,omitempty"`
	Scope      ActionNextScope   `json:"scope,omitempty"`
	Filters    ActionNextFilters `json:"filters,omitempty"`
	Limit      int               `json:"limit,omitempty"`
}

type ActionNextResult struct {
	Candidates []ActionCandidate `json:"candidates"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

type ActionCandidate struct {
	InstanceID            string               `json:"instanceId"`
	Task                  string               `json:"task"`
	SemanticActionKey     string               `json:"semanticActionKey"`
	Action                string               `json:"action"`
	Transition            string               `json:"transition"`
	Role                  string               `json:"role"`
	RequiredEvidenceKind  string               `json:"requiredEvidenceKind"`
	ExpectedStateRevision int64                `json:"expectedStateRevision"`
	ExpectedState         State                `json:"expectedState"`
	ExpectedTaskDocHash   string               `json:"expectedTaskDocHash,omitempty"`
	InputHash             string               `json:"inputHash,omitempty"`
	Source                *ActionSourceBinding `json:"source,omitempty"`
	HandlerContract       string               `json:"handlerContract,omitempty"`
	WorkspaceMode         string               `json:"workspaceMode,omitempty"`
	WorkspaceRef          string               `json:"workspaceRef,omitempty"`
	SideEffectClasses     []string             `json:"sideEffectClasses,omitempty"`
	Rank                  int                  `json:"rank"`
	Blocked               bool                 `json:"blocked,omitempty"`
	BlockedReason         string               `json:"blockedReason,omitempty"`
}

type ActionSourceBinding struct {
	SourceRunID      string `json:"sourceRunId"`
	SourceEvidenceID string `json:"sourceEvidenceId,omitempty"`
	CommitSha        string `json:"commitSha,omitempty"`
	ArtifactRef      string `json:"artifactRef,omitempty"`
}

type StartRunOptions struct {
	IdempotencyKey string
	DeliveryRef    string
	Lane           string
	ExternalRunRef string
	// Action is the optional semantic-action label (triage/implement/...) for
	// runs created through the wrkf.action.* surface. Empty for low-level
	// wrkf.run.start callers.
	Action         string
	LeaseOwner     string
	LeaseToken     string
	LeaseExpiresAt string
	HeartbeatAt    string
	LeaseMs        int64
}

type BindExternalOptions struct {
	DeliveryRef    string
	Lane           string
	IdempotencyKey string
}

type HookCatalog struct {
	SchemaVersion  string              `json:"schemaVersion"`
	Hooks          map[string]HookSpec `json:"hooks"`
	EffectHandlers map[string]HookSpec `json:"effectHandlers,omitempty"`
}

type HookSpec struct {
	Kind           string              `json:"kind"`
	Argv           []string            `json:"argv"`
	Stdin          string              `json:"stdin"`
	Stdout         string              `json:"stdout"`
	TimeoutMs      int                 `json:"timeoutMs"`
	CWD            string              `json:"cwd"`
	Env            map[string][]string `json:"env"`
	MaxStdoutBytes int                 `json:"maxStdoutBytes"`
	MaxStderrBytes int                 `json:"maxStderrBytes"`
}

type NextActionResponse struct {
	Instance           NextInstance        `json:"instance"`
	Actions            []NextAction        `json:"actions"`
	BlockedTransitions []BlockedTransition `json:"blockedTransitions"`
	OpenObligations    []Obligation        `json:"openObligations"`
	PendingEffects     []Effect            `json:"pendingEffects"`
}

type NextInstance struct {
	ID       string `json:"id"`
	TaskRef  string `json:"taskRef"`
	Template struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Hash    string `json:"hash"`
	} `json:"template"`
	State       State  `json:"state"`
	Revision    int64  `json:"revision"`
	ContextHash string `json:"contextHash"`
	TaskDoc     struct {
		Etag string `json:"etag"`
		Hash string `json:"hash"`
	} `json:"taskDoc"`
	Stale bool `json:"stale"`
}

type NextAction struct {
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	Mode          string      `json:"mode"`
	Owner         ActionOwner `json:"owner"`
	Rank          int         `json:"rank"`
	Confidence    float64     `json:"confidence,omitempty"`
	Why           string      `json:"why"`
	Unblocks      []string    `json:"unblocks,omitempty"`
	BlocksOn      []Blocker   `json:"blocksOn,omitempty"`
	Command       string      `json:"command,omitempty"`
	Preflight     string      `json:"preflight,omitempty"`
	ExpectedState *State      `json:"expectedState,omitempty"`
	Guardrails    Guardrails  `json:"guardrails"`
}

type ActionOwner struct {
	Role         string `json:"role"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	DeliveryRef  string `json:"deliveryRef,omitempty"`
	Lane         string `json:"lane,omitempty"`
}

type Blocker struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Message string `json:"message"`
}

type Guardrails struct {
	Hard     []string `json:"hard"`
	Warnings []string `json:"warnings"`
}

type BlockedTransition struct {
	ID       string    `json:"id"`
	Role     string    `json:"role,omitempty"`
	BlocksOn []Blocker `json:"blocksOn"`
}

type ValidateResult struct {
	Valid   bool     `json:"valid"`
	ID      string   `json:"id,omitempty"`
	Version string   `json:"version,omitempty"`
	Hash    string   `json:"hash,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

type TransitionOptions struct {
	PrincipalRef   string
	Role           string
	ExpectRevision *int64
	IdempotencyKey string
	ContextHash    string
	CheckIDs       []string
	RunChecks      bool
	DryRun         bool
	HookCatalog    *HookCatalog
	TemplateDir    string
	// RunID, when set, links the resulting workflow.transitioned event to a
	// wrkf run (used by the wrkf.action.* surface so action history can join
	// transition events to their action run).
	RunID string
}

type TransitionResult = map[string]interface{}

type nowFunc func() time.Time
