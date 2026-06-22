package cli

import (
	"sort"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/rpccli"
	"github.com/spf13/cobra"
)

// TestRPCCLITreeParity is the standing guardrail for the RPC-backed mirror: the
// production `wrkq` top-level command surface (paths + aliases) and the mirror's
// must not drift. It lives in package cli so it can read the unexported rootCmd,
// and imports rpccli for the mirror tree (rpccli never imports cli — Core Rule).
//
// Behavior parity is NOT asserted here; per the migration plan the mirror's
// commands are not-started stubs except the `cat` seam smoke. Those gaps are
// reported explicitly (t.Log below), never silently ignored.
func TestRPCCLITreeParity(t *testing.T) {
	prod := topLevelSurface(rootCmd)
	mirror := topLevelSurface(rpccli.NewRootCmd())

	for name, pAliases := range prod {
		mAliases, ok := mirror[name]
		if !ok {
			t.Errorf("mirror is missing production command %q (command-tree drift)", name)
			continue
		}
		if !equalStringSlices(pAliases, mAliases) {
			t.Errorf("command %q alias drift: production=%v mirror=%v", name, pAliases, mAliases)
		}
	}
	for name := range mirror {
		if _, ok := prod[name]; !ok {
			t.Errorf("mirror has command %q absent from the production surface", name)
		}
	}

	// Explicit not-started gap report: which mirrored commands are still stubs.
	var gaps []string
	for _, c := range rpccli.NewRootCmd().Commands() {
		if strings.HasPrefix(c.Short, "(mirror stub") {
			gaps = append(gaps, c.Name())
		}
	}
	sort.Strings(gaps)
	t.Logf("rpccli not-started commands (%d): %s", len(gaps), strings.Join(gaps, " "))
	t.Logf("rpccli implemented (parity): ack, stat; cat (--json partial, via wrkq.task.catView)")
}

// topLevelSurface maps command name -> sorted aliases for a root command,
// skipping cobra's auto-generated help/completion commands.
func topLevelSurface(root *cobra.Command) map[string][]string {
	out := map[string][]string{}
	for _, c := range root.Commands() {
		name := c.Name()
		if name == "help" || name == "completion" {
			continue
		}
		aliases := append([]string{}, c.Aliases...)
		sort.Strings(aliases)
		out[name] = aliases
	}
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
