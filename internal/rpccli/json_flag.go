package rpccli

import (
	"io"

	"github.com/spf13/cobra"
)

// jsonFlagExempt names leaves whose output is not wrkq's to shape: agent and rpc
// stream another program's or the raw wire's bytes, server serve runs the daemon,
// and watch is a §13 pointer shim that refuses every invocation.
var jsonFlagExempt = map[string]bool{
	"agent":        true,
	"rpc":          true,
	"server serve": true,
	"watch":        true,
}

// addJSONFlagEverywhere gives every runnable leaf that lacks its own --json one.
// The agent guide says "Use --json for structured results", so agents pass it to
// every verb; a verb that refused it (wrkq set, mv, relation add, ...) exited 2
// before mutating, and a caller that skipped the exit check lost the edit. The
// flag forces the non-TTY rendering, which is JSON for each of these verbs.
func addJSONFlagEverywhere(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range c.Commands() {
			walk(child)
		}
		if c == root || c.HasSubCommands() || !c.Runnable() || c.Flags().Lookup("json") != nil {
			return
		}
		path := c.Name()
		for p := c.Parent(); p != nil && p != root; p = p.Parent() {
			path = p.Name() + " " + path
		}
		if jsonFlagExempt[path] {
			return
		}
		c.Flags().Bool("json", false, "Output as JSON")
		// Hidden: help pages stay under the 40-line cap, and the guide already
		// tells agents --json works everywhere.
		_ = c.Flags().MarkHidden("json")
		forceJSON := func(cmd *cobra.Command) {
			if on, _ := cmd.Flags().GetBool("json"); on {
				cmd.SetOut(nonTTYWriter{cmd.OutOrStdout()})
			}
		}
		if run := c.RunE; run != nil {
			c.RunE = func(cmd *cobra.Command, args []string) error {
				forceJSON(cmd)
				return run(cmd, args)
			}
		} else if run := c.Run; run != nil {
			c.Run = func(cmd *cobra.Command, args []string) {
				forceJSON(cmd)
				run(cmd, args)
			}
		}
	}
	walk(root)
}

// nonTTYWriter hides the *os.File behind stdout so isStdoutTTY reports false.
type nonTTYWriter struct{ io.Writer }
