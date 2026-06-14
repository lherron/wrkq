// Command doc-link-check verifies that router and canonical documentation paths resolve.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/doclink"
)

var trackedDocs = []string{
	"AGENTS.md",
	"internal/search/README.md",
	"internal/domain/README.md",
	"docs/SPEC.md",
	"docs/wrkf-rpc.md",
	"docs/change-validation.md",
	"internal/cli/embedded/WRKQ-USAGE.md",
}

func main() {
	var root string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.Parse()

	violations, err := doclink.Check(root, trackedDocs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doc-link-check: %v\n", err)
		os.Exit(2)
	}

	if len(violations) > 0 {
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, violation.Message)
		}
		os.Exit(1)
	}

	fmt.Printf("doc-link-check: %d docs checked; all references resolve.\n", len(trackedDocs))
}
