// Package workflow_test is a rotguard fixture with BENIGN comments that must NOT
// be flagged. It contains:
//   - a generic "does not exist" phrase NOT paired with any red/compile-failure claim
//   - a plain "TODO: not yet implemented" NOT paired with any hard marker
//   - a free-form TODO with "does not exist" in prose
// None of these qualify under the daedalus ruling (C-04379), so rotguard.Check
// must return zero violations for this directory (TestsGreen=true).
package workflow_test

import "testing"

// TODO: future work — this feature does not exist yet in this release cycle.
func TestFutureFeature(t *testing.T) {
	t.Skip()
}

// The old binding API does not exist in this version; skip until migration.
func TestLegacyBinding(t *testing.T) {
	t.Skip()
}

// TODO: not yet implemented — waiting on upstream contract from wrkfrpc.
func TestUpstreamDependency(t *testing.T) {
	t.Skip()
}
