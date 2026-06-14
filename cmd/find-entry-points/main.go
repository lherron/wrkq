package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/discovery"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: find-entry-points [-root ROOT] <topic>")
		os.Exit(2)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "find-entry-points: %v\n", err)
		os.Exit(1)
	}
	topic := strings.TrimPrefix(flag.Arg(0), "topic=")

	hits, err := discovery.FindEntryPoints(absRoot, topic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "find-entry-points: %v\n", err)
		os.Exit(1)
	}
	for _, hit := range hits {
		fmt.Printf("%s:%d + %s\n", rel(absRoot, hit.File), hit.Line, hit.Role)
	}
}

func rel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
