// Package source is a fixture with a valid ARCH-EXCEPTION on the import line.
// The exception is on the same line as the import spec, with a valid ticket and reason.
// Expected: NO violation; ExceptionsByRule count incremented.
package source

import _ "github.com/spf13/cobra" // ARCH-EXCEPTION(T-04349): cobra is reach via this import; authorized per architecture ticket
