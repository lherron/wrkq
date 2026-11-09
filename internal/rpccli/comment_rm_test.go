//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// TestCommentDeleteParams_ForwardsIfMatch drives `wrkq comment rm --if-match`
// end to end. A stale --if-match is a typed WRKQ_CONFLICT failure (exit 1) that
// leaves the comment live (T-12765: it used to warn, skip and exit 0); a
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
		wantCode      string
		wantStderr    string
		wantRows      int
		wantDeleted   int
	}{
		{name: "stale --if-match is a typed conflict and keeps the comment", args: []string{"--if-match", "999"},
			wantErr: true, wantCode: "WRKQ_CONFLICT", wantRows: 1, wantDeleted: 0},
		{name: "edited between show and delete is refused by the server CAS", args: []string{"--if-match", "1"},
			raceAfterShow: true, wantErr: true, wantCode: "WRKQ_CONFLICT", wantRows: 1, wantDeleted: 0},
		{name: "matching --if-match soft-deletes", args: []string{"--if-match", "1"}, wantRows: 1, wantDeleted: 1},
		{name: "matching --if-match with --purge purges", args: []string{"--if-match", "1", "--purge"}, wantRows: 0, wantDeleted: 0},
		{name: "no --if-match deletes unconditionally", raceAfterShow: true, wantRows: 1, wantDeleted: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, taskID := migratedDBWithTask(t)
			if _, stderr, err := runCommentRmCLI(dbPath, "comment", "add", taskID, "-m", "doomed comment"); err != nil {
				t.Fatalf("comment add: %v\n%s", err, stderr)
			}

			locator := dbPath
			if tc.raceAfterShow {
				locator = newCommentEditRaceRemote(t, dbPath)
			}
			_, stderr, err := runCommentRmCLI(locator, append([]string{"comment", "rm", "C-00001", "--yes"}, tc.args...)...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("comment rm err = %v, wantErr %v\nstderr:\n%s", err, tc.wantErr, stderr)
			}
			if tc.wantCode != "" {
				assertTypedFailure(t, err, tc.wantCode)
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

// TestCommentRm_MultiIDStaleFenceReportsPerItem: one fence over two comments
// where only the first still matches deletes the first, refuses the second with
// a typed conflict naming it, and exits 1 (bulk `set` semantics: failed>0).
func TestCommentRm_MultiIDStaleFenceReportsPerItem(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	for _, body := range []string{"kept fence", "moved fence"} {
		if _, stderr, err := runCommentRmCLI(dbPath, "comment", "add", taskID, "-m", body); err != nil {
			t.Fatalf("comment add: %v\n%s", err, stderr)
		}
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`UPDATE comments SET etag = 2 WHERE id = 'C-00002'`); err != nil {
		t.Fatalf("bump C-00002: %v", err)
	}

	stdout, stderr, err := runCommentRmCLI(dbPath, "comment", "rm", "C-00001", "C-00002", "--if-match", "1", "--yes", "--json")
	assertTypedFailure(t, err, "WRKQ_CONFLICT")
	if !strings.Contains(err.Error(), "C-00002") || strings.Contains(err.Error(), "C-00001") {
		t.Fatalf("failure should name only C-00002: %v", err)
	}
	var rows []map[string]any
	if uerr := json.Unmarshal([]byte(stdout), &rows); uerr != nil {
		t.Fatalf("stdout is not the deleted-rows array: %v\n%s\nstderr:\n%s", uerr, stdout, stderr)
	}
	if len(rows) != 1 || rows[0]["id"] != "C-00001" || rows[0]["deleted"] != true {
		t.Fatalf("stdout rows = %v, want only C-00001 deleted", rows)
	}
	var deleted1, deleted2 int
	if err := database.QueryRow(
		`SELECT COUNT(CASE WHEN id='C-00001' THEN deleted_at END), COUNT(CASE WHEN id='C-00002' THEN deleted_at END) FROM comments`,
	).Scan(&deleted1, &deleted2); err != nil {
		t.Fatalf("read comment state: %v", err)
	}
	if deleted1 != 1 || deleted2 != 0 {
		t.Fatalf("soft-deleted C-00001=%d C-00002=%d, want 1/0", deleted1, deleted2)
	}
}

func runCommentRmCLI(locator string, args ...string) (stdout, stderr string, err error) {
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", locator, "--principal-ref", "agent:test-user"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// assertTypedFailure checks what the wrkq binary would do with err: exit 1 and,
// under --json, a structured envelope carrying code.
func assertTypedFailure(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %s failure, got success", code)
	}
	if got := ExitCodeForError(err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (err: %v)", got, err)
	}
	var envelope bytes.Buffer
	clifunnel.Report(&envelope, err, clifunnel.ErrorJSON, "WRKQ")
	if !strings.Contains(envelope.String(), `"code": "`+code+`"`) {
		t.Fatalf("--json envelope missing code %s:\n%s", code, envelope.String())
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
