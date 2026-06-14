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
		fmt.Fprintln(os.Stderr, "usage: explain-area [-root ROOT] <file|dir>")
		os.Exit(2)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "explain-area: %v\n", err)
		os.Exit(1)
	}
	path := strings.TrimPrefix(flag.Arg(0), "path=")

	area, err := discovery.ExplainArea(absRoot, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "explain-area: %v\n", err)
		os.Exit(1)
	}

	target := path
	if filepath.IsAbs(target) {
		if rel, err := filepath.Rel(absRoot, target); err == nil {
			target = rel
		}
	}
	target = filepath.ToSlash(target)
	fmt.Printf("%s:1 + %s\n", target, area.Role)
	for _, export := range area.Exports {
		fmt.Printf("%s:1 + export: %s\n", target, export)
	}
	for _, dep := range area.Dependencies {
		fmt.Printf("%s:1 + dependency: %s\n", target, dep)
	}
	for _, dependent := range area.Dependents {
		fmt.Printf("%s:1 + dependent: %s\n", target, dependent)
	}
}
