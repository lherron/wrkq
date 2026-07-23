package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type campaignContainer struct {
	UUID          string   `json:"uuid"`
	ID            string   `json:"id"`
	Path          string   `json:"path"`
	Description   string   `json:"description"`
	Specification *string  `json:"specification,omitempty"`
	Labels        []string `json:"labels"`
	CampaignState *string  `json:"campaignState,omitempty"`
	ETag          int64    `json:"etag"`
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
	cmd.AddCommand(newCampaignActivateCmd())
	cmd.AddCommand(newCampaignEditCmd())
	cmd.AddCommand(newCampaignCloseCmd())
	cmd.AddCommand(newCampaignPortfolioCmd())
	return cmd
}

func newCampaignConvertCmd() *cobra.Command {
	var description, specification, labels, state string
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "convert <container>",
		Short: "Convert a plain container into a draft or active campaign",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state = strings.TrimSpace(state)
			if state != "draft" && state != "active" {
				return fmt.Errorf("--state must be draft or active")
			}
			return runCampaignContentMutation(
				cmd, args[0], "wrkq.container.campaignConvert",
				cmd.Flags().Changed("description"), description,
				cmd.Flags().Changed("specification"), specification,
				cmd.Flags().Changed("labels"), labels,
				map[string]any{"state": state},
				ifMatch,
			)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Campaign brief (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&specification, "specification", "", "Campaign specification (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&labels, "labels", "", "Campaign labels (JSON array; [] clears)")
	cmd.Flags().StringVar(&state, "state", "active", "Initial campaign state: draft or active")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only convert if the container etag matches")
	return cmd
}

func newCampaignActivateCmd() *cobra.Command {
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "activate <container>",
		Short: "Activate a draft campaign",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCampaignTransitionMutation(
				cmd, args[0], "wrkq.container.campaignActivate",
				map[string]any{}, ifMatch, "Activated campaign",
			)
		},
	}
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only activate if the campaign etag matches")
	return cmd
}

func newCampaignEditCmd() *cobra.Command {
	var description, specification, labels string
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "edit <container>",
		Short: "Edit campaign brief or specification",
		Long: `Edit campaign content.

The generic container.updated event snapshots BOTH complete bodies after every
edit. Use a kind=decision container comment for curated amendment rationale.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("description") &&
				!cmd.Flags().Changed("specification") &&
				!cmd.Flags().Changed("labels") {
				return fmt.Errorf("campaign edit requires --description, --specification, or --labels")
			}
			return runCampaignContentMutation(
				cmd, args[0], "wrkq.container.campaignUpdate",
				cmd.Flags().Changed("description"), description,
				cmd.Flags().Changed("specification"), specification,
				cmd.Flags().Changed("labels"), labels,
				nil,
				ifMatch,
			)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Campaign brief (literal, @file, or - for stdin; empty clears)")
	cmd.Flags().StringVar(&specification, "specification", "", "Campaign specification (literal, @file, or - for stdin; empty clears)")
	cmd.Flags().StringVar(&labels, "labels", "", "Campaign labels (JSON array; [] clears)")
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
			return runCampaignTransitionMutation(
				cmd, args[0], "wrkq.container.campaignClose",
				map[string]any{"state": state}, ifMatch, "Closed campaign",
			)
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
	hasLabels bool,
	labels string,
	extra map[string]any,
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
	if hasLabels {
		var values []string
		if err := json.Unmarshal([]byte(labels), &values); err != nil {
			return fmt.Errorf("invalid --labels JSON array: %w", err)
		}
		if values == nil {
			return fmt.Errorf("invalid --labels JSON array: null is not allowed")
		}
		params["labels"] = values
	}
	for key, value := range extra {
		params[key] = value
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

func runCampaignTransitionMutation(
	cmd *cobra.Command,
	ref, method string,
	extra map[string]any,
	ifMatch int64,
	action string,
) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	params := map[string]any{"container": sc.selector(ref, false)}
	for key, value := range extra {
		params[key] = value
	}
	if ifMatch != 0 {
		params["expectEtag"] = ifMatch
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), method, params)
	if err != nil {
		if method == "wrkq.container.campaignClose" {
			return formatCampaignCloseError(err)
		}
		return errors.New(rpcMessage(err))
	}
	var result campaignTransitionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	return renderCampaignTransition(cmd, result, action)
}

type campaignPortfolioResult struct {
	Items []struct {
		Container      campaignContainer `json:"container"`
		TotalMembers   int               `json:"totalMembers"`
		InProgress     int               `json:"inProgressCount"`
		LastActivityAt string            `json:"lastActivityAt"`
	} `json:"items"`
}

func newCampaignPortfolioCmd() *cobra.Command {
	var states []string
	var includeArchived bool
	cmd := &cobra.Command{
		Use:   "portfolio",
		Short: "Show the complete campaign portfolio aggregate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{}
			if cmd.Flags().Changed("state") {
				params["states"] = states
			}
			if includeArchived {
				params["includeArchived"] = true
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.container.campaignPortfolio", params)
			if err != nil {
				return errors.New(rpcMessage(err))
			}
			if !isStdoutTTY(cmd.OutOrStdout()) {
				return writeTimelineJSON(cmd.OutOrStdout(), raw)
			}
			var result campaignPortfolioResult
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			for _, row := range result.Items {
				state := ""
				if row.Container.CampaignState != nil {
					state = *row.Container.CampaignState
				}
				fmt.Fprintf(
					cmd.OutOrStdout(), "%s  %-9s  %d members  %d moving  %s\n",
					row.Container.ID, state, row.TotalMembers, row.InProgress, row.Container.Path,
				)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&states, "state", nil, "Campaign states to include (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived campaign containers")
	return cmd
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
