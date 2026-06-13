package testdata

import "fmt"

// ValidSingle uses the fully sanctioned single-rule form.
// Expected: NO finding; errcheck counted once in PerRuleCount.
func ValidSingle() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(T-04347): specific real reason for suppression
}
