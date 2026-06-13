package testdata

import "fmt"

// MalformedNoTicket has an ARCH-EXCEPTION with an empty ticket field — ungoverned.
// Expected: FINDING (KindUngoverned).
func MalformedNoTicket() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(): reason without ticket
}
