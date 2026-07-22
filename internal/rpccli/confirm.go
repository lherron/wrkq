package rpccli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// promptConfirm is the tiny parameterizable core of the caller-side confirmation
// seam (caller-owned-confirmation invariant): ALL human interaction lives on the
// CLI side of the RPC boundary. It renders an optional warning block + a prompt
// line to stderr, then reads ONE line from cmd.InOrStdin() and accepts iff the
// space-trimmed answer satisfies accept. It returns an "aborted" error otherwise.
//
// IMPORTANT byte-behavior contract: each legacy command's prompt text and
// accept-token differ and MUST NOT be normalized away — e.g. legacy rm --purge
// renders a WARNING block + "Type 'yes' to confirm: " and accepts EXACTLY "yes";
// comment rm uses "[y/N]" accepting y/Y; rmdir --force uses
// "Are you sure? (yes/no): " requiring exact "yes". This core takes those as
// parameters so the comment-rm / rmdir-force fanout can reuse it WITHOUT
// copy-paste while each keeps its own exact bytes. B0 only wires the rm --purge
// variant (purgeConfirm); the others are NOT implemented here.
//
// Non-TTY behavior is deliberate and must not hang: it reads the command's stdin
// (NEVER the RPC server's), so a closed/empty stdin yields EOF → empty response →
// abort, exactly as the legacy CLI does. The RPC transport stdin is never
// consulted; the RPC method stays non-interactive.
func promptConfirm(cmd *cobra.Command, warning, promptLine string, accept func(answer string) bool) error {
	w := cmd.ErrOrStderr()
	if warning != "" {
		fmt.Fprint(w, warning)
	}
	fmt.Fprint(w, promptLine)

	reader := bufio.NewReader(cmd.InOrStdin())
	response, _ := reader.ReadString('\n')
	if accept(strings.TrimSpace(response)) {
		return nil
	}
	return fmt.Errorf("aborted")
}

// purgeConfirm is the RM-PURGE confirmation (legacy internal/cli confirmPurge):
// the caller-rendered destructive WARNING block, then "Type 'yes' to confirm: ",
// accepting the case-folded, space-trimmed answer EXACTLY "yes". skip (the
// command's --yes flag) bypasses the prompt and accepts. This is the ONLY
// confirmation variant wired in B0; the comment-rm / rmdir-force fanout add their
// own (different prompt text / accept-token) on the same promptConfirm core.
//
// warning is the impact block written verbatim to stderr before the confirm line
// (must match legacy confirmPurge bytes).
func purgeConfirm(cmd *cobra.Command, skip bool, warning string) error {
	if skip {
		return nil
	}
	return promptConfirm(cmd, warning, "Type 'yes' to confirm: ", func(answer string) bool {
		return strings.ToLower(answer) == "yes"
	})
}

// commentRmConfirm is the COMMENT-RM confirmation (legacy
// internal/rpccli/comment_rm.go): NO warning block, a single inline prompt line
// "<Action> comment <id> (task <id>)? [y/N]: " on stderr, accepting EXACTLY "y"
// or "Y" (case-sensitive — legacy compares response != "y" && response != "Y").
// Unlike rm-purge this prompts EVEN for soft-delete; skip is the command's --yes
// flag. Distinct prompt text + accept-token from purgeConfirm, by daedalus
// per-command nuance (#10190); shares the promptConfirm core.
func commentRmConfirm(cmd *cobra.Command, skip bool, promptLine string) error {
	if skip {
		return nil
	}
	return promptConfirm(cmd, "", promptLine, func(answer string) bool {
		return answer == "y" || answer == "Y"
	})
}

// rmdirForceConfirm is the RMDIR --force confirmation (legacy
// internal/rpccli/containers.go): the destructive WARNING block + "Are you sure?
// (yes/no): ", requiring EXACTLY "yes". Prompts ONLY when the container is
// non-empty (the caller decides whether to invoke this). skip is the command's
// --yes flag. Distinct accept-token wording from purgeConfirm ("Are you sure?
// (yes/no)" vs "Type 'yes' to confirm"); shares the promptConfirm core.
func rmdirForceConfirm(cmd *cobra.Command, skip bool, warning string) error {
	if skip {
		return nil
	}
	return promptConfirm(cmd, warning, "Are you sure? (yes/no): ", func(answer string) bool {
		return answer == "yes"
	})
}
