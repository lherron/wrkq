package rpccli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// attachByteChunkBytes is the mirror's raw-bytes-per-chunk for stdin upload. It
// matches the server read chunk size (1 MiB raw → ~1.34 MiB base64) so a single
// JSON-RPC frame stays well under the 8 MiB frame cap. Bytes cross the boundary
// as base64 protocol data, never as a host path (T-05103, daedalus OPTION 1).
const attachByteChunkBytes = 1 << 20

// newAttachCmd mirrors `wrkq attach`. `ls` is RPC-backed via the server-owned
// wrkq.attachment.listView compat list projection (cursor-paginated, DB-only).
// `put` (real-file) and `rm` are RPC-backed via wrkq.attachment.add /
// wrkq.attachment.remove — for a real local file the mirror sends the host PATH
// (the server reads the bytes), which is the "server-local host-path hint" fast
// path. `get` and `put` FROM STDIN move attachment CONTENT across the RPC boundary
// as base64 PROTOCOL DATA (chunked) via wrkq.attachment.getBytes /
// wrkq.attachment.addBytes — never a host path (T-05103, daedalus OPTION 1). Raw
// bytes are emitted ONLY here, after RPC-frame decode; the server stdout stays
// JSON-RPC-pure.
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
				return runAttachPutStdin(cmd, args[0], name, mime)
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

// newAttachGetCmd mirrors `wrkq attach get <id>`. Attachment CONTENT is pulled
// back across the RPC boundary as base64 PROTOCOL DATA (chunked) via
// wrkq.attachment.getBytes; the mirror decodes each frame and writes the RAW
// bytes to stdout (default) or `--as <path>`. The server stdout stays
// JSON-RPC-pure — raw bytes are emitted only here, post-decode (T-05103,
// daedalus OPTION 1; invariant wrkq.wrkf-rpc.attachment-byte-transfer).
func newAttachGetCmd() *cobra.Command {
	var as string
	cmd := &cobra.Command{
		Use:   "get <attachment-id>",
		Short: "Get an attachment file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttachGet(cmd, args[0], as)
		},
	}
	cmd.Flags().StringVar(&as, "as", "-", "Output path (use '-' for stdout)")
	return cmd
}

// runAttachGet streams an attachment's bytes via wrkq.attachment.getBytes and
// writes the decoded content to stdout (`--as -`) or a local file (`--as path`).
// The reassembled bytes are checksum/size-verified against the server metadata.
func runAttachGet(cmd *cobra.Command, ref, as string) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	// Resolve the destination writer. For `--as -` (stdout) write raw bytes
	// directly. For a file path, the CLI owns the local write.
	var dst io.Writer
	var fileWriter io.Closer
	if as == "-" {
		dst = cmd.OutOrStdout()
	} else {
		f, ferr := createLocalFile(as)
		if ferr != nil {
			return fmt.Errorf("failed to copy attachment: %w", ferr)
		}
		dst = f
		fileWriter = f
	}

	hasher := sha256.New()
	var (
		offset   int64
		meta     attachBytesMeta
		gotMeta  bool
		writeErr error
	)
	for {
		raw, cerr := tr.Call(cmd.Context(), "wrkq.attachment.getBytes", map[string]any{
			"id":     ref,
			"offset": offset,
		})
		if cerr != nil {
			if fileWriter != nil {
				_ = fileWriter.Close()
			}
			if re, ok := cerr.(*Error); ok {
				if re.DomainID == "WRKQ_NOT_FOUND" {
					// Legacy emits "attachment not found: <ref>" for a missing row and
					// "failed to copy attachment: ..." for missing file bytes. The server
					// distinguishes via the not-found kind in the message.
					return errors.New(re.Message)
				}
				return errors.New(re.Message)
			}
			return cerr
		}
		var chunk attachBytesChunk
		if uerr := json.Unmarshal(raw, &chunk); uerr != nil {
			if fileWriter != nil {
				_ = fileWriter.Close()
			}
			return uerr
		}
		if !gotMeta {
			meta = attachBytesMeta{
				ID:        chunk.ID,
				UUID:      chunk.UUID,
				Filename:  chunk.Filename,
				SizeBytes: chunk.SizeBytes,
				Checksum:  chunk.Checksum,
			}
			gotMeta = true
		}
		if chunk.Content != "" {
			decoded, derr := base64.StdEncoding.DecodeString(chunk.Content)
			if derr != nil {
				if fileWriter != nil {
					_ = fileWriter.Close()
				}
				return derr
			}
			if _, werr := dst.Write(decoded); werr != nil {
				writeErr = werr
				break
			}
			_, _ = hasher.Write(decoded)
			offset += int64(len(decoded))
		}
		if chunk.EOF {
			break
		}
	}
	if fileWriter != nil {
		if cerr := fileWriter.Close(); cerr != nil && writeErr == nil {
			writeErr = cerr
		}
	}
	if writeErr != nil {
		return fmt.Errorf("failed to copy attachment: %w", writeErr)
	}

	// Integrity: the reassembled bytes must match the server's authoritative size
	// + checksum.
	if got := hex.EncodeToString(hasher.Sum(nil)); meta.Checksum != "" && got != meta.Checksum {
		return fmt.Errorf("attachment checksum mismatch: got %s want %s", got, meta.Checksum)
	}
	if offset != meta.SizeBytes {
		return fmt.Errorf("attachment size mismatch: got %d want %d", offset, meta.SizeBytes)
	}

	if as != "-" {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return encodeJSONIndent(cmd, map[string]interface{}{
				"id":       meta.ID,
				"uuid":     meta.UUID,
				"filename": meta.Filename,
				"path":     as,
				"copied":   true,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Copied %s to %s\n", meta.Filename, as)
	}
	return nil
}

// runAttachPutStdin reads the attachment bytes from stdin and uploads them via
// wrkq.attachment.addBytes in 1 MiB chunks (base64 protocol data, never a host
// path). The first chunk supplies task/filename/mime and receives an uploadId;
// the final chunk commits and returns the WrkqAttachment, re-projected to legacy's
// snake_case map exactly like the real-file `attach put` path.
func runAttachPutStdin(cmd *cobra.Command, taskRef, name, mime string) error {
	// --name required + unknown-task are enforced server-side (resolve-then-name
	// order, matching legacy runAttachPut) so the error precedence is identical.
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	scopedTask := sc.selector(taskRef, false)

	in := cmd.InOrStdin()
	buf := make([]byte, attachByteChunkBytes)
	var (
		uploadID string
		seq      int
		dto      attachPutDTO
		gotDTO   bool
	)
	for {
		n, rerr := io.ReadFull(in, buf)
		if rerr == io.EOF {
			n = 0
		} else if rerr != nil && rerr != io.ErrUnexpectedEOF {
			return rerr
		}
		final := rerr == io.EOF || rerr == io.ErrUnexpectedEOF
		params := map[string]any{
			"seq":           seq,
			"contentBase64": base64.StdEncoding.EncodeToString(buf[:n]),
			"final":         final,
		}
		if uploadID == "" {
			params["task"] = scopedTask
			params["filename"] = name
			if mime != "" {
				params["mimeType"] = mime
			}
			if actor != "" {
				params["actor"] = actor
			}
		} else {
			params["uploadId"] = uploadID
		}
		raw, cerr := tr.Call(cmd.Context(), "wrkq.attachment.addBytes", params)
		if cerr != nil {
			if re, ok := cerr.(*Error); ok {
				return errors.New(re.Message)
			}
			return cerr
		}
		var res attachAddBytesResult
		if uerr := json.Unmarshal(raw, &res); uerr != nil {
			return uerr
		}
		if uploadID == "" {
			uploadID = res.UploadID
		}
		if res.Committed && res.Attachment != nil {
			dto = *res.Attachment
			gotDTO = true
		}
		seq++
		if final {
			break
		}
	}
	if !gotDTO {
		return errors.New("attach put: upload did not commit")
	}

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
}

// attachBytesChunk decodes a wrkq.attachment.getBytes response frame.
type attachBytesChunk struct {
	UUID      string `json:"uuid"`
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	Checksum  string `json:"checksum"`
	Offset    int64  `json:"offset"`
	Content   string `json:"contentBase64"`
	EOF       bool   `json:"eof"`
}

// attachBytesMeta is the whole-file metadata captured from the first chunk.
type attachBytesMeta struct {
	ID        string
	UUID      string
	Filename  string
	SizeBytes int64
	Checksum  string
}

// attachPutDTO is the committed-attachment subset the stdin-put output needs.
type attachPutDTO struct {
	UUID         string `json:"uuid"`
	ID           string `json:"id"`
	TaskUUID     string `json:"taskUuid"`
	Filename     string `json:"filename"`
	RelativePath string `json:"relativePath"`
	MimeType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
}

// attachAddBytesResult decodes a wrkq.attachment.addBytes response frame.
type attachAddBytesResult struct {
	UploadID      string        `json:"uploadId"`
	Seq           int           `json:"seq"`
	BytesReceived int64         `json:"bytesReceived"`
	Committed     bool          `json:"committed"`
	Attachment    *attachPutDTO `json:"attachment"`
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

// createLocalFile opens (creating parent dirs) a local destination file for
// `attach get --as <path>`. The CLI owns the local write; the server never sees
// the destination path. Mirrors legacy attach.CopyFile's dst handling.
func createLocalFile(path string) (*os.File, error) {
	if dir := filepathDir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.Create(path) // #nosec G304 -- user-supplied output path, CLI-side write
}

// filepathDir returns the directory portion of a path (avoids importing
// path/filepath solely for Dir; matches filepath.Dir for the cases we hit).
func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return ""
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
