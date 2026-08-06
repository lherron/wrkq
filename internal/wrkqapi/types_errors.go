package wrkqapi

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
