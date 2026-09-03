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

// RenderList terminates every item, including the last. Separator-style output
// silently cost `--output raw` its final record: a `while read` loop dropped it
// and `wc -l` undercounted by one.
func TestRenderListTerminatesEveryItem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []string
		want  string
	}{
		{"empty", []string{}, ""},
		{"single", []string{"R-00001"}, "R-00001\n"},
		{"multiple", []string{"R-00001", "R-00002"}, "R-00001\nR-00002\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			if err := NewRenderer(&buf, Options{}).RenderList(tc.items); err != nil {
				t.Fatalf("RenderList failed: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("RenderList = %q, want %q", got, tc.want)
			}
		})
	}
}

// A reader that splits on the delimiter sees one record per item, with no empty
// trailing field and no lost final field.
func TestRenderListRoundTripsThroughLineSplit(t *testing.T) {
	items := []string{"R-00001", "R-00002", "R-00003"}

	var buf strings.Builder
	if err := NewRenderer(&buf, Options{}).RenderList(items); err != nil {
		t.Fatalf("RenderList failed: %v", err)
	}

	got := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(got) != len(items) {
		t.Fatalf("split yielded %d records, want %d (%q)", len(got), len(items), buf.String())
	}
	for i, want := range items {
		if got[i] != want {
			t.Fatalf("record %d = %q, want %q", i, got[i], want)
		}
	}
}
