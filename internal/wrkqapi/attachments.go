package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

const nsAttachmentAdd = "wrkq.attachment.add"

// AttachmentAdd copies a local file into the configured attachment directory and
// inserts the metadata row. Attachment storage config must be present (non-empty
// attach dir) or it returns WRKQ_VALIDATION. Duplicate filename for a task →
// WRKQ_CONFLICT. Idempotency is honored when idempotencyKey is supplied.
func (a *API) AttachmentAdd(ctx context.Context, p AttachmentAddParams) (*WrkqAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.attachDir) == "" {
		return nil, NewValidationError(
			"attachment storage is not configured (set WRKQ_ATTACH_DIR)",
			map[string]any{"field": "attachDir"},
		)
	}
	if strings.TrimSpace(p.Path) == "" {
		return nil, NewValidationError("attachment path is required", map[string]any{"field": "path"})
	}

	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	// Idempotency replay BEFORE any file/row mutation.
	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsAttachmentAdd, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqAttachment
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	filename := strings.TrimSpace(p.Filename)
	if filename == "" {
		filename = baseName(p.Path)
	}
	if filename == "" {
		return nil, NewValidationError("attachment filename could not be determined", map[string]any{"field": "filename"})
	}

	// Duplicate filename for a task → conflict.
	var existing int
	if scanErr := a.db.QueryRow(
		"SELECT COUNT(*) FROM attachments WHERE task_uuid = ? AND filename = ?",
		taskUUID, filename,
	).Scan(&existing); scanErr != nil {
		return nil, NewInternalError(scanErr)
	}
	if existing > 0 {
		return nil, NewConflictError("attachment with this filename already exists for the task", map[string]any{
			"filename": filename,
		})
	}

	mimeType := strings.TrimSpace(p.MimeType)
	if mimeType == "" {
		mimeType = attach.DetectMimeType(filename)
	}

	// Pre-validate size from source file when possible.
	if size, gerr := attach.GetFileSize(p.Path); gerr == nil {
		if verr := attach.ValidateSize(size, int64(a.attachMaxMB)); verr != nil {
			return nil, NewValidationError(verr.Error(), map[string]any{"field": "path"})
		}
	} else {
		return nil, NewValidationError("cannot read attachment source: "+gerr.Error(), map[string]any{"field": "path"})
	}

	if eerr := attach.EnsureTaskDir(a.attachDir, taskUUID); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	relativePath := attach.RelativePath(taskUUID, filename)
	absPath := attach.AbsolutePath(a.attachDir, relativePath)

	size, checksum, cerr := attach.CopyFile(p.Path, absPath)
	if cerr != nil {
		return nil, NewInternalError(cerr)
	}
	// Enforce size limit on actual copied bytes, cleaning up on reject.
	if verr := attach.ValidateSize(size, int64(a.attachMaxMB)); verr != nil {
		_ = os.Remove(absPath)
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "path"})
	}

	attr, aerr := a.attributionFor(p.Actor)
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
	`, taskUUID, filename, relativePath, mimeType, size, checksum,
		attr.PrincipalRef, scopeBind(attr))
	if ierr != nil {
		_ = os.Remove(absPath)
		return nil, mapStoreError(ierr, p.Task)
	}

	var attachUUID string
	lastID, _ := res.LastInsertId()
	if scanErr := tx.QueryRow("SELECT uuid FROM attachments WHERE rowid = ?", lastID).Scan(&attachUUID); scanErr != nil {
		_ = os.Remove(absPath)
		return nil, NewInternalError(scanErr)
	}

	payload, _ := json.Marshal(map[string]any{
		"filename":   filename,
		"size_bytes": size,
		"mime_type":  mimeType,
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
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsAttachmentAdd, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// AttachmentList returns the attachments for a task.
func (a *API) AttachmentList(ctx context.Context, p AttachmentListParams) (*WrkqAttachmentListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	page, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{"created_at"},
		SQLFields:  []string{"created_at"},
		Descending: []bool{false},
		IDField:    "id",
		Limit:      limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
	}

	where := []string{"task_uuid = ?"}
	args := []any{taskUUID}
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}

	query := "SELECT uuid, id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_at, created_by_principal_ref FROM attachments"
	query += " WHERE " + strings.Join(where, " AND ")
	query += " " + page.OrderByClause
	if page.LimitClause != "" {
		query += " " + page.LimitClause
		args = append(args, *page.LimitParam)
	}

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	items := []WrkqAttachment{}
	for rows.Next() {
		att, scanErr := scanAttachmentRow(rows)
		if scanErr != nil {
			return nil, NewInternalError(scanErr)
		}
		items = append(items, *att)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	result := &WrkqAttachmentListResult{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		anchor := result.Items[limit-1]
		next, cerr := cursor.BuildNextCursor([]string{"created_at"}, []any{anchor.createdAtRaw}, anchor.ID)
		if cerr == nil {
			result.NextCursor = next
		}
	}
	return result, nil
}

// AttachmentShow returns a single attachment by id or uuid.
func (a *API) AttachmentShow(ctx context.Context, p AttachmentShowParams) (*WrkqAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, NewValidationError("attachment id is required", map[string]any{"field": "id"})
	}
	attachUUID, err := a.resolveAttachment(p.ID)
	if err != nil {
		return nil, err
	}
	return a.loadAttachment(attachUUID)
}

// AttachmentRemove deletes an attachment metadata row and its file. Removing a
// missing attachment → WRKQ_NOT_FOUND.
func (a *API) AttachmentRemove(ctx context.Context, p AttachmentRemoveParams) (*WrkqAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, NewValidationError("attachment id is required", map[string]any{"field": "id"})
	}

	var attachUUID, relativePath string
	var filename string
	scanErr := a.db.QueryRow(
		"SELECT uuid, relative_path, filename FROM attachments WHERE id = ? OR uuid = ? LIMIT 1",
		p.ID, p.ID,
	).Scan(&attachUUID, &relativePath, &filename)
	if scanErr == sql.ErrNoRows {
		return nil, NewNotFoundError(p.ID, "attachment")
	}
	if scanErr != nil {
		return nil, NewInternalError(scanErr)
	}

	// Snapshot the DTO before deletion so we can return it.
	dto, lerr := a.loadAttachment(attachUUID)
	if lerr != nil {
		return nil, lerr
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, derr := tx.Exec("DELETE FROM attachments WHERE uuid = ?", attachUUID); derr != nil {
		return nil, NewInternalError(derr)
	}

	payload, _ := json.Marshal(map[string]any{"filename": filename})
	payloadStr := string(payload)
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "attachment",
		ResourceUUID: &attachUUID,
		EventType:    "attachment.deleted",
		Payload:      &payloadStr,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	// Delete the file AFTER the metadata is gone (best-effort).
	if a.attachDir != "" && relativePath != "" {
		_ = attach.DeleteFile(a.attachDir, relativePath)
	}
	return dto, nil
}

// resolveAttachment resolves an attachment id or uuid to its uuid.
func (a *API) resolveAttachment(ref string) (string, error) {
	var attachUUID string
	scanErr := a.db.QueryRow(
		"SELECT uuid FROM attachments WHERE id = ? OR uuid = ? LIMIT 1",
		ref, ref,
	).Scan(&attachUUID)
	if scanErr == sql.ErrNoRows {
		return "", NewNotFoundError(ref, "attachment")
	}
	if scanErr != nil {
		return "", NewInternalError(scanErr)
	}
	return attachUUID, nil
}

// loadAttachment reads an attachment by UUID into a WrkqAttachment DTO.
func (a *API) loadAttachment(attachUUID string) (*WrkqAttachment, error) {
	row := a.db.QueryRow(
		"SELECT uuid, id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_at, created_by_principal_ref FROM attachments WHERE uuid = ?",
		attachUUID,
	)
	att, err := scanAttachmentRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(attachUUID, "attachment")
		}
		return nil, NewInternalError(err)
	}
	return att, nil
}

// scanAttachmentRow scans an attachment row (column order matches the queries
// above) into a WrkqAttachment DTO.
func scanAttachmentRow(s rowScanner) (*WrkqAttachment, error) {
	var (
		attachUUID, attachID, taskUUID, filename, relativePath string
		mimeType, checksum, createdByPrincipal                 sql.NullString
		sizeBytes                                              int64
		createdAt                                              string
	)
	if err := s.Scan(
		&attachUUID, &attachID, &taskUUID, &filename, &relativePath,
		&mimeType, &sizeBytes, &checksum, &createdAt, &createdByPrincipal,
	); err != nil {
		return nil, err
	}
	return &WrkqAttachment{
		UUID:                  attachUUID,
		ID:                    attachID,
		TaskUUID:              taskUUID,
		Filename:              filename,
		RelativePath:          relativePath,
		MimeType:              mimeType.String,
		SizeBytes:             sizeBytes,
		Checksum:              checksum.String,
		CreatedAt:             toRFC3339(createdAt),
		CreatedByPrincipalRef: createdByPrincipal.String,
		createdAtRaw:          createdAt,
	}, nil
}

// baseName returns the final path element of a local file path, handling both
// '/' and the OS separator.
func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if idx := strings.LastIndexAny(p, "/\\"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}
