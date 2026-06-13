// Package intermediate is a fixture representing a barrel/intermediate package.
// It imports cobra. The governed domain package reaches cobra through this.
// This is the "barrel bypass" case: a direct-imports-only guard would miss it.
package intermediate

import _ "github.com/spf13/cobra"
