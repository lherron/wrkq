package wrkfapi

import (
	"fmt"

	"github.com/lherron/wrkq/internal/workflow"
)

const (
	CodeNotFound             = "WRKF_NOT_FOUND"
	CodeValidation           = "WRKF_VALIDATION"
	CodeStaleRevision        = "WRKF_STALE_REVISION"
	CodeTransitionBlocked    = "WRKF_TRANSITION_BLOCKED"
	CodeRoleDenied           = "WRKF_ROLE_DENIED"
	CodeIdempotencyMismatch  = "WRKF_IDEMPOTENCY_MISMATCH"
	CodeLeaseConflict        = "WRKF_LEASE_CONFLICT"
	CodeEffectNotDeliverable = "WRKF_EFFECT_NOT_DELIVERABLE"
	CodeEffectDeliveryFailed = "WRKF_EFFECT_DELIVERY_FAILED"
	CodeHookFailed           = "WRKF_HOOK_FAILED"
	CodeDBMigrationRequired  = "WRKF_DB_MIGRATION_REQUIRED"
	CodeKindRoleDenied       = "WRKF_KIND_ROLE_DENIED"
	CodeLinkageUnresolved    = "WRKF_LINKAGE_UNRESOLVED"
	CodeLinkageStale         = "WRKF_LINKAGE_STALE"
	CodeInternal             = "WRKF_INTERNAL"
)

type Error interface {
	error
	Code() string
	Retryable() bool
}

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

func NewValidationError(msg string, data any) *DomainError {
	if msg == "" {
		msg = "validation failed"
	}
	return newError(CodeValidation, msg, false, data, nil)
}

func NewStaleRevisionError(instanceID string, expected, actual int64) *DomainError {
	return newError(CodeStaleRevision, "workflow revision mismatch", true, struct {
		InstanceID       string `json:"instanceId,omitempty"`
		ExpectedRevision int64  `json:"expectedRevision"`
		ActualRevision   int64  `json:"actualRevision"`
	}{InstanceID: instanceID, ExpectedRevision: expected, ActualRevision: actual}, nil)
}

func NewTransitionBlockedError(instanceID, transition string, blocksOn []Blocker) *DomainError {
	return newError(CodeTransitionBlocked, "transition is blocked", false, struct {
		InstanceID string    `json:"instanceId,omitempty"`
		Transition string    `json:"transition,omitempty"`
		BlocksOn   []Blocker `json:"blocksOn,omitempty"`
	}{InstanceID: instanceID, Transition: transition, BlocksOn: blocksOn}, nil)
}

func NewRoleDeniedError(instanceID, transition, role string) *DomainError {
	return newError(CodeRoleDenied, "role is not permitted for transition", false, struct {
		InstanceID string `json:"instanceId,omitempty"`
		Transition string `json:"transition,omitempty"`
		Role       string `json:"role,omitempty"`
	}{InstanceID: instanceID, Transition: transition, Role: role}, nil)
}

func NewIdempotencyMismatchError(key string) *DomainError {
	return newError(CodeIdempotencyMismatch, "idempotency key was reused with different params", false, struct {
		Key string `json:"key,omitempty"`
	}{Key: key}, nil)
}

func NewLeaseConflictError(effectID, token string) *DomainError {
	return newError(CodeLeaseConflict, "effect lease conflict", true, struct {
		EffectID string `json:"effectId,omitempty"`
		Token    string `json:"token,omitempty"`
	}{EffectID: effectID, Token: token}, nil)
}

func NewEffectNotDeliverableError(effectID, status string) *DomainError {
	return newError(CodeEffectNotDeliverable, "effect is not deliverable", false, struct {
		EffectID string `json:"effectId,omitempty"`
		Status   string `json:"status,omitempty"`
	}{EffectID: effectID, Status: status}, nil)
}

func NewHookFailedError(hookID, msg string, retryable bool) *DomainError {
	if msg == "" {
		msg = "hook failed"
	}
	return newError(CodeHookFailed, msg, retryable, struct {
		HookID string `json:"hookId,omitempty"`
	}{HookID: hookID}, nil)
}

func NewDBMigrationRequiredError(have, want int) *DomainError {
	return newError(CodeDBMigrationRequired, "database migration required", false, struct {
		Have int `json:"have"`
		Want int `json:"want"`
	}{Have: have, Want: want}, nil)
}

// NewDetailError wraps a structured workflow.ErrorDetail as a DomainError,
// carrying the detail (field/expected/allowed/fix) as data so the RPC envelope
// and CLI --json envelope expose the same parseable shape.
func NewDetailError(detail workflow.ErrorDetail, retryable bool) *DomainError {
	code := detail.Code
	if code == "" {
		code = CodeValidation
	}
	return newError(code, detail.Message, retryable, detail, nil)
}

func NewInternalError(err error) *DomainError {
	msg := "internal error"
	if err != nil {
		msg = err.Error()
	}
	return newError(CodeInternal, msg, false, nil, err)
}

func newError(code, msg string, retryable bool, data any, err error) *DomainError {
	return &DomainError{code: code, message: msg, retryable: retryable, data: data, err: err}
}

var _ Error = (*DomainError)(nil)

type Blocker = workflow.Blocker
