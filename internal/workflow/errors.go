package workflow

import "fmt"

const (
	wrkfCodeStaleRevision       = "WRKF_STALE_REVISION"
	wrkfCodeContextMismatch     = "WRKF_CONTEXT_MISMATCH"
	wrkfCodeTransitionBlocked   = "WRKF_TRANSITION_BLOCKED"
	wrkfCodeRoleDenied          = "WRKF_ROLE_DENIED"
	wrkfCodeIdempotencyMismatch = "WRKF_IDEMPOTENCY_MISMATCH"
)

type wrkfError struct {
	code string
	msg  string
}

func (e *wrkfError) Error() string {
	if e == nil {
		return ""
	}
	return e.msg
}

func (e *wrkfError) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

func staleRevisionError(instanceID string, expected, actual int64) error {
	return &wrkfError{
		code: wrkfCodeStaleRevision,
		msg:  fmt.Sprintf("workflow revision mismatch: instance %s expected %d, got %d", instanceID, expected, actual),
	}
}

func contextMismatchError(instanceID, expected, actual string) error {
	return &wrkfError{
		code: wrkfCodeContextMismatch,
		msg:  fmt.Sprintf("workflow context hash mismatch: instance %s expected %s, got %s", instanceID, expected, actual),
	}
}

func transitionBlockedError(instanceID, transition string, blockers []Blocker) error {
	msg := "transition is blocked"
	if len(blockers) > 0 && blockers[0].Message != "" {
		msg = "transition is blocked: " + blockers[0].Message
	}
	return &wrkfError{code: wrkfCodeTransitionBlocked, msg: msg}
}

func roleDeniedError(instanceID, transition, role string) error {
	return &wrkfError{
		code: wrkfCodeRoleDenied,
		msg:  fmt.Sprintf("role %s is not allowed for transition %s", role, transition),
	}
}

func idempotencyMismatchError(key string) error {
	return &wrkfError{
		code: wrkfCodeIdempotencyMismatch,
		msg:  fmt.Sprintf("idempotency key %q was reused with different params", key),
	}
}
