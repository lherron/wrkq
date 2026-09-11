package wrkqapi

import "encoding/json"

type ProjectEventPostParams struct {
	Project        string          `json:"project,omitempty"`
	Task           string          `json:"task,omitempty"`
	Type           string          `json:"type"`
	Summary        string          `json:"summary"`
	Attributes     json.RawMessage `json:"attributes"`
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
	UUID           string          `json:"uuid"`
	ProjectUUID    string          `json:"projectUuid"`
	ContainerUUID  string          `json:"containerUuid"`
	CampaignUUID   *string         `json:"campaignUuid"`
	TaskUUID       *string         `json:"taskUuid"`
	Type           string          `json:"type"`
	Attributes     json.RawMessage `json:"attributes"`
	PrincipalRef   *string         `json:"principalRef"`
	ScopeRef       *string         `json:"scopeRef"`
	Summary        string          `json:"summary"`
	IdempotencyKey *string         `json:"idempotencyKey"`
	OccurredAt     string          `json:"occurredAt"`
	CreatedAt      string          `json:"createdAt"`
	Task           *string         `json:"task"`
	Container      *string         `json:"container"`
	Campaign       *string         `json:"campaign"`
}

type WrkqProjectEventPostResult struct {
	UUID    string `json:"uuid"`
	Created bool   `json:"created"`
	ID      int64  `json:"-"`
}

type WrkqProjectEventType struct {
	Type          string `json:"type"`
	Count         int64  `json:"count"`
	LastCreatedAt string `json:"lastCreatedAt"`
}

type WrkqProjectEventTypesView struct {
	Items []WrkqProjectEventType `json:"items"`
}
