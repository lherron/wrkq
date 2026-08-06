//go:build darwin && wrkq_local

package rpccli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// These helpers are reachable only from the package's darwin-gated PTY tests
// (pty_rm_darwin_test.go). Left in the cross-platform parity_test.go they read
// as live code on darwin and as dead code everywhere else, which made
// `golangci-lint run` — and therefore `just verify` — fail on linux while
// passing on the maintainers' macs. The build constraint puts the helpers on
// the same platform as their only callers so the gate agrees with itself on
// every host (T-06894).

func copyFixture(t *testing.T, base string) string {
	t.Helper()
	dst := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(filepath.Join(base, "wrkq.db"+suffix))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("copy fixture read: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dst, "wrkq.db"+suffix), b, 0o644); err != nil {
			t.Fatalf("copy fixture write: %v", err)
		}
	}
	return dst
}

func normalize(s string) string {
	return rfc3339Re.ReplaceAllString(s, "<TS>")
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatalf("snapshot open: %v", err)
	}
	defer func() { _ = database.Close() }()
	rows, err := database.Query(`
		SELECT id, slug, state, priority, kind,
		       project_uuid,
		       COALESCE(parent_task_uuid, ''), COALESCE(assignee_principal_ref, ''),
		       COALESCE(description, ''), COALESCE(specification, ''),
		       COALESCE(labels, ''), COALESCE(meta, ''),
		       COALESCE(requested_by_project_id, ''), COALESCE(assigned_project_id, ''),
		       COALESCE(resolution, ''),
		       COALESCE(start_at, ''), COALESCE(due_at, ''),
		       etag,
		       CASE WHEN acknowledged_at IS NOT NULL AND acknowledged_at != '' THEN 'ack' ELSE '-' END,
		       CASE WHEN completed_at    IS NOT NULL AND completed_at    != '' THEN 'done' ELSE '-' END
		FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, slug, state, kind, projectUUID, description, specification, labels, meta string
		var parentTaskUUID, assignee, requestedBy, assignedProject, resolution string
		var startAt, dueAt, ackd, done string
		var prio, etag int
		if err := rows.Scan(&id, &slug, &state, &prio, &kind, &projectUUID, &parentTaskUUID, &assignee, &description, &specification, &labels, &meta, &requestedBy, &assignedProject, &resolution, &startAt, &dueAt, &etag, &ackd, &done); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(slug)
		b.WriteByte('|')
		b.WriteString(state)
		b.WriteString("|p=")
		b.WriteString(jsonNumber(prio))
		b.WriteString("|kind=")
		b.WriteString(kind)
		b.WriteString("|project=")
		b.WriteString(projectUUID)
		b.WriteString("|parent=")
		b.WriteString(parentTaskUUID)
		b.WriteString("|assignee=")
		b.WriteString(assignee)
		b.WriteString("|desc=")
		b.WriteString(description)
		b.WriteString("|spec=")
		b.WriteString(specification)
		b.WriteString("|labels=")
		b.WriteString(labels)
		b.WriteString("|meta=")
		b.WriteString(meta)
		b.WriteString("|requested=")
		b.WriteString(requestedBy)
		b.WriteString("|assigned_project=")
		b.WriteString(assignedProject)
		b.WriteString("|resolution=")
		b.WriteString(resolution)
		b.WriteString("|start=")
		b.WriteString(startAt)
		b.WriteString("|due=")
		b.WriteString(dueAt)
		b.WriteString("|etag=")
		b.WriteString(jsonNumber(etag))
		b.WriteByte('|')
		b.WriteString(ackd)
		b.WriteByte('|')
		b.WriteString(done)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}
	return b.String()
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
