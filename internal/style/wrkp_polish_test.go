package style

import (
	"bytes"
	"strings"
	"testing"
)

// TestWidthResolvedPerRender is the regression for T-08225: internal/style used
// to resolve the terminal width ONCE at package init, so a long-lived render
// (wrkp log --follow) kept wrapping to the width the stream started with. This
// renders TWICE in ONE PROCESS under two different COLUMNS values and asserts the
// second respects the change — which is impossible to satisfy with a
// package-level var.
func TestWidthResolvedPerRender(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })

	const body = "wrapped prose long enough to break differently at sixty columns than it does at a hundred and ten columns wide"

	render := func(columns string) string {
		t.Setenv("COLUMNS", columns)
		var buf bytes.Buffer
		emitFlow(&buf, "", "", body)
		return buf.String()
	}

	narrow := render("60")
	wide := render("110")

	if narrow == wide {
		t.Fatalf("width did not change between renders in one process; still resolved once at init\nnarrow:\n%s\nwide:\n%s", narrow, wide)
	}

	longestLine := func(s string) int {
		longest := 0
		for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			if n := visibleWidth(line); n > longest {
				longest = n
			}
		}
		return longest
	}
	if got := longestLine(narrow); got >= 60 {
		t.Errorf("narrow render longest line = %d, want < 60", got)
	}
	if got := longestLine(wide); got < 60 {
		t.Errorf("wide render longest line = %d, want >= 60 (did not widen)", got)
	}
}

// TestWidthDeterministicUnderFixedColumns guards the property that made
// resolving-once defensible: with COLUMNS fixed, repeated renders in one process
// are byte-identical, so non-TTY --pretty byte-parity survives T-08225.
func TestWidthDeterministicUnderFixedColumns(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })
	t.Setenv("COLUMNS", "72")

	const body = "the same prose rendered twice under one fixed COLUMNS value must not move a single byte"
	var first, second bytes.Buffer
	emitFlow(&first, "  ", "  ", body)
	emitFlow(&second, "  ", "  ", body)

	if first.String() != second.String() {
		t.Fatalf("repeated render under fixed COLUMNS differed:\nfirst:\n%q\nsecond:\n%q", first.String(), second.String())
	}
}

// TestRenderStyledTypesEmptySpeaks is the regression for T-08224's headline
// symptom: `wrkp types` on a project with no facts printed NOTHING on a TTY,
// which is indistinguishable from a hang or a broken command. The empty set must
// say so and must name the project.
func TestRenderStyledTypesEmptySpeaks(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })

	var buf bytes.Buffer
	RenderStyledTypes(&buf, "hcs", nil)
	got := buf.String()

	if strings.TrimSpace(got) == "" {
		t.Fatal("empty type set rendered nothing; that is the defect")
	}
	if !strings.Contains(got, "hcs") {
		t.Errorf("empty render does not name the project:\n%s", got)
	}
	if !strings.Contains(got, "no facts have been posted") {
		t.Errorf("empty render does not say the set is empty:\n%s", got)
	}
	// The cause is almost always a missing producer; say so rather than leaving
	// the reader to guess whether the command is broken.
	if !strings.Contains(got, "wrkp post") {
		t.Errorf("empty render does not point at the cause:\n%s", got)
	}
}

// TestRenderStyledTypesListsFacts covers the populated path: every type, its
// count, and a relative age pinned by WRKQ_NOW so the assertion is stable.
func TestRenderStyledTypesListsFacts(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })
	t.Setenv("WRKQ_NOW", "2026-09-09T18:00:00Z")

	var buf bytes.Buffer
	RenderStyledTypes(&buf, "hrc-runtime", []StyledType{
		{Type: "git.commit", Count: 81, LastCreatedAt: "2026-09-09T17:00:00Z"},
		{Type: "git.push", Count: 46, LastCreatedAt: "2026-09-08T18:00:00Z"},
	})
	got := buf.String()

	for _, want := range []string{"hrc-runtime", "git.commit", "81", "git.push", "46", "1 hour ago", "1 day ago"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q:\n%s", want, got)
		}
	}
	// It must not degrade into the raw JSON it used to emit.
	if strings.Contains(got, "\"type\"") || strings.HasPrefix(strings.TrimSpace(got), "[") {
		t.Errorf("render still looks like JSON:\n%s", got)
	}
}
