package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

type claimOutput struct {
	Task            string `json:"task"`
	ClaimedBy       string `json:"claimedBy"`
	ClaimedScope    string `json:"claimedScope"`
	ClaimedNode     string `json:"claimedNode"`
	ClaimedAt       string `json:"claimedAt"`
	ClaimGeneration int64  `json:"claimGeneration"`
	ClaimToken      string `json:"claimToken,omitempty"`
}

func newClaimCmd() *cobra.Command {
	var scopeRef string
	var takeOver, yes, asJSON, porcelain bool
	cmd := &cobra.Command{
		Use:   "claim <task>",
		Short: "Atomically claim a task for this session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			principalRef, err := actorFlag(cmd)
			if err != nil {
				return err
			}
			if principalRef == "" {
				return fmt.Errorf("--as agent:<id> is required")
			}
			resolvedScope, err := claimScopeRef(scopeRef)
			if err != nil {
				return err
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return claimTransportError(err)
			}
			defer closeFn()
			task := sc.selector(args[0], false)
			if takeOver && !yes {
				warning := fmt.Sprintf("WARNING: this supersedes the current holder of %s.\n", task)
				if err := promptConfirm(cmd, warning, "Type 'yes' to confirm takeover: ", func(answer string) bool { return answer == "yes" }); err != nil {
					return err
				}
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.task.claim", map[string]any{
				"task": task, "principalRef": principalRef, "scope": resolvedScope, "takeOver": takeOver,
			})
			if err != nil {
				return formatClaimRPCError(err)
			}
			var out claimOutput
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			if asJSON || porcelain || !isStdoutTTY(cmd.OutOrStdout()) {
				if porcelain {
					data, err := json.Marshal(out)
					if err != nil {
						return err
					}
					_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
					return err
				}
				return encodeJSONIndent(cmd, out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Claimed %s as %s on %s (generation %d)\n", out.Task, out.ClaimedBy, out.ClaimedNode, out.ClaimGeneration)
			fmt.Fprintf(cmd.OutOrStdout(), "claim_token=%s\n", out.ClaimToken)
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeRef, "scope", "", "Task-scoped sessionRef (defaults to the active runtime scope)")
	cmd.Flags().BoolVar(&takeOver, "take-over", false, "Supersede the current holder and bump generation")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip takeover confirmation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Output compact machine-readable JSON")
	return cmd
}

func newReleaseCmd() *cobra.Command {
	var scopeRef, token string
	var generation int64
	var force, yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "release <task>",
		Short: "Release task holdership without changing task state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			principalRef, err := actorFlag(cmd)
			if err != nil {
				return err
			}
			if principalRef == "" {
				return fmt.Errorf("--as agent:<id> is required")
			}
			if token == "" {
				token = strings.TrimSpace(os.Getenv("WRKQ_CLAIM_TOKEN"))
			}
			if generation == 0 {
				generation, _ = strconv.ParseInt(strings.TrimSpace(os.Getenv("WRKQ_CLAIM_GENERATION")), 10, 64)
			}
			resolvedScope := strings.TrimSpace(scopeRef)
			if !force {
				resolvedScope, err = claimScopeRef(scopeRef)
				if err != nil {
					return err
				}
				if token == "" || generation <= 0 {
					return fmt.Errorf("--claim-token and --claim-generation are required (or set WRKQ_CLAIM_TOKEN/WRKQ_CLAIM_GENERATION)")
				}
			}
			if force && !yes {
				warning := fmt.Sprintf("WARNING: force-release supersedes the current holder of %s.\n", args[0])
				if err := promptConfirm(cmd, warning, "Type 'yes' to confirm force release: ", func(answer string) bool { return answer == "yes" }); err != nil {
					return err
				}
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return claimTransportError(err)
			}
			defer closeFn()
			raw, err := tr.Call(cmd.Context(), "wrkq.task.release", map[string]any{
				"task": sc.selector(args[0], false), "principalRef": principalRef, "scope": resolvedScope,
				"claimToken": token, "claimGeneration": generation, "force": force,
			})
			if err != nil {
				return formatClaimRPCError(err)
			}
			var out claimOutput
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			if asJSON || !isStdoutTTY(cmd.OutOrStdout()) {
				return encodeJSONIndent(cmd, out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Released %s generation %d (state unchanged)\n", out.Task, out.ClaimGeneration)
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeRef, "scope", "", "Claimed task sessionRef (defaults to active runtime scope)")
	cmd.Flags().StringVar(&token, "claim-token", "", "Claim token returned by wrkq claim")
	cmd.Flags().Int64Var(&generation, "claim-generation", 0, "Current claim generation")
	cmd.Flags().BoolVar(&force, "force", false, "Operator force release without holder token")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip force-release confirmation")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

func claimScopeRef(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		parsed, err := scope.ParseScopeRef(strings.TrimSpace(override))
		if err != nil || parsed.TaskID == "" {
			return "", fmt.Errorf("--scope must be a task-scoped sessionRef")
		}
		return parsed.ScopeRef, nil
	}
	resolved, _, err := scope.Resolve("")
	if err != nil || resolved.TaskID == "" {
		return "", fmt.Errorf("--scope is required when no task-scoped runtime scope is active")
	}
	return resolved.FullRef(), nil
}

func formatClaimRPCError(err error) error {
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		return claimTransportError(err)
	}
	var data struct {
		ClaimedBy       string `json:"claimedBy"`
		ClaimedNode     string `json:"claimedNode"`
		ClaimedScope    string `json:"claimedScope"`
		ClaimGeneration int64  `json:"claimGeneration"`
		State           string `json:"state"`
	}
	_ = json.Unmarshal(rpcErr.Data, &data)
	switch rpcErr.DomainID {
	case "WRKQ_ALREADY_CLAIMED":
		return fmt.Errorf("already_claimed: holder=%s node=%s scope=%s generation=%d", data.ClaimedBy, data.ClaimedNode, data.ClaimedScope, data.ClaimGeneration)
	case "WRKQ_CLAIM_SUPERSEDED":
		return fmt.Errorf("claim_superseded: holder=%s node=%s scope=%s generation=%d", data.ClaimedBy, data.ClaimedNode, data.ClaimedScope, data.ClaimGeneration)
	case "WRKQ_WRONG_STATE":
		return fmt.Errorf("wrong_state: %s", data.State)
	default:
		return errors.New(rpcErr.Error())
	}
}

func claimTransportError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("unreachable: %w", err)
}
