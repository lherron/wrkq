// Command wrkq-legacy preserves the pre-cutover direct-store CLI as a test-only
// oracle for the RPC cutover parity harness. It is not installed by default.
package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		if !cli.ErrorAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(cli.ExitCodeForError(err))
	}
}
