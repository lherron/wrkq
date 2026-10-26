//go:build wrkq_local

package rpccli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// `comment add` stores the body exactly as given — from stdin when the body
// argument is "-", from -m otherwise — and records one comment.created event
// for the new comment.
func TestCommentAddStoresBodyFromStdinOrFlagAndEmitsCommentCreated(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
	}{
		{
			name:  "dash reads stdin byte-exact",
			args:  []string{"-"},
			stdin: strings.NewReader("stdin-backed comment\nsecond line\n"),
			want:  "stdin-backed comment\nsecond line\n",
		},
		{
			name: "-m flag",
			args: []string{"-m", "message-backed comment"},
			want: "message-backed comment",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, taskID := migratedDBWithTask(t)

			cmd := NewRootCmdFor("wrkq")
			cmd.SetArgs(append([]string{
				"--db", dbPath,
				"--principal-ref", "agent:test-user",
				"comment", "add", taskID,
			}, tc.args...))
			if tc.stdin != nil {
				cmd.SetIn(tc.stdin)
			}
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("comment add failed: %v\noutput:\n%s", err, out.String())
			}

			database, err := db.Open(dbPath)
			if err != nil {
				t.Fatalf("db.Open: %v", err)
			}
			defer func() { _ = database.Close() }()

			var stored, commentUUID string
			if err := database.QueryRow(`
				SELECT body, uuid
				FROM comments
				WHERE task_uuid = ?
				ORDER BY id DESC
				LIMIT 1
			`, seedTaskUUID).Scan(&stored, &commentUUID); err != nil {
				t.Fatalf("query stored comment: %v", err)
			}
			if stored != tc.want {
				t.Fatalf("stored body mismatch:\nwant %q\ngot  %q", tc.want, stored)
			}

			var created int
			if err := database.QueryRow(`
				SELECT COUNT(*) FROM event_log
				WHERE resource_uuid = ? AND event_type = 'comment.created'
			`, commentUUID).Scan(&created); err != nil {
				t.Fatalf("query comment event: %v", err)
			}
			if created != 1 {
				t.Fatalf("comment.created events for the new comment = %d, want 1", created)
			}
			var commentTyped, legacy int
			if err := database.QueryRow(`
				SELECT
				  (SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'comment.created' AND resource_type = 'comment'),
				  (SELECT COUNT(*) FROM event_log WHERE event_type = 'comment_added')
			`, commentUUID).Scan(&commentTyped, &legacy); err != nil {
				t.Fatalf("query comment event shape: %v", err)
			}
			if commentTyped != 1 || legacy != 0 {
				t.Fatalf("comment.created with resource_type comment = %d (want 1), legacy comment_added rows = %d (want 0)", commentTyped, legacy)
			}
		})
	}
}
