// Package workflow_test is a rotguard fixture for the ARCH-EXCEPTION escape hatch.
// The comment group below carries both a hard-fail marker ("RED TODAY" / "compile failure")
// AND a well-formed ARCH-EXCEPTION token. The guard must NOT produce a violation for this
// group; instead, it must increment ExceptionsByRule["stale-tdd-comment"] to 1.
package workflow_test

import "testing"

// RED TODAY: RotateLease does not exist → compile failure
// ARCH-EXCEPTION(T-04368): retained during cross-module migration; to be removed in follow-up
func TestRotateLease_Exempted(t *testing.T) {
	t.Skip()
}
