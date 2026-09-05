package wrkqapi

import "encoding/json"

type ProjectEventPostParams struct {
	Project        string          `json:"project,omitempty"`
	Task           string          `json:"task,omitempty"`
	Type           string          `json:"type"`
	Source         string          `json:"source"`
	Node           string          `json:"node,omitempty"`
	Summary        string          `json:"summary"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	OccurredAt     string          `json:"occurredAt,omitempty"`
	PrincipalRef   string          `json:"principalRef,omitempty"`
	ScopeRef       string          `json:"scopeRef,omitempty"`
}

type ProjectEventGetParams struct {
	ProjectEvent string `json:"projectEvent"`
}

type ProjectEventTypesViewParams struct {
	Project string `json:"project,omitempty"`
}

type WrkqProjectEvent struct {
	ID             int64           `json:"id"`
	FID            string          `json:"fid"`
	ProjectUUID    string          `json:"projectUuid"`
	ContainerUUID  string          `json:"containerUuid"`
	CampaignUUID   *string         `json:"campaignUuid"`
	TaskUUID       *string         `json:"taskUuid"`
	Type           string          `json:"type"`
	Source         string          `json:"source"`
	Node           *string         `json:"node,omitempty"`
	PrincipalRef   string          `json:"principalRef"`
	ScopeRef       *string         `json:"scopeRef,omitempty"`
	Summary        string          `json:"summary"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	IdempotencyKey *string         `json:"idempotencyKey,omitempty"`
	OccurredAt     string          `json:"occurredAt"`
	CreatedAt      string          `json:"createdAt"`
}

type WrkqProjectEventPostResult struct {
	ID      int64  `json:"id"`
	FID     string `json:"fid"`
	Created bool   `json:"created"`
}

type WrkqProjectEventType struct {
	Type          string `json:"type"`
	Count         int64  `json:"count"`
	LastCreatedAt string `json:"lastCreatedAt"`
}

type WrkqProjectEventTypesView struct {
	Items []WrkqProjectEventType `json:"items"`
}
