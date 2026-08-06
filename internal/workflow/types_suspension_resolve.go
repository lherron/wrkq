package workflow

// ResolveSuspensionParams is the input to the atomic resolution command. Only
// SuspensionID and Disposition are required; ExpectRevision is the ordinary CAS
// precondition and Explanation is recorded free text.
type ResolveSuspensionParams struct {
	SuspensionID   string
	Disposition    string
	Explanation    string
	ExpectRevision *int64
	PrincipalRef   string
	Role           string
}
