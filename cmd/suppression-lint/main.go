package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/lherron/wrkq/internal/suppressionlint"
)

type multiFlag []string

func (m *multiFlag) String() string {
	return fmt.Sprint([]string(*m))
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func main() {
	var root string
	var exclude multiFlag
	flag.StringVar(&root, "root", ".", "root directory to scan")
	flag.Var(&exclude, "exclude", "path segment to exclude; repeatable")
	flag.Parse()

	result, err := suppressionlint.Scan(root, exclude)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suppression-lint: %v\n", err)
		os.Exit(2)
	}

	printCounts(result.PerRuleCount)
	if len(result.Findings) > 0 {
		for _, finding := range result.Findings {
			fmt.Fprintln(os.Stderr, finding.Message)
		}
		os.Exit(1)
	}
}

func printCounts(counts map[string]int) {
	fmt.Println("suppression-lint per-rule counts:")
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
