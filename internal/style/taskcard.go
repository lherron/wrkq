package style

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// StyledTask is the flattened view model the interactive `cat` card needs. It is
// decoupled from any storage/JSON struct so the renderer stays presentation only;
// both the CLI and the RPC mirror populate it from their own task projections.
type StyledTask struct {
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
	Outcome       string
	NoFrontmatter bool
}

type StyledComment struct {
	ID        string
	CreatedAt string
	Actor     string
	Role      string
	Body      string
}

// bodyIndent nests section content under its § header so the parent/child
// relationship is obvious at a glance.
const bodyIndent = "    "

// RenderStyledTask writes a task as a colorized card: a state-anchored header,
// the description rendered as markdown, an optional specification section, and
// quoted comments. Reached on an interactive TTY or when --pretty forces it.
func RenderStyledTask(w io.Writer, t StyledTask, comments []StyledComment) {
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
		_, _ = io.WriteString(w, Paint(ColSection, label)+"\n")
		_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n\n")
		RenderMarkdown(w, body, bodyIndent)
		wrote = true
	}

	section("§ Description", t.Description)
	section("§ Specification", t.Specification)
	section("§ Outcome", t.Outcome)

	if len(comments) > 0 {
		renderComments(w, comments)
	}
}

// renderHeader prints the title line plus an aligned metadata trailer and a
// full-width divider. The state-colored dot is the card's single accent.
func renderHeader(w io.Writer, t StyledTask) {
	dot := Paint(StateColor(t.State), "●")
	fmt.Fprintf(w, "%s  %s %s\n", Paint(ColDim, t.ID), dot, Paint(ColHeading, t.Title))

	indent := strings.Repeat(" ", len(t.ID)+3)
	sep := Paint(ColRule, " · ")

	var meta []string
	meta = append(meta, Paint(StateColor(t.State), t.State))
	meta = append(meta, Paint(ColDim, fmt.Sprintf("P%d", t.Priority)))
	if t.Assignee != nil && *t.Assignee != "" {
		meta = append(meta, Paint(ColDir, "@"+*t.Assignee))
	}
	if t.Path != "" {
		meta = append(meta, Paint(ColDim, t.Path))
	}
	_, _ = io.WriteString(w, indent+strings.Join(meta, sep)+"\n")

	var trailer []string
	if t.UpdatedAt != "" {
		// "updated <duration>" reads at full contrast so freshness pops; the
		// absolute "on <date>" recedes as a quiet reference.
		age, on := formatUpdatedAge(t.UpdatedAt)
		seg := "updated " + age
		if on != "" {
			seg += Paint(ColDim, " "+on)
		}
		trailer = append(trailer, seg)
	}
	if labels := formatLabels(t.Labels); labels != "" {
		trailer = append(trailer, Paint(ColMarker, labels))
	}
	if t.DueAt != nil && *t.DueAt != "" {
		trailer = append(trailer, Paint(ColDim, "due "+ShortStamp(*t.DueAt)))
	}
	if t.BlockedCount > 0 {
		trailer = append(trailer, Paint(ColStateStop, fmt.Sprintf("%d blocked", t.BlockedCount)))
	}
	if len(trailer) > 0 {
		_, _ = io.WriteString(w, indent+strings.Join(trailer, sep)+"\n")
	}

	_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n")
}

// renderComments prints the comment thread under a counted heading.
func renderComments(w io.Writer, comments []StyledComment) {
	_, _ = io.WriteString(w, "\n"+Paint(ColSection, fmt.Sprintf("Comments (%d)", len(comments)))+"\n")
	_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n")

	bar := Paint(ColRule, "▏ ")
	for _, c := range comments {
		head := Paint(ColDim, c.ID) + Paint(ColRule, " · ") + Paint(ColDim, ShortStamp(c.CreatedAt))
		if c.Actor != "" {
			head += " " + Paint(ColDir, "@"+c.Actor)
		}
		if c.Role != "" {
			head += Paint(ColDim, " ("+c.Role+")")
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

// formatUpdatedAge splits a timestamp into a humanized "<duration> ago" phrase
// and an "on <date>" absolute reference, so each can be styled independently.
// When the timestamp can't be parsed, the date carries the age and on is empty.
func formatUpdatedAge(ts string) (age, on string) {
	parsed, ok := ParseTimestamp(ts)
	if !ok {
		return ShortStamp(ts), ""
	}
	elapsed := NowUTC().Sub(parsed)
	if elapsed < 0 {
		elapsed = 0
	}
	return FormatDuration(elapsed) + " ago", "on " + ShortStamp(ts)
}
