package workrpc

import (
	"encoding/json"
	"errors"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603

	CodeWRKQNotFound          = "WRKQ_NOT_FOUND"
	CodeWRKQValidation        = "WRKQ_VALIDATION"
	CodeWRKQConflict          = "WRKQ_CONFLICT"
	CodeWRKQForbidden         = "WRKQ_FORBIDDEN"
	CodeWRKQPermissionDenied  = "WRKQ_PERMISSION_DENIED"
	CodeWRKQMigrationRequired = "WRKQ_DB_MIGRATION_REQUIRED"
	CodeWRKQDBBusy            = "WRKQ_DB_BUSY"
	CodeWRKQAlreadyClaimed    = "WRKQ_ALREADY_CLAIMED"
	CodeWRKQWrongState        = "WRKQ_WRONG_STATE"
	CodeWRKQClaimSuperseded   = "WRKQ_CLAIM_SUPERSEDED"
	CodeWRKQNodeIdentity      = "WRKQ_NODE_IDENTITY_REQUIRED"
	CodeWRKQCursorInvalid     = "WRKQ_CURSOR_INVALID"
	CodeWorkRPCInternal       = "WORKRPC_INTERNAL"
)

var domainRPCCode = map[string]int{
	CodeWRKQNotFound:                 -32004,
	CodeWRKQValidation:               -32602,
	CodeWRKQConflict:                 -32021,
	CodeWRKQForbidden:                -32031,
	CodeWRKQPermissionDenied:         -32022,
	CodeWRKQMigrationRequired:        -32023,
	CodeWRKQDBBusy:                   -32024,
	CodeWRKQAlreadyClaimed:           -32027,
	CodeWRKQWrongState:               -32028,
	CodeWRKQClaimSuperseded:          -32029,
	CodeWRKQNodeIdentity:             -32030,
	CodeWRKQCursorInvalid:            -32032,
	wrkfapi.CodeNotFound:             -32004,
	wrkfapi.CodeValidation:           -32602,
	wrkfapi.CodeStaleRevision:        -32009,
	wrkfapi.CodeTransitionBlocked:    -32011,
	wrkfapi.CodeRoleDenied:           -32012,
	wrkfapi.CodeIdempotencyMismatch:  -32013,
	wrkfapi.CodeLeaseConflict:        -32014,
	wrkfapi.CodeEffectNotDeliverable: -32015,
	wrkfapi.CodeHookFailed:           -32016,
	wrkfapi.CodeKindRoleDenied:       -32018,
	wrkfapi.CodeLinkageUnresolved:    -32019,
	wrkfapi.CodeLinkageStale:         -32020,
	wrkfapi.CodeSuspensionNotFound:   -32025,
	wrkfapi.CodeSuspended:            -32026,
	wrkfapi.CodeInternal:             -32603,
	CodeWorkRPCInternal:              -32603,
}

type DomainError struct {
	code      string
	message   string
	retryable bool
	data      any
}

func NewValidationError(message string, data any) *DomainError {
	return NewDomainError(CodeWRKQValidation, message, false, data)
}

func NewDomainError(code, message string, retryable bool, data any) *DomainError {
	if code == "" {
		code = CodeWorkRPCInternal
	}
	if message == "" {
		message = "request failed"
	}
	return &DomainError{code: code, message: message, retryable: retryable, data: data}
}

func (e *DomainError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *DomainError) Code() string {
	if e == nil {
		return CodeWorkRPCInternal
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

type errorData interface {
	Data() any
}

func MapError(err error) *RPCError {
	if err == nil {
		return nil
	}
	// SQLite write contention that outlasted busy_timeout always maps to the
	// typed, retryable WRKQ_DB_BUSY — never a generic internal error — no
	// matter which layer wrapped it (T-05066, docs/wrkq-wrkf-rpc.md §5). This
	// is checked first because a busy error is always an infrastructure
	// contention signal, never a semantic conflict/validation failure.
	if isSQLiteBusy(err) {
		return domainError(CodeWRKQDBBusy, "database is busy due to write contention; retry", true, map[string]any{"reason": "sqlite_busy"})
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		code := validation.Code
		if code == "" {
			code = CodeWRKQValidation
		}
		return domainError(code, validation.Error(), false, validation.Data)
	}
	var workErr *DomainError
	if errors.As(err, &workErr) {
		return domainError(workErr.Code(), workErr.Error(), workErr.Retryable(), workErr.Data())
	}
	var apiErr wrkfapi.Error
	if !errors.As(err, &apiErr) {
		return domainError(CodeWorkRPCInternal, "internal error", false, nil)
	}
	msg := apiErr.Error()
	if apiErr.Code() == wrkfapi.CodeInternal {
		msg = "internal error"
	}
	var data any
	var carrier errorData
	if errors.As(err, &carrier) {
		data = carrier.Data()
	}
	return domainError(apiErr.Code(), msg, apiErr.Retryable(), data)
}

func domainError(code, message string, retryable bool, data any) *RPCError {
	if message == "" {
		message = "request failed"
	}
	payload := dataMap(data)
	payload["code"] = code
	payload["retryable"] = retryable
	raw, _ := json.Marshal(payload)
	rpcCode, ok := domainRPCCode[code]
	if !ok {
		rpcCode = codeInternal
	}
	return &RPCError{Code: rpcCode, Message: message, Data: raw}
}

func dataMap(data any) map[string]any {
	out := map[string]any{}
	if data == nil {
		return out
	}
	b, err := json.Marshal(data)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func protocolError(code int, message string, data any) *RPCError {
	if message == "" {
		message = "JSON-RPC protocol error"
	}
	var raw json.RawMessage
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	return &RPCError{Code: code, Message: message, Data: raw}
}
