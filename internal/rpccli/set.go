package rpccli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

// newSetCmd mirrors `wrkq set` for task field updates that map to
// wrkq.task.update.
func newSetCmd() *cobra.Command {
	var description, specification, outcome, state, title, slug, labels, meta, kind, assignee, dueAt, startAt string
	var parentTask, parentID, requestedBy, assignedProject, resolution, causedBy, campaign string
	var projectRoot string
	var metaFile string
	var priority, jobs, batchSize int
	var ifMatch int64
	var dryRun, continueOnError, ordered bool

	cmd := &cobra.Command{
		Use:     "set <path|id>... [flags]",
		Aliases: []string{"edit"},
		Short:   "Update task fields; outcome is curated, comments are raw, and completion never requires outcome",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("root") {
				return runSetProjectRoot(cmd, args, projectRoot, ifMatch, dryRun)
			}
			claims := &stdinClaims{}
			patch := map[string]any{}
			dryFields := map[string]any{}
			if state != "" {
				patch["state"] = state
				dryFields["state"] = state
			}
			if priority != 0 {
				patch["priority"] = priority
				dryFields["priority"] = priority
			}
			if title != "" {
				patch["title"] = title
				dryFields["title"] = title
			}
			if slug != "" {
				normalized, err := paths.NormalizeSlug(slug)
				if err != nil {
					return fmt.Errorf("invalid slug: %w", err)
				}
				newSlug, err := paths.NewSlug(normalized)
				if err != nil {
					return fmt.Errorf("invalid slug: %w", err)
				}
				patch["slug"] = string(newSlug)
				dryFields["slug"] = string(newSlug)
			}
			if description != "" {
				desc, err := readTextValue(description, "--description", cmd.InOrStdin(), claims)
				if err != nil {
					return fmt.Errorf("failed to read description: %w", err)
				}
				patch["description"] = desc
				dryFields["description"] = desc
			}
			if specification != "" {
				spec, err := readTextValue(specification, "--specification", cmd.InOrStdin(), claims)
				if err != nil {
					return fmt.Errorf("failed to read specification: %w", err)
				}
				patch["specification"] = spec
				dryFields["specification"] = spec
			}
			if cmd.Flags().Changed("outcome") {
				value, err := readNullableTextValue(outcome, "--outcome", cmd.InOrStdin(), claims)
				if err != nil {
					return fmt.Errorf("failed to read outcome: %w", err)
				}
				patch["outcome"] = value
				if strings.TrimSpace(value) == "" {
					dryFields["outcome"] = nil
				} else {
					dryFields["outcome"] = value
				}
			}
			if kind != "" {
				patch["kind"] = kind
				dryFields["kind"] = kind
			}
			parentChanged := false
			parentValue := ""
			switch {
			case cmd.Flags().Changed("parent-task") && cmd.Flags().Changed("parent-id"):
				return fmt.Errorf("specify only one of --parent-task or --parent-id")
			case cmd.Flags().Changed("parent-task"):
				parentChanged = true
				parentValue = parentTask
			case cmd.Flags().Changed("parent-id"):
				parentChanged = true
				parentValue = parentID
			}
			if assignee != "" {
				principalRef, err := attribution.NormalizeCompat(assignee)
				if err != nil {
					return fmt.Errorf("failed to resolve assignee: %w", err)
				}
				patch["assigneePrincipalRef"] = principalRef
				dryFields["assignee_principal_ref"] = principalRef
			}
			if requestedBy != "" {
				patch["requestedBy"] = requestedBy
				dryFields["requested_by_project_id"] = requestedBy
			}
			if assignedProject != "" {
				patch["assignedProject"] = assignedProject
				dryFields["assigned_project_id"] = assignedProject
			}
			if resolution != "" {
				patch["resolution"] = resolution
				dryFields["resolution"] = resolution
			}
			// caused_by: --caused-by '' clears, omitted leaves unchanged. The empty
			// slice (vs absent) signals an explicit clear to the server.
			if cmd.Flags().Changed("caused-by") {
				toks := splitCausedBy(causedBy)
				patch["causedBy"] = toks
				dryFields["caused_by"] = toks
			}
			if cmd.Flags().Changed("campaign") {
				patch["campaign"] = campaign
				if campaign == "" {
					dryFields["campaign_uuid"] = nil
				} else {
					dryFields["campaign"] = campaign
				}
			}
			if dueAt != "" {
				patch["dueAt"] = dueAt
				dryFields["due_at"] = dueAt
			}
			if startAt != "" {
				patch["startAt"] = startAt
				dryFields["start_at"] = startAt
			}
			if labels != "" {
				var lbls []string
				if err := json.Unmarshal([]byte(labels), &lbls); err != nil {
					return fmt.Errorf("invalid --labels JSON array: %w", err)
				}
				patch["labels"] = lbls
				dryFields["labels"] = labels
			}
			if meta != "" || metaFile != "" {
				_, raw, m, err := readMetaValue(meta, metaFile, cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				if raw != nil {
					patch["metaRaw"] = *raw
					dryFields["meta"] = *raw
				} else if m != nil {
					patch["meta"] = m
				}
			}
			if len(patch) == 0 {
				if !parentChanged {
					return fmt.Errorf("no updates specified")
				}
			}
			return runSet(cmd, args, setRunOpts{
				patch: patch, dryFields: dryFields, ifMatch: ifMatch,
				dryRun: dryRun, continueOnError: continueOnError, jobs: jobs,
				ordered:       ordered,
				parentChanged: parentChanged, parentValue: parentValue, kindChanged: kind != "",
				stdinClaims: claims,
			})
		},
	}
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only update if etag matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be changed without applying")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 1, "Number of parallel workers (0 = auto-detect CPU count)")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "Continue processing on errors")
	cmd.Flags().IntVar(&batchSize, "batch-size", 1, "Group operations into batches (not yet implemented)")
	cmd.Flags().BoolVar(&ordered, "ordered", false, "Preserve input order (disables parallelism)")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Update task description")
	cmd.Flags().StringVar(&specification, "specification", "", "Update task specification")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Set curated task outcome (use @file or - for stdin; blank clears)")
	cmd.Flags().StringVar(&state, "state", "", "Update task state")
	cmd.Flags().IntVar(&priority, "priority", 0, "Update task priority (1-4)")
	cmd.Flags().StringVar(&title, "title", "", "Update task title")
	cmd.Flags().StringVar(&slug, "slug", "", "Update task slug")
	cmd.Flags().StringVar(&labels, "labels", "", "Update task labels (JSON array)")
	cmd.Flags().StringVar(&meta, "meta", "", "Update task metadata (JSON object or null)")
	cmd.Flags().StringVar(&metaFile, "meta-file", "", "Load task metadata from file")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "Update task due date")
	cmd.Flags().StringVar(&startAt, "start-at", "", "Update task start date")
	cmd.Flags().StringVar(&kind, "kind", "", "Update task kind")
	cmd.Flags().StringVar(&parentTask, "parent-task", "", "Set parent task ID or path")
	cmd.Flags().StringVar(&parentID, "parent-id", "", "Alias for --parent-task")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Update task assignee")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "Update requester project ID")
	cmd.Flags().StringVar(&assignedProject, "assigned-project", "", "Update assignee project ID")
	cmd.Flags().StringVar(&resolution, "resolution", "", "Update task resolution")
	cmd.Flags().StringVar(&causedBy, "caused-by", "", "Replace causal lineage with comma-separated task IDs (empty string clears; omit to leave unchanged)")
	cmd.Flags().StringVar(&campaign, "campaign", "", "Enroll in an active campaign by ID or path (empty string unenrolls)")
	cmd.Flags().StringVar(&projectRoot, "root", "", "Set a top-level project's checkout root (stored as ~/... when under $HOME; empty clears; consumers expand it)")
	_ = cmd.Flags().MarkHidden("batch-size") // Accepted for compatibility; it has no behavior.
	_ = batchSize                            // Legacy accepts --batch-size but does not apply batching.
	return cmd
}

func runSetProjectRoot(cmd *cobra.Command, args []string, rawRoot string, ifMatch int64, dryRun bool) error {
	for _, name := range []string{
		"description", "specification", "outcome", "state", "priority", "title", "slug",
		"labels", "meta", "meta-file", "due-at", "start-at", "kind",
		"parent-task", "parent-id", "assignee", "requested-by", "assigned-project",
		"resolution", "caused-by", "campaign",
	} {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--root cannot be combined with task field --%s", name)
		}
	}
	if len(args) != 1 {
		return fmt.Errorf("--root requires exactly one project")
	}
	project := strings.TrimSpace(args[0])
	if project == "" || project == "-" {
		return fmt.Errorf("--root requires a project slug, ID, or UUID")
	}
	if parsed := selectors.Parse(project); parsed.Type == selectors.TypeTask || strings.HasPrefix(parsed.Token, "T-") {
		return fmt.Errorf("--root can only be set on a top-level project; task ID %q is not a project", project)
	}

	root, err := normalizeRegisteredProjectRoot(rawRoot)
	if err != nil {
		return err
	}
	if dryRun {
		return encodeJSONIndent(cmd, map[string]any{
			"dry_run": true,
			"project": project,
			"root":    root,
		})
	}

	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	params := map[string]any{"project": project, "root": root}
	if actor != "" {
		params["actor"] = actor
	}
	if ifMatch != 0 {
		params["expectEtag"] = ifMatch
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.project.setRoot", params)
	if err != nil {
		if re, ok := err.(*Error); ok {
			return errors.New(re.Message)
		}
		return err
	}
	var updated projectEntry
	if err := json.Unmarshal(raw, &updated); err != nil {
		return err
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		if updated.Root == nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Cleared project root for %s\n", updated.Slug)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Set project root for %s to %s\n", updated.Slug, *updated.Root)
		}
		return nil
	}
	return encodeJSONIndent(cmd, map[string]any{
		"total": 1, "succeeded": 1, "failed": 0, "errors": []any{},
	})
}

// normalizeRegisteredProjectRoot converts relative paths and paths beneath the
// caller's HOME into host-portable ~/... strings. Absolute paths outside HOME
// remain absolute. The server stores this result verbatim.
func normalizeRegisteredProjectRoot(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve $HOME for --root: %w", err)
	}
	var absolute string
	switch {
	case value == "~":
		absolute = home
	case strings.HasPrefix(value, "~/"):
		absolute = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	case strings.HasPrefix(value, "~"):
		return "", fmt.Errorf("--root supports only the current user's ~ prefix")
	case filepath.IsAbs(value):
		absolute = value
	default:
		absolute, err = filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("normalize --root: %w", err)
		}
	}
	absolute = filepath.Clean(absolute)
	home = filepath.Clean(home)
	rel, err := filepath.Rel(home, absolute)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if rel == "." {
			return "~", nil
		}
		return "~/" + filepath.ToSlash(rel), nil
	}
	return absolute, nil
}

type setRunOpts struct {
	patch           map[string]any
	dryFields       map[string]any
	ifMatch         int64
	dryRun          bool
	continueOnError bool
	jobs            int
	ordered         bool
	parentChanged   bool
	parentValue     string
	kindChanged     bool
	stdinClaims     *stdinClaims
}

func runSet(cmd *cobra.Command, args []string, opts setRunOpts) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}

	if len(args) == 1 && args[0] == "-" {
		if err := opts.stdinClaims.claim("task refs"); err != nil {
			return err
		}
		if isReaderTTY(cmd.InOrStdin()) {
			return fmt.Errorf("stdin is a terminal; pipe input or use a heredoc")
		}
		scanner := bufio.NewScanner(cmd.InOrStdin())
		args = nil
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				args = append(args, line)
			}
		}
		if serr := scanner.Err(); serr != nil {
			return fmt.Errorf("failed to read from stdin: %w", serr)
		}
		if len(args) == 0 {
			return fmt.Errorf("no tasks specified")
		}
	}

	scopedArgs := make([]string, 0, len(args))
	for _, ref := range args {
		scopedArgs = append(scopedArgs, sc.selector(ref, false))
	}

	if opts.parentChanged {
		parentRef := strings.TrimSpace(opts.parentValue)
		if parentRef == "" {
			opts.patch["parentTask"] = ""
			opts.dryFields["parent_task_uuid"] = nil
			if !opts.kindChanged {
				opts.dryFields["kind"] = "task"
			}
		} else {
			scopedParent := sc.selector(parentRef, false)
			opts.patch["parentTask"] = scopedParent
			if opts.dryRun {
				show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": scopedParent})
				if err != nil {
					return fmt.Errorf("failed to resolve parent task: %w", err)
				}
				var parent struct {
					UUID string `json:"uuid"`
				}
				if err := json.Unmarshal(show, &parent); err != nil {
					return err
				}
				opts.dryFields["parent_task_uuid"] = parent.UUID
			}
			if !opts.kindChanged {
				opts.dryFields["kind"] = "subtask"
			}
		}
	}

	if opts.dryRun {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return encodeJSONIndent(cmd, map[string]interface{}{
				"dry_run": true,
				"targets": scopedArgs,
				"fields":  opts.dryFields,
			})
		}
		for _, ref := range scopedArgs {
			fmt.Fprintf(cmd.OutOrStdout(), "Would update task %s: %+v\n", ref, opts.dryFields)
		}
		return nil
	}

	op := &bulk.Operation{
		Jobs:            opts.jobs,
		ContinueOnError: opts.continueOnError,
		Ordered:         opts.ordered,
		ShowProgress:    isStdoutTTY(cmd.OutOrStdout()),
	}
	result := op.Execute(scopedArgs, func(ref string) error {
		params := map[string]any{"task": ref, "patch": opts.patch}
		if actor != "" {
			params["actor"] = actor
		}
		if opts.ifMatch != 0 {
			params["expectEtag"] = opts.ifMatch
		}
		if state, ok := opts.patch["state"].(string); ok && state == "completed" {
			if token := strings.TrimSpace(os.Getenv("WRKQ_CLAIM_TOKEN")); token != "" {
				params["claimToken"] = token
			}
			if generation, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("WRKQ_CLAIM_GENERATION")), 10, 64); generation > 0 {
				params["claimGeneration"] = generation
			}
			if runtimeScope := resolvedRuntimeScope(); runtimeScope != nil && runtimeScope.TaskID != "" {
				params["claimScope"] = runtimeScope.FullRef()
			}
		}
		_, err := tr.Call(cmd.Context(), "wrkq.task.update", params)
		if err != nil {
			if re, ok := err.(*Error); ok {
				if re.DomainID == "WRKQ_CLAIM_SUPERSEDED" {
					return formatClaimRPCError(re)
				}
				return errors.New(re.Message)
			}
			return err
		}
		return nil
	})

	if !isStdoutTTY(cmd.OutOrStdout()) {
		errorsOut := make([]map[string]string, len(result.Errors))
		for i, itemErr := range result.Errors {
			errorsOut[i] = map[string]string{
				"item":  itemErr.Item,
				"error": itemErr.Error.Error(),
			}
		}
		if err := encodeJSONIndent(cmd, map[string]interface{}{
			"total": result.TotalItems, "succeeded": result.Succeeded, "failed": result.Failed, "errors": errorsOut,
		}); err != nil {
			return err
		}
	} else {
		result.PrintSummary(cmd.OutOrStdout())
	}
	if result.ExitCode() != 0 {
		if len(result.Errors) == 1 {
			return result.Errors[0].Error
		}
		return fmt.Errorf("%d task updates failed", result.Failed)
	}
	return nil
}
