// Package source is a fixture with a malformed ARCH-EXCEPTION (empty reason after colon).
// Expected: violation is reported (empty reason makes this malformed).
package source

import _ "github.com/spf13/cobra" // ARCH-EXCEPTION(T-04349):
