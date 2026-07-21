package workrpc

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

var errorCodeTable = []struct {
	name      string
	err       error
	rpcCode   int
	dataCode  string
	retryable bool
}{
	{"WRKF_STALE_REVISION", wrkfapi.NewStaleRevisionError("wfi_abc123", 3, 4), -32009, wrkfapi.CodeStaleRevision, true},
	{"WRKF_NOT_FOUND", wrkfapi.NewNotFoundError("T-99999", "task"), -32004, wrkfapi.CodeNotFound, false},
	{"WRKF_VALIDATION", wrkfapi.NewValidationError("invalid protocolVersion", nil), -32602, wrkfapi.CodeValidation, false},
	{"WRKF_TRANSITION_BLOCKED", wrkfapi.NewTransitionBlockedError("wfi_abc123", "plan_ready", nil), -32011, wrkfapi.CodeTransitionBlocked, false},
	{"WRKF_ROLE_DENIED", wrkfapi.NewRoleDeniedError("wfi_abc123", "plan_ready", "observer"), -32012, wrkfapi.CodeRoleDenied, false},
	{"WRKF_IDEMPOTENCY_MISMATCH", wrkfapi.NewIdempotencyMismatchError("idem_key_1"), -32013, wrkfapi.CodeIdempotencyMismatch, false},
	{"WRKF_LEASE_CONFLICT", wrkfapi.NewLeaseConflictError("eff_xyz", "wrong-token"), -32014, wrkfapi.CodeLeaseConflict, true},
	{"WRKF_EFFECT_NOT_DELIVERABLE", wrkfapi.NewEffectNotDeliverableError("eff_xyz", "cancelled"), -32015, wrkfapi.CodeEffectNotDeliverable, false},
	{"WRKF_HOOK_FAILED", wrkfapi.NewHookFailedError("hook_1", "failed", true), -32016, wrkfapi.CodeHookFailed, true},
	{"WRKF_INTERNAL", wrkfapi.NewInternalError(errors.New("sensitive detail")), -32603, wrkfapi.CodeInternal, false},
	{"WRKQ_ALREADY_CLAIMED", wrkqapi.NewAlreadyClaimedError(nil), -32027, wrkqapi.CodeAlreadyClaimed, false},
	{"WRKQ_WRONG_STATE", wrkqapi.NewWrongStateError(nil), -32028, wrkqapi.CodeWrongState, false},
	{"WRKQ_CLAIM_SUPERSEDED", wrkqapi.NewClaimSupersededError(nil), -32029, wrkqapi.CodeClaimSuperseded, false},
	{"WRKQ_NODE_IDENTITY_REQUIRED", wrkqapi.NewNodeIdentityError(), -32030, wrkqapi.CodeNodeIdentity, false},
}

func TestErrorMapper_StaleRevision(t *testing.T) {
	rpcErr := MapError(wrkfapi.NewStaleRevisionError("wfi_abc123", 3, 4))
	if rpcErr == nil || rpcErr.Code != -32009 {
		t.Fatalf("MapError stale revision = %+v", rpcErr)
	}
	var data struct {
		Code             string `json:"code"`
		InstanceID       string `json:"instanceId"`
		ExpectedRevision int64  `json:"expectedRevision"`
		ActualRevision   int64  `json:"actualRevision"`
		Retryable        *bool  `json:"retryable"`
	}
	if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.Code != wrkfapi.CodeStaleRevision || data.Retryable == nil || !*data.Retryable || data.InstanceID != "wfi_abc123" || data.ExpectedRevision != 3 || data.ActualRevision != 4 {
		t.Fatalf("unexpected stale revision data: %+v", data)
	}
}

func TestErrorMapper_Table(t *testing.T) {
	for _, tc := range errorCodeTable {
		t.Run(tc.name, func(t *testing.T) {
			rpcErr := MapError(tc.err)
			if rpcErr == nil || rpcErr.Code != tc.rpcCode {
				t.Fatalf("MapError = %+v, want numeric code %d", rpcErr, tc.rpcCode)
			}
			var data struct {
				Code      string `json:"code"`
				Retryable *bool  `json:"retryable"`
			}
			if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			if data.Code != tc.dataCode || data.Retryable == nil || *data.Retryable != tc.retryable {
				t.Fatalf("error data = %+v, want code=%q retryable=%v", data, tc.dataCode, tc.retryable)
			}
		})
	}
}

func TestErrorMapper_NonDomainError(t *testing.T) {
	raw := errors.New("sensitive detail that must not reach the wire")
	rpcErr := MapError(raw)
	if rpcErr == nil || rpcErr.Code != -32603 || rpcErr.Message == raw.Error() {
		t.Fatalf("plain error mapping leaked or used wrong code: %+v", rpcErr)
	}
	var data struct {
		Code      string `json:"code"`
		Retryable *bool  `json:"retryable"`
	}
	if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if data.Code != CodeWorkRPCInternal || data.Retryable == nil || *data.Retryable {
		t.Fatalf("unexpected internal error data: %+v", data)
	}
}

func TestErrorMapper_WrkfApiInterface(t *testing.T) {
	for _, tc := range errorCodeTable {
		var domainErr wrkfapi.Error
		if !errors.As(tc.err, &domainErr) {
			t.Fatalf("%s does not implement wrkfapi.Error", tc.name)
		}
		if domainErr.Code() != tc.dataCode || domainErr.Retryable() != tc.retryable {
			t.Fatalf("%s interface values differ: code=%q retryable=%v", tc.name, domainErr.Code(), domainErr.Retryable())
		}
	}
}
