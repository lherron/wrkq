// Command surface-guard enforces delta-based public-surface coverage for wrkf RPC methods.
//
// A new wrkf.* RPC method registered in internal/workrpc/registry.go after the committed
// baseline (internal/surfaceguard/baseline.json) must point at executable test evidence — the
// exact method string as a Go string literal in a *_test.go, or on a non-comment line of a
// test/smoke-*.sh — or carry a local ARCH-EXCEPTION(T-NNNNN): <reason> on the registration.
//
// Exit codes: 0 = clean, 1 = violations found, 2 = tool error.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/surfaceguard"
)

const (
	registryRelPath = "internal/workrpc/registry.go"
	baselineRelPath = "internal/surfaceguard/baseline.json"
)

func main() {
	var root string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.Parse()

	registryPath := filepath.Join(root, registryRelPath)
	registrations, err := surfaceguard.ExtractRegistrations(registryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}

	baseline, err := surfaceguard.LoadBaseline(filepath.Join(root, baselineRelPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}

	testFiles, err := collectTestFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}
	smokeFiles, err := collectSmokeFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}

	evidence, err := surfaceguard.CollectEvidence(testFiles, smokeFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}

	result, err := surfaceguard.Check(registrations, baseline, evidence)
	if err != nil {
		fmt.Fprintf(os.Stderr, "surface-guard: %v\n", err)
		os.Exit(2)
	}

	printCounts(result.ExceptionsByRule)
	if len(result.Violations) > 0 {
		for _, violation := range result.Violations {
			fmt.Fprintln(os.Stderr, violation.Message)
		}
		os.Exit(1)
	}
	fmt.Printf("surface-guard: %d RPC registrations checked against %d-method baseline; no new unevidenced surfaces.\n",
		len(registrations), len(baseline.Methods))
}

// collectTestFiles walks root for first-party *_test.go files, excluding vendor and
// testdata directories (testdata fixtures intentionally contain dummy method literals
// that must not be mistaken for real evidence).
func collectTestFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if isExcludedDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// collectSmokeFiles returns test/smoke-*.sh scripts under root.
func collectSmokeFiles(root string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, "test", "smoke-*.sh"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func isExcludedDir(name string) bool {
	switch name {
	case "vendor", "testdata", "node_modules", ".git":
		return true
	}
	return false
}

func printCounts(counts map[string]int) {
	fmt.Println("surface-guard governed exception counts:")
	if len(counts) == 0 {
		fmt.Println("  none")
		return
	}
	rules := make([]string, 0, len(counts))
	for rule := range counts {
		rules = append(rules, rule)
	}
	sort.Strings(rules)
	for _, rule := range rules {
		fmt.Printf("  %s: %d\n", rule, counts[rule])
	}
}
