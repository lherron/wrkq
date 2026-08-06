package wrkfapi

type TransitionApplyParams struct {
	TaskSelector   string   `json:"task"`
	InstanceID     string   `json:"instanceId,omitempty"`
	Transition     string   `json:"transition"`
	Role           string   `json:"role,omitempty"`
	PrincipalRef   string   `json:"principal_ref,omitempty"`
	ExpectRevision *int64   `json:"expectRevision,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
	CheckIDs       []string `json:"checkIds,omitempty"`
	RunChecks      bool     `json:"runChecks,omitempty"`
	DryRun         bool     `json:"dryRun,omitempty"`
}

type codedError interface {
	Code() string
}
