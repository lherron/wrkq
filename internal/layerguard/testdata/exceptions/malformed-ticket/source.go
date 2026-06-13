// Package source is a fixture with a malformed ARCH-EXCEPTION (no ticket number).
// Expected: violation is reported (malformed exception does not authorize).
package source

import _ "github.com/spf13/cobra" // ARCH-EXCEPTION(): missing ticket number makes this malformed
