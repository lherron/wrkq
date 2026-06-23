package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/parse"
	"github.com/spf13/cobra"
)

// newApplyCmd mirrors `wrkq apply <PATHSPEC|ID> <FILE|->`. The CLI owns ALL the
// human-facing work — reading the file/stdin, format detection + parse, the
// metadata-gating warning, the empty/size validation, the legacy etag precheck,
// dry-run rendering, and project-root scoping — then sends a single
// wrkq.task.update patch over the RPC boundary. The server never sees the file,
// the format, or the --with-metadata gate; it only receives the resolved patch.
func newApplyCmd() *cobra.Command {
	var format string
	var withMetadata, dryRun bool
	var ifMatch int64

	cmd := &cobra.Command{
		Use:   "apply <PATHSPEC|ID> <FILE|->",
		Short: "Update task description/specification from file or stdin",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(cmd, args, format, withMetadata, dryRun, ifMatch)
		},
	}
	cmd.Flags().StringVar(&format, "format", "", "Input format: md, yaml, json (auto-detected if not specified)")
	cmd.Flags().BoolVar(&withMetadata, "with-metadata", false, "Update metadata fields (title, state, priority, due_at) in addition to description/specification")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only apply if etag matches (0 = no check)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be changed without applying")
	return cmd
}

func runApply(cmd *cobra.Command, args []string, format string, withMetadata, dryRun bool, ifMatch int64) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToSelector(args[0], false) then ResolveTask.
	taskID := sc.selector(args[0], false)
	show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		if re, ok := err.(*Error); ok {
			// Legacy wraps the resolver error as "failed to resolve task: <err>".
			// The RPC NOT_FOUND message ("task not found: <ref>") byte-matches the
			// legacy selectors.ResolveTask text, so the wrapped string is identical.
			return fmt.Errorf("failed to resolve task: %s", re.Message)
		}
		return fmt.Errorf("failed to resolve task: %w", err)
	}
	var current struct {
		UUID string `json:"uuid"`
		ID   string `json:"id"`
		ETag int64  `json:"etag"`
	}
	if err := json.Unmarshal(show, &current); err != nil {
		return err
	}
	taskUUID, friendlyID := current.UUID, current.ID

	// Determine input source (mirror owns file/stdin reading — never the server).
	var input io.Reader
	var inputSource string
	if args[1] == "-" {
		input = cmd.InOrStdin()
		inputSource = "stdin"
	} else {
		file, err := os.Open(args[1])
		if err != nil {
			absPath, _ := os.Getwd()
			if absPath != "" {
				absPath = absPath + "/" + args[1]
			} else {
				absPath = args[1]
			}
			return fmt.Errorf("failed to open file %s: %w", absPath, err)
		}
		defer func() { _ = file.Close() }()
		input = file
		inputSource = args[1]
	}

	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("input is empty (from %s)", inputSource)
	}

	const (
		warnSize  = 1 * 1024 * 1024
		errorSize = 10 * 1024 * 1024
	)
	if len(data) > errorSize {
		return fmt.Errorf("input too large: %d bytes (max %d bytes)", len(data), errorSize)
	}
	if len(data) > warnSize {
		fmt.Fprintf(os.Stderr, "Warning: Large input detected (%d bytes). This may take a moment to process.\n", len(data))
	}

	updates, err := parse.Parse(data, format)
	if err != nil {
		preview := string(data)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		lines := strings.Split(preview, "\n")
		if len(lines) > 5 {
			lines = lines[:5]
		}
		previewText := strings.Join(lines, "\n")
		return fmt.Errorf("failed to parse input: %w\n\nFirst few lines of input:\n%s", err, previewText)
	}

	// Metadata gating: without --with-metadata the metadata fields are dropped
	// (and warned about), so only description/specification reach the patch.
	if !withMetadata {
		hasMetadata := updates.Title != nil || updates.State != nil || updates.Priority != nil || updates.DueAt != nil
		if hasMetadata {
			if os.Getenv("WRKQ_SUPPRESS_METADATA_WARNING") == "" {
				fmt.Fprintf(os.Stderr, "Warning: Metadata ignored without --with-metadata. Use 'wrkq set %s' for quick updates.\n", taskID)
			}
			updates.Title = nil
			updates.State = nil
			updates.Priority = nil
			updates.DueAt = nil
		}
	}

	if !withMetadata && updates.Description == nil && updates.Specification == nil {
		return fmt.Errorf("no description or specification found in input (body-only mode requires description or specification)")
	}

	// Legacy etag precheck owned by the CLI (distinct error text); the patch call
	// itself does NOT send expectEtag so this message stays the source of truth.
	if ifMatch > 0 && current.ETag != ifMatch {
		return fmt.Errorf("etag mismatch: task was modified (expected etag %d, current etag %d). Use --if-match %d to update, or --if-match 0 to force",
			ifMatch, current.ETag, current.ETag)
	}

	if dryRun {
		return renderApplyDryRun(cmd, friendlyID, taskUUID, updates, withMetadata)
	}

	return execApply(cmd, tr, actor, taskUUID, updates, !withMetadata, ifMatch)
}

func renderApplyDryRun(cmd *cobra.Command, friendlyID, taskUUID string, updates *parse.TaskUpdate, withMetadata bool) error {
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, map[string]interface{}{
			"dry_run":   true,
			"id":        friendlyID,
			"uuid":      taskUUID,
			"updates":   updates,
			"body_only": !withMetadata,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Would update task %s (%s)\n", friendlyID, taskUUID)
	if updates.Description != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "  description: %s\n", *updates.Description)
	}
	if updates.Specification != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "  specification: %s\n", *updates.Specification)
	}
	if withMetadata {
		if updates.Title != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  title: %s\n", *updates.Title)
		}
		if updates.State != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  state: %s\n", *updates.State)
		}
		if updates.Priority != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  priority: %d\n", *updates.Priority)
		}
		if updates.DueAt != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  due_at: %s\n", *updates.DueAt)
		}
	}
	return nil
}

func execApply(cmd *cobra.Command, tr Transport, actor, taskUUID string, updates *parse.TaskUpdate, bodyOnly bool, ifMatch int64) error {
	// fields preserves the legacy OUTPUT shape (snake_case keys, marshaled in the
	// "fields" object). patch is the camelCase wrkq.task.update wire shape.
	fields := map[string]interface{}{}
	patch := map[string]any{}
	if bodyOnly {
		if updates.Description != nil {
			fields["description"] = *updates.Description
			patch["description"] = *updates.Description
		}
		if updates.Specification != nil {
			fields["specification"] = *updates.Specification
			patch["specification"] = *updates.Specification
		}
		if len(fields) == 0 {
			return fmt.Errorf("no description or specification provided in --body-only mode")
		}
	} else {
		if updates.Title != nil {
			fields["title"] = *updates.Title
			patch["title"] = *updates.Title
		}
		if updates.State != nil {
			fields["state"] = *updates.State
			patch["state"] = *updates.State
		}
		if updates.Priority != nil {
			fields["priority"] = *updates.Priority
			patch["priority"] = *updates.Priority
		}
		if updates.DueAt != nil {
			fields["due_at"] = *updates.DueAt
			patch["dueAt"] = *updates.DueAt
		}
		if updates.Description != nil {
			fields["description"] = *updates.Description
			patch["description"] = *updates.Description
		}
		if updates.Specification != nil {
			fields["specification"] = *updates.Specification
			patch["specification"] = *updates.Specification
		}
	}
	if len(fields) == 0 {
		return fmt.Errorf("no fields to update")
	}

	params := map[string]any{"task": taskUUID, "patch": patch}
	if actor != "" {
		params["actor"] = actor
	}
	if ifMatch > 0 {
		params["expectEtag"] = ifMatch
	}
	if _, err := tr.Call(cmd.Context(), "wrkq.task.update", params); err != nil {
		if re, ok := err.(*Error); ok {
			return fmt.Errorf("failed to update task: %s", re.Message)
		}
		return fmt.Errorf("failed to update task: %w", err)
	}

	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, map[string]interface{}{
			"uuid":    taskUUID,
			"updated": true,
			"fields":  fields,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated task %s\n", taskUUID)
	return nil
}
