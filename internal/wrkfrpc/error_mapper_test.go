package wrkfrpc

// error_mapper_test.go — typed wrkfapi errors → JSON-RPC error objects (§3 contract).
//
// Imports "github.com/lherron/wrkq/internal/wrkfapi" (no non-test Go files there yet)
// and references MapError (undefined in wrkfrpc) → compile failure = TDD red state.
//
// When green: every WRKF_* domain error carries data.code (stable string) and
// data.retryable (always a boolean, never absent).  No fmt.Errorf string may
// cross the protocol boundary.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

// errorCodeTable drives the full §3 mapping table so any new WRKF_* error added
// to wrkfapi must also be wired in the mapper (add a row here).
var errorCodeTable = []struct {
	name      string
	err       error       // typed wrkfapi error
	rpcCode   int         // expected JSON-RPC numeric code
	dataCode  string      // expected data.code
	retryable bool        // expected data.retryable (always a boolean)
}{
	{
		name:      "WRKF_STALE_REVISION",
		err:       wrkfapi.NewStaleRevisionError("wfi_abc123", 3, 4),
		rpcCode:   -32009,
		dataCode:  "WRKF_STALE_REVISION",
		retryable: true,
	},
	{
		name:      "WRKF_NOT_FOUND",
		err:       wrkfapi.NewNotFoundError("T-99999", "task"),
		rpcCode:   -32004,
		dataCode:  "WRKF_NOT_FOUND",
		retryable: false,
	},
	{
		name:      "WRKF_VALIDATION",
		err:       wrkfapi.NewValidationError("invalid protocolVersion", nil),
		rpcCode:   -32602,
		dataCode:  "WRKF_VALIDATION",
		retryable: false,
	},
	{
		name:      "WRKF_TRANSITION_BLOCKED",
		err:       wrkfapi.NewTransitionBlockedError("wfi_abc123", "plan_ready", nil),
		rpcCode:   -32011,
		dataCode:  "WRKF_TRANSITION_BLOCKED",
		retryable: false,
	},
	{
		name:      "WRKF_ROLE_DENIED",
		err:       wrkfapi.NewRoleDeniedError("wfi_abc123", "plan_ready", "observer"),
		rpcCode:   -32012,
		dataCode:  "WRKF_ROLE_DENIED",
		retryable: false,
	},
	{
		name:      "WRKF_IDEMPOTENCY_MISMATCH",
		err:       wrkfapi.NewIdempotencyMismatchError("idem_key_1"),
		rpcCode:   -32013,
		dataCode:  "WRKF_IDEMPOTENCY_MISMATCH",
		retryable: false,
	},
	{
		name:      "WRKF_LEASE_CONFLICT",
		err:       wrkfapi.NewLeaseConflictError("eff_xyz", "wrong-token"),
		rpcCode:   -32014,
		dataCode:  "WRKF_LEASE_CONFLICT",
		retryable: true,
	},
	{
		name:      "WRKF_EFFECT_NOT_DELIVERABLE",
		err:       wrkfapi.NewEffectNotDeliverableError("eff_xyz", "cancelled"),
		rpcCode:   -32015,
		dataCode:  "WRKF_EFFECT_NOT_DELIVERABLE",
		retryable: false,
	},
	{
		name:      "WRKF_INTERNAL",
		err:       wrkfapi.NewInternalError(errors.New("unexpected nil pointer")),
		rpcCode:   -32603,
		dataCode:  "WRKF_INTERNAL",
		retryable: false,
	},
}

// TestErrorMapper_StaleRevision is the primary acceptance test for the error code
// the contract calls out explicitly in §3:
//
//	{ "code": -32009, "data": { "code": "WRKF_STALE_REVISION", "retryable": true } }
func TestErrorMapper_StaleRevision(t *testing.T) {
	err := wrkfapi.NewStaleRevisionError("wfi_abc123", 3, 4)

	rpcErr := MapError(err)
	if rpcErr == nil {
		t.Fatal("MapError returned nil for wrkfapi.StaleRevisionError")
	}
	if rpcErr.Code != -32009 {
		t.Errorf("RPC code = %d, want -32009", rpcErr.Code)
	}

	var data struct {
		Code             string `json:"code"`
		InstanceID       string `json:"instanceId"`
		ExpectedRevision int64  `json:"expectedRevision"`
		ActualRevision   int64  `json:"actualRevision"`
		Retryable        *bool  `json:"retryable"` // pointer to detect absence
	}
	if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
		t.Fatalf("error.data is not valid JSON: %v — raw: %s", err, rpcErr.Data)
	}
	if data.Code != "WRKF_STALE_REVISION" {
		t.Errorf("data.code = %q, want \"WRKF_STALE_REVISION\"", data.Code)
	}
	if data.Retryable == nil {
		t.Error("data.retryable is absent; contract §3 requires it to always be a boolean")
	} else if !*data.Retryable {
		t.Errorf("data.retryable = false, want true for WRKF_STALE_REVISION")
	}
	if data.InstanceID != "wfi_abc123" {
		t.Errorf("data.instanceId = %q, want \"wfi_abc123\"", data.InstanceID)
	}
	if data.ExpectedRevision != 3 {
		t.Errorf("data.expectedRevision = %d, want 3", data.ExpectedRevision)
	}
	if data.ActualRevision != 4 {
		t.Errorf("data.actualRevision = %d, want 4", data.ActualRevision)
	}
}

// TestErrorMapper_Table drives the complete §3 error code table.
func TestErrorMapper_Table(t *testing.T) {
	for _, tc := range errorCodeTable {
		t.Run(tc.name, func(t *testing.T) {
			rpcErr := MapError(tc.err)
			if rpcErr == nil {
				t.Fatalf("MapError returned nil for %s", tc.name)
			}
			if rpcErr.Code != tc.rpcCode {
				t.Errorf("code = %d, want %d", rpcErr.Code, tc.rpcCode)
			}

			// data.code and data.retryable are required on every WRKF_* error.
			var data struct {
				Code      string `json:"code"`
				Retryable *bool  `json:"retryable"`
			}
			if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
				t.Fatalf("data is not valid JSON: %v — raw: %s", err, rpcErr.Data)
			}
			if data.Code != tc.dataCode {
				t.Errorf("data.code = %q, want %q", data.Code, tc.dataCode)
			}
			if data.Retryable == nil {
				t.Error("data.retryable absent; must always be a boolean (§3)")
			} else if *data.Retryable != tc.retryable {
				t.Errorf("data.retryable = %v, want %v", *data.Retryable, tc.retryable)
			}
		})
	}
}

// TestErrorMapper_NonDomainError verifies that a plain Go error (not wrkfapi.Error)
// maps to WRKF_INTERNAL (-32603) and does NOT let the raw error string cross the
// protocol boundary.  Contract: "No fmt.Errorf strings reach the wire."
func TestErrorMapper_NonDomainError(t *testing.T) {
	raw := errors.New("internal: some sensitive detail that must not reach the wire")

	rpcErr := MapError(raw)
	if rpcErr == nil {
		t.Fatal("MapError returned nil for plain error")
	}
	if rpcErr.Code != -32603 {
		t.Errorf("code = %d, want -32603 (WRKF_INTERNAL)", rpcErr.Code)
	}

	// The raw error message must NOT be directly in the protocol message.
	if rpcErr.Message == raw.Error() {
		t.Error("raw error string leaked into protocol message; must be sanitised")
	}

	var data struct {
		Code      string `json:"code"`
		Retryable *bool  `json:"retryable"`
	}
	if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
		t.Fatalf("data is not valid JSON: %v", err)
	}
	if data.Code != "WRKF_INTERNAL" {
		t.Errorf("data.code = %q, want \"WRKF_INTERNAL\"", data.Code)
	}
	if data.Retryable == nil {
		t.Error("data.retryable absent for WRKF_INTERNAL; must always be a boolean")
	} else if *data.Retryable {
		t.Error("data.retryable = true for WRKF_INTERNAL; must be false")
	}
}

// TestErrorMapper_WrkfApiInterface ensures that all errors produced by wrkfapi
// constructors satisfy the wrkfapi.Error interface checked by errors.As.
func TestErrorMapper_WrkfApiInterface(t *testing.T) {
	for _, tc := range errorCodeTable {
		t.Run(tc.name, func(t *testing.T) {
			var wrkfErr wrkfapi.Error
			if !errors.As(tc.err, &wrkfErr) {
				t.Fatalf("errors.As(err, &wrkfapi.Error) = false for %s; typed errors must implement wrkfapi.Error", tc.name)
			}
			if wrkfErr.Code() != tc.dataCode {
				t.Errorf("Code() = %q, want %q", wrkfErr.Code(), tc.dataCode)
			}
			if wrkfErr.Retryable() != tc.retryable {
				t.Errorf("Retryable() = %v, want %v", wrkfErr.Retryable(), tc.retryable)
			}
		})
	}
}
