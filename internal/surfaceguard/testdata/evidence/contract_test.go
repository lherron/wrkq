// Fixture: a *_test.go file containing "wrkf.test.uncovered" as a Go string literal.
// CollectEvidence must detect this and include it in the evidence set.
// Note: the method also appears in a comment below — that must NOT count as evidence.
// Only the string literal on the rpc() call line counts.
package fixture_test

import "testing"

// TestUncoveredRPC exercises the wrkf.test.uncovered endpoint. (comment — NOT evidence)
func TestUncoveredRPC(t *testing.T) {
	t.Helper()
	// call the RPC method
	method := "wrkf.test.uncovered"
	_ = method
}
