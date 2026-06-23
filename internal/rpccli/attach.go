package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// newAttachCmd mirrors `wrkq attach`. `ls` is RPC-backed via the server-owned
// wrkq.attachment.listView compat list projection (cursor-paginated, DB-only).
// `put` (real-file) and `rm` are RPC-backed via wrkq.attachment.add /
// wrkq.attachment.remove — the attachment file BYTES are read/written by the
// server from the configured (server-local) attach dir, which is exactly the
// "server-local host-path hint" model: the mirror sends the host path string,
// never the bytes. `get` and `put` FROM STDIN are deliberately NOT implemented:
// they require a byte-return / byte-upload over the RPC boundary, which is an
// open design decision (see docs/rpc-cli-migration.md "attach get/stdin gap").
func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "attach", Short: "Manage task attachments"}
	cmd.AddCommand(newAttachLsCmd())
	cmd.AddCommand(newAttachPutCmd())
	cmd.AddCommand(newAttachGetCmd())
	cmd.AddCommand(newAttachRmCmd())
	return cmd
}

func newAttachLsCmd() *cobra.Command {
	var asJSON, ndjson, porcelain bool
	var limit int
	var cursorTok string
	cmd := &cobra.Command{
		Use:   "ls <task>",
		Short: "List attachments for a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !asJSON && !ndjson && !porcelain && isStdoutTTY(cmd.OutOrStdout()) {
				return fmt.Errorf("attach ls: only --json / --ndjson / --porcelain / non-TTY output is implemented so far")
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			// Legacy: applyProjectRootToSelector(taskRef, false).
			params := map[string]any{"task": sc.selector(args[0], false)}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.attachment.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					return fmt.Errorf("%s", re.Message)
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
			// Legacy prints next_cursor to stderr (porcelain only) BEFORE rows.
			if porcelain && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.MarshalIndent(res.Items, "", "  ")
				if err != nil {
					return err
				}
				if _, err := out.Write(append(data, '\n')); err != nil {
					return err
				}
			} else {
				for _, it := range res.Items {
					if _, err := out.Write(append([]byte(it), '\n')); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	return cmd
}

// newAttachPutCmd mirrors `wrkq attach put <task> <file>`. The host file path is
// sent to wrkq.attachment.add; the server reads the bytes and writes them into
// the (server-local) attach dir. Reading FROM STDIN ('-') is a byte-upload over
// the RPC boundary and is hard-gated as an open design decision.
func newAttachPutCmd() *cobra.Command {
	var mime, name string
	cmd := &cobra.Command{
		Use:   "put <task> <file|-|>",
		Short: "Attach a file to a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcPath := args[1]
			if srcPath == "-" {
				// Legacy reads the bytes from stdin and writes them server-side; over
				// the RPC seam this is a byte-UPLOAD with no host path. Pending a
				// daedalus ruling on bytes-over-RPC vs a staged host path.
				return errors.New("attach put: reading attachment bytes from stdin is not implemented in wrkq-rpccli (bytes-over-RPC upload is an open design decision)")
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			actor := actorFlag(cmd)

			// Legacy: applyProjectRootToSelector(taskRef, false). The source FILE path
			// is a host-local filesystem path, NOT a selector — never project-scoped.
			params := map[string]any{
				"task": sc.selector(args[0], false),
				"path": srcPath,
			}
			if name != "" {
				params["filename"] = name
			}
			if mime != "" {
				params["mimeType"] = mime
			}
			if actor != "" {
				params["actor"] = actor
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.attachment.add", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					// Legacy surfaces the raw resolve / validation text (no domain code).
					return errors.New(re.Message)
				}
				return err
			}

			// The server WrkqAttachment DTO is camelCase; legacy `attach put` prints a
			// snake_case map. Re-project so bytes match. Legacy's map marshal sorts keys
			// alphabetically (filename, id, mime_type, relative_path, size_bytes,
			// task_id, task_uuid, uuid).
			var dto struct {
				UUID         string `json:"uuid"`
				ID           string `json:"id"`
				TaskUUID     string `json:"taskUuid"`
				Filename     string `json:"filename"`
				RelativePath string `json:"relativePath"`
				MimeType     string `json:"mimeType"`
				SizeBytes    int64  `json:"sizeBytes"`
			}
			if err := json.Unmarshal(raw, &dto); err != nil {
				return err
			}
			// Legacy reports task_id as the friendly task ID (T-…). The DTO carries
			// only taskUuid, so resolve the friendly id via task.show.
			taskID, err := attachTaskID(cmd, tr, dto.TaskUUID)
			if err != nil {
				return err
			}
			out := map[string]interface{}{
				"id":            dto.ID,
				"uuid":          dto.UUID,
				"task_id":       taskID,
				"task_uuid":     dto.TaskUUID,
				"filename":      dto.Filename,
				"relative_path": dto.RelativePath,
				"mime_type":     dto.MimeType,
				"size_bytes":    dto.SizeBytes,
			}
			if !isStdoutTTY(cmd.OutOrStdout()) {
				return encodeJSONIndent(cmd, out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Attached: %s (%s, %d bytes)\n", dto.ID, dto.Filename, dto.SizeBytes)
			return nil
		},
	}
	cmd.Flags().StringVar(&mime, "mime", "", "MIME type (auto-detected if not specified)")
	cmd.Flags().StringVar(&name, "name", "", "Filename (defaults to basename of file)")
	return cmd
}

// newAttachGetCmd is a hard-gate: `attach get` reads attachment BYTES back out of
// the server-local attach dir and writes them to stdout/--as. There is no RPC
// method that returns bytes (or a guaranteed-reachable host path) to the client,
// and `artifact_dir` is a server-local host-path HINT, not a remote-filesystem
// guarantee. Reproducing byte-parity forces a bytes-over-RPC vs host-path-return
// decision that is daedalus's to make, so the mirror refuses rather than invent
// a contract. See docs/rpc-cli-migration.md "attach get/stdin gap".
func newAttachGetCmd() *cobra.Command {
	var as string
	cmd := &cobra.Command{
		Use:   "get <attachment-id>",
		Short: "Get an attachment file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("attach get: not implemented in wrkq-rpccli — returning attachment bytes over the RPC boundary (vs a host-path hint) is an open design decision (see docs/rpc-cli-migration.md)")
		},
	}
	cmd.Flags().StringVar(&as, "as", "-", "Output path (use '-' for stdout)")
	return cmd
}

// newAttachRmCmd mirrors `wrkq attach rm <id>...`. Each id is removed via
// wrkq.attachment.remove (metadata DELETE + server-side file unlink). The legacy
// interactive confirmation prompt is reproduced; --yes skips it.
func newAttachRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <attachment-id>...",
		Short: "Remove attachment(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			actor := actorFlag(cmd)

			results := []map[string]interface{}{}
			for _, ref := range args {
				// attach rm takes attachment IDs/UUIDs, not paths — no project scoping.
				params := map[string]any{"id": ref}
				if actor != "" {
					params["actor"] = actor
				}
				// Resolve-and-snapshot first via remove; the server returns the deleted
				// DTO. Legacy resolves before prompting; to keep the prompt identical we
				// must know the friendly id + filename. The server's remove returns them.
				if !yes {
					// Legacy prompts using the resolved id + filename. The server remove
					// is destructive, so for the prompt path we resolve via show first.
					dto, rerr := attachShow(cmd, tr, ref)
					if rerr != nil {
						if re, ok := rerr.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
							fmt.Fprintf(cmd.ErrOrStderr(), "Warning: attachment not found: %s\n", ref)
							continue
						}
						return rerr
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "Delete attachment %s (%s)? [y/N]: ", dto.ID, dto.Filename)
					var response string
					_, _ = fmt.Scanln(&response)
					if response != "y" && response != "Y" {
						continue
					}
				}
				raw, err := tr.Call(cmd.Context(), "wrkq.attachment.remove", params)
				if err != nil {
					if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: attachment not found: %s\n", ref)
						continue
					}
					return err
				}
				var dto struct {
					UUID     string `json:"uuid"`
					ID       string `json:"id"`
					Filename string `json:"filename"`
				}
				if err := json.Unmarshal(raw, &dto); err != nil {
					return err
				}
				results = append(results, map[string]interface{}{
					"id":       dto.ID,
					"uuid":     dto.UUID,
					"filename": dto.Filename,
					"deleted":  true,
				})
				if isStdoutTTY(cmd.OutOrStdout()) {
					fmt.Fprintf(cmd.OutOrStdout(), "Deleted: %s (%s)\n", dto.ID, dto.Filename)
				}
			}

			if !isStdoutTTY(cmd.OutOrStdout()) {
				return encodeJSONIndent(cmd, results)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation")
	return cmd
}

// attachShowDTO is the subset of the WrkqAttachment resource the rm prompt needs.
type attachShowDTO struct {
	UUID     string `json:"uuid"`
	ID       string `json:"id"`
	Filename string `json:"filename"`
}

// attachShow reads an attachment by id/uuid via wrkq.attachment.show.
func attachShow(cmd *cobra.Command, tr Transport, ref string) (*attachShowDTO, error) {
	raw, err := tr.Call(cmd.Context(), "wrkq.attachment.show", map[string]any{"id": ref})
	if err != nil {
		return nil, err
	}
	var dto attachShowDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// attachTaskID resolves a task UUID to its friendly task ID (T-…) via
// wrkq.task.show so `attach put` can echo task_id like legacy.
func attachTaskID(cmd *cobra.Command, tr Transport, taskUUID string) (string, error) {
	raw, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]any{"task": taskUUID})
	if err != nil {
		if re, ok := err.(*Error); ok {
			return "", errors.New(re.Message)
		}
		return "", err
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		return "", err
	}
	return dto.ID, nil
}
