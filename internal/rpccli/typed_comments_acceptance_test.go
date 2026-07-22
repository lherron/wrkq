package rpccli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestCommentCLIPlainAndTypedTaskComments(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	runCommentCLI(t, dbPath, "comment", "add", taskID, "-m", "[blocker] literal prose")
	runCommentCLI(t, dbPath, "comment", "add", taskID, "-m", "typed decision", "--kind", "decision")

	out := runCommentCLI(t, dbPath, "comment", "ls", taskID, "--json")
	if !strings.Contains(out, "[blocker] literal prose") || !strings.Contains(out, `"kind": "decision"`) {
		t.Fatalf("task comment list missing plain/typed comments:\n%s", out)
	}

	database := openCommentAcceptanceDB(t, dbPath)
	var plainKind, typedKind sql.NullString
	if err := database.QueryRow("SELECT kind FROM comments WHERE body = ?", "[blocker] literal prose").Scan(&plainKind); err != nil {
		t.Fatalf("load plain task kind: %v", err)
	}
	if err := database.QueryRow("SELECT kind FROM comments WHERE body = ?", "typed decision").Scan(&typedKind); err != nil {
		t.Fatalf("load typed task kind: %v", err)
	}
	if plainKind.Valid || !typedKind.Valid || typedKind.String != "decision" {
		t.Fatalf("task kinds plain=%#v typed=%#v, want NULL/decision", plainKind, typedKind)
	}
}

func TestCommentCLIContainerMetaRoundTrip(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	database := openCommentAcceptanceDB(t, dbPath)
	var containerID string
	if err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", seedProject).Scan(&containerID); err != nil {
		t.Fatalf("load container id: %v", err)
	}

	runCommentCLI(t, dbPath, "comment", "add", containerID, "-m", "plain container comment")
	runCommentCLI(t, dbPath, "comment", "add", containerID, "-m", "digest comment", "--kind", "digest", "--meta", `{"event_log_id":913}`)
	out := runCommentCLI(t, dbPath, "comment", "ls", containerID, "--json")
	for _, want := range []string{"plain container comment", `"kind": "digest"`, `"event_log_id": 913`} {
		if !strings.Contains(out, want) {
			t.Errorf("container comment list missing %q:\n%s", want, out)
		}
	}

	var taskUUID, containerUUID, kind, meta sql.NullString
	if err := database.QueryRow(
		"SELECT task_uuid, container_uuid, kind, meta FROM comments WHERE body = ?", "digest comment",
	).Scan(&taskUUID, &containerUUID, &kind, &meta); err != nil {
		t.Fatalf("load digest comment: %v", err)
	}
	if taskUUID.Valid || !containerUUID.Valid || containerUUID.String != seedProject || kind.String != "digest" {
		t.Fatalf("digest parent/kind task=%#v container=%#v kind=%#v", taskUUID, containerUUID, kind)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(meta.String), &decoded); err != nil {
		t.Fatalf("decode digest meta %q: %v", meta.String, err)
	}
	if decoded["event_log_id"] != float64(913) {
		t.Fatalf("digest meta = %#v, want event_log_id=913", decoded)
	}
}

func TestCommentCLIRejectsUnknownKindHelpfully(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{"--db", dbPath, "--principal-ref", "agent:test-user", "comment", "add", taskID, "-m", "bad", "--kind", "heartbeat"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("CLI accepted invalid comment kind heartbeat")
	}
	message := strings.ToLower(err.Error() + "\n" + out.String())
	for _, want := range []string{"invalid comment kind", "blocker", "decision", "postmortem", "digest"} {
		if !strings.Contains(message, want) {
			t.Errorf("CLI invalid-kind error %q does not contain %q", message, want)
		}
	}
}

func runCommentCLI(t *testing.T, dbPath string, args ...string) string {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:test-user"}, args...))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wrkq %s: %v\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func openCommentAcceptanceDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
