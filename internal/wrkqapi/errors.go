// Package wrkqapi implements the wrkq-namespace business surface of the unified
// wrkq/wrkf JSON-RPC protocol (docs/wrkq-wrkf-rpc.md). It calls the existing
// store/domain/selectors layers directly — never Cobra handlers — and returns
// named camelCase DTOs plus WRKQ_* typed domain errors.
package wrkqapi

import (
	"fmt"

	"github.com/lherron/wrkq/internal/db"
)

// Stable machine-readable error codes (docs/wrkq-wrkf-rpc.md §5).
const (
	CodeNotFound         = "WRKQ_NOT_FOUND"
	CodeValidation       = "WRKQ_VALIDATION"
	CodeConflict         = "WRKQ_CONFLICT"
	CodePermissionDenied = "WRKQ_PERMISSION_DENIED"
	CodeMigrationReq     = "WRKQ_DB_MIGRATION_REQUIRED"
	CodeDBBusy           = "WRKQ_DB_BUSY"
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
	if db.IsBusy(err) {
		return NewBusyError(err)
	}
	msg := "internal error"
	if err != nil {
		msg = err.Error()
	}
	return newError(CodeInternal, msg, false, nil, err)
}

var _ Error = (*DomainError)(nil)
