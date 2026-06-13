package testdata

import "fmt"

// NolintAll uses //nolint:all — banned regardless of ARCH-EXCEPTION.
// Expected: FINDING (KindNolintAll).
func NolintAll() {
	fmt.Println("hello") //nolint:all
}
