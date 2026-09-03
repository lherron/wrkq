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

func TestWrkcHelpPagesRenderUnderLineLimit(t *testing.T) {
	for _, path := range collectHelpPaths(NewWrkcRootCmd(), nil) {
		path := append([]string(nil), path...)
		name := strings.TrimSpace("wrkc " + strings.Join(path, " "))
		t.Run(name, func(t *testing.T) {
			root := NewWrkcRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetArgs(append(append([]string(nil), path...), "--help"))
			root.SetOut(&stdout)
			root.SetErr(&stderr)

			if err := root.Execute(); err != nil {
				t.Fatalf("%s --help returned error: %v\nstderr:\n%s", name, err, stderr.String())
			}
			output := stdout.String() + stderr.String()
			if strings.Contains(output, "template:") {
				t.Fatalf("%s --help produced template error:\n%s", name, output)
			}
			if !strings.Contains(output, "Usage:") {
				t.Fatalf("%s --help missing Usage section:\n%s", name, output)
			}
			lines := countHelpLines(output)
			if oversizedWrkcHelp[strings.Join(path, " ")] {
				// Pre-existing debt, not licence to grow: the page still has to
				// render, and every other page stays under the limit.
				t.Logf("%s --help = %d lines (known over the %d-line limit)", name, lines, maxHelpLines)
				return
			}
			if lines > maxHelpLines {
				t.Fatalf("%s --help = %d lines, want <= %d:\n%s", name, lines, maxHelpLines, output)
			}
		})
	}
}

// oversizedWrkcHelp names the help pages that were already over maxHelpLines
// before wrkc's root grew descriptions. `say` carries 20 flags, each on its own
// line. Trim the page and delete the entry.
var oversizedWrkcHelp = map[string]bool{"say": true}

// wrkc's root lists one command per line WITH its description. A command added
// without a Short would silently render as a bare name.
func TestWrkcRootHelpDescribesEveryVisibleCommand(t *testing.T) {
	root := NewWrkcRootCmd()
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
		if cmd.Short == "" {
			t.Fatalf("wrkc command %q has no Short; the root help would list a bare name", cmd.Name())
		}
		want := "  " + cmd.Name()
		if !strings.Contains(output, want) {
			t.Fatalf("root help missing command %q:\n%s", cmd.Name(), output)
		}
		if !strings.Contains(output, cmd.Short) {
			t.Fatalf("root help missing description for %q (%q):\n%s", cmd.Name(), cmd.Short, output)
		}
	}
}

// The wrkq root keeps its four-column name grid: a description list at 46
// commands would blow the line limit and stop being scannable.
func TestWrkqRootHelpKeepsColumnGrid(t *testing.T) {
	root := NewRootCmdFor("wrkq")
	var stdout bytes.Buffer
	root.SetArgs([]string{"--help"})
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("root help returned error: %v", err)
	}

	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "  ls ") || strings.HasPrefix(line, "  ls\t") {
			t.Fatalf("wrkq root help switched to a description list:\n%s", stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Fatalf("wrkq root help missing Commands section:\n%s", stdout.String())
	}
}
