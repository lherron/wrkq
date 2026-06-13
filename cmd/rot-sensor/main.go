package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/lherron/wrkq/internal/rotguard"
)

func main() {
	var root string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.Parse()

	testsGreen, err := workflowTestsGreen(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rot-sensor: %v\n", err)
		os.Exit(2)
	}

	result, err := rotguard.Check(rotguard.Scope{
		Dir:        filepath.Join(root, "internal", "workflow"),
		TestsGreen: testsGreen,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rot-sensor: %v\n", err)
		os.Exit(2)
	}

	printCounts(result.ExceptionsByRule)
	if len(result.Violations) > 0 {
		for _, violation := range result.Violations {
			fmt.Fprintln(os.Stderr, violation.Message)
		}
		os.Exit(1)
	}
}

func workflowTestsGreen(root string) (bool, error) {
	cmd := exec.Command("go", "test", "-tags", "sqlite_fts5", "./internal/workflow")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		fmt.Fprintf(os.Stderr, "rot-sensor: workflow tests are not green; stale-comment scan skipped\n%s", output)
		return false, nil
	}
	return false, err
}

func printCounts(counts map[string]int) {
	fmt.Println("rot-sensor governed exception counts:")
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
