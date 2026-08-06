//go:build wrkq_local

package wrkfapi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

// TestMissingTask_TypedError verifies that looking up a non-existent task/instance
// returns a typed wrkfapi error satisfying the §3 contract:
//   - Code() == "WRKF_NOT_FOUND"
//   - Retryable() == false
func TestMissingTask_TypedError(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	_, err := api.TaskInspect(ctx, "T-99999")
	if err == nil {
		t.Fatal("expected error for non-existent task, got nil")
	}

	var wrkfErr wrkfapi.Error
	if !errors.As(err, &wrkfErr) {
		t.Fatalf("expected wrkfapi.Error, got %T: %v", err, err)
	}

	if got := wrkfErr.Code(); got != "WRKF_NOT_FOUND" {
		t.Errorf("Code() = %q, want %q", got, "WRKF_NOT_FOUND")
	}
	if wrkfErr.Retryable() {
		t.Error("Retryable() = true for WRKF_NOT_FOUND; want false")
	}
}

// TestMissingTask_Timeline_TypedError verifies Timeline also returns WRKF_NOT_FOUND
// for a non-existent task.
func TestMissingTask_Timeline_TypedError(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	_, err := api.Timeline(ctx, "T-99999")
	if err == nil {
		t.Fatal("expected error for non-existent task, got nil")
	}

	var wrkfErr wrkfapi.Error
	if !errors.As(err, &wrkfErr) {
		t.Fatalf("expected wrkfapi.Error, got %T: %v", err, err)
	}

	if got := wrkfErr.Code(); got != "WRKF_NOT_FOUND" {
		t.Errorf("Code() = %q, want %q", got, "WRKF_NOT_FOUND")
	}
	if wrkfErr.Retryable() {
		t.Error("Retryable() = true for WRKF_NOT_FOUND; want false")
	}
}

// TestErrorInterface_CompileShape ensures the wrkfapi.Error interface has exactly
// the two methods required by §3: Code() string and Retryable() bool.
func TestErrorInterface_CompileShape(t *testing.T) {
	// Compile-time check: wrkfapi.Error must satisfy the interface below.
	// If wrkfapi.Error doesn't expose Code() / Retryable(), this won't compile.
	type domainError interface {
		error
		Code() string
		Retryable() bool
	}
	var _ domainError = (wrkfapi.Error)(nil)
}