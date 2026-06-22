// Command architecture-records validates the structure and freshness of wrkq's
// durable architecture law under architecture/ and generates its projections.
//
// Default (check) mode is what `just verify` runs: it validates records and diffs
// the generated projections against disk. `--write` regenerates the projections.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/archrecords"
)

func main() {
	var root string
	var write bool
	flag.StringVar(&root, "root", ".", "repository root")
	flag.BoolVar(&write, "write", false, "regenerate the generated projections (INVARIANTS.md, RISKS.md, index.jsonl) instead of diffing them")
	flag.Parse()

	result, err := archrecords.Run(archrecords.Options{Root: root, Write: write})
	if err != nil {
		fmt.Fprintf(os.Stderr, "architecture-records: %v\n", err)
		os.Exit(2)
	}

	if len(result.Findings) > 0 {
		for _, finding := range result.Findings {
			fmt.Fprintln(os.Stderr, finding.Message)
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintf(os.Stderr, "architecture-records: %d finding(s) across %d record(s).\n", len(result.Findings), len(result.Records))
		os.Exit(1)
	}

	if write {
		fmt.Printf("architecture-records: regenerated projections from %d record(s) (%d active).\n", len(result.Records), result.Active)
		return
	}
	fmt.Printf("architecture-records: %d record(s) (%d active); structure + freshness gates pass.\n", len(result.Records), result.Active)
}
