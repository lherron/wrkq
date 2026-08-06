package workflow

type CancelInstanceParams struct {
	Task           string
	InstanceID     string
	ExpectRevision *int64
	Explanation    string
	PrincipalRef   string
	Role           string
}
