package wrkfcli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/workflow"
	"github.com/spf13/cobra"
)

type actionStartOptions struct {
	workflowRef    string
	action         string
	role           string
	actor          string
	lane           string
	deliveryRef    string
	externalRunRef string
	idempotencyKey string
	leaseOwner     string
	leaseMs        int64
}

type actionNextOptions struct {
	instanceID     string
	actionFilter   string
	role           string
	status         string
	phase          string
	includeBlocked bool
	limit          int
}

type actionClaimOptions struct {
	instanceID        string
	semanticActionKey string
	actionFilter      string
	runnerID          string
	actor             string
	scopeRef          string
	leaseMs           int64
	workspaceRoot     string
	priorRun          string
}

type actionBindOptions struct {
	externalRunRef string
	deliveryRef    string
	lane           string
	idempotencyKey string
}

type actionEvidenceOptions struct {
	kind    string
	ref     string
	summary string
	facts   string
	data    string
}

func (opts actionEvidenceOptions) evidenceInput() *workflow.ActionEvidenceInput {
	if opts.kind == "" && opts.ref == "" && opts.summary == "" && opts.facts == "" && opts.data == "" {
		return nil
	}
	return &workflow.ActionEvidenceInput{
		Kind: opts.kind, Ref: opts.ref, Summary: opts.summary,
		Facts: opts.facts, Data: opts.data,
	}
}

type actionTransitionOptions struct {
	transition   string
	noTransition bool
}

func (opts actionTransitionOptions) transitionSelection() (workflow.TransitionMode, string) {
	switch {
	case opts.noTransition:
		return workflow.TransitionSkip, ""
	case opts.transition != "":
		return workflow.TransitionExplicit, opts.transition
	default:
		return workflow.TransitionDefault, ""
	}
}

type actionCompleteOptions struct {
	evidence   actionEvidenceOptions
	transition actionTransitionOptions
	runSummary string
	leaseToken string
}

type actionSettleOptions struct {
	result          string
	ownerToken      string
	ownerGeneration int64
	evidence        actionEvidenceOptions
	terminalSummary string
	transition      actionTransitionOptions
}

type actionFailOptions struct {
	runSummary string
	leaseToken string
	evidence   actionEvidenceOptions
}

type actionHeartbeatOptions struct {
	leaseToken string
	leaseMs    int64
}

type actionListOptions struct {
	includeClosed bool
	status        string
	actionFilter  string
	limit         int
}

func actionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "action", Short: "Low-ceremony task lifecycle actions over wrkf runs"}
	cmd.AddCommand(
		actionNextCmd(),
		actionClaimCmd(),
		actionStartCmd(),
		actionBindCmd(),
		actionCompleteCmd(),
		actionSettleCmd(),
		actionFailCmd(),
		actionHeartbeatCmd(),
		actionShowCmd(),
		actionListCmd(),
	)
	return cmd
}

func actionStartCmd() *cobra.Command {
	opts := actionStartOptions{}
	cmd := &cobra.Command{
		Use:  "start TASK --action ACTION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if opts.action == "" {
				return fmt.Errorf("--action is required")
			}
			run, err := a.service.StartAction(workflow.StartActionParams{
				Task:           args[0],
				Workflow:       opts.workflowRef,
				Action:         opts.action,
				Role:           opts.role,
				PrincipalRef:   firstNonEmpty(opts.actor, a.actor),
				Lane:           opts.lane,
				DeliveryRef:    opts.deliveryRef,
				ExternalRunRef: opts.externalRunRef,
				IdempotencyKey: opts.idempotencyKey,
				LeaseOwner:     opts.leaseOwner,
				LeaseMs:        opts.leaseMs,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	cmd.Flags().StringVar(&opts.action, "action", "", "Semantic action (triage|implement|review|verify|...)")
	cmd.Flags().StringVar(&opts.workflowRef, "workflow", "", "Workflow ref to attach when none exists (defaults to built-in wrkq-simple-task@1)")
	cmd.Flags().StringVar(&opts.role, "role", "", "Workflow role (defaults from action)")
	cmd.Flags().StringVar(&opts.actor, "principal-ref", "", "Principal ref (agent:<id>)")
	cmd.Flags().StringVar(&opts.lane, "lane", "", "Lane (defaults from action)")
	cmd.Flags().StringVar(&opts.deliveryRef, "delivery-ref", "", "Delivery ref")
	cmd.Flags().StringVar(&opts.externalRunRef, "external-run-ref", "", "External run ref (hrc:<runId>)")
	cmd.Flags().StringVar(&opts.idempotencyKey, "idempotency-key", "", "Idempotency key")
	cmd.Flags().StringVar(&opts.leaseOwner, "lease-owner", "", "Lease owner")
	cmd.Flags().Int64Var(&opts.leaseMs, "lease-ms", 0, "Lease duration in milliseconds")
	return cmd
}

func actionNextCmd() *cobra.Command {
	opts := actionNextOptions{}
	cmd := &cobra.Command{
		Use:  "next [TASK]",
		Args: cobra.MaximumNArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			task := ""
			if len(args) > 0 {
				task = args[0]
			}
			result, err := a.service.ActionNext(workflow.ActionNextParams{
				Task:       task,
				InstanceID: opts.instanceID,
				Filters: workflow.ActionNextFilters{
					Actions:        cliFilterList(opts.actionFilter),
					Roles:          cliFilterList(opts.role),
					Statuses:       cliFilterList(opts.status),
					Phases:         cliFilterList(opts.phase),
					IncludeBlocked: opts.includeBlocked,
				},
				Limit: opts.limit,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
		}),
	}
	cmd.Flags().StringVar(&opts.instanceID, "instance-id", "", "Workflow instance id")
	cmd.Flags().StringVar(&opts.actionFilter, "action", "", "Filter by action")
	cmd.Flags().StringVar(&opts.role, "role", "", "Filter by role")
	cmd.Flags().StringVar(&opts.status, "status", "", "Filter by workflow status")
	cmd.Flags().StringVar(&opts.phase, "phase", "", "Filter by workflow phase")
	cmd.Flags().BoolVar(&opts.includeBlocked, "include-blocked", false, "Include blocked candidates with reasons")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "Maximum candidates to return")
	return cmd
}

func actionClaimCmd() *cobra.Command {
	opts := actionClaimOptions{}
	cmd := &cobra.Command{
		Use: "claim [TASK]",
		Long: `Claim the next executable action and return its fenced run binding.

The success response includes the values needed by settle:
  binding.run.id
  binding.authority.ownerToken
  binding.authority.ownerGeneration

With --json, a predecessor conflict is returned as:
  error.message
  error.fix
  error.predecessor.runId

Follow error.fix and retry with --prior-run when predecessor review is required.`,
		Args: cobra.MaximumNArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			task := ""
			if len(args) > 0 {
				task = args[0]
			}
			claimLeaseMs := opts.leaseMs
			if claimLeaseMs <= 0 {
				claimLeaseMs = 300000
			}
			var priorRun *string
			if value := strings.TrimSpace(opts.priorRun); value != "" && value != "null" {
				priorRun = &value
			}
			result, err := a.service.ClaimAction(workflow.ClaimActionParams{
				Task:       task,
				InstanceID: opts.instanceID,
				Prefer: workflow.ActionClaimPrefer{
					SemanticActionKey: opts.semanticActionKey,
					Action:            opts.actionFilter,
				},
				RunnerID:         opts.runnerID,
				AgentRef:         firstNonEmpty(opts.actor, a.actor),
				ScopeRef:         opts.scopeRef,
				LeaseMs:          claimLeaseMs,
				WorkspaceRoot:    opts.workspaceRoot,
				PriorRun:         priorRun,
				PriorRunProvided: true,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
		}),
	}
	cmd.Flags().StringVar(&opts.instanceID, "instance-id", "", "Workflow instance id")
	cmd.Flags().StringVar(&opts.semanticActionKey, "semantic-action-key", "", "Preferred semantic action key")
	cmd.Flags().StringVar(&opts.actionFilter, "action", "", "Preferred action")
	cmd.Flags().StringVar(&opts.runnerID, "runner-id", "", "Runner identity")
	cmd.Flags().StringVar(&opts.actor, "agent-ref", "", "Agent ref (agent:<id>)")
	cmd.Flags().StringVar(&opts.scopeRef, "scope-ref", "", "Runtime scope ref")
	cmd.Flags().Int64Var(&opts.leaseMs, "lease-ms", 300000, "Lease duration in milliseconds")
	cmd.Flags().StringVar(&opts.workspaceRoot, "workspace-root", "", "Opaque physical worktree ref recorded on the run record (never interpreted by the engine)")
	cmd.Flags().StringVar(&opts.priorRun, "prior-run", "null", "Acknowledged predecessor run id, or null for a first-ever claim")
	return cmd
}

func actionBindCmd() *cobra.Command {
	opts := actionBindOptions{}
	cmd := &cobra.Command{
		Use:  "bind ACTION_RUN --external-run-ref hrc:RUNID",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if opts.externalRunRef == "" {
				return fmt.Errorf("--external-run-ref is required")
			}
			run, err := a.service.BindActionExternal(workflow.BindActionExternalParams{
				ActionRunID:    args[0],
				ExternalRunRef: opts.externalRunRef,
				DeliveryRef:    opts.deliveryRef,
				Lane:           opts.lane,
				IdempotencyKey: opts.idempotencyKey,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	cmd.Flags().StringVar(&opts.externalRunRef, "external-run-ref", "", "External run ref (hrc:<runId>)")
	cmd.Flags().StringVar(&opts.deliveryRef, "delivery-ref", "", "Delivery ref")
	cmd.Flags().StringVar(&opts.lane, "lane", "", "Lane")
	cmd.Flags().StringVar(&opts.idempotencyKey, "idempotency-key", "", "Idempotency key")
	return cmd
}

func actionCompleteCmd() *cobra.Command {
	opts := actionCompleteOptions{}
	cmd := &cobra.Command{
		Use:  "complete ACTION_RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			mode, transitionID := opts.transition.transitionSelection()
			out, err := a.service.CompleteAction(workflow.CompleteActionParams{
				ActionRunID:    args[0],
				LeaseToken:     opts.leaseToken,
				Evidence:       opts.evidence.evidenceInput(),
				TransitionMode: mode,
				TransitionID:   transitionID,
				RunSummary:     opts.runSummary,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.Flags().StringVar(&opts.evidence.kind, "kind", "", "Evidence kind (defaults to <action>_result)")
	cmd.Flags().StringVar(&opts.evidence.ref, "ref", "", "Evidence ref")
	cmd.Flags().StringVar(&opts.evidence.summary, "summary", "", "Evidence summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.facts, "facts", "", "Evidence facts JSON object (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.data, "data", "", "Evidence data JSON (- reads stdin)")
	cmd.Flags().StringVar(&opts.runSummary, "run-summary", "", "Run terminal summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.leaseToken, "lease-token", "", "Lease token for leased action runs")
	cmd.Flags().StringVar(&opts.transition.transition, "transition", "", "Explicit transition id (default: auto-resolve)")
	cmd.Flags().BoolVar(&opts.transition.noTransition, "no-transition", false, "Skip the transition; finish the run only")
	return cmd
}

func actionSettleCmd() *cobra.Command {
	opts := actionSettleOptions{}
	cmd := &cobra.Command{
		Use: "settle ACTION_RUN --result RESULT --owner-token TOKEN --owner-generation N",
		Long: `Settle a claimed action run with its current fenced authority.

The success response shape is:
  run.id
  evidence (optional)
  transition (optional)
  effects (optional)
  obligations (optional)

Use binding.run.id, binding.authority.ownerToken, and
binding.authority.ownerGeneration from the claim response.`,
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			mode, transitionID := opts.transition.transitionSelection()
			out, err := a.service.SettleAction(workflow.SettleActionParams{
				ActionRunID:     args[0],
				OwnerToken:      opts.ownerToken,
				OwnerGeneration: opts.ownerGeneration,
				Result:          opts.result,
				Evidence:        opts.evidence.evidenceInput(),
				TransitionMode:  mode,
				TransitionID:    transitionID,
				TerminalSummary: opts.terminalSummary,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.Flags().StringVar(&opts.result, "result", "completed", "Terminal settlement result")
	cmd.Flags().StringVar(&opts.ownerToken, "owner-token", "", "Current owner token from action.claim")
	cmd.Flags().Int64Var(&opts.ownerGeneration, "owner-generation", 0, "Current owner generation from action.claim")
	cmd.Flags().StringVar(&opts.evidence.kind, "kind", "", "Evidence kind (defaults to executable action result kind)")
	cmd.Flags().StringVar(&opts.evidence.ref, "ref", "", "Evidence ref")
	cmd.Flags().StringVar(&opts.evidence.summary, "summary", "", "Evidence summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.facts, "facts", "", "Evidence facts JSON object (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.data, "data", "", "Evidence data JSON (- reads stdin)")
	cmd.Flags().StringVar(&opts.terminalSummary, "terminal-summary", "", "Run terminal summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.transition.transition, "transition", "", "Explicit transition id (default: executable action transition)")
	cmd.Flags().BoolVar(&opts.transition.noTransition, "no-transition", false, "Skip the workflow transition; terminalize the run only")
	return cmd
}

func actionFailCmd() *cobra.Command {
	opts := actionFailOptions{}
	cmd := &cobra.Command{
		Use:  "fail ACTION_RUN --run-summary SUMMARY",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if opts.runSummary == "" {
				return fmt.Errorf("--run-summary is required")
			}
			run, err := a.service.FailAction(workflow.FailActionParams{
				ActionRunID: args[0],
				LeaseToken:  opts.leaseToken,
				Summary:     opts.runSummary,
				Evidence:    opts.evidence.evidenceInput(),
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	cmd.Flags().StringVar(&opts.runSummary, "run-summary", "", "Failure summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.leaseToken, "lease-token", "", "Lease token for leased action runs")
	cmd.Flags().StringVar(&opts.evidence.kind, "kind", "", "Failure evidence kind (defaults to failure_result)")
	cmd.Flags().StringVar(&opts.evidence.ref, "ref", "", "Failure evidence ref")
	cmd.Flags().StringVar(&opts.evidence.summary, "summary", "", "Failure evidence summary (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.facts, "facts", "", "Failure evidence facts JSON object (- reads stdin)")
	cmd.Flags().StringVar(&opts.evidence.data, "data", "", "Failure evidence data JSON (- reads stdin)")
	return cmd
}

func actionHeartbeatCmd() *cobra.Command {
	opts := actionHeartbeatOptions{}
	cmd := &cobra.Command{
		Use: "heartbeat ACTION_RUN --lease-token TOKEN",
		Long: `Extend an active action lease and return the updated action run.

The success response includes:
  actionRunId
  runId
  leaseToken
  leaseExpiresAt
  heartbeatAt

Use binding.run.id and binding.authority.ownerToken from the claim response as
ACTION_RUN and --lease-token.`,
		Aliases: []string{"renew-lease"},
		Args:    cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.HeartbeatAction(workflow.HeartbeatActionParams{
				ActionRunID: args[0],
				LeaseToken:  opts.leaseToken,
				LeaseMs:     opts.leaseMs,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	cmd.Flags().StringVar(&opts.leaseToken, "lease-token", "", "Lease token")
	cmd.Flags().Int64Var(&opts.leaseMs, "lease-ms", 0, "Lease duration in milliseconds")
	return cmd
}

func actionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "show ACTION_RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.ShowAction(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
}

func actionListCmd() *cobra.Command {
	opts := actionListOptions{}
	cmd := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			runs, err := a.service.ListActions(workflow.ListActionsParams{
				Task:                   args[0],
				IncludeClosedInstances: opts.includeClosed,
				Status:                 opts.status,
				Action:                 opts.actionFilter,
				Limit:                  opts.limit,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"items": runs})
		}),
	}
	cmd.Flags().BoolVar(&opts.includeClosed, "include-closed-instances", false, "Include runs from closed workflow instances")
	cmd.Flags().StringVar(&opts.status, "status", "", "Filter by run status")
	cmd.Flags().StringVar(&opts.actionFilter, "action", "", "Filter by action")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "Maximum runs to return")
	return cmd
}

func cliFilterList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}
