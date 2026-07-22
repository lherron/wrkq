package wrkfapi

import (
	"encoding/json"

	"github.com/lherron/wrkq/internal/workflow"
)

type Instance = workflow.Instance
type Event = workflow.Event
type EventQueryParams = workflow.EventQueryParams
type EventQueryResult = workflow.EventQueryResult
type QueriedEvent = workflow.QueriedEvent
type Evidence = workflow.Evidence
type Obligation = workflow.Obligation
type Effect = workflow.Effect
type LedgerEntry = workflow.LedgerEntry
type LedgerAppendParams = workflow.AppendLedgerParams
type LedgerListParams = workflow.ListLedgerParams
type LedgerListResult = workflow.LedgerListResult
type EffectClaim = workflow.EffectClaim
type Run = workflow.Run
type CheckRun = workflow.CheckRun

type TemplateSummary struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Hash           string `json:"hash"`
	Kind           string `json:"kind,omitempty"`
	Description    string `json:"description,omitempty"`
	InstalledAt    string `json:"installedAt,omitempty"`
	InstalledBy    string `json:"installedBy,omitempty"`
	DiscontinuedAt string `json:"discontinuedAt,omitempty"`
	DiscontinuedBy string `json:"discontinuedBy,omitempty"`
}

type WorkflowListResult struct {
	Templates []TemplateSummary `json:"templates"`
}

type WorkflowShowResult struct {
	Template       workflow.Template `json:"template"`
	Hash           string            `json:"hash"`
	DiscontinuedAt string            `json:"discontinuedAt,omitempty"`
	DiscontinuedBy string            `json:"discontinuedBy,omitempty"`
}

type InstallResult struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	Installed bool   `json:"installed"`
}

type TransitionResult struct {
	Task        string                `json:"task"`
	InstanceID  string                `json:"instanceId"`
	State       workflow.State        `json:"state"`
	Revision    int64                 `json:"revision"`
	EventID     string                `json:"eventId"`
	Effects     []workflow.Effect     `json:"effects"`
	Obligations []workflow.Obligation `json:"obligations"`
}

type SuggestResult struct {
	Transition string                             `json:"transition"`
	Required   []workflow.EvidenceRequirementSpec `json:"required"`
	Missing    []string                           `json:"missing"`
	Checks     []string                           `json:"checks"`
	Warnings   []string                           `json:"warnings"`
}

type DiffResult struct {
	Old      TemplateSummary `json:"old"`
	New      TemplateSummary `json:"new"`
	SameHash bool            `json:"sameHash"`
}

type RoleListParams struct {
	TaskSelector string `json:"task,omitempty"`
	InstanceID   string `json:"instanceId,omitempty"`
}

type RoleBindParams struct {
	TaskSelector string `json:"task,omitempty"`
	InstanceID   string `json:"instanceId,omitempty"`
	Role         string `json:"role"`
	PrincipalRef string `json:"principal_ref"`
	DeliveryRef  string `json:"deliveryRef,omitempty"`
	Lane         string `json:"lane,omitempty"`
	BindingMode  string `json:"bindingMode,omitempty"`
}

type RoleUnbindParams struct {
	TaskSelector string `json:"task,omitempty"`
	InstanceID   string `json:"instanceId,omitempty"`
	Role         string `json:"role"`
	PrincipalRef string `json:"principal_ref,omitempty"`
}

type RoleSetParams struct {
	TaskSelector string            `json:"task,omitempty"`
	InstanceID   string            `json:"instanceId,omitempty"`
	RoleMap      map[string]string `json:"roleMap"`
}

type CheckRunResult struct {
	Runs []workflow.CheckRun `json:"runs"`
}

type EffectClaimParams struct {
	Adapter      string `json:"adapter"`
	Limit        int    `json:"limit"`
	LeaseMs      int64  `json:"leaseMs"`
	TaskSelector string `json:"task,omitempty"`
	Kind         string `json:"kind,omitempty"`
}

type EffectAckParams struct {
	EffectID   string          `json:"effectId"`
	LeaseToken string          `json:"leaseToken"`
	Receipt    json.RawMessage `json:"receipt,omitempty"`
	Force      bool            `json:"force,omitempty"`
}

type EffectFailParams struct {
	EffectID   string `json:"effectId"`
	LeaseToken string `json:"leaseToken"`
	Reason     string `json:"reason"`
	Retryable  bool   `json:"retryable,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

type EffectDeliverParams struct {
	EffectID string `json:"effectId"`
	Adapter  string `json:"adapter,omitempty"`
}

type HookSummary struct {
	ID   string   `json:"id"`
	Kind string   `json:"kind,omitempty"`
	Argv []string `json:"argv,omitempty"`
}

type HookListResult struct {
	Hooks []HookSummary `json:"hooks"`
}

type HookShowResult struct {
	ID   string            `json:"id"`
	Hook workflow.HookSpec `json:"hook"`
}

type EvidenceAddParams struct {
	TaskSelector   string                  `json:"task"`
	InstanceID     string                  `json:"instanceId,omitempty"`
	Kind           string                  `json:"kind"`
	Ref            string                  `json:"ref"`
	Summary        string                  `json:"summary,omitempty"`
	Facts          json.RawMessage         `json:"facts,omitempty"`
	Data           json.RawMessage         `json:"data,omitempty"`
	PrincipalRef   string                  `json:"principal_ref,omitempty"`
	Role           string                  `json:"role,omitempty"`
	RunID          string                  `json:"runId,omitempty"`
	ContentHash    string                  `json:"contentHash,omitempty"`
	Build          *workflow.EvidenceBuild `json:"build,omitempty"`
	IdempotencyKey string                  `json:"idempotencyKey,omitempty"`
}

type ObligationStatusParams struct {
	TaskSelector string `json:"task"`
	ID           string `json:"id"`
	EvidenceID   string `json:"evidenceId,omitempty"`
	Reason       string `json:"reason,omitempty"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	Role         string `json:"role,omitempty"`
}

type CheckRunParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	Role         string `json:"role,omitempty"`
}

type HookRunParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
	HookID       string `json:"hookId"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	Role         string `json:"role,omitempty"`
}

func (p EvidenceAddParams) workflowParams() workflow.AddEvidenceParams {
	return workflow.AddEvidenceParams{
		TaskSelector:   p.TaskSelector,
		InstanceID:     p.InstanceID,
		Kind:           p.Kind,
		Ref:            p.Ref,
		Summary:        p.Summary,
		Facts:          rawString(p.Facts),
		Data:           rawString(p.Data),
		PrincipalRef:   p.PrincipalRef,
		Role:           p.Role,
		RunID:          p.RunID,
		ContentHash:    p.ContentHash,
		Build:          p.Build,
		IdempotencyKey: p.IdempotencyKey,
	}
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}
