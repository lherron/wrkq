package workflow

// ErrorDetail is the single machine-parseable error shape carried by wrkf
// domain/validation errors. CLI (--json envelope) and wrkfapi/RPC both map
// from it so the contract stays uniform across surfaces.
type ErrorDetail struct {
	Code        string                  `json:"code"`
	Field       string                  `json:"field,omitempty"`
	Message     string                  `json:"message"`
	Expected    string                  `json:"expected,omitempty"`
	Allowed     []string                `json:"allowed,omitempty"`
	Fix         string                  `json:"fix,omitempty"`
	Suspension  *Suspension             `json:"suspension,omitempty"`
	Predecessor *ActionClaimPredecessor `json:"predecessor,omitempty"`
}

// DetailedError is implemented by wrkf errors that carry an ErrorDetail.
type DetailedError interface {
	error
	Code() string
	Detail() ErrorDetail
}

type wrkfError struct {
	code        string
	msg         string
	field       string
	expected    string
	allowed     []string
	fix         string
	suspension  *Suspension
	predecessor *ActionClaimPredecessor
}

type transitionEffectDeliveryError struct {
	transitionID string
	eventID      string
	effectID     string
	kind         string
	status       string
	err          error
	result       map[string]interface{}
}
