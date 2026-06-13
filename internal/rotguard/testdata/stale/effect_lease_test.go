// Package workflow_test is a rotguard fixture representing a stale red-phase test file.
// It contains two distinct comment groups carrying hard-fail TDD markers — the kind
// left behind after the symbols were implemented but comments were never cleaned up.
// With TestsGreen=true, both groups must be flagged by rotguard.Check.
package workflow_test

import "testing"

// RED TODAY: RotateLease does not exist → compile failure
func TestRotateLease_StillRed(t *testing.T) {
	t.Skip()
}

// compile failure: effectApply was not yet exported when this test was written
func TestEffectApply_CompileFailure(t *testing.T) {
	t.Skip()
}
