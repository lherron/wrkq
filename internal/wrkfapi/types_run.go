package wrkfapi

type RunStartParams struct {
	TaskSelector   string `json:"task"`
	InstanceID     string `json:"instanceId,omitempty"`
	Role           string `json:"role"`
	PrincipalRef   string `json:"principal_ref"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	ExternalRunRef string `json:"externalRunRef,omitempty"`
}

type RunBindExternalParams struct {
	RunID          string `json:"runId"`
	ExternalRunRef string `json:"externalRunRef"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type RunFinishParams struct {
	RunID   string `json:"runId"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type RunFailParams struct {
	RunID   string `json:"runId"`
	Summary string `json:"summary,omitempty"`
}
