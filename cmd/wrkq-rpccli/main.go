// Command wrkq-rpccli is the RPC-backed mirror of the wrkq CLI, used as a parity
// harness during the JSON-RPC CLI migration. It is not a production binary; every
// command obtains durable wrkq behavior through the same JSON-RPC boundary as
// `wrkq rpc --stdio`. See docs/rpc-cli-migration.md.
package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/rpccli"
)

func main() {
	if err := rpccli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
