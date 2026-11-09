package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
)

// refusal is a server refusal reworded for the CLI (T-12766). Its text is the
// caller-facing message, never the "CODE: " prefixed *Error string, while
// Code() and ErrorData() still carry the server's typed code and data, so the
// funnel renders error.code / error.data instead of WRKQ_ERROR. It does not
// unwrap to the *Error: callers that branched on a flattened error before
// (isNotFound and the like) see the same non-RPC error they always did.
type refusal struct {
	cause   *Error
	message string
}

func (r *refusal) Error() string              { return r.message }
func (r *refusal) Code() string               { return r.cause.Code() }
func (r *refusal) ErrorData() json.RawMessage { return r.cause.ErrorData() }

// rpcRefusal returns err's bare server message as an error that keeps the
// server's typed code and data. A non-RPC error passes through unchanged.
func rpcRefusal(err error) error {
	var re *Error
	if errors.As(err, &re) {
		return &refusal{cause: re, message: re.Message}
	}
	return err
}

// rewordRefusal rewords err with format while keeping an RPC refusal's typed
// code and data; a non-RPC err becomes a plain error with the same text.
func rewordRefusal(err error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	var re *Error
	if errors.As(err, &re) {
		return &refusal{cause: re, message: msg}
	}
	return errors.New(msg)
}
