package wrkqapi

import "fmt"

// Stable machine-readable error codes (docs/wrkq-wrkf-rpc.md §5).
const (
	CodeNotFound         = "WRKQ_NOT_FOUND"
	CodeValidation       = "WRKQ_VALIDATION"
	CodeConflict         = "WRKQ_CONFLICT"
	CodeForbidden        = "WRKQ_FORBIDDEN"
	CodePermissionDenied = "WRKQ_PERMISSION_DENIED"
	CodeMigrationReq     = "WRKQ_DB_MIGRATION_REQUIRED"
	CodeDBBusy           = "WRKQ_DB_BUSY"
	CodeAlreadyClaimed   = "WRKQ_ALREADY_CLAIMED"
	CodeWrongState       = "WRKQ_WRONG_STATE"
	CodeClaimSuperseded  = "WRKQ_CLAIM_SUPERSEDED"
	CodeNodeIdentity     = "WRKQ_NODE_IDENTITY_REQUIRED"
	CodeInternal         = "WORKRPC_INTERNAL"
)

func (e *DomainError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *DomainError) Code() string {
	if e == nil {
		return CodeInternal
	}
	return e.code
}

func (e *DomainError) Retryable() bool {
	if e == nil {
		return false
	}
	return e.retryable
}

func (e *DomainError) Data() any {
	if e == nil {
		return nil
	}
	return e.data
}

func (e *DomainError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newError(code, msg string, retryable bool, data any, err error) *DomainError {
	if msg == "" {
		msg = "request failed"
	}
	return &DomainError{code: code, message: msg, retryable: retryable, data: data, err: err}
}

// NewNotFoundError reports a missing wrkq resource (WRKQ_NOT_FOUND).
func NewNotFoundError(ref, kind string) *DomainError {
	if kind == "" {
		kind = "resource"
	}
	msg := fmt.Sprintf("%s not found: %s", kind, ref)
	if ref == "" {
		msg = fmt.Sprintf("%s not found", kind)
	}
	return newError(CodeNotFound, msg, false, struct {
		Ref  string `json:"ref,omitempty"`
		Kind string `json:"kind,omitempty"`
	}{Ref: ref, Kind: kind}, nil)
}

// NewValidationError reports malformed params or an invalid mutation
// (WRKQ_VALIDATION). data should follow the structured validation shape (§5).
func NewValidationError(msg string, data any) *DomainError {
	if msg == "" {
		msg = "validation failed"
	}
	return newError(CodeValidation, msg, false, data, nil)
}

// NewConflictError reports a CAS / uniqueness / idempotency conflict
// (WRKQ_CONFLICT). It is retryable per the error table.
func NewConflictError(msg string, data any) *DomainError {
	if msg == "" {
		msg = "conflict"
	}
	return newError(CodeConflict, msg, true, data, nil)
}

// NewForbiddenError reports an authenticated caller crossing a resource
// ownership boundary (WRKQ_FORBIDDEN).
func NewForbiddenError(msg string, data any) *DomainError {
	if msg == "" {
		msg = "forbidden"
	}
	return newError(CodeForbidden, msg, false, data, nil)
}

func NewAlreadyClaimedError(data any) *DomainError {
	return newError(CodeAlreadyClaimed, "already_claimed", false, data, nil)
}

func NewWrongStateError(data any) *DomainError {
	return newError(CodeWrongState, "wrong_state", false, data, nil)
}

// NewWrongStateMessageError reports the same WRKQ_WRONG_STATE condition with a
// message that says WHAT state refused the call. A caller that has to open the
// data bag to learn "the room is closed" has been told nothing useful.
func NewWrongStateMessageError(msg string, data any) *DomainError {
	return newError(CodeWrongState, msg, false, data, nil)
}

func NewClaimSupersededError(data any) *DomainError {
	return newError(CodeClaimSuperseded, "claim_superseded", false, data, nil)
}

func NewNodeIdentityError() *DomainError {
	return newError(CodeNodeIdentity, "node identity is required for task claims", false, map[string]any{
		"reason": "missing_verified_node_identity",
	}, nil)
}

// NewBusyError reports SQLite write contention that outlasted busy_timeout
// (WRKQ_DB_BUSY). It is retryable: the caller should back off and retry the
// whole operation. data carries reason:"sqlite_busy" for clients.
func NewBusyError(err error) *DomainError {
	return newError(CodeDBBusy, "database is busy due to write contention; retry", true, struct {
		Reason string `json:"reason"`
	}{Reason: "sqlite_busy"}, err)
}

// NewInternalError wraps an unclassified failure (WORKRPC_INTERNAL). A SQLite
// busy/locked contention error is reclassified to the typed, retryable
// WRKQ_DB_BUSY so contention never surfaces as a generic internal error
// (docs/wrkq-wrkf-rpc.md §5; T-05066).
func NewInternalError(err error) *DomainError {
	if isSQLiteBusy(err) {
		return NewBusyError(err)
	}
	msg := "internal error"
	if err != nil {
		msg = err.Error()
	}
	return newError(CodeInternal, msg, false, nil, err)
}

var _ Error = (*DomainError)(nil)
