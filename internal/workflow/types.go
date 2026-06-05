package workflow

import (
	"encoding/json"
	"time"
)

type State struct {
	Status  string `json:"status"`
	Phase   string `json:"phase,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type Template struct {
	SchemaVersion   string                     `json:"schemaVersion"`
	ID              string                     `json:"id"`
	Version         string                     `json:"version"`
	Kind            string                     `json:"kind"`
	Description     string                     `json:"description,omitempty"`
	Initial         State                      `json:"initial"`
	Roles           map[string]RoleSpec        `json:"roles"`
	States          []State                    `json:"states"`
	EvidenceKinds   map[string]KindSpec        `json:"evidenceKinds,omitempty"`
	ObligationKinds map[string]KindSpec        `json:"obligationKinds,omitempty"`
	Checks          map[string]CheckSpec       `json:"checks,omitempty"`
	Transitions     []TransitionSpec           `json:"transitions"`
	StateHooks      map[string][]HookRef       `json:"stateHooks,omitempty"`
	NextActionModel map[string]json.RawMessage `json:"nextActionModel,omitempty"`
	Supervisor      map[string]json.RawMessage `json:"supervisor,omitempty"`
	Raw             map[string]json.RawMessage `json:"-"`
}

type RoleSpec struct {
	Description string `json:"description,omitempty"`
}

type KindSpec struct {
	Description string         `json:"description,omitempty"`
	Class       string         `json:"class,omitempty"`
	Facts       *FactsContract `json:"facts,omitempty"`
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
	ID             string               `json:"id"`
	From           State                `json:"from"`
	By             []string             `json:"by"`
	Responsibility *ResponsibilitySpec  `json:"responsibility,omitempty"`
	Guards         []Predicate          `json:"guards,omitempty"`
	Requires       []RequirementSpec    `json:"requires,omitempty"`
	Checks         []string             `json:"checks,omitempty"`
	Outcomes       []OutcomeCase        `json:"outcomes"`
	Hooks          map[string][]HookRef `json:"hooks,omitempty"`
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
	When        Predicate              `json:"when"`
	To          State                  `json:"to"`
	Effects     []EffectSpec           `json:"effects,omitempty"`
	Obligations []ObligationCreateSpec `json:"obligations,omitempty"`
}

type EffectSpec struct {
	Kind   string                 `json:"kind"`
	Role   string                 `json:"role,omitempty"`
	Reason string                 `json:"reason,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

type ObligationCreateSpec struct {
	Kind       string `json:"kind"`
	OwnerRole  string `json:"ownerRole,omitempty"`
	OwnerActor string `json:"ownerActor,omitempty"`
	Blocking   bool   `json:"blocking"`
	Reason     string `json:"reason,omitempty"`
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
	ID              string `json:"id"`
	TaskUUID        string `json:"taskUuid,omitempty"`
	TaskRef         string `json:"taskRef"`
	ProjectID       string `json:"projectId,omitempty"`
	TemplateID      string `json:"templateId"`
	TemplateVersion string `json:"templateVersion"`
	TemplateHash    string `json:"templateHash"`
	Status          string `json:"status"`
	Phase           string `json:"phase,omitempty"`
	Outcome         string `json:"outcome,omitempty"`
	Revision        int64  `json:"revision"`
	ContextHash     string `json:"contextHash"`
	TaskDocEtag     string `json:"taskDocEtag"`
	TaskDocHash     string `json:"taskDocHash"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	ClosedAt        string `json:"closedAt,omitempty"`
}

func (i Instance) State() State {
	return State{Status: i.Status, Phase: i.Phase, Outcome: i.Outcome}
}

type Event struct {
	ID               string          `json:"id"`
	InstanceID       string          `json:"instanceId"`
	Seq              int64           `json:"seq"`
	SchemaVersion    string          `json:"schemaVersion"`
	Type             string          `json:"type"`
	Actor            string          `json:"actor,omitempty"`
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
	Actor                string          `json:"actor,omitempty"`
	Role                 string          `json:"role,omitempty"`
	RunID                string          `json:"runId,omitempty"`
	TaskEtagAtProduction string          `json:"taskEtagAtProduction,omitempty"`
	ProducedAt           string          `json:"producedAt"`
}

type AddEvidenceParams struct {
	TaskSelector string
	Kind         string
	Ref          string
	Summary      string
	Facts        string
	Data         string
	Actor        string
	Role         string
}

type Obligation struct {
	ID                    string          `json:"id"`
	InstanceID            string          `json:"instanceId,omitempty"`
	Kind                  string          `json:"kind"`
	OwnerRole             string          `json:"ownerRole,omitempty"`
	OwnerActor            string          `json:"ownerActor,omitempty"`
	Blocking              bool            `json:"blocking"`
	Status                string          `json:"status"`
	Reason                string          `json:"reason,omitempty"`
	Data                  json.RawMessage `json:"data,omitempty"`
	SatisfiedByEvidenceID string          `json:"satisfiedByEvidenceId,omitempty"`
	CreatedAt             string          `json:"createdAt"`
	UpdatedAt             string          `json:"updatedAt"`
}

type Effect struct {
	ID             string          `json:"id"`
	InstanceID     string          `json:"instanceId,omitempty"`
	Revision       int64           `json:"revision"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Status         string          `json:"status"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	Attempts       int64           `json:"attempts"`
	LeasedBy       string          `json:"leasedBy,omitempty"`
	LeasedUntil    string          `json:"leasedUntil,omitempty"`
	DeliveredAt    string          `json:"deliveredAt,omitempty"`
	LastError      string          `json:"lastError,omitempty"`
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
	Actor        string          `json:"actor,omitempty"`
	Role         string          `json:"role,omitempty"`
	RunID        string          `json:"runId,omitempty"`
	StartedAt    string          `json:"startedAt"`
	CompletedAt  string          `json:"completedAt,omitempty"`
}

type Run struct {
	ID             string `json:"id"`
	InstanceID     string `json:"instanceId,omitempty"`
	Role           string `json:"role"`
	Actor          string `json:"actor"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	ExternalRunRef string `json:"externalRunRef,omitempty"`
	Status         string `json:"status"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt,omitempty"`
	TerminalResult string `json:"terminalResult,omitempty"`
}

type StartRunOptions struct {
	IdempotencyKey string
	DeliveryRef    string
	Lane           string
	ExternalRunRef string
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
	Role        string `json:"role"`
	Actor       string `json:"actor,omitempty"`
	DeliveryRef string `json:"deliveryRef,omitempty"`
	Lane        string `json:"lane,omitempty"`
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
	Actor          string
	Role           string
	ExpectRevision *int64
	IdempotencyKey string
	ContextHash    string
	CheckIDs       []string
	RunChecks      bool
	DryRun         bool
	HookCatalog    *HookCatalog
	TemplateDir    string
}

type nowFunc func() time.Time
