package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/lherron/wrkq/internal/layerguard"
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
	exclude := multiFlag{"vendor", "testdata"}
	flag.StringVar(&root, "root", ".", "root directory to scan")
	flag.Var(&exclude, "exclude", "path segment to exclude from governed sources; repeatable")
	flag.Parse()

	cfg := liveConfig()
	cfg.Exclude = exclude
	result, err := layerguard.Check(root, cfg, []string{"sqlite_fts5", "wrkq_local"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "layer-boundary: %v\n", err)
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

func liveConfig() layerguard.Config {
	const module = "github.com/lherron/wrkq"
	return layerguard.Config{
		Rules: []layerguard.Rule{
			{
				ID:      "domain-layer",
				Sources: []string{module + "/internal/domain"},
				Forbidden: []string{
					module + "/internal/admincli",
					module + "/internal/wrkqd",
					module + "/internal/wrkfcli",
					module + "/internal/store",
					module + "/internal/db",
					module + "/internal/workflow",
					module + "/internal/wrkfapi",
					module + "/internal/workrpc",
					"github.com/spf13/cobra",
				},
			},
			{
				ID:      "workflow-api-no-adapters",
				Sources: []string{module + "/internal/workflow", module + "/internal/wrkfapi"},
				Forbidden: []string{
					module + "/internal/admincli",
					module + "/internal/wrkqd",
					module + "/internal/wrkfcli",
					module + "/internal/workrpc",
				},
			},
			{
				ID:        "workrpc-ownership",
				Sources:   []string{module},
				Forbidden: []string{module + "/internal/workrpc"},
				Except: []string{
					module + "/internal/workrpc",
					module + "/pkg/client",
					module + "/internal/rpccli",
					module + "/internal/wrkqd",
					module + "/internal/wrkfcli",
					module + "/cmd",
				},
			},
		},
	}
}

func printCounts(counts map[string]int) {
	fmt.Println("layer-boundary governed exception counts:")
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
