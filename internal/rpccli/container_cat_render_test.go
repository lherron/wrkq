//go:build wrkq_local

package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestContainerCatMarkdownRender proves the markdown (front matter + body) and the
// "raw" (--no-frontmatter, body-only) render modes byte-for-byte against the legacy
// runContainerCat format. The pipe-based parity harness cannot drive a TTY, so the
// front-matter markdown branch (which legacy only takes on a TTY) is proven here by
// pulling the REAL wrkq.container.catView projection over the in-process transport
// and rendering it through the same renderContainerMarkdown the CLI uses. UUIDs and
// timestamps are normalized; the field set, order, and YAML framing are pinned.
func TestContainerCatMarkdownRender(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	tr := inProcessTransport(t, dbPath)
	defer func() { _ = tr.Close() }()

	raw, err := tr.Call(context.Background(), "wrkq.container.catView",
		map[string]string{"container": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("container.catView: %v", err)
	}
	var c containerCatModel
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("decode catView: %v", err)
	}

	norm := func(s string) string {
		s = uuidRe.ReplaceAllString(s, "<UUID>")
		s = rfc3339Re.ReplaceAllString(s, "<TS>")
		return s
	}

	// Full markdown (the TTY default): YAML front matter then body. This fixture's
	// container is top-level under the synthetic root (so parent_id/parent_uuid are
	// emitted but parent_path is omitted — the path has no '/'), has no webhook_urls,
	// no archived_at, and no description (body omitted). created_by/updated_by are
	// resolved actor slugs. This pins the field set and order exactly against legacy
	// runContainerCat.
	var md bytes.Buffer
	if err := renderContainerMarkdown(&md, &c, false); err != nil {
		t.Fatal(err)
	}
	wantMarkdown := "---\n" +
		"id: " + c.ID + "\n" +
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
	if got := norm(md.String()); got != wantMarkdown {
		t.Errorf("markdown render mismatch:\n got: %q\n want: %q", got, wantMarkdown)
	}

	// --no-frontmatter ("raw"): body only. No description on this fixture → empty.
	var rawOut bytes.Buffer
	if err := renderContainerMarkdown(&rawOut, &c, true); err != nil {
		t.Fatal(err)
	}
	if rawOut.Len() != 0 {
		t.Errorf("no-frontmatter render should be empty for description-less container, got %q", rawOut.String())
	}

	// With a description, the body is the description line (raw mode), and the full
	// markdown appends the same body after a blank line.
	withDesc := c
	withDesc.Description = "a project description"
	var rawDesc bytes.Buffer
	if err := renderContainerMarkdown(&rawDesc, &withDesc, true); err != nil {
		t.Fatal(err)
	}
	if got, want := rawDesc.String(), "a project description\n"; got != want {
		t.Errorf("raw body render mismatch:\n got: %q\n want: %q", got, want)
	}
	var mdDesc bytes.Buffer
	if err := renderContainerMarkdown(&mdDesc, &withDesc, false); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(mdDesc.String(), "---\n\na project description\n") {
		t.Errorf("markdown-with-body should end with framed body, got %q", mdDesc.String())
	}
}
