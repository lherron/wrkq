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

	"github.com/lherron/wrkq/internal/style"
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
	UUID           string          `json:"uuid"`
	ProjectUUID    string          `json:"projectUuid"`
	ContainerUUID  string          `json:"containerUuid"`
	CampaignUUID   *string         `json:"campaignUuid"`
	TaskUUID       *string         `json:"taskUuid"`
	Type           string          `json:"type"`
	Attributes     json.RawMessage `json:"attributes"`
	PrincipalRef   *string         `json:"principalRef"`
	ScopeRef       *string         `json:"scopeRef"`
	Summary        string          `json:"summary"`
	IdempotencyKey *string         `json:"idempotencyKey"`
	OccurredAt     string          `json:"occurredAt"`
	CreatedAt      string          `json:"createdAt"`
	Task           *string         `json:"task"`
	Container      *string         `json:"container"`
	Campaign       *string         `json:"campaign"`
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
	root.AddCommand(newWrkpPostCmd(), newWrkpGitCmd(), newWrkpLogCmd(), newWrkpShowCmd(), newWrkpTypesCmd(), newWrkpInfoCmd(), newVersionCmd())
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
	var eventType, summary, task, key, occurredAt string
	var attrs []string
	cmd := &cobra.Command{
		Use: "post [project]", Short: "Post one foreign fact", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			message, err := readTextValue(summary, "--message", cmd.InOrStdin(), claims)
			if err != nil {
				return err
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
				"summary":        strings.TrimSuffix(message, "\n"),
				"idempotencyKey": key, "occurredAt": occurredAt,
			}
			if len(attrs) > 0 {
				attributes := map[string]string{}
				for _, raw := range attrs {
					name, value, ok := strings.Cut(raw, "=")
					if !ok || name == "" {
						return fmt.Errorf("--attr requires key=value")
					}
					attributes[name] = value
				}
				params["attributes"] = attributes
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
				UUID    string `json:"uuid"`
				Created bool   `json:"created"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if wrkpJSON(cmd) {
				return encodeJSONIndent(cmd, result)
			}
			if result.Created {
				fmt.Fprintln(cmd.OutOrStdout(), result.UUID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (existing)\n", result.UUID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "Dotted event type")
	cmd.Flags().StringVarP(&summary, "message", "m", "", "One-line summary, or - for stdin")
	cmd.Flags().StringArrayVar(&attrs, "attr", nil, "Attribute key=value (repeatable)")
	cmd.Flags().StringVar(&task, "task", "", "Optional task selector")
	cmd.Flags().StringVar(&key, "key", "", "Project-scoped idempotency key")
	cmd.Flags().StringVar(&occurredAt, "occurred-at", "", "Occurrence time (RFC3339)")
	return cmd
}

func newWrkpLogCmd() *cobra.Command {
	var after, since, task, typeList string
	var limit int
	var follow, ndjson, porcelain, pretty bool
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
			mode := resolveWrkpMode(cmd, pretty, ndjson)
			var styled *style.TimelineWriter
			cursor := after
			containerSet := ""
			// previousCursor and followIdleNoticed exist only for the
			// empty-window notice below: one detects that a tail has caught up,
			// the other keeps the notice to a single printing per session.
			previousCursor := ""
			followIdleNoticed := false
			containerPath := ""
			delivered := 0
			deliveredLimit := limit
			if deliveredLimit <= 0 {
				deliveredLimit = wrkpDefaultLimit
			}
			for {
				params := map[string]any{"container": project, "scope": "subtree", "entriesOnly": true, "tail": follow}
				if cursor != "" {
					params["cursor"] = cursor
				} else if !follow {
					// A log reads newest-first: --limit N is "the N most recent",
					// and the newest entry is the first one delivered. --follow
					// stays ascending because it follows APPENDS, which only
					// arrive at the newest end. Later pages take their direction
					// from the cursor, which is authoritative, so the flag is
					// sent only on the opening page.
					params["order"] = "desc"
				}
				if since != "" {
					params["since"] = since
				}
				if task != "" {
					params["task"] = sc.selector(task, false)
				}
				// --limit is a delivered BUDGET for a bounded read and a per-poll
				// page size under --follow, where the read never ends and a
				// decreasing budget would collapse to the server default.
				if follow {
					params["limit"] = wrkpPageLimit(deliveredLimit)
				} else {
					params["limit"] = wrkpPageLimit(deliveredLimit - delivered)
				}
				if typeList != "" {
					params["types"] = splitCommaValues(typeList)
				}
				raw, err := tr.Call(cmd.Context(), "wrkq.container.timelineView", params)
				if err != nil {
					if cmd.Context().Err() != nil {
						return nil
					}
					return wrkpRPCError(err)
				}
				var view wrkpLogView
				if err := json.Unmarshal(raw, &view); err != nil {
					return err
				}
				if mode == "human" && styled == nil {
					styled = style.NewTimelineWriter(cmd.OutOrStdout(), view.Container.Path)
				}
				containerPath = view.Container.Path
				if err := renderWrkpEntries(cmd, view.Entries, mode, styled); err != nil {
					return err
				}
				delivered += len(view.Entries)
				if follow {
					currentSet, err := wrkpSubtreeFingerprint(cmd.Context(), tr, view.Container.UUID)
					if err != nil {
						if cmd.Context().Err() != nil {
							return nil
						}
						return err
					}
					if containerSet != "" && currentSet != containerSet {
						fmt.Fprintln(cmd.ErrOrStderr(), "wrkp: project container set changed; restart with --since to replay moved history")
					}
					containerSet = currentSet
				}
				cursor = view.NextCursor
				// A tail with an empty window prints nothing and waits, which on
				// a terminal is indistinguishable from a hang -- that is exactly
				// how `--since 3h --follow` was read as a stall. Say it once, and
				// only when the read is demonstrably CAUGHT UP: a tail cursor
				// moves whenever the scan advances, so two identical cursors mean
				// the backlog is drained rather than that a sparse page of
				// excluded rows is still being walked.
				if follow && mode == "human" && delivered == 0 && !followIdleNoticed &&
					cursor != "" && cursor == previousCursor {
					fmt.Fprintln(cmd.ErrOrStderr(),
						wrkpEmptyWindowNotice(cmd.Context(), tr, project, containerPath, since, true))
					followIdleNoticed = true
				}
				previousCursor = cursor
				if !follow {
					// A raw-scan page may consume only excluded rows. Keep advancing
					// sparse filtered reads until the delivered limit is full or the
					// fixed fence is drained; --porcelain reports that final position.
					if cursor != "" && delivered < deliveredLimit {
						continue
					}
					if porcelain && cursor != "" {
						fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", cursor)
					}
					if mode == "human" && delivered == 0 {
						fmt.Fprintln(cmd.ErrOrStderr(),
							wrkpEmptyWindowNotice(cmd.Context(), tr, project, containerPath, since, false))
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
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled timeline even when not a TTY")
	return cmd
}

// wrkpDefaultLimit matches the server's own default page size, and
// wrkpMaxPageLimit matches its hard per-page cap. The cap belongs to one PAGE,
// not to the read: --limit is what the CALLER asked for, and the paging loop
// below already stitches pages together, so a --limit above the cap is
// satisfied by more pages rather than refused. Before T-08328 the whole --limit
// went to the server verbatim, so `--limit 5000` was rejected outright and a
// caller who piped the output saw an empty stream and the pipeline's exit code.
const (
	wrkpDefaultLimit = 100
	wrkpMaxPageLimit = 1000
)

func wrkpPageLimit(remaining int) int {
	if remaining > wrkpMaxPageLimit {
		return wrkpMaxPageLimit
	}
	return remaining
}

// resolveWrkpMode picks the output mode for a wrkp read. Interactive displays
// default to the human porcelain: on a terminal the styled render wins, and a
// pipe falls to the machine format so scripted callers are untouched. --pretty
// forces the styled LAYOUT exactly as it does for `wrkq cat` — it never flips
// style.ColorEnabled, which is what keeps non-TTY --pretty byte-parity provable.
func resolveWrkpMode(cmd *cobra.Command, pretty, ndjson bool) string {
	if pretty {
		return "human"
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		switch outF.Value.String() {
		case "human":
			return "human"
		case "json":
			return "json"
		case "ndjson":
			return "ndjson"
		}
	}
	if wrkpJSON(cmd) {
		return "json"
	}
	if ndjson || !isStdoutTTY(cmd.OutOrStdout()) {
		return "ndjson"
	}
	return "human"
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
	var pretty bool
	cmd := &cobra.Command{
		Use: "show <uuid>", Short: "Show one project event", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
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
			if resolveWrkpMode(cmd, pretty, false) != "human" {
				return encodeJSONIndent(cmd, event)
			}
			style.RenderStyledEvent(cmd.OutOrStdout(), styledProjectEvent(event, sc.selector("", true)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled card even when not a TTY")
	return cmd
}

// styledProjectEvent flattens the wire event into the presentation view model.
func styledProjectEvent(event wrkpProjectEvent, project string) style.StyledEvent {
	styled := style.StyledEvent{
		ID:         event.UUID,
		Type:       event.Type,
		Summary:    event.Summary,
		Attributes: event.Attributes,
		Project:    project,
		OccurredAt: event.OccurredAt,
		CreatedAt:  event.CreatedAt,
	}
	if event.PrincipalRef != nil {
		styled.Principal = *event.PrincipalRef
	}
	if event.ScopeRef != nil {
		styled.ScopeRef = *event.ScopeRef
	}
	if event.IdempotencyKey != nil {
		styled.Idempotency = *event.IdempotencyKey
	}
	return styled
}

func newWrkpTypesCmd() *cobra.Command {
	var pretty bool
	cmd := &cobra.Command{
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
			// Same mode contract as log and show (T-08224): a terminal gets the
			// styled render, a pipe stays machine-readable, and --pretty forces the
			// LAYOUT without flipping color. `types` previously hard-coded JSON for
			// every path including --output human, which is the defect.
			if resolveWrkpMode(cmd, pretty, false) != "human" {
				return encodeJSONIndent(cmd, result.Items)
			}
			project, _ := params["project"].(string)
			types := make([]style.StyledType, 0, len(result.Items))
			for _, item := range result.Items {
				types = append(types, style.StyledType{
					Type:          item.Type,
					Count:         item.Count,
					LastCreatedAt: item.LastCreatedAt,
				})
			}
			style.RenderStyledTypes(cmd.OutOrStdout(), project, types)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled list even when not a TTY")
	return cmd
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

func renderWrkpEntries(cmd *cobra.Command, entries []timelineEntry, mode string, styled *style.TimelineWriter) error {
	if mode == "human" {
		styled.Write(styledTimelineEntries(entries))
		return nil
	}
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
	return nil
}

// styledTimelineEntries flattens merged-timeline entries into the presentation
// view model. Each kind is given the weight it carries: prose entries hand their
// body to the renderer to flow, ticks resolve to a single label. An entry whose
// type is not recognized still renders as itself — project-event types are
// free-form dotted names validated only against a reserved-namespace list, so a
// renderer that only knew today's names would start dropping tomorrow's facts.
func styledTimelineEntries(entries []timelineEntry) []style.StyledEntry {
	styledEntries := make([]style.StyledEntry, 0, len(entries))
	for _, entry := range entries {
		styled := style.StyledEntry{
			Timestamp: entry.Timestamp,
			Principal: entry.PrincipalRef,
			TaskID:    entry.TaskID,
			TaskPath:  entry.TaskPath,
			Label:     entry.Type,
			Accent:    style.ColDim,
		}
		switch {
		case entry.Comment != nil:
			// A comment is an author writing ON a task, so the row is drawn with
			// the same grammar as a message: the authoring seat leads, an arrow
			// names what it wrote on, and the comment id rides dim beside them.
			// The seat is the entry's principal — which IS the scope ref — so it
			// moves out of the right-hand actor column rather than being repeated
			// there. A comment attached to no task keeps the older shape: there is
			// no target to point at.
			styled.ID = entry.Comment.ID
			styled.Label = entry.Comment.ID
			styled.Body = entry.Comment.Body
			if entry.Comment.Kind != nil && *entry.Comment.Kind != "" {
				styled.ID = entry.Comment.ID + " " + *entry.Comment.Kind
			}
			if entry.TaskID != "" {
				styled.Label = "→ " + entry.TaskID
				if styled.Principal != "" {
					styled.From = styled.Principal
					styled.Principal = ""
				}
			}
		case entry.Message != nil:
			// A message is prose and carries the same weight as a comment: the
			// body flows. The label answers the question a room message raises
			// and a comment does not — who handed what to whom — so it names
			// BOTH seats: "sender → addressee   EN-xxxxx". Addressee alone, with
			// the sender pushed to the right-hand actor column, made the reader
			// pair two columns to recover a direction the arrow can just state.
			// The id is carried in ID, which the renderer prints beside the
			// label, so the label must not repeat it.
			styled.ID = entry.Message.EnvelopeID
			styled.Label = "→ " + strings.Join(entry.Message.To, ", ")
			if entry.Message.From != "" {
				// The sender leads the row in From, which the renderer paints in
				// the actor color it wore at the right margin. Leaving it in the
				// actor column too would print the same handle twice on one row.
				styled.From = entry.Message.From
				styled.Principal = ""
			}
			if len(entry.Message.To) == 0 {
				// A log entry (obligation "none") addresses nobody: there is no
				// direction to draw, so the sender goes back to the actor column.
				styled.Label = "logged"
				styled.From = ""
				if entry.Message.From != "" {
					styled.Principal = entry.Message.From
				}
			}
			styled.Accent = style.ColMarker
			styled.Body = entry.Message.Body
		case entry.Outcome != nil:
			styled.Label = "outcome"
			styled.Accent = style.ColDone
			if entry.Outcome.Text != nil {
				styled.Body = *entry.Outcome.Text
			}
		case entry.TaskState != nil:
			styled.Label = "→ " + entry.TaskState.State
			if entry.TaskState.From != nil && *entry.TaskState.From != "" {
				styled.Label = *entry.TaskState.From + " → " + entry.TaskState.State
			}
			styled.Accent = style.StateColor(entry.TaskState.State)
			styled.TaskState = entry.TaskState.State
		case entry.ContainerState != nil:
			styled.Label = "campaign → " + entry.ContainerState.To
			if entry.ContainerState.From != nil && *entry.ContainerState.From != "" {
				styled.Label = "campaign " + *entry.ContainerState.From + " → " + entry.ContainerState.To
			}
			styled.Accent = style.ColMarker
		case entry.ProjectEvent != nil:
			styled.Label = entry.ProjectEvent.Type
			styled.Accent = style.ColMarker
			styled.Body = entry.ProjectEvent.Summary
			styled.Attributes = entry.ProjectEvent.Attributes
			if styled.Principal == "" && entry.ProjectEvent.PrincipalRef != nil {
				styled.Principal = *entry.ProjectEvent.PrincipalRef
			}
		}
		styledEntries = append(styledEntries, styled)
	}
	return styledEntries
}

// wrkpEmptyWindowNotice explains a read that delivered nothing. An empty window
// is silent on BOTH paths -- the bounded read exits 0 with no output at all, and
// the tail simply waits -- and that silence is read as a broken command rather
// than as an answer. `wrkp log --since 3h --follow` on a project whose newest
// entry was ten hours old was reported as a performance regression for exactly
// this reason: the command was correct and fast, and said so in no way.
//
// The notice names the window that was asked for and, when the timeline does
// hold older entries, WHEN the newest one was. That second fact is what
// separates "your window is empty" from "the timeline is dead", and it is the
// question a reader asks next.
func wrkpEmptyWindowNotice(ctx context.Context, tr Transport, project, containerPath, since string, following bool) string {
	where := containerPath
	if where == "" {
		where = project
	}
	notice := "no entries"
	if where != "" {
		notice += " in " + where
	}
	if since != "" {
		notice += " since " + since
	}
	if newest := wrkpNewestEntryStamp(ctx, tr, project); newest != "" {
		notice += "; newest is " + newest
	}
	if following {
		notice += " — following for new ones (ctrl-c to stop)"
	}
	return "wrkp: " + notice
}

// wrkpNewestEntryStamp reports when the project last had a deliverable entry,
// in the reader's own zone, for the empty-window notice. It is one descending
// limit-1 read issued ONLY on the empty path, so a read that delivered anything
// pays nothing for it.
//
// Its failure is never the caller's error -- the read it annotates already
// succeeded -- so every failure degrades to the empty string and the notice
// falls back to naming the window alone. A descending page can also legitimately
// deliver nothing when its newest raw rows are all excluded from this container;
// that too degrades quietly, and the surviving wording claims only that the
// WINDOW is empty, never that the timeline is.
func wrkpNewestEntryStamp(ctx context.Context, tr Transport, project string) string {
	raw, err := tr.Call(ctx, "wrkq.container.timelineView", map[string]any{
		"container": project, "scope": "subtree", "entriesOnly": true,
		"order": "desc", "limit": 1,
	})
	if err != nil {
		return ""
	}
	var view wrkpLogView
	if err := json.Unmarshal(raw, &view); err != nil || len(view.Entries) == 0 {
		return ""
	}
	newest, ok := style.ParseTimestamp(view.Entries[0].Timestamp)
	if !ok {
		return ""
	}
	elapsed := style.NowUTC().Sub(newest)
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("%s (%s ago)",
		newest.In(style.DisplayLocation()).Format("2006-01-02 15:04 MST"),
		style.FormatDuration(elapsed))
}
