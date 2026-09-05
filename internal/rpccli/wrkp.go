package rpccli

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

//go:embed embedded/WRKP-USAGE.md
var wrkpUsageContent string

type wrkpLogView struct {
	Container struct {
		UUID string `json:"uuid"`
		Path string `json:"path"`
	} `json:"container"`
	Entries    []timelineEntry `json:"entries"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type wrkpContainerTaskCounts struct {
	Items []struct {
		UUID string `json:"uuid"`
		Path string `json:"path"`
	} `json:"items"`
}

type wrkpProjectEvent struct {
	ID             int64           `json:"id"`
	FID            string          `json:"fid"`
	ProjectUUID    string          `json:"projectUuid"`
	ContainerUUID  string          `json:"containerUuid"`
	CampaignUUID   *string         `json:"campaignUuid"`
	TaskUUID       *string         `json:"taskUuid"`
	Type           string          `json:"type"`
	Source         string          `json:"source"`
	Node           *string         `json:"node,omitempty"`
	PrincipalRef   string          `json:"principalRef"`
	ScopeRef       *string         `json:"scopeRef,omitempty"`
	Summary        string          `json:"summary"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	IdempotencyKey *string         `json:"idempotencyKey,omitempty"`
	OccurredAt     string          `json:"occurredAt"`
	CreatedAt      string          `json:"createdAt"`
}

func NewWrkpRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use: "wrkp", Short: "Post and read foreign project facts",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	root.PersistentFlags().String("principal-ref", "", "Caller principal for write attribution: agent:<id> or full agent ScopeRef")
	root.PersistentFlags().String("as", "", "Alias for --principal-ref; accepts agent:<id> or a full agent ScopeRef")
	root.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	root.PersistentFlags().String("output", "", "Output mode: human, json, ndjson, porcelain, yaml, tsv")
	root.PersistentFlags().Bool("json", false, "Output as JSON")
	root.PersistentFlags().String("scope-ref", "", "Caller scope handle (defaults to $HRC_SESSION_REF)")
	root.AddCommand(newWrkpPostCmd(), newWrkpLogCmd(), newWrkpShowCmd(), newWrkpTypesCmd(), newWrkpInfoCmd(), newVersionCmd())
	applyWrkcHelpTemplates(root)
	return root
}

func ExecuteWrkp() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := NewWrkpRootCmd().ExecuteContext(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func newWrkpPostCmd() *cobra.Command {
	var eventType, summary, source, node, task, key, payload, occurredAt string
	cmd := &cobra.Command{
		Use: "post [project]", Short: "Post one foreign fact", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			message, err := readTextValue(summary, "--message", cmd.InOrStdin(), claims)
			if err != nil {
				return err
			}
			var payloadRaw json.RawMessage
			if payload != "" {
				value, err := readTextValue(payload, "--payload", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				payloadRaw = json.RawMessage(value)
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := ""
			if len(args) == 1 {
				project = args[0]
			} else if task == "" {
				project = sc.selector("", true)
			}
			params := map[string]any{
				"project": project, "task": sc.selector(task, false), "type": eventType,
				"source": source, "node": node, "summary": strings.TrimSuffix(message, "\n"),
				"idempotencyKey": key, "occurredAt": occurredAt,
			}
			if len(payloadRaw) > 0 {
				params["payload"] = payloadRaw
			}
			principal, err := actorFlag(cmd)
			if err != nil {
				return err
			}
			if principal != "" {
				params["principalRef"] = principal
			}
			if scopeRef := wrkcScopeRef(cmd); scopeRef != "" {
				params["scopeRef"] = scopeRef
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.post", params)
			if err != nil {
				return wrkpRPCError(err)
			}
			var result struct {
				ID      int64  `json:"id"`
				FID     string `json:"fid"`
				Created bool   `json:"created"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if wrkpJSON(cmd) {
				return encodeJSONIndent(cmd, result)
			}
			if result.Created {
				fmt.Fprintln(cmd.OutOrStdout(), result.FID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (existing)\n", result.FID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "Dotted event type")
	cmd.Flags().StringVarP(&summary, "message", "m", "", "One-line summary, or - for stdin")
	cmd.Flags().StringVar(&source, "source", "wrkp", "Provenance source")
	cmd.Flags().StringVar(&node, "node", "", "Optional node label")
	cmd.Flags().StringVar(&task, "task", "", "Optional task selector")
	cmd.Flags().StringVar(&key, "key", "", "Project-scoped idempotency key")
	cmd.Flags().StringVar(&payload, "payload", "", "JSON object, @file, or - for stdin")
	cmd.Flags().StringVar(&occurredAt, "occurred-at", "", "Occurrence time (RFC3339)")
	return cmd
}

func newWrkpLogCmd() *cobra.Command {
	var after, since, task, typeList string
	var limit int
	var follow, ndjson, porcelain bool
	cmd := &cobra.Command{
		Use: "log [project]", Short: "Read the merged project timeline", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := sc.selector("", true)
			if len(args) == 1 {
				project = args[0]
			}
			mode := "human"
			if wrkpJSON(cmd) {
				mode = "json"
			} else if ndjson || !isStdoutTTY(cmd.OutOrStdout()) {
				mode = "ndjson"
			}
			cursor := after
			containerSet := ""
			for {
				params := map[string]any{"container": project, "scope": "subtree", "entriesOnly": true, "tail": follow}
				if cursor != "" {
					params["cursor"] = cursor
				}
				if since != "" {
					params["since"] = since
				}
				if task != "" {
					params["task"] = sc.selector(task, false)
				}
				if limit != 0 {
					params["limit"] = limit
				}
				if typeList != "" {
					params["types"] = splitCommaValues(typeList)
				}
				raw, err := tr.Call(cmd.Context(), "wrkq.container.timelineView", params)
				if err != nil {
					return wrkpRPCError(err)
				}
				var view wrkpLogView
				if err := json.Unmarshal(raw, &view); err != nil {
					return err
				}
				if err := renderWrkpEntries(cmd, view.Entries, mode); err != nil {
					return err
				}
				if follow {
					currentSet, err := wrkpSubtreeFingerprint(cmd.Context(), tr, view.Container.UUID)
					if err != nil {
						return err
					}
					if containerSet != "" && currentSet != containerSet {
						fmt.Fprintln(cmd.ErrOrStderr(), "wrkp: project container set changed; restart with --since to replay moved history")
					}
					containerSet = currentSet
				}
				cursor = view.NextCursor
				if !follow {
					if porcelain && cursor != "" {
						fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", cursor)
					}
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(monitorPollInterval):
				}
			}
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "Opaque cursor from a previous page")
	cmd.Flags().StringVar(&since, "since", "", "RFC3339 time or duration")
	cmd.Flags().StringVar(&typeList, "type", "", "Comma-separated exact or trailing-glob types")
	cmd.Flags().StringVar(&task, "task", "", "Task selector")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum delivered entries")
	cmd.Flags().BoolVar(&follow, "follow", false, "Follow newly appended matching entries")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output entries as NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Write the next cursor to stderr")
	return cmd
}

// wrkpSubtreeFingerprint is advisory only: the timeline cursor remains the
// authority. It lets follow readers notice that a move changed the mutable
// subtree whose production-time stamps are being filtered at each poll.
func wrkpSubtreeFingerprint(ctx context.Context, tr Transport, projectUUID string) (string, error) {
	raw, err := tr.Call(ctx, "wrkq.container.taskCounts", map[string]any{"includeArchived": true})
	if err != nil {
		return "", wrkpRPCError(err)
	}
	var counts wrkpContainerTaskCounts
	if err := json.Unmarshal(raw, &counts); err != nil {
		return "", err
	}
	projectPath := ""
	for _, item := range counts.Items {
		if item.UUID == projectUUID {
			projectPath = item.Path
			break
		}
	}
	if projectPath == "" {
		return "", fmt.Errorf("project container %s is absent from task counts", projectUUID)
	}
	members := make([]string, 0)
	for _, item := range counts.Items {
		if item.Path == projectPath || strings.HasPrefix(item.Path, projectPath+"/") {
			members = append(members, item.UUID+"\x00"+item.Path)
		}
	}
	sort.Strings(members)
	return strings.Join(members, "\x01"), nil
}

func newWrkpShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show PE-xxxxx", Short: "Show one project event", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.get", map[string]any{"projectEvent": args[0]})
			if err != nil {
				return wrkpRPCError(err)
			}
			var event wrkpProjectEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			return encodeJSONIndent(cmd, event)
		},
	}
}

func newWrkpTypesCmd() *cobra.Command {
	return &cobra.Command{
		Use: "types [project]", Short: "List observed project-event types", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{}
			if len(args) == 1 {
				params["project"] = args[0]
			} else if root := sc.selector("", true); root != "" {
				params["project"] = root
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.typesView", params)
			if err != nil {
				return wrkpRPCError(err)
			}
			var result struct {
				Items []struct {
					Type          string `json:"type"`
					Count         int64  `json:"count"`
					LastCreatedAt string `json:"lastCreatedAt"`
				} `json:"items"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if wrkpJSON(cmd) || !isStdoutTTY(cmd.OutOrStdout()) {
				return encodeJSONIndent(cmd, result.Items)
			}
			rows := make([][]string, 0, len(result.Items))
			for _, item := range result.Items {
				rows = append(rows, []string{item.Type, fmt.Sprint(item.Count), item.LastCreatedAt})
			}
			return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTable}).RenderTable([]string{"TYPE", "COUNT", "LAST CREATED"}, rows)
		},
	}
}

func newWrkpInfoCmd() *cobra.Command {
	return &cobra.Command{Use: "info", Short: "Display wrkp usage documentation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return renderEmbeddedUsage(cmd, wrkpUsageContent, wrkpJSON(cmd))
	}}
}

func wrkpJSON(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("json")
	return value
}

// wrkpRPCError keeps the public domain code and stable validation reason in
// CLI diagnostics. Hooks use both to distinguish permanent input failures from
// transport errors, and the generic mirror helper intentionally strips them.
func wrkpRPCError(err error) error {
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	message := rpcErr.Error()
	if len(rpcErr.Data) > 0 {
		var data struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(rpcErr.Data, &data) == nil && data.Reason != "" {
			message += " (" + data.Reason + ")"
		}
	}
	return errors.New(message)
}

func splitCommaValues(raw string) []string {
	result := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func renderWrkpEntries(cmd *cobra.Command, entries []timelineEntry, mode string) error {
	if mode == "json" {
		return encodeJSONIndent(cmd, entries)
	}
	if mode == "ndjson" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, entry := range entries {
			if err := enc.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}
	for _, entry := range entries {
		if entry.ProjectEvent != nil {
			source := entry.ProjectEvent.Source
			if entry.ProjectEvent.Node != nil {
				source += "@" + *entry.ProjectEvent.Node
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-9s  %s  %-24s  %-14s  %s\n", entry.ProjectEvent.FID, entry.Timestamp, entry.ProjectEvent.Type, source, entry.ProjectEvent.Summary)
			continue
		}
		detail := entry.TaskID
		if entry.TaskState != nil {
			if entry.TaskState.From != nil {
				detail += " " + *entry.TaskState.From + "→" + entry.TaskState.State
			} else {
				detail += " →" + entry.TaskState.State
			}
		}
		if entry.TaskPath != "" {
			detail += "  " + entry.TaskPath
		}
		fmt.Fprintf(cmd.OutOrStdout(), "#%-8d  %s  %-24s  %-14s  %s\n", entry.EventID, entry.Timestamp, entry.Type, "wrkq", strings.TrimSpace(detail))
	}
	return nil
}
