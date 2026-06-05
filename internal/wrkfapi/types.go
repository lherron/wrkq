package wrkfapi

import (
	"encoding/json"

	"github.com/lherron/wrkq/internal/workflow"
)

type Instance = workflow.Instance
type Event = workflow.Event
type Evidence = workflow.Evidence
type Obligation = workflow.Obligation
type Effect = workflow.Effect
type Run = workflow.Run
type CheckRun = workflow.CheckRun
type NextActionResponse = workflow.NextActionResponse
type ValidateResult = workflow.ValidateResult
type Template = workflow.Template
type State = workflow.State
type EvidenceRequirementSpec = workflow.EvidenceRequirementSpec
type HookSpec = workflow.HookSpec
type HookCatalog = workflow.HookCatalog

type TemplateSummary struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Hash        string `json:"hash"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	InstalledAt string `json:"installedAt,omitempty"`
	InstalledBy string `json:"installedBy,omitempty"`
}

type WorkflowListResult struct {
	Templates []TemplateSummary `json:"templates"`
}

type WorkflowShowResult struct {
	Template workflow.Template `json:"template"`
	Hash     string            `json:"hash"`
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
	ContextHash string                `json:"contextHash"`
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

type SyncMetaResult struct {
	Updated int `json:"updated"`
}

type EffectClaim struct {
	Effects        []workflow.Effect `json:"effects"`
	LeaseToken     string            `json:"leaseToken"`
	LeaseExpiresAt string            `json:"leaseExpiresAt"`
}

type CheckRunResult struct {
	Runs []workflow.CheckRun `json:"runs"`
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
	TaskSelector string          `json:"task"`
	Kind         string          `json:"kind"`
	Ref          string          `json:"ref"`
	Summary      string          `json:"summary,omitempty"`
	Facts        json.RawMessage `json:"facts,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
	Actor        string          `json:"actor,omitempty"`
	Role         string          `json:"role,omitempty"`
}

type ObligationStatusParams struct {
	TaskSelector string `json:"task"`
	ID           string `json:"id"`
	EvidenceID   string `json:"evidenceId,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type CheckRunParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
	Actor        string `json:"actor,omitempty"`
	Role         string `json:"role,omitempty"`
}

type HookRunParams struct {
	TaskSelector string `json:"task"`
	Transition   string `json:"transition"`
	HookID       string `json:"hookId"`
	Actor        string `json:"actor,omitempty"`
	Role         string `json:"role,omitempty"`
}

func (p EvidenceAddParams) workflowParams() workflow.AddEvidenceParams {
	return workflow.AddEvidenceParams{
		TaskSelector: p.TaskSelector,
		Kind:         p.Kind,
		Ref:          p.Ref,
		Summary:      p.Summary,
		Facts:        rawString(p.Facts),
		Data:         rawString(p.Data),
		Actor:        p.Actor,
		Role:         p.Role,
	}
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}
