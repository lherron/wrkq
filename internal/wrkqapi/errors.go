// Package wrkqapi implements the wrkq-namespace business surface of the unified
// wrkq/wrkf JSON-RPC protocol (docs/wrkq-wrkf-rpc.md). It calls the existing
// store/domain/selectors layers directly — never Cobra handlers — and returns
// named camelCase DTOs plus WRKQ_* typed domain errors.
package wrkqapi

import "fmt"

// Stable machine-readable error codes (docs/wrkq-wrkf-rpc.md §5).
const (
	CodeNotFound         = "WRKQ_NOT_FOUND"
	CodeValidation       = "WRKQ_VALIDATION"
	CodeConflict         = "WRKQ_CONFLICT"
	CodePermissionDenied = "WRKQ_PERMISSION_DENIED"
	CodeMigrationReq     = "WRKQ_DB_MIGRATION_REQUIRED"
	CodeInternal         = "WORKRPC_INTERNAL"
)

// Error is the wrkq domain error interface. It is method-set compatible with
// the wrkf error interface used by workrpc.MapError, so workrpc maps these to
// the right JSON-RPC envelope (error.data.code = the WRKQ_* code) without any
// special-casing: errors.As(err, &wrkfapi.Error) matches any value that
// implements Error()/Code()/Retryable(), and the data carrier interface picks
// up Data().
type Error interface {
	error
	Code() string
	Retryable() bool
}

// DomainError is the concrete wrkq error type.
type DomainError struct {
	code      string
	message   string
	retryable bool
	data      any
	err       error
}

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

// NewInternalError wraps an unclassified failure (WORKRPC_INTERNAL).
func NewInternalError(err error) *DomainError {
	msg := "internal error"
	if err != nil {
		msg = err.Error()
	}
	return newError(CodeInternal, msg, false, nil, err)
}

var _ Error = (*DomainError)(nil)
