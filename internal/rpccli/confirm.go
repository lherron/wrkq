package rpccli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// confirmPrompt is the shared caller-side confirmation seam for destructive
// mirror mutations (rm --purge, and — on the proven seam — comment rm /
// rmdir --force later). It enforces the caller-owned-confirmation invariant
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml):
// ALL human interaction lives on the CLI side of the RPC boundary. The RPC
// methods are non-interactive and take an explicit mode/intent; this helper owns
// the legacy prompt text, the --yes skip, accept/abort, and the non-TTY
// behavior.
//
// Fields mirror the legacy `confirmPurge` flow exactly:
//   - skip true (the command's --yes) bypasses the prompt and accepts.
//   - warning is written to stderr verbatim (the legacy WARNING block).
//   - the helper then writes the trailing "Type 'yes' to confirm: " line and
//     reads ONE line from cmd.InOrStdin(). Anything other than a case-folded,
//     space-trimmed "yes" is an abort.
//
// Non-TTY behavior is deliberate and must not hang: it reads from the command's
// stdin (NOT the RPC server's stdin), so a closed/empty stdin yields EOF → empty
// response → abort, exactly as the legacy CLI does. The RPC transport stdin is
// never consulted.
type confirmPrompt struct {
	// skip, when true, accepts without prompting (the command's --yes flag).
	skip bool
	// warning is the destructive-impact block rendered to stderr before the
	// confirm line (matches the legacy CLI text byte-for-byte).
	warning string
}

// run renders the prompt and returns nil on confirmation, or an "aborted" error
// otherwise. It is a no-op (returns nil) when skip is set.
func (c confirmPrompt) run(cmd *cobra.Command) error {
	if c.skip {
		return nil
	}
	err := cmd.ErrOrStderr()
	if c.warning != "" {
		fmt.Fprint(err, c.warning)
	}
	fmt.Fprintf(err, "Type 'yes' to confirm: ")

	reader := bufio.NewReader(cmd.InOrStdin())
	response, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(response)) != "yes" {
		return fmt.Errorf("aborted")
	}
	return nil
}
