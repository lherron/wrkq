package render

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestRenderNDJSONTypedSlice(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = reader.Close()
	})

	if err := RenderNDJSON([]item{{ID: "one"}, {ID: "two"}}); err != nil {
		_ = writer.Close()
		t.Fatalf("RenderNDJSON failed: %v", err)
	}
	_ = writer.Close()
	os.Stdout = oldStdout

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), string(out))
	}
	if lines[0] != `{"id":"one"}` || lines[1] != `{"id":"two"}` {
		t.Fatalf("unexpected NDJSON output: %s", string(out))
	}
}
