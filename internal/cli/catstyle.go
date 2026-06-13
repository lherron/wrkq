package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// styledTask is the flattened view model the interactive `cat` renderer needs.
// It is decoupled from cat.go's JSON struct so the renderer stays presentation
// only.
type styledTask struct {
	ID            string
	Path          string
	Title         string
	State         string
	Priority      int
	Assignee      *string
	Labels        *string
	DueAt         *string
	UpdatedAt     string
	BlockedCount  int
	Description   string
	Specification string
	NoFrontmatter bool
}

type styledComment struct {
	ID        string
	CreatedAt string
	Actor     string
	Role      string
	Body      string
}

// renderStyledTask writes a task as a colorized card: a state-anchored header,
// the description rendered as markdown, an optional specification section, and
// quoted comments. Only reached on an interactive TTY.
// bodyIndent nests section content under its § header so the parent/child
// relationship is obvious at a glance.
const bodyIndent = "    "

func renderStyledTask(w io.Writer, t styledTask, comments []styledComment) {
	if !t.NoFrontmatter {
		renderHeader(w, t)
	}
	wrote := !t.NoFrontmatter

	section := func(label, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		if wrote {
			_, _ = io.WriteString(w, "\n")
		}
		_, _ = io.WriteString(w, paint(colSection, label)+"\n")
		_, _ = io.WriteString(w, paint(colRule, strings.Repeat("─", ruleWidth))+"\n\n")
		renderMarkdown(w, body, bodyIndent)
		wrote = true
	}

	section("§ Description", t.Description)
	section("§ Specification", t.Specification)

	if len(comments) > 0 {
		renderComments(w, comments)
	}
}

// renderHeader prints the title line plus an aligned metadata trailer and a
// full-width divider. The state-colored dot is the card's single accent.
func renderHeader(w io.Writer, t styledTask) {
	dot := paint(stateColor(t.State), "●")
	fmt.Fprintf(w, "%s  %s %s\n", paint(colDim, t.ID), dot, paint(colHeading, t.Title))

	indent := strings.Repeat(" ", len(t.ID)+3)
	sep := paint(colRule, " · ")

	var meta []string
	meta = append(meta, paint(stateColor(t.State), t.State))
	meta = append(meta, paint(colDim, fmt.Sprintf("P%d", t.Priority)))
	if t.Assignee != nil && *t.Assignee != "" {
		meta = append(meta, paint(colDir, "@"+*t.Assignee))
	}
	if t.Path != "" {
		meta = append(meta, paint(colDim, t.Path))
	}
	_, _ = io.WriteString(w, indent+strings.Join(meta, sep)+"\n")

	var trailer []string
	if t.UpdatedAt != "" {
		// "updated <duration>" reads at full contrast so freshness pops; the
		// absolute "on <date>" recedes as a quiet reference.
		age, on := formatUpdatedAge(t.UpdatedAt)
		seg := "updated " + age
		if on != "" {
			seg += paint(colDim, " "+on)
		}
		trailer = append(trailer, seg)
	}
	if labels := formatLabels(t.Labels); labels != "" {
		trailer = append(trailer, paint(colMarker, labels))
	}
	if t.DueAt != nil && *t.DueAt != "" {
		trailer = append(trailer, paint(colDim, "due "+shortStamp(*t.DueAt)))
	}
	if t.BlockedCount > 0 {
		trailer = append(trailer, paint(colStateStop, fmt.Sprintf("%d blocked", t.BlockedCount)))
	}
	if len(trailer) > 0 {
		_, _ = io.WriteString(w, indent+strings.Join(trailer, sep)+"\n")
	}

	_, _ = io.WriteString(w, paint(colRule, strings.Repeat("─", ruleWidth))+"\n")
}

// renderComments prints the comment thread under a counted heading.
func renderComments(w io.Writer, comments []styledComment) {
	_, _ = io.WriteString(w, "\n"+paint(colSection, fmt.Sprintf("Comments (%d)", len(comments)))+"\n")
	_, _ = io.WriteString(w, paint(colRule, strings.Repeat("─", ruleWidth))+"\n")

	bar := paint(colRule, "▏ ")
	for _, c := range comments {
		head := paint(colDim, c.ID) + paint(colRule, " · ") + paint(colDim, shortStamp(c.CreatedAt))
		if c.Actor != "" {
			head += " " + paint(colDir, "@"+c.Actor)
		}
		if c.Role != "" {
			head += paint(colDim, " ("+c.Role+")")
		}
		_, _ = io.WriteString(w, bar+head+"\n")
		for _, line := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				_, _ = io.WriteString(w, bar+"\n")
				continue
			}
			emitFlow(w, bar, bar, line)
		}
		_, _ = io.WriteString(w, "\n")
	}
}

// formatLabels normalizes a label string into "#a #b". Labels are stored as a
// JSON array; it falls back to comma/space splitting for legacy plain values.
func formatLabels(labels *string) string {
	if labels == nil || strings.TrimSpace(*labels) == "" {
		return ""
	}
	var fields []string
	if err := json.Unmarshal([]byte(*labels), &fields); err != nil {
		fields = strings.FieldsFunc(*labels, func(r rune) bool { return r == ',' || r == ' ' })
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, "#"+f)
		}
	}
	return strings.Join(out, " ")
}

// shortStamp trims an RFC3339-ish timestamp to its date for compact display.
func shortStamp(ts string) string {
	if len(ts) >= 10 && ts[4] == '-' && ts[7] == '-' {
		return ts[:10]
	}
	return ts
}

// formatUpdatedAge splits a timestamp into a humanized "<duration> ago" phrase
// and an "on <date>" absolute reference, so each can be styled independently.
// When the timestamp can't be parsed, the date carries the age and on is empty.
func formatUpdatedAge(ts string) (age, on string) {
	parsed, ok := parseTreeTimestamp(ts)
	if !ok {
		return shortStamp(ts), ""
	}
	elapsed := time.Now().UTC().Sub(parsed)
	if elapsed < 0 {
		elapsed = 0
	}
	return formatTreeDuration(elapsed) + " ago", "on " + shortStamp(ts)
}
