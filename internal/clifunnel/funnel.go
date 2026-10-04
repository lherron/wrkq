// Package clifunnel is the one error-and-help funnel shared by wrkq's cobra
// binaries (wrkq, wrkc, wrkp, wrkf, wrkqadm).
//
// It carries the Praesidium CLI standard's mechanisms, so no command has to
// remember them call site by call site:
//
//   - §1: `--help` anywhere in argv is inert. It is never consumed as a flag
//     value or positional and never lets the command run.
//   - §4: every usage error (unknown flag, wrong argument count, missing
//     required flag, unknown command) names the command's usage line and the
//     exact help command; an unknown flag also suggests the nearest real flag.
//   - §4: every not-found error, whether built in the CLI (NotFound) or decoded
//     from a server WRKQ_NOT_FOUND/WRKF_NOT_FOUND, gets a hint naming the valid
//     selector forms and the command that lists real candidates, in the
//     binary's own vocabulary.
//   - §5/§6: a mistyped subcommand under a group is an unknown-command usage
//     error, not the group's help with exit 0. Every usage error exits 2
//     (ExitCode).
//   - §9: an unrecognized --output value is a usage error on every command.
//   - §4: Report renders an error once, with its code in a dedicated field,
//     structured under --json/--ndjson (T-10234).
package clifunnel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// HintFunc returns the valid selector forms and the next command for a missing
// resource of kind, in one binary's vocabulary. "" leaves the error unchanged.
type HintFunc func(kind, ref string) string

// Options configures the funnel for one binary.
type Options struct {
	NotFoundHint HintFunc
	// OutputModes is the binary's --output vocabulary. When set, a --output
	// value outside it is a usage error on every command (§9).
	OutputModes []string
}

// Execute runs root with args through the funnel: help-anywhere rewriting
// before dispatch, and usage/not-found decoration of the returned error.
func Execute(ctx context.Context, root *cobra.Command, args []string, opts Options) error {
	install(root, opts.OutputModes)
	root.SetArgs(HelpArgs(root, args))
	// cobra answers a non-runnable group's leftover positional with the group's
	// help and exit 0. Intercept that help so a typo fails as a usage error.
	var typo error
	help := root.HelpFunc()
	root.SetHelpFunc(func(c *cobra.Command, a []string) {
		if err := unknownSubcommand(c); err != nil {
			typo = err
			return
		}
		help(c, a)
	})
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		err = typo
	}
	return Decorate(cmd, err, opts)
}

// Install wires the funnel's flag-error rewriting and argument-count wrapping
// into every command under root. It is idempotent.
func Install(root *cobra.Command) { install(root, nil) }

func install(root *cobra.Command, outputModes []string) {
	root.SetFlagErrorFunc(flagError)
	walk(root, func(c *cobra.Command) {
		if c.Annotations[installedAnnotation] == "1" {
			return
		}
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		c.Annotations[installedAnnotation] = "1"
		// A nil Args on the root keeps cobra's own unknown-command check; on any
		// other command it means "any args", which the wrapper preserves.
		if c.Args == nil && !c.HasParent() {
			return
		}
		validate := c.Args
		c.Args = func(cmd *cobra.Command, args []string) error {
			if validate != nil {
				if err := validate(cmd, args); err != nil {
					return &UsageError{Err: err, Cmd: cmd}
				}
			}
			return checkOutputMode(cmd, outputModes)
		}
	})
}

// unknownSubcommand returns the usage error for a group invoked with a
// positional that names none of its subcommands, or nil.
func unknownSubcommand(c *cobra.Command) error {
	if c.Runnable() || !c.HasAvailableSubCommands() || !c.HasParent() {
		return nil
	}
	if f := c.Flags().Lookup("help"); f != nil && f.Changed {
		return nil
	}
	positionals := c.Flags().Args()
	if len(positionals) == 0 {
		return nil
	}
	ue := &UsageError{Err: fmt.Errorf("unknown command %q for %q", positionals[0], c.CommandPath()), Cmd: c}
	if c.SuggestionsMinimumDistance <= 0 {
		c.SuggestionsMinimumDistance = 2 // cobra's own default for root typos
	}
	if s := c.SuggestionsFor(positionals[0]); len(s) > 0 {
		ue.Suggestion = "Did you mean " + strings.Join(s, " or ") + "?"
	}
	return ue
}

// checkOutputMode refuses a --output value outside the binary's vocabulary.
// Whether a recognized mode suits a given command stays that command's call.
func checkOutputMode(cmd *cobra.Command, modes []string) error {
	if len(modes) == 0 || cmd.DisableFlagParsing {
		return nil
	}
	f := cmd.Flags().Lookup("output")
	if f == nil || !f.Changed || slices.Contains(modes, f.Value.String()) {
		return nil
	}
	return &UsageError{
		Err: fmt.Errorf("invalid output mode %q: choose %s", f.Value.String(), choiceList(modes)),
		Cmd: cmd,
	}
}

// choiceList renders "a, b, or c" (CLI standard §9's unrecognized-mode shape).
func choiceList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
}

const installedAnnotation = "clifunnel.installed"

// HelpArgs rewrites args so a `--help` or `-h` anywhere before a `--`
// terminator renders the resolved command's help instead of being consumed as
// a flag value or positional (§1: `wrkq cat --as --help` must not run cat).
// Commands that pass their argv through verbatim (DisableFlagParsing, e.g.
// `wrkp just`) keep their args.
func HelpArgs(root *cobra.Command, args []string) []string {
	stripped := make([]string, 0, len(args))
	found := false
	for i, a := range args {
		if a == "--" {
			stripped = append(stripped, args[i:]...)
			break
		}
		if a == "--help" || a == "-h" {
			found = true
			continue
		}
		stripped = append(stripped, a)
	}
	if !found {
		return args
	}
	// Persistent flags are merged into each command's flag set lazily; merge now
	// so Find knows which inherited flags are booleans and do not take a value.
	walk(root, func(c *cobra.Command) { _ = c.InheritedFlags() })
	cmd, _, err := root.Find(stripped)
	if err != nil || cmd == nil || cmd.DisableFlagParsing {
		return args
	}
	path := strings.Fields(cmd.CommandPath())[1:]
	return append(path, "--help")
}

// UsageError is a caller mistake in how a command was invoked. Its message is
// the original error followed by the command's usage line and help command.
type UsageError struct {
	Err error
	Cmd *cobra.Command
	// Suggestion is an optional "did you mean" line.
	Suggestion string
}

func (e *UsageError) Error() string {
	var b strings.Builder
	b.WriteString(e.Err.Error())
	if e.Suggestion != "" {
		b.WriteString("\n")
		b.WriteString(e.Suggestion)
	}
	if e.Cmd != nil {
		fmt.Fprintf(&b, "\nUsage: %s\nRun '%s --help' for flags and examples.", e.Cmd.UseLine(), e.Cmd.CommandPath())
	}
	return b.String()
}

func (e *UsageError) Unwrap() error { return e.Err }

// NotFoundError is a selector that names nothing in an enumerable domain. Its
// first line keeps the long-standing "<kind> not found: <ref>" wording.
type NotFoundError struct {
	Kind string
	Ref  string
	// Prefix, when set, is prepended as "<prefix>: " (e.g. "failed to resolve task").
	Prefix string
}

// NotFound is the one constructor for CLI-built not-found errors (§4).
func NotFound(kind, ref string) error { return &NotFoundError{Kind: kind, Ref: ref} }

// NotFoundf is NotFound with a leading context phrase.
func NotFoundf(prefix, kind, ref string) error {
	return &NotFoundError{Kind: kind, Ref: ref, Prefix: prefix}
}

func (e *NotFoundError) Error() string {
	msg := e.Kind + " not found"
	if e.Kind == "" {
		msg = "not found"
	}
	if e.Ref != "" {
		msg += ": " + e.Ref
	}
	if e.Prefix != "" {
		msg = e.Prefix + ": " + msg
	}
	return msg
}

// hinted is an error with trailing hint lines.
type hinted struct {
	err  error
	hint string
}

func (h *hinted) Error() string { return h.err.Error() + "\n" + h.hint }
func (h *hinted) Unwrap() error { return h.err }

// Decorate appends the funnel's next-action lines to err. cmd is the command
// cobra resolved (from ExecuteC); it may be nil.
func Decorate(cmd *cobra.Command, err error, opts Options) error {
	if err == nil {
		return nil
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return err
	}
	if cmd != nil && isCobraUsageError(err) {
		return &UsageError{Err: err, Cmd: cmd}
	}
	if opts.NotFoundHint != nil {
		if kind, ref, ok := notFoundTarget(err); ok {
			if h := opts.NotFoundHint(kind, ref); h != "" {
				return &hinted{err: err, hint: "hint: " + h}
			}
		}
	}
	return err
}

// isCobraUsageError recognizes the usage errors cobra raises itself, outside
// any Args validator or flag parse this package can wrap.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	return strings.HasPrefix(msg, "unknown command ") ||
		strings.HasPrefix(msg, "required flag(s) ") ||
		strings.HasPrefix(msg, "if any flags in the group ")
}

var notFoundMessage = regexp.MustCompile(`(?:^|: )([a-z][a-z ]*?) not found(?:: (\S+))?$`)

func notFoundTarget(err error) (kind, ref string, ok bool) {
	var nf *NotFoundError
	if errors.As(err, &nf) {
		return nf.Kind, nf.Ref, true
	}
	// Server not-found errors keep the NewNotFoundError shape "<kind> not
	// found: <ref>" whether or not a command preserved their *_NOT_FOUND code,
	// so the shape alone identifies them; the binary's HintFunc only answers
	// for kinds it knows.
	first := strings.SplitN(err.Error(), "\n", 2)[0]
	if m := notFoundMessage.FindStringSubmatch(first); m != nil {
		return m[1], m[2], true
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) && strings.HasSuffix(coded.Code(), "_NOT_FOUND") {
		return "resource", "", true
	}
	return "", "", false
}

func flagError(cmd *cobra.Command, err error) error {
	ue := &UsageError{Err: err, Cmd: cmd}
	if name, ok := unknownFlagName(err); ok {
		if s := nearestFlag(cmd, name); s != "" {
			ue.Suggestion = "Did you mean --" + s + "?"
		}
	}
	return ue
}

func unknownFlagName(err error) (string, bool) {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "unknown flag: --"); ok {
		return rest, true
	}
	return "", false
}

// nearestFlag returns the visible flag name closest to name by edit distance,
// within a similarity floor, or "" when nothing is close enough.
func nearestFlag(cmd *cobra.Command, name string) string {
	best, bestDist := "", -1
	floor := max(2, len(name)/3)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		d := levenshtein(name, f.Name)
		if strings.HasPrefix(f.Name, name) && len(name) >= 3 {
			d = min(d, 1)
		}
		if d <= floor && (bestDist < 0 || d < bestDist) {
			best, bestDist = f.Name, d
		}
	})
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, child := range c.Commands() {
		walk(child, fn)
	}
}
