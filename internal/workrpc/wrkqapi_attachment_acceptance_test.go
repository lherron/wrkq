//go:build wrkq_local

package workrpc_test

// wrkqapi_attachment_acceptance_test.go — wrkq.attachment.add/list/remove over
// the real JSON-RPC stdio surface (T-04448), including that the wrkq and wrkf
// entrypoints store files in the same WRKQ_ATTACH_DIR layout.

import (
	"os"
	"path/filepath"
	"testing"
)

// attachFixture is a migrated DB plus a WRKQ_ATTACH_DIR for one entrypoint.
type attachFixture struct {
	entrypoint string
	dbPath     string
	attachDir  string
	env        []string
}

func newAttachFixture(t *testing.T, entrypoint string, extraEnv ...string) attachFixture {
	t.Helper()
	attachDir := t.TempDir()
	return attachFixture{
		entrypoint: entrypoint,
		dbPath:     migratedDB(t),
		attachDir:  attachDir,
		env:        append([]string{"WRKQ_ATTACH_DIR=" + attachDir}, extraEnv...),
	}
}

func (f attachFixture) run(t *testing.T, reqs ...string) []map[string]any {
	t.Helper()
	return pdRunEnv(t, f.entrypoint, f.dbPath, f.env, reqs...)
}

// createTask creates a task through the fixture's entrypoint, returning (id, uuid).
func (f attachFixture) createTask(t *testing.T, title string) (id, uuid string) {
	t.Helper()
	frames := f.run(t, mkRPC("c1", "wrkq.task.create", map[string]any{"title": title, "kind": "task"}))
	return createdTaskIDs(t, frames[1])
}

// add sends one wrkq.attachment.add and returns its response frame. An empty
// idempotencyKey is omitted.
func (f attachFixture) add(t *testing.T, taskID, path, filename, idempotencyKey string) map[string]any {
	t.Helper()
	params := map[string]any{"task": taskID, "path": path, "filename": filename}
	if idempotencyKey != "" {
		params["idempotencyKey"] = idempotencyKey
	}
	return f.run(t, mkRPC("a1", "wrkq.attachment.add", params))[1]
}

// assertAttachmentDTO checks the WrkqAttachment shape: checksum (never sha256),
// uuid, filename, taskUuid.
func assertAttachmentDTO(t *testing.T, result map[string]any) {
	t.Helper()
	p2AssertStr(t, result, "checksum")
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "filename")
	p2AssertStr(t, result, "taskUuid")
	p2AssertAbsent(t, result, "sha256")
}

// TestWrkqAttachment_EntrypointsShareAttachDir adds an attachment through the
// wrkq and the wrkf entrypoint; each stores it at
// <WRKQ_ATTACH_DIR>/tasks/<taskUuid>/<filename> and returns the same DTO shape.
func TestWrkqAttachment_EntrypointsShareAttachDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		entrypoint string
		extraEnv   []string
	}{
		{entrypoint: "wrkq"},
		// The wrkf entrypoint derives caller attribution from its principal ref;
		// supply a canonical principal so writes pass principal-only validation.
		{entrypoint: "wrkf", extraEnv: []string{"WRKF_PRINCIPAL_REF=agent:smokey"}},
	}
	for _, tc := range cases {
		t.Run(tc.entrypoint, func(t *testing.T) {
			f := newAttachFixture(t, tc.entrypoint, tc.extraEnv...)
			taskID, taskUUID := f.createTask(t, "Attach "+tc.entrypoint+" Entrypoint")

			filePath := p4WriteTempFile(t, "test-attach-"+tc.entrypoint+".txt", "attachment content for "+tc.entrypoint+" entrypoint")
			filename := filepath.Base(filePath)

			result := p2ResultOrFail(t, f.add(t, taskID, filePath, filename, ""), "wrkq.attachment.add via "+tc.entrypoint)
			assertAttachmentDTO(t, result)

			expectedPath := filepath.Join(f.attachDir, "tasks", taskUUID, filename)
			if _, err := os.Stat(expectedPath); err != nil {
				t.Errorf("attachment file not found at expected path %s: %v", expectedPath, err)
			}
		})
	}
}

// TestWrkqAttachment_MissingAttachDir_Fails proves attachment.add is a real
// WRKQ_VALIDATION when WRKQ_ATTACH_DIR is not set.
func TestWrkqAttachment_MissingAttachDir_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "f0000007-0000-4000-8000-000000000001", "attach-no-dir", "Attach No Dir Task")

	filePath := p4WriteTempFile(t, "test-no-dir.txt", "content")

	// Strip WRKQ_ATTACH_DIR from environment so it is unset in the subprocess.
	cleanEnv := filterEnv(os.Environ(), "WRKQ_ATTACH_DIR")
	frames := pdRunEnv(t, "wrkq", dbPath, cleanEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filepath.Base(filePath),
		}),
	)
	p4AssertDomainError(t, frames[1], "WRKQ_VALIDATION", "missing attach dir")
}

// TestWrkqAttachmentAdd_DuplicateFilename_Conflict adds the same filename twice:
// the second add is a real WRKQ_CONFLICT.
func TestWrkqAttachmentAdd_DuplicateFilename_Conflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := newAttachFixture(t, "wrkq")
	taskID, _ := f.createTask(t, "Attach Duplicate Test")
	filePath := p4WriteTempFile(t, "dup-test.txt", "duplicate content")
	filename := filepath.Base(filePath)

	p2ResultOrFail(t, f.add(t, taskID, filePath, filename, ""), "first wrkq.attachment.add")
	p4AssertDomainError(t, f.add(t, taskID, filePath, filename, ""), "WRKQ_CONFLICT", "duplicate attachment")
}

// TestWrkqAttachmentAdd_Idempotency_Replay adds an attachment twice under one
// idempotencyKey: both calls return the same uuid and one file lands on disk.
func TestWrkqAttachmentAdd_Idempotency_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := newAttachFixture(t, "wrkq")
	taskID, taskUUID := f.createTask(t, "Attach Idempotency Test")
	filePath := p4WriteTempFile(t, "idem-test.txt", "idempotency test content")
	filename := filepath.Base(filePath)
	idemKey := "p4-smokey:attach:idem:" + taskID + ":idem-test.txt"

	r1 := p2ResultOrFail(t, f.add(t, taskID, filePath, filename, idemKey), "first wrkq.attachment.add with idempotencyKey")
	r2 := p2ResultOrFail(t, f.add(t, taskID, filePath, filename, idemKey), "second wrkq.attachment.add replay")
	uuid1, _ := r1["uuid"].(string)
	uuid2, _ := r2["uuid"].(string)
	if uuid1 == "" || uuid1 != uuid2 {
		t.Errorf("attachment idempotency replay: expected same uuid; first=%q second=%q", uuid1, uuid2)
	}

	taskDir := filepath.Join(f.attachDir, "tasks", taskUUID)
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		t.Logf("could not read task attach dir %s: %v", taskDir, err)
	} else if len(entries) != 1 {
		t.Errorf("attachment idempotency: expected 1 file on disk, found %d", len(entries))
	}
}

// TestWrkqAttachmentAdd_Idempotency_Mismatch reuses an idempotencyKey for
// different content: the second add is a real WRKQ_CONFLICT.
func TestWrkqAttachmentAdd_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := newAttachFixture(t, "wrkq")
	taskID, _ := f.createTask(t, "Attach Idem Mismatch")
	idemKey := "p4-smokey:attach:idem-mismatch:" + taskID

	file1 := p4WriteTempFile(t, "mismatch-1.txt", "original content")
	file2 := p4WriteTempFile(t, "mismatch-2.txt", "different content entirely")

	p2ResultOrFail(t, f.add(t, taskID, file1, "mismatch.txt", idemKey), "first wrkq.attachment.add")
	p4AssertDomainError(t, f.add(t, taskID, file2, "mismatch.txt", idemKey), "WRKQ_CONFLICT", "attachment idempotency mismatch")
}

// TestWrkqAttachmentList_ReturnsItems lists a task's attachments after one add.
func TestWrkqAttachmentList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := newAttachFixture(t, "wrkq")
	taskID, _ := f.createTask(t, "Attach List Test")
	filePath := p4WriteTempFile(t, "list-test.txt", "list test content")
	p2ResultOrFail(t, f.add(t, taskID, filePath, filepath.Base(filePath), ""), "wrkq.attachment.add")

	lf := f.run(t, mkRPC("l1", "wrkq.attachment.list", map[string]any{"task": taskID}))
	result := p2ResultOrFail(t, lf[1], "wrkq.attachment.list")
	p2AssertHasItems(t, result, "wrkq.attachment.list")
	if items, _ := result["items"].([]any); len(items) < 1 {
		t.Errorf("wrkq.attachment.list: expected at least 1 item, got %d", len(items))
	}
}

// TestWrkqAttachmentRemove_NotFound removes a nonexistent attachment: a real
// WRKQ_NOT_FOUND.
func TestWrkqAttachmentRemove_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := newAttachFixture(t, "wrkq")
	rf := f.run(t, mkRPC("r1", "wrkq.attachment.remove", map[string]any{"id": "00000000-0000-0000-0000-nonexistent00"}))
	p4AssertDomainError(t, rf[1], "WRKQ_NOT_FOUND", "remove nonexistent attachment")
}
