package rpccli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const maxHelpLines = 40

func TestHelpPagesRenderUnderLineLimit(t *testing.T) {
	discoveryRoot := NewRootCmdFor("wrkq")
	paths := collectHelpPaths(discoveryRoot, nil)
	if len(paths) == 0 {
		t.Fatal("expected at least root help path")
	}

	for _, path := range paths {
		path := append([]string(nil), path...)
		name := "wrkq"
		if len(path) > 0 {
			name += " " + strings.Join(path, " ")
		}
		t.Run(name, func(t *testing.T) {
			root := NewRootCmdFor("wrkq")
			var stdout, stderr bytes.Buffer
			args := append(append([]string(nil), path...), "--help")
			root.SetArgs(args)
			root.SetOut(&stdout)
			root.SetErr(&stderr)

			if err := root.Execute(); err != nil {
				t.Fatalf("%s --help returned error: %v\nstderr:\n%s", name, err, stderr.String())
			}
			output := stdout.String() + stderr.String()
			if strings.TrimSpace(output) == "" {
				t.Fatalf("%s --help produced no output", name)
			}
			if strings.Contains(output, "template:") {
				t.Fatalf("%s --help produced template error:\n%s", name, output)
			}
			if !strings.Contains(output, "Usage:") {
				t.Fatalf("%s --help missing Usage section:\n%s", name, output)
			}
			if lines := countHelpLines(output); lines > maxHelpLines {
				t.Fatalf("%s --help = %d lines, want <= %d:\n%s", name, lines, maxHelpLines, output)
			}
		})
	}
}

func TestRootHelpListsEveryVisibleCommand(t *testing.T) {
	root := NewRootCmdFor("wrkq")
	var stdout bytes.Buffer
	root.SetArgs([]string{"--help"})
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("root help returned error: %v", err)
	}
	output := stdout.String()

	for _, cmd := range root.Commands() {
		if !isVisibleHelpCommand(cmd) {
			continue
		}
		if !strings.Contains(output, cmd.Name()) {
			t.Fatalf("root help missing command %q:\n%s", cmd.Name(), output)
		}
	}
}

func collectHelpPaths(cmd *cobra.Command, prefix []string) [][]string {
	paths := [][]string{append([]string(nil), prefix...)}
	for _, child := range cmd.Commands() {
		if !isVisibleHelpCommand(child) {
			continue
		}
		childPrefix := append(append([]string(nil), prefix...), child.Name())
		paths = append(paths, collectHelpPaths(child, childPrefix)...)
	}
	return paths
}

func isVisibleHelpCommand(cmd *cobra.Command) bool {
	return cmd.IsAvailableCommand() || cmd.Name() == "help"
}

func countHelpLines(output string) int {
	if output == "" {
		return 0
	}
	lines := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		lines++
	}
	return lines
}
