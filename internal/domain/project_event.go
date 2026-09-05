package domain

// ProjectEvent is one foreign fact posted into the shared wrkq ledger. Its
// container and campaign fields are immutable production-time affiliation
// stamps; ProjectUUID scopes idempotency only.
type ProjectEvent struct {
	ID             int64
	FID            string
	ProjectUUID    string
	ContainerUUID  string
	CampaignUUID   *string
	TaskUUID       *string
	Type           string
	Source         string
	Node           *string
	PrincipalRef   string
	ScopeRef       *string
	Summary        string
	Payload        *string
	IdempotencyKey *string
	OccurredAt     string
	CreatedAt      string
}

// ProjectEventTypeCount is the project-event vocabulary projection.
type ProjectEventTypeCount struct {
	Type          string
	Count         int64
	LastCreatedAt string
}
