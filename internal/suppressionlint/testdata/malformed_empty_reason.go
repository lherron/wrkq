package testdata

import "fmt"

// MalformedEmptyReason has a ticket but an empty reason after the colon — ungoverned.
// Expected: FINDING (KindUngoverned).
func MalformedEmptyReason() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(T-04347):
}
