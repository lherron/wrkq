package workflow

import (
	"errors"
	"fmt"
	"strings"
)

const (
	wrkfCodeStaleRevision        = "WRKF_STALE_REVISION"
	wrkfCodeTransitionBlocked    = "WRKF_TRANSITION_BLOCKED"
	wrkfCodeRoleDenied           = "WRKF_ROLE_DENIED"
	wrkfCodeIdempotencyMismatch  = "WRKF_IDEMPOTENCY_MISMATCH"
	wrkfCodeLeaseConflict        = "WRKF_LEASE_CONFLICT"
	wrkfCodeNotDeliverable       = "WRKF_EFFECT_NOT_DELIVERABLE"
	wrkfCodeEffectDeliveryFailed = "WRKF_EFFECT_DELIVERY_FAILED"
	wrkfCodeValidation           = "WRKF_VALIDATION"
	wrkfCodeKindRoleDenied       = "WRKF_KIND_ROLE_DENIED"
	wrkfCodeLinkageUnresolved    = "WRKF_LINKAGE_UNRESOLVED"
	wrkfCodeLinkageStale         = "WRKF_LINKAGE_STALE"
	wrkfCodeSuspended            = "WRKF_SUSPENDED"
	wrkfCodeAlreadySuspended     = "WRKF_ALREADY_SUSPENDED"
	wrkfCodeSuspensionNotFound   = "WRKF_SUSPENSION_NOT_FOUND"
	wrkfCodeActiveRunGuard       = "WRKF_ACTIVE_RUN"
)

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

func claimRefusedError(predecessor *ActionClaimPredecessor) error {
	return &wrkfError{
		code:        wrkfCodeLeaseConflict,
		msg:         fmt.Sprintf("claim refused: priorRun must name predecessor %s", predecessor.RunID),
		field:       "priorRun",
		expected:    predecessor.RunID,
		fix:         fmt.Sprintf("review predecessor and retry with priorRun %s", predecessor.RunID),
		predecessor: predecessor,
	}
}

func supersededSettleError(runID, successorRunID string) error {
	return &wrkfError{
		code:     wrkfCodeLeaseConflict,
		msg:      fmt.Sprintf("late settle refused: action run %s was superseded by %s", runID, successorRunID),
		field:    "runId",
		expected: "current unsuperseded run",
	}
}

// DetailedError is implemented by wrkf errors that carry an ErrorDetail.
type DetailedError interface {
	error
	Code() string
	Detail() ErrorDetail
}

// AsErrorDetail extracts a structured ErrorDetail from err when present.
func AsErrorDetail(err error) (ErrorDetail, bool) {
	var de DetailedError
	if errors.As(err, &de) {
		return de.Detail(), true
	}
	return ErrorDetail{}, false
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

func (e *wrkfError) Detail() ErrorDetail {
	if e == nil {
		return ErrorDetail{}
	}
	return ErrorDetail{Code: e.code, Field: e.field, Message: e.msg, Expected: e.expected, Allowed: e.allowed, Fix: e.fix, Suspension: e.suspension, Predecessor: e.predecessor}
}

// validationError builds a structured WRKF_VALIDATION error. The message is
// the human-readable text; field/expected/allowed/fix feed the --json envelope
// and RPC data so an agent can self-correct deterministically.
func validationError(field, msg, expected string, allowed []string, fix string) error {
	full := msg
	if fix != "" {
		full = msg + "; " + fix
	}
	return &wrkfError{code: wrkfCodeValidation, msg: full, field: field, expected: expected, allowed: allowed, fix: fix}
}

// kindRoleDeniedError reports that the supplied role is not declared as a
// producer of the evidence kind. This is supplied-role conformance, not an
// authenticated-actor authorization boundary.
func kindRoleDeniedError(kind, role string, allowed []string) error {
	roleLabel := role
	if strings.TrimSpace(role) == "" {
		roleLabel = "(empty)"
	}
	msg := fmt.Sprintf("evidence kind %s is not producible by role %s", kind, roleLabel)
	fix := fmt.Sprintf("add evidence with --role one of [%s]", strings.Join(allowed, "|"))
	return &wrkfError{code: wrkfCodeKindRoleDenied, msg: msg + "; " + fix, field: "role", expected: "one of declared producers", allowed: allowed, fix: fix}
}

// linkageUnresolvedError reports that a declared data linkage reference did not
// resolve to a live evidence id on the same workflow instance.
func linkageUnresolvedError(field, ref, resolvesToKind, detail string) error {
	var msg string
	switch {
	case ref == "":
		msg = fmt.Sprintf("data%s is required but missing", field)
	case detail != "":
		msg = fmt.Sprintf("data%s references %q which %s", field, ref, detail)
	default:
		msg = fmt.Sprintf("data%s references %q which does not resolve to a live evidence id on this instance", field, ref)
	}
	expected := "live evidence id on this instance"
	if resolvesToKind != "" {
		expected = fmt.Sprintf("live evidence id of kind %s on this instance", resolvesToKind)
	}
	fix := fmt.Sprintf("set data%s to an existing evidence id (see `wrkf evidence list`)", field)
	return &wrkfError{code: wrkfCodeLinkageUnresolved, msg: msg + "; " + fix, field: "data" + field, expected: expected, fix: fix}
}

// linkageStaleError reports that a declared linkage ref points at a superseded
// (non-latest) evidence of the expected kind.
func linkageStaleError(field, ref, resolvesToKind, latestID string) error {
	msg := fmt.Sprintf("data%s references %q but the latest %s on this instance is %s", field, ref, resolvesToKind, latestID)
	fix := fmt.Sprintf("set data%s to %s (the latest %s)", field, latestID, resolvesToKind)
	expected := fmt.Sprintf("latest %s id (%s)", resolvesToKind, latestID)
	return &wrkfError{code: wrkfCodeLinkageStale, msg: msg + "; " + fix, field: "data" + field, expected: expected, fix: fix}
}

// suspendedWriteError is the suspended-write gate rejection. It bounces any
// write to a suspended instance at all three doors: ordinary transitions,
// action settlement, and action claims.
func suspendedWriteError(inst *Instance) error {
	sus := inst.Suspension
	return &wrkfError{
		code:       wrkfCodeSuspended,
		msg:        fmt.Sprintf("instance %s is suspended (%s %s); writes are rejected until the suspension is resolved", inst.ID, sus.ID, sus.Reason),
		field:      "suspension",
		expected:   "running instance (no active suspension)",
		fix:        fmt.Sprintf("resolve the active suspension with `wrkf suspension resolve %s --disposition cancel` before writing to this instance", sus.ID),
		suspension: sus,
	}
}

// noActiveSuspensionError is the id-gate rejection for resolveSuspension: the
// presented suspension id names no active suspension (wrong id, or already
// resolved). The matching suspension id is the only gate, so this is the only
// way resolution is refused.
func noActiveSuspensionError(suspensionID string) error {
	return &wrkfError{
		code:     wrkfCodeSuspensionNotFound,
		msg:      fmt.Sprintf("no active suspension matches id %s", suspensionID),
		field:    "suspensionId",
		expected: "the id of the instance's active suspension",
		fix:      "inspect the instance for its current active suspension id and retry",
	}
}

// alreadySuspendedError rejects a second suspension: exactly one is active at a
// time, there is no stack.
func alreadySuspendedError(inst *Instance) error {
	sus := inst.Suspension
	return &wrkfError{
		code:     wrkfCodeAlreadySuspended,
		msg:      fmt.Sprintf("instance %s already has an active suspension (%s %s)", inst.ID, sus.ID, sus.Reason),
		field:    "suspension",
		expected: "running instance (no active suspension)",
		fix:      "resolve the active suspension before opening a new one",
	}
}

func staleRevisionError(instanceID string, expected, actual int64) error {
	return &wrkfError{
		code: wrkfCodeStaleRevision,
		msg:  fmt.Sprintf("workflow revision mismatch: instance %s expected %d, got %d", instanceID, expected, actual),
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

func leaseConflictError(effectID, token string) error {
	return &wrkfError{
		code: wrkfCodeLeaseConflict,
		msg:  fmt.Sprintf("effect lease conflict: effect %s token %s", effectID, token),
	}
}

func actionLeaseConflictError(actionRunID string) error {
	return &wrkfError{
		code: wrkfCodeLeaseConflict,
		msg:  fmt.Sprintf("action lease conflict: action run %s", actionRunID),
	}
}

// activeRunGuardError refuses an operator-class transition (requiresNoActiveRun)
// while the instance holds an open action run. The action run is the lease: the
// current seat must settle, or an expired claim must be explicitly succeeded
// and its successor settled, before terminalizing/moving the instance.
func activeRunGuardError(instanceID, transitionID string, runIDs []string) error {
	return &wrkfError{
		code:     wrkfCodeActiveRunGuard,
		msg:      fmt.Sprintf("transition %s refused: instance %s has an active action run (%s); settle the current seat or settle an acknowledged successor first", transitionID, instanceID, strings.Join(runIDs, ",")),
		field:    "transition",
		expected: "no active action run on the instance",
		fix:      "settle the active run with `wrkf action settle`; if its lease expired, inspect it, claim a successor with `wrkf action claim --prior-run <run-id>`, then settle the successor before resolving",
	}
}

func effectNotDeliverableError(effectID, status string) error {
	return &wrkfError{
		code: wrkfCodeNotDeliverable,
		msg:  fmt.Sprintf("effect %s is not deliverable from status %s", effectID, status),
	}
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

func (e *transitionEffectDeliveryError) Error() string {
	if e == nil {
		return ""
	}
	msg := fmt.Sprintf("transition %s committed as event %s but builtin effect %s (%s) delivery failed", e.transitionID, e.eventID, e.effectID, e.kind)
	if e.status != "" {
		msg += fmt.Sprintf(" with status %s", e.status)
	}
	if e.err != nil {
		msg += ": " + e.err.Error()
	}
	return msg
}

func (e *transitionEffectDeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *transitionEffectDeliveryError) Code() string {
	return wrkfCodeEffectDeliveryFailed
}

func (e *transitionEffectDeliveryError) Detail() ErrorDetail {
	if e == nil {
		return ErrorDetail{}
	}
	return ErrorDetail{
		Code:    wrkfCodeEffectDeliveryFailed,
		Field:   "effects",
		Message: e.Error(),
		Fix:     "inspect the named effect and retry delivery after resolving the failure",
	}
}

func (e *transitionEffectDeliveryError) PartialResult() map[string]interface{} {
	if e == nil {
		return nil
	}
	return e.result
}
