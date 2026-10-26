//go:build wrkq_local

package rpccli

import (
	"regexp"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// `wrkq cat <P-id> --output raw` renders a container as a YAML front-matter
// card — fixed field set and order, top-level parent_path omitted, actor slugs
// for created_by/updated_by — followed by the description after a blank line;
// --no-frontmatter prints the description alone (nothing for none). UUIDs and
// timestamps are normalized; everything else is byte-exact.
func TestCatContainerRawPrintsFrontMatterCardInFixedFieldOrderThenDescription(t *testing.T) {
	t.Setenv("TZ", "UTC")
	dbPath, _ := migratedDBWithTask(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	var projectID string
	if err := database.QueryRow(`SELECT id FROM containers WHERE uuid = ?`, seedProject).Scan(&projectID); err != nil {
		t.Fatalf("read project id: %v", err)
	}
	_ = database.Close()

	cat := func(t *testing.T, extra ...string) string {
		t.Helper()
		result := runCatOneTestCLI(t, dbPath, append([]string{"cat", projectID, "--output", "raw"}, extra...))
		if result.errText != "" {
			t.Fatalf("cat %s: %s\n%s", projectID, result.errText, result.stderr)
		}
		out := uuidRe.ReplaceAllString(result.stdout, "<UUID>")
		return containerCardTimestampRe.ReplaceAllString(out, "$1: <TS>")
	}
	card := "---\n" +
		"id: " + projectID + "\n" +
		"uuid: <UUID>\n" +
		"slug: rpccli-test-proj\n" +
		"title: rpccli Test Project\n" +
		"kind: project\n" +
		"path: rpccli-test-proj\n" +
		"parent_id: P-00000\n" +
		"parent_uuid: <UUID>\n" +
		"sort_index: 0\n" +
		"etag: 1\n" +
		"created_at: <TS>\n" +
		"updated_at: <TS>\n" +
		"created_by: wrkq-system\n" +
		"updated_by: wrkq-system\n" +
		"---\n\n"

	if got, want := cat(t), card; got != want {
		t.Errorf("description-less card:\n got: %q\nwant: %q", got, want)
	}
	if got := cat(t, "--no-frontmatter"); got != "" {
		t.Errorf("--no-frontmatter without a description = %q, want empty", got)
	}

	database, err = db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := database.Exec(`UPDATE containers SET description = 'a project description' WHERE uuid = ?`, seedProject); err != nil {
		t.Fatalf("seed description: %v", err)
	}
	_ = database.Close()
	if got, want := cat(t), card+"a project description\n"; got != want {
		t.Errorf("card with description:\n got: %q\nwant: %q", got, want)
	}
	if got, want := cat(t, "--no-frontmatter"), "a project description\n"; got != want {
		t.Errorf("--no-frontmatter with a description = %q, want %q", got, want)
	}
}

var containerCardTimestampRe = regexp.MustCompile(`(?m)^(created_at|updated_at): .+$`)
