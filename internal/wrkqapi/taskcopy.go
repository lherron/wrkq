//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

const nsTaskCopy = "wrkq.task.copy"

// TaskCopy performs the server-owned deep copy of a SINGLE source task into a
// destination container: source resolution, destination-container resolution,
// source expectEtag CAS, create-or-overwrite decision, the task-row write, the
// attachment-metadata cascade, an optional SAME-STORE attachment-file copy, a
// task.copied event, and a post-commit `created` webhook carrying a synthetic
// source_uuid change. The CLI owns fan-out / prompts / dry-run / output.
//
// FILE-COPY SAFETY (daedalus risk): the physical attachment-file copy is NOT
// inside the SQLite tx, so this is NOT claimed fully transactional. The required
// property — no RPC-visible partial durable state — is achieved by staging every
// file into a temp `.copy-*` path under the destination task dir BEFORE commit. A
// staging failure rolls the DB tx back deterministically (deferred Rollback) and
// removes all staged temps, so a failed copy leaves NEITHER a task row NOR a
// stray file. Only after tx.Commit() succeeds are the staged temps atomically
// renamed into place (same-dir rename, no cross-fs partial write).
func (a *API) TaskCopy(ctx context.Context, p TaskCopyParams) (*WrkqTaskCopyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p.WithAttachments && p.Shallow {
		return nil, NewValidationError("withAttachments and shallow are mutually exclusive", map[string]any{
			"field": "withAttachments",
		})
	}
	if strings.TrimSpace(p.Source) == "" {
		return nil, NewValidationError("source is required", map[string]any{"field": "source"})
	}
	if strings.TrimSpace(p.Destination) == "" {
		return nil, NewValidationError("destination is required", map[string]any{"field": "destination"})
	}

	// Mandatory idempotency replay BEFORE any mutation (create-like under fan-out:
	// a retried source copy must not duplicate the task). Keyed on the params with
	// the idempotency key zeroed.
	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsTaskCopy, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqTaskCopyResult
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	sourceUUID, _, serr := selectors.ResolveTask(a.db, p.Source)
	if serr != nil {
		return nil, NewNotFoundError(p.Source, "task")
	}

	destUUID, _, derr := selectors.ResolveContainer(a.db, p.Destination)
	if derr != nil {
		return nil, NewNotFoundError(p.Destination, "container")
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	committed := false
	var staged []stagedFile
	defer func() {
		if !committed {
			_ = tx.Rollback()

			for _, sf := range staged {
				_ = os.Remove(sf.tmpPath)
			}
		}
	}()

	// Source expectEtag CAS (read inside the tx).
	var (
		sourceID, slug, title, state string
		priority                     int
		description, specification   *string
		labels, startAt, dueAt       *string
		sourceEtag                   int64
	)
	scanErr := tx.QueryRow(`
		SELECT id, slug, title, state, priority, description, specification, labels, start_at, due_at, etag
		FROM tasks WHERE uuid = ?
	`, sourceUUID).Scan(&sourceID, &slug, &title, &state, &priority, &description, &specification, &labels, &startAt, &dueAt, &sourceEtag)
	if scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Source, "task")
		}
		return nil, NewInternalError(scanErr)
	}
	if p.ExpectEtag != nil && *p.ExpectEtag != sourceEtag {
		return nil, NewConflictError("source task etag precondition failed", map[string]any{
			"expectEtag":  *p.ExpectEtag,
			"currentEtag": sourceEtag,
		})
	}

	// Create-or-overwrite decision.
	var existingUUID string
	_ = tx.QueryRow("SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ?", destUUID, slug).Scan(&existingUUID)
	if existingUUID != "" && !p.Overwrite {
		return nil, NewConflictError(
			fmt.Sprintf("task with slug '%s' already exists in destination container", slug),
			map[string]any{"slug": slug},
		)
	}

	var newUUID, newID string
	if p.Overwrite && existingUUID != "" {
		if _, uerr := tx.Exec(`
			UPDATE tasks SET title = ?, state = ?, priority = ?, description = ?, specification = ?, labels = ?,
				start_at = ?, due_at = ?, updated_at = CURRENT_TIMESTAMP,
				updated_by_principal_ref = ?, updated_by_scope_ref = ?, etag = etag + 1
			WHERE uuid = ?
		`, title, state, priority, description, specification, labels, startAt, dueAt,
			attr.PrincipalRef, scopeBind(attr), existingUUID); uerr != nil {
			return nil, NewInternalError(uerr)
		}
		newUUID = existingUUID
		if qerr := tx.QueryRow("SELECT id FROM tasks WHERE uuid = ?", newUUID).Scan(&newID); qerr != nil {
			return nil, NewInternalError(qerr)
		}
	} else {
		res, ierr := tx.Exec(`
			INSERT INTO tasks (slug, title, project_uuid, state, priority, description, specification, labels,
				start_at, due_at,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, slug, title, destUUID, state, priority, description, specification, labels, startAt, dueAt,
			attr.PrincipalRef, attr.PrincipalRef, scopeBind(attr), scopeBind(attr))
		if ierr != nil {
			return nil, mapStoreError(ierr, "")
		}
		rowID, _ := res.LastInsertId()
		if qerr := tx.QueryRow("SELECT uuid, id FROM tasks WHERE rowid = ?", rowID).Scan(&newUUID, &newID); qerr != nil {
			return nil, NewInternalError(qerr)
		}
		if verr := store.ValidateTaskResidentAdmissionTx(tx, newUUID); verr != nil {
			return nil, mapStoreError(verr, "")
		}
	}

	// Attachment-metadata cascade (+ optional SAME-STORE file staging).
	var attachmentCount int
	if !p.Shallow {
		attachmentCount, staged, err = a.copyAttachmentsTx(tx, attr, sourceUUID, newUUID, p.WithAttachments)
		if err != nil {
			return nil, err
		}
	}

	payloadData := map[string]interface{}{
		"source_id":   sourceID,
		"source_uuid": sourceUUID,
	}
	if attachmentCount > 0 {
		payloadData["attachment_count"] = attachmentCount
		payloadData["with_files"] = p.WithAttachments
	}
	payloadJSON, _ := json.Marshal(payloadData)
	payloadStr := string(payloadJSON)

	eventMeta, eerr := events.NewWriter(a.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &newUUID,
		EventType:    "task.copied",
		Payload:      &payloadStr,
	})
	if eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}
	committed = true

	for _, sf := range staged {
		if rerr := os.Rename(sf.tmpPath, sf.finalPath); rerr != nil {
			return nil, NewInternalError(fmt.Errorf("failed to place copied attachment file: %w", rerr))
		}
	}

	webhooks.DispatchTaskEvent(a.db, newUUID, webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "created",
		PrincipalRef: attr.PrincipalRef,
		Via:          "rpc",
		Transition:   &webhooks.Transition{From: nil, To: &state},
		Changed:      []string{"source_uuid"},
		Changes: map[string]webhooks.Change{
			"source_uuid": {From: nil, To: sourceUUID},
		},
	})

	var destPath string
	_ = a.db.QueryRow("SELECT path FROM task_paths WHERE uuid = ?", newUUID).Scan(&destPath)

	dto := &WrkqTaskCopyResult{
		SourceID:          sourceID,
		SourceUUID:        sourceUUID,
		DestID:            newID,
		DestUUID:          newUUID,
		DestPath:          destPath,
		AttachmentsCopied: attachmentCount,
		WithFiles:         p.WithAttachments,
	}
	if p.IdempotencyKey != "" {
		if storeErr := a.idempotentStore(nsTaskCopy, p.IdempotencyKey, requestHash, dto); storeErr != nil {
			return nil, storeErr
		}
	}
	return dto, nil
}

// copyAttachmentsTx inserts the attachment metadata cascade for a copied task and,
// when withFiles is set, STAGES each source file into a temp `.copy-*` path under
// the destination task dir (returned for post-commit rename). It does NOT mutate
// the canonical attachment store before commit — a tx rollback removes the staged
// temps, so a failed copy leaves no stray durable file.
func (a *API) copyAttachmentsTx(tx *sql.Tx, attr attribution.Attribution, sourceTaskUUID, destTaskUUID string, withFiles bool) (int, []stagedFile, error) {
	rows, err := tx.Query(`
		SELECT uuid, filename, relative_path, mime_type, size_bytes, checksum
		FROM attachments WHERE task_uuid = ?
	`, sourceTaskUUID)
	if err != nil {
		return 0, nil, NewInternalError(err)
	}
	type attachRow struct {
		filename, relativePath, mimeType string
		sizeBytes                        int64
		checksum                         sql.NullString
	}
	var srcRows []attachRow
	for rows.Next() {
		var r attachRow
		var attUUID string
		if scanErr := rows.Scan(&attUUID, &r.filename, &r.relativePath, &r.mimeType, &r.sizeBytes, &r.checksum); scanErr != nil {
			_ = rows.Close()
			return 0, nil, NewInternalError(scanErr)
		}
		srcRows = append(srcRows, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return 0, nil, NewInternalError(rowsErr)
	}
	_ = rows.Close()

	if withFiles && len(srcRows) > 0 && strings.TrimSpace(a.attachDir) == "" {
		return 0, nil, NewValidationError(
			"attachment storage is not configured (set WRKQ_ATTACH_DIR)",
			map[string]any{"field": "attachDir"},
		)
	}

	var staged []stagedFile
	count := 0
	for _, r := range srcRows {
		newRelativePath := fmt.Sprintf("tasks/%s/%s", destTaskUUID, r.filename)

		if withFiles {
			sf, scperr := a.stageAttachmentCopy(r.relativePath, newRelativePath, destTaskUUID)
			if scperr != nil {
				return 0, staged, scperr
			}
			staged = append(staged, sf)
		}

		if _, ierr := tx.Exec(`
			INSERT INTO attachments (
				id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum,
				created_by_principal_ref, created_by_scope_ref
			)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?)
		`, destTaskUUID, r.filename, newRelativePath, r.mimeType, r.sizeBytes, r.checksum,
			attr.PrincipalRef, scopeBind(attr)); ierr != nil {
			return 0, staged, NewInternalError(ierr)
		}
		count++
	}
	return count, staged, nil
}

// stageAttachmentCopy copies a source attachment file into a temp `.copy-*` file
// in the destination task dir and returns the staging→final path pair. The final
// rename happens only AFTER the DB tx commits (TaskCopy). A missing source file →
// WRKQ_NOT_FOUND with kind "attachment file" (mirrors legacy's failed-copy error).
func (a *API) stageAttachmentCopy(sourceRelative, destRelative, destTaskUUID string) (stagedFile, error) {
	sourcePath := attach.AbsolutePath(a.attachDir, sourceRelative)
	finalPath := attach.AbsolutePath(a.attachDir, destRelative)

	if eerr := attach.EnsureTaskDir(a.attachDir, destTaskUUID); eerr != nil {
		return stagedFile{}, NewInternalError(eerr)
	}

	src, oerr := os.Open(sourcePath)
	if oerr != nil {
		if os.IsNotExist(oerr) {
			return stagedFile{}, NewNotFoundError(sourceRelative, "attachment file")
		}
		return stagedFile{}, NewInternalError(oerr)
	}
	defer func() { _ = src.Close() }()

	tmp, terr := os.CreateTemp(filepath.Dir(finalPath), ".copy-*")
	if terr != nil {
		return stagedFile{}, NewInternalError(terr)
	}
	if _, cerr := io.Copy(tmp, src); cerr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return stagedFile{}, NewInternalError(cerr)
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmp.Name())
		return stagedFile{}, NewInternalError(cerr)
	}
	return stagedFile{tmpPath: tmp.Name(), finalPath: finalPath}, nil
}
