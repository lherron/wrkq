package rpccli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A single-value flag given twice silently keeps the last value: `wrkq set
// --labels a --labels b` replaced the label set with just [b] (T-08044). On the
// replace-style mutation flags a repeat can only be a mistake, so it refuses.

// repeatedFlagError is the refusal for a replace-style flag given more than once.
type repeatedFlagError struct {
	flag string
	hint string
}

func (e repeatedFlagError) Error() string {
	msg := fmt.Sprintf("--%s given more than once; it takes one value and replaces, it does not accumulate", e.flag)
	if e.hint != "" {
		msg += "; " + e.hint
	}
	return msg
}

// repeatedFlagHints names the one-flag form for flags that carry several values.
var repeatedFlagHints = map[string]string{
	"labels":    `pass them in one flag: --labels a,b or --labels '["a","b"]'`,
	"caused-by": "pass them in one flag: --caused-by T-00012,T-00034",
}

type onceValue struct {
	pflag.Value
	name string
	seen bool
}

func (v *onceValue) Set(s string) error {
	if v.seen {
		return repeatedFlagError{flag: v.name, hint: repeatedFlagHints[v.name]}
	}
	v.seen = true
	return v.Value.Set(s)
}

// refuseRepeatedFlags makes every single-value string/int flag on cmd refuse a
// second occurrence. Boolean and slice flags are left alone: repeating a bool is
// idempotent and slices accumulate by design.
func refuseRepeatedFlags(cmd *cobra.Command) *cobra.Command {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "string", "int", "int64":
			f.Value = &onceValue{Value: f.Value, name: f.Name}
		}
	})
	return cmd
}
