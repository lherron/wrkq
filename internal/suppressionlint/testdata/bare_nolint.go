package testdata

import "fmt"

// BareNolint uses a bare //nolint with no rule list — ungoverned, always a violation.
func BareNolint() {
	fmt.Println("hello") //nolint
}
