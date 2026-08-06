//go:build wrkq_local

package workrpc

import (
	"encoding/json"
	"fmt"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"

	"github.com/lherron/wrkq/internal/wrkqapi"
)

func decodeData(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode data: %v", err)
		}
	}
	return m
}

// RPC error-contract test (T-05066 AC#3): a SQLite busy/locked contention
// error must never surface as WORKRPC_INTERNAL. It maps to the typed, retryable
// WRKQ_DB_BUSY with reason="sqlite_busy", regardless of how it is wrapped.
func TestMapError_SQLiteBusy(t *testing.T) {
	busy := sqlite3.Error{Code: sqlite3.ErrBusy}

	cases := []struct {
		name string
		err  error
	}{
		{"raw busy", busy},
		{"wrapped busy", fmt.Errorf("failed to update task: %w", busy)},
		{"locked", sqlite3.Error{Code: sqlite3.ErrLocked}},
		{"wrkqapi internal wrapping busy", wrkqapi.NewInternalError(fmt.Errorf("failed to update task: %w", busy))},
		{"wrkqapi busy error", wrkqapi.NewBusyError(busy)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := MapError(tc.err)
			if re == nil {
				t.Fatal("expected an RPC error")
			}
			data := decodeData(t, re.Data)
			if got := data["code"]; got != CodeWRKQDBBusy {
				t.Errorf("code = %v, want %s", got, CodeWRKQDBBusy)
			}
			if got := data["retryable"]; got != true {
				t.Errorf("retryable = %v, want true", got)
			}
			if got := data["reason"]; got != "sqlite_busy" {
				t.Errorf("reason = %v, want sqlite_busy", got)
			}
			if re.Code != domainRPCCode[CodeWRKQDBBusy] {
				t.Errorf("rpcCode = %d, want %d", re.Code, domainRPCCode[CodeWRKQDBBusy])
			}
			if re.Message == "internal error" {
				t.Error("busy contention must not surface as bare \"internal error\"")
			}
		})
	}
}

// A genuine non-busy internal error must still map to WORKRPC_INTERNAL.
func TestMapError_InternalUnchanged(t *testing.T) {
	re := MapError(fmt.Errorf("some unexpected failure"))
	if re == nil {
		t.Fatal("expected an RPC error")
	}
	data := decodeData(t, re.Data)
	if got := data["code"]; got != CodeWorkRPCInternal {
		t.Errorf("code = %v, want %s", got, CodeWorkRPCInternal)
	}
}