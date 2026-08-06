//go:build wrkq_local

package wrkfapi

import (
	"context"
	"errors"
	"testing"
)

func TestTransitionApplyRejectsInlineCheckExecution(t *testing.T) {
	_, err := New(nil).TransitionApply(context.Background(), TransitionApplyParams{RunChecks: true})
	if err == nil {
		t.Fatal("expected runChecks refusal")
	}
	var domainErr Error
	if !errors.As(err, &domainErr) || domainErr.Code() != CodeValidation {
		t.Fatalf("error = %#v", err)
	}
}