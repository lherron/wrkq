package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// newCommentCmd mirrors `wrkq comment`. Only `add` is implemented so far (via
// wrkq.comment.add); ls/cat/rm are pending (ls needs the list-view projection
// ruling, see docs/rpc-cli-migration.md).
func newCommentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage task comments"}
	cmd.AddCommand(newCommentAddCmd())
	cmd.AddCommand(newCommentCatCmd())
	cmd.AddCommand(newCommentLsCmd())
	cmd.AddCommand(newCommentRmCmd())
	return cmd
}

func newCommentLsCmd() *cobra.Command {
	var asJSON, ndjson, asYAML, tsv, human, porcelain, includeDeleted, reverse bool
	var limit int
	var cursorTok, sort string
	cmd := &cobra.Command{
		Use:   "ls <task>...",
		Short: "List comments for task(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, stable, err := resolveCommentLsMode(cmd, asJSON, ndjson, asYAML, tsv, human, porcelain)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: for each task arg, applyProjectRootToSelector(taskRef, false)
			// after stripping the t: prefix (scoping preserves the prefix). Project
			// root scoping is CALLER semantics — done here, never in the RPC method.
			scoped := make([]string, len(args))
			for i, a := range args {
				scoped[i] = strings.TrimPrefix(sc.selector(strings.TrimPrefix(a, "t:"), false), "t:")
			}

			params := map[string]any{"tasks": scoped}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			if includeDeleted {
				params["includeDeleted"] = true
			}
			if sort != "" {
				params["sort"] = sort
			}
			if reverse {
				params["desc"] = true
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.comment.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" && len(args) == 1 {
					containerParams := map[string]any{"container": scoped[0]}
					if limit > 0 {
						containerParams["limit"] = limit
					}
					if cursorTok != "" {
						containerParams["cursor"] = cursorTok
					}
					if includeDeleted {
						containerParams["includeDeleted"] = true
					}
					containerRaw, containerErr := tr.Call(cmd.Context(), "wrkq.comment.list", containerParams)
					if containerErr == nil {
						items, nextCursor, decodeErr := containerCommentListItems(containerRaw)
						if decodeErr != nil {
							return decodeErr
						}
						if stable && nextCursor != "" {
							fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", nextCursor)
						}
						return renderCommentLs(cmd, mode, stable, items)
					}
					if containerRPCError, ok := containerErr.(*Error); ok {
						return errors.New(containerRPCError.Message)
					}
					return containerErr
				}
				if re, ok := err.(*Error); ok {
					if re.DomainID == "WRKQ_NOT_FOUND" {
						// Map the failing scoped selector back to the original arg.
						failed := strings.TrimPrefix(re.Message, "task not found: ")
						orig := failed
						for i, s := range scoped {
							if s == failed {
								orig = args[i]
								break
							}
						}
						return fmt.Errorf("failed to resolve task %s: task not found: %s", orig, failed)
					}
					// Legacy surfaces the raw error (e.g. malformed cursor) without
					// a domain-code prefix.
					return errors.New(re.Message)
				}
				return err
			}
			var res struct {
				Items      []json.RawMessage `json:"items"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			// Legacy prints next_cursor to stderr (Stable/porcelain only) BEFORE
			// rendering rows to stdout; match that ordering.
			if stable && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}
			return renderCommentLs(cmd, mode, stable, res.Items)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as NDJSON")
	cmd.Flags().BoolVar(&asYAML, "yaml", false, "Output as YAML")
	cmd.Flags().BoolVar(&tsv, "tsv", false, "Output as TSV")
	cmd.Flags().BoolVar(&human, "human", false, "Human-readable output")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "Include soft-deleted comments")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results (0 = no limit)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	cmd.Flags().StringVar(&sort, "sort", "", "Sort field")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	return cmd
}

func containerCommentListItems(raw json.RawMessage) ([]json.RawMessage, string, error) {
	var response struct {
		Items []struct {
			UUID                  string         `json:"uuid"`
			ID                    string         `json:"id"`
			Container             string         `json:"container"`
			Kind                  string         `json:"kind"`
			Body                  string         `json:"body"`
			Meta                  map[string]any `json:"meta"`
			ETag                  int64          `json:"etag"`
			CreatedAt             string         `json:"createdAt"`
			UpdatedAt             string         `json:"updatedAt"`
			DeletedAt             string         `json:"deletedAt"`
			CreatedByPrincipalRef string         `json:"createdByPrincipalRef"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, "", err
	}
	items := make([]json.RawMessage, 0, len(response.Items))
	for _, item := range response.Items {
		projection := map[string]any{
			"uuid": item.UUID, "id": item.ID, "container_id": item.Container,
			"body": item.Body, "meta": item.Meta, "etag": item.ETag,
			"created_at": item.CreatedAt,
		}
		if item.Kind != "" {
			projection["kind"] = item.Kind
		}
		if item.UpdatedAt != "" {
			projection["updated_at"] = item.UpdatedAt
		}
		if item.DeletedAt != "" {
			projection["deleted_at"] = item.DeletedAt
		}
		if item.CreatedByPrincipalRef != "" {
			projection["created_by_principal_ref"] = item.CreatedByPrincipalRef
		}
		encoded, err := json.Marshal(projection)
		if err != nil {
			return nil, "", err
		}
		items = append(items, encoded)
	}
	return items, response.NextCursor, nil
}

// resolveCommentLsMode reproduces legacy resolveOutputMode for `comment ls`
// (shape=List, Allow=[table,human,json,ndjson,yaml,tsv], DefaultTTY=table). It
// returns the rendering mode and whether it is the Stable (porcelain) variant.
// Note legacy maps --human onto the table renderer (no dedicated human branch).
func resolveCommentLsMode(cmd *cobra.Command, asJSON, ndjson, asYAML, tsv, human, porcelain bool) (mode string, stable bool, err error) {
	count := 0
	var explicit string
	set := func(m string) {
		explicit = m
		count++
	}
	if asJSON {
		set("json")
	}
	if ndjson {
		set("ndjson")
	}
	if human {
		set("table") // legacy: human shape has no renderer → falls through to table
	}
	if asYAML {
		set("yaml")
	}
	if tsv {
		set("tsv")
	}
	if count > 1 {
		return "", false, fmt.Errorf("choose only one output mode")
	}
	stable = porcelain
	if explicit != "" {
		return explicit, stable, nil
	}
	if stable { // bare --porcelain → canonical machine mode for a list = ndjson
		return "ndjson", true, nil
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		m := strings.ToLower(strings.TrimSpace(outF.Value.String()))
		switch m {
		case "json", "ndjson", "yaml", "tsv", "table":
			return m, false, nil
		case "human":
			return "table", false, nil
		case "porcelain":
			return "ndjson", true, nil
		case "raw":
			// raw is not in comment ls's allowed set → legacy's exact error.
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "table", false, nil // DefaultTTY=table
	}
	return "ndjson", false, nil // non-TTY default for a list
}

// commentLsTableHeaders matches legacy's `comment ls` table/tsv header row.
var commentLsTableHeaders = []string{"ID", "Task", "Author", "Created", "Body Preview"}

// renderCommentLs renders the comment rows in the resolved mode, byte-matching
// legacy. JSON/NDJSON emit the raw compat items; yaml/tsv/table reconstruct the
// legacy map projection (alphabetical keys for yaml; the 5-column preview for
// tsv/table).
func renderCommentLs(cmd *cobra.Command, mode string, stable bool, items []json.RawMessage) error {
	out := cmd.OutOrStdout()
	switch mode {
	case "json":
		// Legacy: json.NewEncoder(SetIndent unless Stable).Encode(allComments).
		var data []byte
		var err error
		if stable {
			data, err = json.Marshal(items)
		} else {
			data, err = json.MarshalIndent(items, "", "  ")
		}
		if err != nil {
			return err
		}
		_, err = out.Write(append(data, '\n'))
		return err
	case "ndjson":
		for _, it := range items {
			if _, err := out.Write(append([]byte(it), '\n')); err != nil {
				return err
			}
		}
		return nil
	}

	// yaml/tsv/table: reconstruct the legacy []map projection.
	comments := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		var m map[string]interface{}
		if err := json.Unmarshal(it, &m); err != nil {
			return err
		}
		comments = append(comments, m)
	}

	if mode == "yaml" {
		// Legacy renders allComments directly; yaml sorts map keys alphabetically,
		// matching the compat struct's alphabetical json tags.
		return render.NewRenderer(out, render.Options{Format: render.FormatYAML}).RenderYAML(comments)
	}

	rowsData := make([][]string, 0, len(comments))
	for _, c := range comments {
		body, _ := c["body"].(string)
		bodyPreview := strings.ReplaceAll(body, "\n", " ")
		if len(bodyPreview) > 50 {
			bodyPreview = bodyPreview[:47] + "..."
		}
		author := ""
		if ref, ok := c["created_by_principal_ref"].(string); ok {
			author = attribution.PrincipalHandle(ref)
		}
		rowsData = append(rowsData, []string{
			str(c["id"]), commentParentID(c), author, str(c["created_at"]), bodyPreview,
		})
	}
	if mode == "tsv" {
		return render.NewRenderer(out, render.Options{Format: render.FormatTSV}).RenderTSV(commentLsTableHeaders, rowsData)
	}
	// table (or human → table)
	return render.NewRenderer(out, render.Options{Porcelain: stable}).RenderTable(commentLsTableHeaders, rowsData)
}

func commentParentID(comment map[string]interface{}) string {
	if taskID := str(comment["task_id"]); taskID != "" {
		return taskID
	}
	return str(comment["container_id"])
}

// str renders a map value as legacy's `.(string)` extraction would (legacy panics
// on a nil/missing value; here we degrade to "" so multi-task table rendering
// stays defined while still byte-matching whenever the field is present).
func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

// commentCatProjection decodes only the fields the mirror needs to re-render the
// legacy raw/human comment-cat shapes. It is the same server-owned compat
// projection used for --json (wrkq.comment.catView); we keep the raw bytes for
// the json/ndjson paths so those stay byte-identical to legacy's map marshal.
type commentCatProjection struct {
	ID                    string `json:"id"`
	CreatedAt             string `json:"created_at"`
	CreatedByPrincipalRef string `json:"created_by_principal_ref"`
	TaskID                string `json:"task_id"`
	Body                  string `json:"body"`
}

func newCommentCatCmd() *cobra.Command {
	var asJSON, ndjson, raw bool
	cmd := &cobra.Command{
		Use:   "cat <comment-id|c:token>...",
		Short: "Print comment details",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Legacy comment cat output selection (internal/rpccli/comment.go):
			//   json   := --json OR (non-TTY AND !raw AND !ndjson)
			//   ndjson := --ndjson
			//   human  := default TTY (header+body) / --raw (body only), `---`-joined
			// --json wins over --ndjson which wins over raw/human; mirror matches.
			jsonMode := asJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !raw && !ndjson)

			// comment cat takes comment IDs / c: tokens, not paths — no project-root
			// scoping applies (the scoper is intentionally unused here).
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// raws keeps the compact server DTO bytes (json/ndjson re-marshal them
			// byte-for-byte); projs decodes the fields raw/human rendering needs.
			raws := make([]json.RawMessage, 0, len(args))
			projs := make([]commentCatProjection, 0, len(args))
			for _, orig := range args {
				ref := strings.TrimPrefix(orig, "c:")
				out, err := tr.Call(cmd.Context(), "wrkq.comment.catView", map[string]string{"comment": ref})
				if err != nil {
					if re, ok := err.(*Error); ok {
						// Legacy reports the ORIGINAL arg (incl. any c: prefix), not the
						// stripped ref, in both error messages.
						switch re.DomainID {
						case "WRKQ_NOT_FOUND":
							return fmt.Errorf("comment not found: %s", orig)
						case "WRKQ_VALIDATION":
							return fmt.Errorf("invalid comment reference: %s (expected friendly ID like C-00001 or UUID)", orig)
						}
						return errors.New(re.Message)
					}
					return err
				}
				raws = append(raws, out)
				var p commentCatProjection
				if err := json.Unmarshal(out, &p); err != nil {
					return err
				}
				projs = append(projs, p)
			}

			out := cmd.OutOrStdout()
			if jsonMode {
				// Legacy renders all comments as one indented JSON array.
				data, err := json.MarshalIndent(raws, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}
			if ndjson {
				// Legacy marshals each comment map compact, one per line. The compat
				// DTO declares fields in alphabetical json-tag order so the server's
				// compact bytes equal legacy's map marshal byte-for-byte.
				for _, r := range raws {
					if _, err := out.Write(append([]byte(r), '\n')); err != nil {
						return err
					}
				}
				return nil
			}
			// Human-readable: `---` between entries; --raw prints body only, otherwise
			// a header line + blank line + body.
			for i, p := range projs {
				if i > 0 {
					fmt.Fprintln(out, "---")
				}
				if raw {
					fmt.Fprintln(out, p.Body)
					continue
				}
				fmt.Fprintf(out, "[%s] [%s] %s - Task: %s\n",
					p.ID, p.CreatedAt, attribution.PrincipalHandle(p.CreatedByPrincipalRef), p.TaskID)
				fmt.Fprintln(out)
				fmt.Fprintln(out, p.Body)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&raw, "raw", false, "Print raw comment bodies")
	return cmd
}

func newCommentAddCmd() *cobra.Command {
	var asJSON bool
	var message, kind, meta string
	cmd := &cobra.Command{
		Use:   "add <task-or-container> [comment-text]",
		Short: "Add a comment to a task or container",
		Long: `Add a comment to a task or container.

Comment text can be supplied with -m/--message or as a positional argument.
Use '-' as the comment-text argument to read the body from stdin. If
comment-text is omitted, the body is also read from stdin.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommentAdd(cmd, args, message, kind, meta)
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Comment text")
	cmd.Flags().StringVar(&kind, "kind", "", "Judgment kind: blocker, decision, postmortem, or digest")
	cmd.Flags().StringVar(&meta, "meta", "", "Comment metadata as a JSON object")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func runCommentAdd(cmd *cobra.Command, args []string, message, kind, meta string) error {
	target := args[0]
	var kindValue *string
	if cmd.Flags().Changed("kind") {
		kindValue = &kind
		if err := domain.ValidateCommentKind(kindValue); err != nil {
			return err
		}
	}
	var metaValue map[string]any
	if cmd.Flags().Changed("meta") {
		if err := json.Unmarshal([]byte(meta), &metaValue); err != nil || metaValue == nil {
			if err != nil {
				return fmt.Errorf("invalid --meta JSON object: %w", err)
			}
			return fmt.Errorf("invalid --meta JSON object: expected an object")
		}
	}
	var body string
	claims := &stdinClaims{}
	if message != "" && len(args) > 1 {
		return fmt.Errorf("only one comment source allowed: use -m, positional argument, or stdin ('-')")
	}
	if message != "" {
		var err error
		body, err = readTextValue(message, "-m/--message", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
	} else if len(args) > 1 {
		var err error
		body, err = readTextValue(args[1], "comment text", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
	} else {
		b, err := readStdinValue("comment body", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
		body = string(b)
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

	// Task and container paths share the same caller-owned project-root scoping.
	target = sc.selector(target, false)
	params := map[string]any{"task": target, "body": body}
	if kindValue != nil {
		params["kind"] = *kindValue
	}
	if metaValue != nil {
		params["meta"] = metaValue
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.comment.add", params)
	parentKind := "task"
	if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
		delete(params, "task")
		params["container"] = target
		raw, err = tr.Call(cmd.Context(), "wrkq.comment.add", params)
		parentKind = "container"
	}
	if err != nil {
		if re, ok := err.(*Error); ok {
			return errors.New(re.Message)
		}
		return err
	}
	var dto struct {
		ID                    string `json:"id"`
		UUID                  string `json:"uuid"`
		Task                  string `json:"task"`
		Container             string `json:"container"`
		ETag                  int64  `json:"etag"`
		CreatedAt             string `json:"createdAt"`
		CreatedByPrincipalRef string `json:"createdByPrincipalRef"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		return err
	}

	output := map[string]interface{}{
		"id":            dto.ID,
		"uuid":          dto.UUID,
		"principal_ref": dto.CreatedByPrincipalRef,
		"created_at":    dto.CreatedAt,
		"etag":          dto.ETag,
	}
	if parentKind == "container" {
		output["container_id"] = dto.Container
	} else {
		output["task_id"] = dto.Task
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, output)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Comment created: %s\n", dto.ID)
	return nil
}
