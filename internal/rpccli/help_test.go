package rpccli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const maxHelpLines = 40

// Every visible help page, on both the wrkq and wrkc roots, renders without a
// template error, carries a Usage section, and stays within maxHelpLines —
// except the pages named in that root's oversized allowlist, which must still
// render.
func TestEveryHelpPageRendersWithUsageWithinFortyLines(t *testing.T) {
	for _, root := range []struct {
		name      string
		newRoot   func() *cobra.Command
		oversized map[string]bool
	}{
		{name: "wrkq", newRoot: func() *cobra.Command { return NewRootCmdFor("wrkq") }},
		{name: "wrkc", newRoot: NewWrkcRootCmd, oversized: oversizedWrkcHelp},
	} {
		paths := collectHelpPaths(root.newRoot(), nil)
		if len(paths) < 2 {
			t.Fatalf("%s: expected the root page plus subcommand pages, got %d", root.name, len(paths))
		}
		for _, path := range paths {
			path := append([]string(nil), path...)
			name := strings.TrimSpace(root.name + " " + strings.Join(path, " "))
			t.Run(name, func(t *testing.T) {
				cmd := root.newRoot()
				var stdout, stderr bytes.Buffer
				cmd.SetArgs(append(append([]string(nil), path...), "--help"))
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
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
				if root.oversized[strings.Join(path, " ")] {
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
}

// The wrkq root lists its visible commands as a bare-name column grid: the
// whitespace tokens between "Commands:" and "Flags:" are exactly the visible
// command names (a description list would add description words; a dropped
// command is missing from the set even when its name is a substring of
// another, like rm/rmdir or ls/details), at most four per row.
func TestWrkqRootHelpListsEveryVisibleCommandInColumnGrid(t *testing.T) {
	root := NewRootCmdFor("wrkq")
	var stdout bytes.Buffer
	root.SetArgs([]string{"--help"})
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("root help returned error: %v", err)
	}

	var want []string
	for _, cmd := range root.Commands() {
		if isVisibleHelpCommand(cmd) {
			want = append(want, cmd.Name())
		}
	}
	got, rows := rootHelpCommandGrid(t, stdout.String())
	assertSameCommandSet(t, got, want, stdout.String())
	for _, row := range rows {
		if n := len(strings.Fields(row)); n > 4 {
			t.Fatalf("grid row has %d names, want <= 4: %q", n, row)
		}
	}
}

// rootHelpCommandGrid returns the whitespace tokens of the root help's
// Commands: section (and its raw rows).
func rootHelpCommandGrid(t *testing.T, output string) (tokens, rows []string) {
	t.Helper()
	in := false
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.TrimSpace(line) == "Commands:":
			in = true
			continue
		case strings.TrimSpace(line) == "Flags:":
			in = false
		}
		if in && strings.TrimSpace(line) != "" {
			rows = append(rows, line)
			tokens = append(tokens, strings.Fields(line)...)
		}
	}
	if len(rows) == 0 {
		t.Fatalf("root help has no Commands: grid:\n%s", output)
	}
	return tokens, rows
}

func assertSameCommandSet(t *testing.T, got, want []string, output string) {
	t.Helper()
	gotSet := map[string]int{}
	for _, name := range got {
		gotSet[name]++
	}
	for _, name := range want {
		if gotSet[name] == 0 {
			t.Fatalf("root help grid missing command %q:\n%s", name, output)
		}
		delete(gotSet, name)
	}
	for name := range gotSet {
		t.Fatalf("root help grid lists %q, which is not a visible command:\n%s", name, output)
	}
	if len(got) != len(want) {
		t.Fatalf("root help grid lists %d names, want %d visible commands:\n%s", len(got), len(want), output)
	}
}

func collectHelpPaths(cmd *cobra.Command, prefix []string) [][]string {
	paths := [][]string{append([]string(nil), prefix...)}
	for _, child := range cmd.Commands() {
		// A command that owns its argv (the retired `watch` pointer shim) answers
		// --help itself, so it has no cobra help page to measure.
		if !isVisibleHelpCommand(child) || child.DisableFlagParsing {
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

func TestContainerLifecycleHelpNamesSafeAndDestructiveChoices(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{
			command: "archive",
			want:    []string{"without deleting", "wrkq unarchive", "--if-match", "--yes"},
		},
		{
			command: "unarchive",
			want:    []string{"wrkq archive", "does not recreate hard-deleted"},
		},
		{
			command: "rmdir",
			want: []string{
				"Permanently destroys",
				"Neither mode is reversible",
				"wrkq archive",
				"Permanently cascade-delete descendant containers and tasks",
				"except when deleting a project",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			root := NewRootCmdFor("wrkq")
			var output bytes.Buffer
			root.SetArgs([]string{tc.command, "--help"})
			root.SetOut(&output)
			root.SetErr(&output)
			if err := root.Execute(); err != nil {
				t.Fatalf("%s --help: %v", tc.command, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("%s help missing %q:\n%s", tc.command, want, output.String())
				}
			}
		})
	}
}
