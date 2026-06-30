package wrkqapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

const nsAttachmentAddBytes = "wrkq.attachment.addBytes"

// AttachmentGetBytes returns one chunk of an attachment's stored content as
// base64 protocol data, plus the whole-file metadata (size + checksum) the client
// needs to verify integrity. The bytes cross the RPC boundary as DATA — the
// server-local file path is never exposed. Callers stream by looping with an
// increasing offset until eof is true.
//
// A missing attachment row → WRKQ_NOT_FOUND. A missing file on disk (row present,
// bytes gone) → WRKQ_NOT_FOUND with kind "attachment file" so the client can
// reproduce legacy's "failed to copy attachment" failure as a clean error.
func (a *API) AttachmentGetBytes(ctx context.Context, p AttachmentGetBytesParams) (*WrkqAttachmentBytes, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, NewValidationError("attachment id is required", map[string]any{"field": "id"})
	}
	if strings.TrimSpace(a.attachDir) == "" {
		return nil, NewValidationError(
			"attachment storage is not configured (set WRKQ_ATTACH_DIR)",
			map[string]any{"field": "attachDir"},
		)
	}
	if p.Offset < 0 {
		return nil, NewValidationError("offset must not be negative", map[string]any{"field": "offset"})
	}

	var (
		attachUUID, attachID, taskUUID, filename, relativePath string
		mimeType                                               string
	)
	row := a.db.QueryRow(
		"SELECT uuid, id, task_uuid, filename, relative_path, COALESCE(mime_type,'') FROM attachments WHERE id = ? OR uuid = ? LIMIT 1",
		p.ID, p.ID,
	)
	if scanErr := row.Scan(&attachUUID, &attachID, &taskUUID, &filename, &relativePath, &mimeType); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.ID, "attachment")
		}
		return nil, NewInternalError(scanErr)
	}

	absPath := attach.AbsolutePath(a.attachDir, relativePath)
	f, oerr := os.Open(absPath)
	if oerr != nil {
		if os.IsNotExist(oerr) {
			return nil, NewNotFoundError(p.ID, "attachment file")
		}
		return nil, NewInternalError(oerr)
	}
	defer func() { _ = f.Close() }()

	// Compute the authoritative full-file size + checksum (cheap relative to the
	// transfer; lets the client verify the reassembled bytes regardless of how the
	// row's stored checksum was produced).
	hasher := sha256.New()
	size, cerr := io.Copy(hasher, f)
	if cerr != nil {
		return nil, NewInternalError(cerr)
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))

	limit := p.Limit
	if limit <= 0 || limit > AttachmentByteChunkBytes {
		limit = AttachmentByteChunkBytes
	}

	out := &WrkqAttachmentBytes{
		UUID:      attachUUID,
		ID:        attachID,
		TaskUUID:  taskUUID,
		Filename:  filename,
		MimeType:  mimeType,
		SizeBytes: size,
		Checksum:  checksum,
		Offset:    p.Offset,
		Content:   "",
		EOF:       true,
	}
	if p.Offset >= size {
		// Past EOF (or empty file): empty terminal chunk.
		return out, nil
	}

	if _, serr := f.Seek(p.Offset, io.SeekStart); serr != nil {
		return nil, NewInternalError(serr)
	}
	buf := make([]byte, limit)
	n, rerr := io.ReadFull(f, buf)
	if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
		return nil, NewInternalError(rerr)
	}
	out.Content = base64.StdEncoding.EncodeToString(buf[:n])
	out.EOF = p.Offset+int64(n) >= size
	return out, nil
}

// attachmentUpload is the staged state for one chunked byte upload.
type attachmentUpload struct {
	taskUUID    string
	filename    string
	mimeType    string
	actor       string
	idemKey     string
	tmpPath     string
	file        *os.File
	nextSeq     int
	received    int64
	maxBytes    int64
	idemReqHash string
}

// AttachmentAddBytes accepts one chunk of a stdin/byte attachment upload. The
// FIRST call (UploadID == "") resolves the task, enforces the duplicate-filename
// rule, and opens a temp staging file; the server returns an uploadId. Subsequent
// calls echo the uploadId and append in monotonic seq order. The final chunk
// (Final == true) validates the accumulated size, atomically renames the temp
// file into the attach dir, inserts the metadata row + event, and returns the
// committed WrkqAttachment. On any rejection the temp file is cleaned up.
func (a *API) AttachmentAddBytes(ctx context.Context, p AttachmentAddBytesParams) (*WrkqAttachmentAddBytesResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.attachDir) == "" {
		return nil, NewValidationError(
			"attachment storage is not configured (set WRKQ_ATTACH_DIR)",
			map[string]any{"field": "attachDir"},
		)
	}

	if strings.TrimSpace(p.UploadID) == "" {
		return a.attachmentAddBytesBegin(p)
	}
	return a.attachmentAddBytesContinue(p)
}

// attachmentAddBytesBegin handles the first chunk: validation, staging, and (when
// the first chunk is also final) immediate finalize.
func (a *API) attachmentAddBytesBegin(p AttachmentAddBytesParams) (*WrkqAttachmentAddBytesResult, error) {
	if p.Seq != 0 {
		return nil, NewValidationError("first upload chunk must have seq 0", map[string]any{"field": "seq"})
	}

	// Legacy runAttachPut resolves the task BEFORE the --name check, so an unknown
	// task surfaces first.
	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	filename := strings.TrimSpace(p.Filename)
	if filename == "" {
		return nil, NewValidationError("--name is required when reading from stdin", map[string]any{"field": "filename"})
	}

	// Idempotency replay BEFORE any file/row mutation, keyed on the begin params
	// (task/filename/mime) so a replayed single-shot upload returns the prior DTO.
	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		hp.UploadID = ""
		hp.Content = ""
		hp.Seq = 0
		hp.Final = false
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsAttachmentAddBytes, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqAttachment
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &WrkqAttachmentAddBytesResult{
				UploadID:      "replayed",
				Seq:           p.Seq,
				BytesReceived: dto.SizeBytes,
				Committed:     true,
				Attachment:    &dto,
			}, nil
		}
	}

	// Duplicate filename for a task → conflict. Legacy `attach put -` (runAttachPut)
	// formats this with the FRIENDLY task ID, so reproduce that wording exactly:
	// `attachment with filename "<f>" already exists for task <T-id>`.
	var existing int
	if scanErr := a.db.QueryRow(
		"SELECT COUNT(*) FROM attachments WHERE task_uuid = ? AND filename = ?",
		taskUUID, filename,
	).Scan(&existing); scanErr != nil {
		return nil, NewInternalError(scanErr)
	}
	if existing > 0 {
		var taskID string
		_ = a.db.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID)
		return nil, NewConflictError(
			fmt.Sprintf("attachment with filename %q already exists for task %s", filename, taskID),
			map[string]any{"filename": filename},
		)
	}

	mimeType := strings.TrimSpace(p.MimeType)
	if mimeType == "" {
		mimeType = attach.DetectMimeType(filename)
	}

	if eerr := attach.EnsureTaskDir(a.attachDir, taskUUID); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	// Stage into a temp file under the task dir so the finalize rename is atomic
	// (same filesystem) — this is the cleanup/atomicity parity with AttachmentAdd.
	tmpFile, terr := os.CreateTemp(attach.TaskDir(a.attachDir, taskUUID), ".upload-*")
	if terr != nil {
		return nil, NewInternalError(terr)
	}

	up := &attachmentUpload{
		taskUUID:    taskUUID,
		filename:    filename,
		mimeType:    mimeType,
		actor:       p.Actor,
		idemKey:     p.IdempotencyKey,
		idemReqHash: requestHash,
		tmpPath:     tmpFile.Name(),
		file:        tmpFile,
		nextSeq:     0,
		maxBytes:    int64(a.attachMaxMB) * 1024 * 1024,
	}

	uploadID := uuid.NewString()
	a.uploadsMu.Lock()
	a.uploads[uploadID] = up
	a.uploadsMu.Unlock()

	res, err := a.attachmentAddBytesWrite(uploadID, up, p)
	if err != nil {
		return nil, err
	}
	res.UploadID = uploadID
	return res, nil
}

// attachmentAddBytesContinue handles a non-first chunk.
func (a *API) attachmentAddBytesContinue(p AttachmentAddBytesParams) (*WrkqAttachmentAddBytesResult, error) {
	a.uploadsMu.Lock()
	up := a.uploads[p.UploadID]
	a.uploadsMu.Unlock()
	if up == nil {
		return nil, NewNotFoundError(p.UploadID, "upload")
	}
	res, err := a.attachmentAddBytesWrite(p.UploadID, up, p)
	if err != nil {
		return nil, err
	}
	res.UploadID = p.UploadID
	return res, nil
}

// attachmentAddBytesWrite appends one chunk, enforcing monotonic seq + the size
// limit, and finalizes when the chunk is marked final. On any error it discards
// the upload session and removes the temp file.
func (a *API) attachmentAddBytesWrite(uploadID string, up *attachmentUpload, p AttachmentAddBytesParams) (*WrkqAttachmentAddBytesResult, error) {
	if p.Seq != up.nextSeq {
		a.discardUpload(uploadID, up)
		return nil, NewValidationError("upload chunk out of order", map[string]any{
			"field":    "seq",
			"expected": up.nextSeq,
			"actual":   p.Seq,
		})
	}

	if p.Content != "" {
		decoded, derr := base64.StdEncoding.DecodeString(p.Content)
		if derr != nil {
			a.discardUpload(uploadID, up)
			return nil, NewValidationError("invalid base64 chunk content", map[string]any{"field": "contentBase64"})
		}
		if _, werr := up.file.Write(decoded); werr != nil {
			a.discardUpload(uploadID, up)
			return nil, NewInternalError(werr)
		}
		up.received += int64(len(decoded))
		// Enforce the size limit incrementally, cleaning up on reject.
		if verr := attach.ValidateSize(up.received, int64(a.attachMaxMB)); verr != nil {
			a.discardUpload(uploadID, up)
			return nil, NewValidationError(verr.Error(), map[string]any{"field": "content"})
		}
	}
	up.nextSeq++

	if !p.Final {
		return &WrkqAttachmentAddBytesResult{
			Seq:           p.Seq,
			BytesReceived: up.received,
			Committed:     false,
		}, nil
	}

	dto, err := a.finalizeUpload(uploadID, up)
	if err != nil {
		return nil, err
	}
	return &WrkqAttachmentAddBytesResult{
		Seq:           p.Seq,
		BytesReceived: up.received,
		Committed:     true,
		Attachment:    dto,
	}, nil
}

// finalizeUpload flushes the staging file, validates the total size, atomically
// renames it into the canonical attach path, inserts the metadata row + event,
// and returns the committed DTO. The session + temp file are always removed.
func (a *API) finalizeUpload(uploadID string, up *attachmentUpload) (*WrkqAttachment, error) {
	// Remove the session bookkeeping; the temp file is renamed (success) or removed
	// (failure) below.
	a.uploadsMu.Lock()
	delete(a.uploads, uploadID)
	a.uploadsMu.Unlock()

	cleanup := func() { _ = os.Remove(up.tmpPath) }

	if cerr := up.file.Close(); cerr != nil {
		cleanup()
		return nil, NewInternalError(cerr)
	}

	if verr := attach.ValidateSize(up.received, int64(a.attachMaxMB)); verr != nil {
		cleanup()
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "content"})
	}

	// Re-check duplicate filename at finalize (a concurrent add may have raced).
	var existing int
	if scanErr := a.db.QueryRow(
		"SELECT COUNT(*) FROM attachments WHERE task_uuid = ? AND filename = ?",
		up.taskUUID, up.filename,
	).Scan(&existing); scanErr != nil {
		cleanup()
		return nil, NewInternalError(scanErr)
	}
	if existing > 0 {
		cleanup()
		return nil, NewConflictError("attachment with this filename already exists for the task", map[string]any{
			"filename": up.filename,
		})
	}

	// Compute checksum from the staged bytes.
	checksum, herr := checksumFile(up.tmpPath)
	if herr != nil {
		cleanup()
		return nil, NewInternalError(herr)
	}

	relativePath := attach.RelativePath(up.taskUUID, up.filename)
	absPath := attach.AbsolutePath(a.attachDir, relativePath)
	if rerr := os.Rename(up.tmpPath, absPath); rerr != nil {
		cleanup()
		return nil, NewInternalError(rerr)
	}

	attr, aerr := a.attributionFor(up.actor)
	if aerr != nil {
		return nil, aerr
	}

	tx, err := a.db.Begin()
	if err != nil {
		_ = os.Remove(absPath)
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	res, ierr := tx.Exec(`
		INSERT INTO attachments (
			id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum,
			created_by_principal_ref, created_by_scope_ref
		)
		VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?)
	`, up.taskUUID, up.filename, relativePath, up.mimeType, up.received, checksum,
		attr.PrincipalRef, scopeBind(attr))
	if ierr != nil {
		_ = os.Remove(absPath)
		return nil, mapStoreError(ierr, up.taskUUID)
	}

	var attachUUID string
	lastID, _ := res.LastInsertId()
	if scanErr := tx.QueryRow("SELECT uuid FROM attachments WHERE rowid = ?", lastID).Scan(&attachUUID); scanErr != nil {
		_ = os.Remove(absPath)
		return nil, NewInternalError(scanErr)
	}

	payload, _ := json.Marshal(map[string]any{
		"filename":   up.filename,
		"size_bytes": up.received,
		"mime_type":  up.mimeType,
	})
	payloadStr := string(payload)
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "attachment",
		ResourceUUID: &attachUUID,
		EventType:    "attachment.created",
		Payload:      &payloadStr,
	}); eerr != nil {
		_ = os.Remove(absPath)
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		_ = os.Remove(absPath)
		return nil, NewInternalError(cerr)
	}

	dto, lerr := a.loadAttachment(attachUUID)
	if lerr != nil {
		return nil, lerr
	}
	if up.idemKey != "" {
		if serr := a.idempotentStore(nsAttachmentAddBytes, up.idemKey, up.idemReqHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// discardUpload tears down a failed upload session (closes + removes the temp
// file, drops the map entry).
func (a *API) discardUpload(uploadID string, up *attachmentUpload) {
	a.uploadsMu.Lock()
	delete(a.uploads, uploadID)
	a.uploadsMu.Unlock()
	if up.file != nil {
		_ = up.file.Close()
	}
	if up.tmpPath != "" {
		_ = os.Remove(up.tmpPath)
	}
}

// checksumFile computes the sha256 of a file's contents as a hex string.
func checksumFile(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- server-controlled staging path
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
