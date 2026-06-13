// Package middle is a fixture intermediate package.
// It imports cobra WITH an ARCH-EXCEPTION, but this is downstream from the governed source.
// A downstream exception does NOT authorize the governed source package.
package middle

import _ "github.com/spf13/cobra" // ARCH-EXCEPTION(T-04349): middle's own import of cobra is authorized
