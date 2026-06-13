package testdata

import "fmt"

// ValidMulti uses the fully sanctioned multi-rule form.
// Expected: NO finding; errcheck and gocyclo each counted once in PerRuleCount.
func ValidMulti() {
	fmt.Println("hello") //nolint:errcheck,gocyclo // ARCH-EXCEPTION(T-04347): reason spanning both rules
}
