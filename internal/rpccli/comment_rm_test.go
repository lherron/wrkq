//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// TestCommentDeleteParams_ForwardsIfMatch drives `wrkq comment rm --if-match`
// end to end. A stale --if-match is a warn+skip that leaves the comment live; a
// matching one soft-deletes (or purges under --purge); no --if-match deletes
// unconditionally. The "edited between show and delete" row runs through a
// remote proxy that bumps the comment's etag right after the CLI reads it, so
// the client-side pre-check passes and only the etag FORWARDED to
// wrkq.comment.delete (the server CAS) can stop the delete.
func TestCommentDeleteParams_ForwardsIfMatch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		raceAfterShow bool
		wantErr       bool
		wantStderr    string
		wantRows      int
		wantDeleted   int
	}{
		{name: "stale --if-match warns, skips and keeps the comment", args: []string{"--if-match", "999"},
			wantStderr: "Warning: etag mismatch for C-00001 (current: 1, expected: 999), skipping", wantRows: 1, wantDeleted: 0},
		{name: "edited between show and delete is refused by the server CAS", args: []string{"--if-match", "1"},
			raceAfterShow: true, wantErr: true, wantRows: 1, wantDeleted: 0},
		{name: "matching --if-match soft-deletes", args: []string{"--if-match", "1"}, wantRows: 1, wantDeleted: 1},
		{name: "matching --if-match with --purge purges", args: []string{"--if-match", "1", "--purge"}, wantRows: 0, wantDeleted: 0},
		{name: "no --if-match deletes unconditionally", raceAfterShow: true, wantRows: 1, wantDeleted: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, taskID := migratedDBWithTask(t)
			run := func(locator string, args ...string) (string, error) {
				cmd := NewRootCmdFor("wrkq")
				cmd.SetArgs(append([]string{"--db", locator, "--principal-ref", "agent:test-user"}, args...))
				var stderr bytes.Buffer
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&stderr)
				err := cmd.Execute()
				return stderr.String(), err
			}
			if stderr, err := run(dbPath, "comment", "add", taskID, "-m", "doomed comment"); err != nil {
				t.Fatalf("comment add: %v\n%s", err, stderr)
			}

			locator := dbPath
			if tc.raceAfterShow {
				locator = newCommentEditRaceRemote(t, dbPath)
			}
			stderr, err := run(locator, append([]string{"comment", "rm", "C-00001", "--yes"}, tc.args...)...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("comment rm err = %v, wantErr %v\nstderr:\n%s", err, tc.wantErr, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tc.wantStderr, stderr)
			}

			database, err := db.Open(dbPath)
			if err != nil {
				t.Fatalf("db.Open: %v", err)
			}
			defer func() { _ = database.Close() }()
			var rows, deleted int
			if err := database.QueryRow(
				`SELECT COUNT(*), COUNT(deleted_at) FROM comments WHERE id = 'C-00001'`,
			).Scan(&rows, &deleted); err != nil {
				t.Fatalf("read comment state: %v", err)
			}
			if rows != tc.wantRows || deleted != tc.wantDeleted {
				t.Fatalf("comment rows=%d soft-deleted=%d, want rows=%d soft-deleted=%d", rows, deleted, tc.wantRows, tc.wantDeleted)
			}
		})
	}
}

// newCommentEditRaceRemote serves the real registry over HTTP and, every time a
// wrkq.comment.show answer goes back to the CLI, bumps that comment's etag as a
// concurrent editor would.
func newCommentEditRaceRemote(t *testing.T, dbPath string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, _ := rpcServer.HandleRequest(r.Context(), req)
		if req.Method == "wrkq.comment.show" {
			if _, err := database.Exec(`UPDATE comments SET etag = etag + 1 WHERE id = 'C-00001'`); err != nil {
				t.Errorf("simulate concurrent edit: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", "")
	return "rpc://" + strings.TrimPrefix(server.URL, "http://")
}
