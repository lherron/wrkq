package rpccli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
)

type timelineMember struct {
	UUID       string  `json:"uuid"`
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	Title      string  `json:"title"`
	State      string  `json:"state"`
	Outcome    *string `json:"outcome,omitempty"`
	Membership string  `json:"membership"`
}

type timelineEntry struct {
	Type       string `json:"type"`
	EventID    int64  `json:"eventId"`
	Timestamp  string `json:"timestamp"`
	TaskUUID   string `json:"taskUuid,omitempty"`
	TaskID     string `json:"taskId,omitempty"`
	TaskPath   string `json:"taskPath,omitempty"`
	Membership string `json:"membership,omitempty"`
	Comment    *struct {
		ID   string  `json:"id,omitempty"`
		Kind *string `json:"kind,omitempty"`
		Body string  `json:"body"`
	} `json:"comment,omitempty"`
	Outcome *struct {
		Text *string `json:"text"`
	} `json:"outcome,omitempty"`
	TaskState *struct {
		State           string `json:"state"`
		SourceEventType string `json:"sourceEventType"`
	} `json:"taskState,omitempty"`
	ContainerState *struct {
		From *string `json:"from"`
		To   string  `json:"to"`
	} `json:"containerState,omitempty"`
}

type containerTimelineView struct {
	Container struct {
		Path          string  `json:"path"`
		Description   string  `json:"description"`
		Specification *string `json:"specification,omitempty"`
	} `json:"container"`
	Campaign *struct {
		State    string `json:"state"`
		Archived bool   `json:"archived"`
	} `json:"campaign"`
	Members []timelineMember `json:"members"`
	Rollup  struct {
		Terminal int `json:"terminal"`
		Total    int `json:"total"`
	} `json:"rollup"`
	MissingOutcomes []campaignMemberDiagnostic `json:"missingOutcomes"`
	DecisionTasks   []timelineMember           `json:"decisionTasks"`
	Entries         []timelineEntry            `json:"entries"`
	SnapshotEventID int64                      `json:"snapshotEventId"`
	NextCursor      string                     `json:"nextCursor,omitempty"`
}

func newTimelineCmd() *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "timeline <container>",
		Short: "Show a container or campaign timeline snapshot",
		Long: `Show the container-neutral composite timeline.

Entries are ordered by durable event id. Outcome display collapses multiple
task.outcome events to the latest text and marks tasks whose outcome was amended.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{
				"container": sc.selector(args[0], false),
			}
			if cursor != "" {
				params["cursor"] = cursor
			}
			if limit != 0 {
				params["limit"] = limit
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.container.timelineView", params)
			if err != nil {
				return fmt.Errorf("%s", rpcMessage(err))
			}
			if !isStdoutTTY(cmd.OutOrStdout()) {
				return writeTimelineJSON(cmd.OutOrStdout(), raw)
			}
			var view containerTimelineView
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			renderTimelineHuman(cmd, view)
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue an event-id-fenced snapshot page")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum entries in this page")
	return cmd
}

func writeTimelineJSON(out io.Writer, raw json.RawMessage) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return err
	}
	formatted.WriteByte('\n')
	_, err := out.Write(formatted.Bytes())
	return err
}

func renderTimelineHuman(cmd *cobra.Command, view containerTimelineView) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Timeline: %s\n", view.Container.Path)
	if view.Campaign == nil {
		fmt.Fprintln(out, "Adornment: plain")
	} else {
		archived := ""
		if view.Campaign.Archived {
			archived = ", archived"
		}
		fmt.Fprintf(out, "Campaign: %s%s\n", view.Campaign.State, archived)
	}
	fmt.Fprintf(out, "Progress: %d/%d terminal\n", view.Rollup.Terminal, view.Rollup.Total)
	if view.Container.Specification != nil {
		fmt.Fprintf(out, "Specification: %s\n", *view.Container.Specification)
	}
	for _, task := range view.DecisionTasks {
		fmt.Fprintf(out, "Decision: %s [%s]\n", task.Path, task.Membership)
	}
	for _, task := range view.MissingOutcomes {
		fmt.Fprintf(out, "Outcome missing: %s [%s] (non-blocking)\n", task.Path, task.Membership)
	}

	type outcomeSummary struct {
		path  string
		text  *string
		count int
	}
	outcomes := map[string]outcomeSummary{}
	for _, entry := range view.Entries {
		if entry.Type != "task.outcome" || entry.Outcome == nil {
			continue
		}
		key := entry.TaskUUID
		if key == "" {
			key = entry.TaskID
		}
		summary := outcomes[key]
		summary.count++
		summary.text = entry.Outcome.Text
		summary.path = entry.TaskPath
		if summary.path == "" {
			summary.path = entry.TaskID
		}
		outcomes[key] = summary
	}
	if len(outcomes) > 0 {
		fmt.Fprintln(out, "Outcomes:")
		keys := make([]string, 0, len(outcomes))
		for key := range outcomes {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return outcomes[keys[i]].path < outcomes[keys[j]].path
		})
		for _, key := range keys {
			summary := outcomes[key]
			text := "(cleared)"
			if summary.text != nil {
				text = *summary.text
			}
			amended := ""
			if summary.count > 1 {
				amended = " (amended)"
			}
			fmt.Fprintf(out, "  %s: %s%s\n", summary.path, text, amended)
		}
	}

	fmt.Fprintln(out, "History:")
	for _, entry := range view.Entries {
		switch entry.Type {
		case "comment":
			kind := ""
			if entry.Comment != nil && entry.Comment.Kind != nil {
				kind = " [" + *entry.Comment.Kind + "]"
			}
			body := ""
			if entry.Comment != nil {
				body = entry.Comment.Body
			}
			fmt.Fprintf(out, "  #%d comment%s: %s\n", entry.EventID, kind, body)
		case "task.state":
			if entry.TaskState != nil {
				fmt.Fprintf(out, "  #%d %s → %s\n", entry.EventID, entry.TaskPath, entry.TaskState.State)
			}
		case "container.state":
			if entry.ContainerState != nil {
				fmt.Fprintf(out, "  #%d campaign → %s\n", entry.EventID, entry.ContainerState.To)
			}
		}
	}
	if view.NextCursor != "" {
		fmt.Fprintf(out, "Next cursor: %s\n", view.NextCursor)
	}
}
