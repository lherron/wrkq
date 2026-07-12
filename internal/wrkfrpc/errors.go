package wrkfrpc

import (
	"encoding/json"
	"errors"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

var domainRPCCode = map[string]int{
	wrkfapi.CodeNotFound:             -32004,
	wrkfapi.CodeValidation:           -32602,
	wrkfapi.CodeStaleRevision:        -32009,
	wrkfapi.CodeTransitionBlocked:    -32011,
	wrkfapi.CodeRoleDenied:           -32012,
	wrkfapi.CodeIdempotencyMismatch:  -32013,
	wrkfapi.CodeLeaseConflict:        -32014,
	wrkfapi.CodeEffectNotDeliverable: -32015,
	wrkfapi.CodeHookFailed:           -32016,
	wrkfapi.CodeDBMigrationRequired:  -32017,
	wrkfapi.CodeKindRoleDenied:       -32018,
	wrkfapi.CodeLinkageUnresolved:    -32019,
	wrkfapi.CodeLinkageStale:         -32020,
	wrkfapi.CodeSuspensionNotFound:   -32025,
	wrkfapi.CodeSuspended:            -32026,
	wrkfapi.CodeInternal:             -32603,
}

type errorData interface {
	Data() any
}

func MapError(err error) *RPCError {
	if err == nil {
		return nil
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		err = wrkfapi.NewValidationError(validation.Message, validation.Data)
	}
	var apiErr wrkfapi.Error
	if !errors.As(err, &apiErr) {
		return domainError(wrkfapi.CodeInternal, "internal error", false, nil)
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
