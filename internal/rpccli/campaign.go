package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type campaignContainer struct {
	UUID          string  `json:"uuid"`
	ID            string  `json:"id"`
	Path          string  `json:"path"`
	Description   string  `json:"description"`
	Specification *string `json:"specification,omitempty"`
	CampaignState *string `json:"campaignState,omitempty"`
	ETag          int64   `json:"etag"`
}

type campaignMemberDiagnostic struct {
	UUID       string `json:"uuid"`
	ID         string `json:"id"`
	Path       string `json:"path"`
	State      string `json:"state"`
	Membership string `json:"membership"`
}

type campaignTransitionResult struct {
	Container       campaignContainer          `json:"container"`
	PreviousState   *string                    `json:"previousState"`
	CampaignState   string                     `json:"campaignState"`
	MissingOutcomes []campaignMemberDiagnostic `json:"missingOutcomes"`
	EventID         int64                      `json:"eventId"`
	EventTimestamp  string                     `json:"eventTimestamp"`
}

func newCampaignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "campaign",
		Short: "Manage the campaign adornment on containers",
		Long: `Manage a container's campaign lifecycle.

Campaigns adorn ordinary containers; the container kind and archive state remain
independent. Brief/specification edits are mechanically retained as full-body
container.updated event snapshots. Use kind=decision container comments for the
curated judgment behind a specification change.`,
	}
	cmd.AddCommand(newCampaignConvertCmd())
	cmd.AddCommand(newCampaignEditCmd())
	cmd.AddCommand(newCampaignCloseCmd())
	return cmd
}

func newCampaignConvertCmd() *cobra.Command {
	var description, specification string
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "convert <container>",
		Short: "Convert a plain container into an active campaign",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCampaignContentMutation(
				cmd, args[0], "wrkq.container.campaignConvert",
				cmd.Flags().Changed("description"), description,
				cmd.Flags().Changed("specification"), specification,
				ifMatch,
			)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Campaign brief (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&specification, "specification", "", "Campaign specification (literal, @file, or - for stdin)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only convert if the container etag matches")
	return cmd
}

func newCampaignEditCmd() *cobra.Command {
	var description, specification string
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "edit <container>",
		Short: "Edit campaign brief or specification",
		Long: `Edit campaign content.

The generic container.updated event snapshots BOTH complete bodies after every
edit. Use a kind=decision container comment for curated amendment rationale.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("description") && !cmd.Flags().Changed("specification") {
				return fmt.Errorf("campaign edit requires --description or --specification")
			}
			return runCampaignContentMutation(
				cmd, args[0], "wrkq.container.campaignUpdate",
				cmd.Flags().Changed("description"), description,
				cmd.Flags().Changed("specification"), specification,
				ifMatch,
			)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Campaign brief (literal, @file, or - for stdin; empty clears)")
	cmd.Flags().StringVar(&specification, "specification", "", "Campaign specification (literal, @file, or - for stdin; empty clears)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only edit if the container etag matches")
	return cmd
}

func newCampaignCloseCmd() *cobra.Command {
	var state string
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "close <container>",
		Short: "Declare an active campaign completed or cancelled",
		Long: `Declare campaign closure.

Completed close requires every resident or enrolled member to be completed,
cancelled, archived, deleted, moved, or unenrolled first. Completed members
without outcomes are displayed but never block close. Cancelled close is a
wholesale abandonment and leaves open members unchanged.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state = strings.TrimSpace(state)
			if state != "completed" && state != "cancelled" {
				return fmt.Errorf("--state must be completed or cancelled")
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			actor, err := actorFlag(cmd)
			if err != nil {
				return err
			}
			params := map[string]any{
				"container": sc.selector(args[0], false),
				"state":     state,
			}
			if ifMatch != 0 {
				params["expectEtag"] = ifMatch
			}
			if actor != "" {
				params["actor"] = actor
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.container.campaignClose", params)
			if err != nil {
				return formatCampaignCloseError(err)
			}
			var result campaignTransitionResult
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			return renderCampaignTransition(cmd, result, "Closed campaign")
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Terminal campaign state: completed or cancelled")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only close if the container etag matches")
	return cmd
}

func runCampaignContentMutation(
	cmd *cobra.Command,
	ref, method string,
	hasDescription bool,
	description string,
	hasSpecification bool,
	specification string,
	ifMatch int64,
) error {
	claims := &stdinClaims{}
	params := map[string]any{}
	if hasDescription {
		value, err := readNullableTextValue(description, "--description", cmd.InOrStdin(), claims)
		if err != nil {
			return fmt.Errorf("failed to read description: %w", err)
		}
		params["description"] = value
	}
	if hasSpecification {
		value, err := readNullableTextValue(specification, "--specification", cmd.InOrStdin(), claims)
		if err != nil {
			return fmt.Errorf("failed to read specification: %w", err)
		}
		params["specification"] = value
	}

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	params["container"] = sc.selector(ref, false)
	if ifMatch != 0 {
		params["expectEtag"] = ifMatch
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), method, params)
	if err != nil {
		return errors.New(rpcMessage(err))
	}
	if method == "wrkq.container.campaignConvert" {
		var result campaignTransitionResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		return renderCampaignTransition(cmd, result, "Converted campaign")
	}

	var container campaignContainer
	if err := json.Unmarshal(raw, &container); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, container)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated campaign content: %s\n", container.Path)
	return nil
}

func renderCampaignTransition(cmd *cobra.Command, result campaignTransitionResult, action string) error {
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", action, result.Container.Path, result.CampaignState)
	for _, member := range result.MissingOutcomes {
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"  outcome missing (non-blocking): %s [%s]\n",
			member.Path, member.Membership,
		)
	}
	return nil
}

func formatCampaignCloseError(err error) error {
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.DomainID != "WRKQ_WRONG_STATE" {
		return errors.New(rpcMessage(err))
	}
	var data struct {
		Stragglers     []campaignMemberDiagnostic `json:"stragglers"`
		MissingOutcome []campaignMemberDiagnostic `json:"missingOutcomes"`
	}
	if json.Unmarshal(rpcErr.Data, &data) != nil || len(data.Stragglers) == 0 {
		return errors.New(rpcMessage(err))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "campaign close blocked by %d open member(s):", len(data.Stragglers))
	for _, member := range data.Stragglers {
		hint := "complete/cancel it, or move it out"
		if member.Membership == "enrolled" {
			hint = "complete/cancel it, or move/unenroll it"
		}
		fmt.Fprintf(&b, "\n  %s (%s, %s): %s", member.Path, member.State, member.Membership, hint)
	}
	for _, member := range data.MissingOutcome {
		fmt.Fprintf(&b, "\n  outcome missing (non-blocking): %s", member.Path)
	}
	return errors.New(b.String())
}
