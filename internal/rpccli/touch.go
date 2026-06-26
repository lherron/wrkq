package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type touchResult struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	Slug        string `json:"slug"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	State       string `json:"state"`
	Priority    int    `json:"priority"`
	Kind        string `json:"kind"`
	ArtifactDir string `json:"artifact_dir"`
}

// newTouchCmd mirrors `wrkq touch`. RPC-backed via wrkq.task.create, re-projected
// into the legacy touchResult array. The mirror owns caller-local file/stdin
// reads and artifact-dir creation; the server owns durable task creation.
func newTouchCmd() *cobra.Command {
	var title, description, specification, state, kind, parentTask, assignee, labels, meta string
	var dueAt, startAt, requestedBy, assignedProject, resolution, metaFile, forceUUID string
	var priority int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "touch <path>...",
		Short: "Create one or more tasks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forceUUID != "" && len(args) > 1 {
				return fmt.Errorf("--force-uuid can only be used when creating a single task")
			}
			return runTouch(cmd, args, touchOpts{
				title: title, description: description, specification: specification,
				state: state, priority: priority, kind: kind, parentTask: parentTask,
				assignee: assignee, requestedBy: requestedBy, assignedProject: assignedProject,
				resolution: resolution, labels: labels, meta: meta, metaFile: metaFile,
				dueAt: dueAt, startAt: startAt, forceUUID: forceUUID, json: asJSON,
			})
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Title for the task (defaults to slug)")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Description for the task (use @file.md for file or - for stdin)")
	cmd.Flags().StringVar(&specification, "specification", "", "Specification for the task (use @file.md for file or - for stdin)")
	cmd.Flags().StringVar(&state, "state", "open", "Initial task state (idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted)")
	cmd.Flags().IntVar(&priority, "priority", 3, "Initial task priority (1-4)")
	cmd.Flags().StringVar(&kind, "kind", "", "Task kind: task, subtask, spike, bug, chore (default: task)")
	cmd.Flags().StringVar(&parentTask, "parent-task", "", "Parent task ID or path (for subtasks)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee principal ref or bare agent slug")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "Requester project ID (return-to target)")
	cmd.Flags().StringVar(&assignedProject, "assigned-project", "", "Assignee project ID")
	cmd.Flags().StringVar(&resolution, "resolution", "", "Task resolution (done, wont_do, duplicate, needs_info)")
	cmd.Flags().StringVar(&labels, "labels", "", "Initial task labels (JSON array)")
	cmd.Flags().StringVar(&meta, "meta", "", "Initial task metadata (JSON object or null)")
	cmd.Flags().StringVar(&metaFile, "meta-file", "", "Load task metadata from file (JSON object or null)")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "Initial task due date")
	cmd.Flags().StringVar(&startAt, "start-at", "", "Initial task start date")
	cmd.Flags().StringVar(&forceUUID, "force-uuid", "", "Force specific UUID instead of auto-generating (must be valid UUIDv4)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

type touchOpts struct {
	title, description, specification, state, kind, parentTask, assignee string
	requestedBy, assignedProject, resolution, labels, meta, metaFile     string
	dueAt, startAt, forceUUID                                            string
	priority                                                             int
	json                                                                 bool
}

func runTouch(cmd *cobra.Command, args []string, o touchOpts) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	priority := o.priority

	// Legacy: applyProjectRootToPaths(args, false) for the new-task paths and
	// applyProjectRootToSelector(parentTask, false) for the parent reference.
	paths := sc.paths(args, false)
	parentTask := ""
	if o.parentTask != "" {
		parentTask = sc.selector(o.parentTask, false)
	}
	description := ""
	if o.description != "" {
		description, err = readTextValue(o.description, "description", cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("failed to read description: %w", err)
		}
	}
	specification := ""
	if o.specification != "" {
		specification, err = readTextValue(o.specification, "specification", cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("failed to read specification: %w", err)
		}
	}
	metaSet, metaRaw, metaValue, err := readMetaValue(o.meta, o.metaFile)
	if err != nil {
		return err
	}

	results := make([]touchResult, 0, len(paths))
	for _, path := range paths {
		// Legacy defaults the title to the (final-segment) slug when --title is empty.
		title := o.title
		if title == "" {
			segs := strings.Split(strings.Trim(path, "/"), "/")
			title = segs[len(segs)-1]
		}
		params := map[string]any{"path": path, "title": title}
		if o.description != "" {
			params["description"] = description
		}
		if o.specification != "" {
			params["specification"] = specification
		}
		if o.state != "" {
			params["state"] = o.state
		}
		if priority != 0 {
			params["priority"] = priority
		}
		if o.kind != "" {
			params["kind"] = o.kind
		}
		if parentTask != "" {
			params["parentTask"] = parentTask
		}
		if o.assignee != "" {
			params["assigneePrincipalRef"] = o.assignee
		}
		if o.requestedBy != "" {
			params["requestedBy"] = o.requestedBy
		}
		if o.assignedProject != "" {
			params["assignedProject"] = o.assignedProject
		}
		if o.resolution != "" {
			params["resolution"] = o.resolution
		}
		if o.labels != "" {
			var lbls []string
			if err := json.Unmarshal([]byte(o.labels), &lbls); err != nil {
				return fmt.Errorf("invalid --labels JSON array: %w", err)
			}
			params["labels"] = lbls
		}
		if metaSet {
			if metaRaw != nil {
				params["metaRaw"] = *metaRaw
			} else if metaValue != nil {
				params["meta"] = metaValue
			}
		}
		if actor != "" {
			params["actor"] = actor
		}
		if o.dueAt != "" {
			params["dueAt"] = o.dueAt
		}
		if o.startAt != "" {
			params["startAt"] = o.startAt
		}
		if o.forceUUID != "" {
			params["forceUuid"] = o.forceUUID
		}

		raw, err := tr.Call(cmd.Context(), "wrkq.task.create", params)
		if err != nil {
			// Legacy surfaces the raw store error (no domain-code prefix).
			if re, ok := err.(*Error); ok {
				return errors.New(re.Message)
			}
			return err
		}
		var dto struct {
			ID       string `json:"id"`
			UUID     string `json:"uuid"`
			Slug     string `json:"slug"`
			Title    string `json:"title"`
			State    string `json:"state"`
			Priority int    `json:"priority"`
			Kind     string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &dto); err != nil {
			return err
		}
		artifactDir, derr := ensureMirrorArtifactDir(dto.ID)
		if derr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to create artifact directory %s: %v\n", artifactDir, derr)
		}
		results = append(results, touchResult{
			ID: dto.ID, UUID: dto.UUID, Slug: dto.Slug, Path: path,
			Title: dto.Title, State: dto.State, Priority: dto.Priority, Kind: dto.Kind,
			ArtifactDir: artifactDir,
		})
	}

	if !o.json && isStdoutTTY(cmd.OutOrStdout()) {
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s (%s)\n", r.ID, r.Path)
		}
		return nil
	}
	return encodeJSONIndent(cmd, results)
}

// mirrorArtifactDir mirrors internal/cli taskArtifactDir (server-local host hint).
func mirrorArtifactDir(taskID string) string {
	root := os.Getenv("PRAESIDIUM_HOME")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, "praesidium")
		} else {
			root = "praesidium"
		}
	}
	return filepath.Join(root, "var", "wrkq-artifacts", taskID)
}

func ensureMirrorArtifactDir(taskID string) (string, error) {
	dir := mirrorArtifactDir(taskID)
	return dir, os.MkdirAll(dir, 0o755)
}
