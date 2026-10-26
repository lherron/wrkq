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
	wantData  map[string]any // detail fields error.data must also carry
}{
	{"WRKF_STALE_REVISION", wrkfapi.NewStaleRevisionError("wfi_abc123", 3, 4), -32009, wrkfapi.CodeStaleRevision, true,
		map[string]any{"instanceId": "wfi_abc123", "expectedRevision": float64(3), "actualRevision": float64(4)}},
	{"WRKF_NOT_FOUND", wrkfapi.NewNotFoundError("T-99999", "task"), -32004, wrkfapi.CodeNotFound, false, nil},
	{"WRKF_VALIDATION", wrkfapi.NewValidationError("invalid protocolVersion", nil), -32602, wrkfapi.CodeValidation, false, nil},
	{"WRKF_TRANSITION_BLOCKED", wrkfapi.NewTransitionBlockedError("wfi_abc123", "plan_ready", nil), -32011, wrkfapi.CodeTransitionBlocked, false, nil},
	{"WRKF_ROLE_DENIED", wrkfapi.NewRoleDeniedError("wfi_abc123", "plan_ready", "observer"), -32012, wrkfapi.CodeRoleDenied, false, nil},
	{"WRKF_IDEMPOTENCY_MISMATCH", wrkfapi.NewIdempotencyMismatchError("idem_key_1"), -32013, wrkfapi.CodeIdempotencyMismatch, false, nil},
	{"WRKF_LEASE_CONFLICT", wrkfapi.NewLeaseConflictError("eff_xyz", "wrong-token"), -32014, wrkfapi.CodeLeaseConflict, true, nil},
	{"WRKF_EFFECT_NOT_DELIVERABLE", wrkfapi.NewEffectNotDeliverableError("eff_xyz", "cancelled"), -32015, wrkfapi.CodeEffectNotDeliverable, false, nil},
	{"WRKF_HOOK_FAILED", wrkfapi.NewHookFailedError("hook_1", "failed", true), -32016, wrkfapi.CodeHookFailed, true, nil},
	{"WRKF_INTERNAL", wrkfapi.NewInternalError(errors.New("sensitive detail")), -32603, wrkfapi.CodeInternal, false, nil},
	{"WRKQ_ALREADY_CLAIMED", wrkqapi.NewAlreadyClaimedError(nil), -32027, wrkqapi.CodeAlreadyClaimed, false, nil},
	{"WRKQ_WRONG_STATE", wrkqapi.NewWrongStateError(nil), -32028, wrkqapi.CodeWrongState, false, nil},
	{"WRKQ_CLAIM_SUPERSEDED", wrkqapi.NewClaimSupersededError(nil), -32029, wrkqapi.CodeClaimSuperseded, false, nil},
	{"WRKQ_NODE_IDENTITY_REQUIRED", wrkqapi.NewNodeIdentityError(), -32030, wrkqapi.CodeNodeIdentity, false, nil},
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
			var detail map[string]any
			if err := json.Unmarshal(rpcErr.Data, &detail); err != nil {
				t.Fatalf("decode data detail: %v", err)
			}
			for k, want := range tc.wantData {
				if got := detail[k]; got != want {
					t.Errorf("error data %s = %#v, want %#v", k, got, want)
				}
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
