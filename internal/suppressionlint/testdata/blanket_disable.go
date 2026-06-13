//nolint:errcheck
package testdata

// BlanketDisable has a nolint comment before the package keyword — file-level blanket disable.
// Expected: FINDING (KindBlanketDisable).
func BlanketDisable() {}
