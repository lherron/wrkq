package style

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Timeline presentation. The merged project timeline is braided, not flat:
// several tasks advance at once while facts that belong to no task at all land
// between them. The rail carries that structure. An entry that names a task
// hangs under a run header on a │ rail; a fact that names none floats at the
// margin behind a ◆. Task attachment is the schema's own distinction — an entry
// either carries a task id or it does not — so one glyph tells the truth about
// every row, including project events posted against a task, which thread into
// that task's run exactly as a comment does.
//
// The five entry kinds do not carry equal weight and are not drawn at equal
// weight: a comment is prose and gets flowed body text, a state change is a tick
// and gets one line. Color stays on structure, per the palette's own rule.
const (
	timelineRail     = "│"
	timelineRailEnd  = "└"
	timelineFloat    = "◆"
	timelineRunDot   = "●"
	timelineGutter   = 12 // "  <glyph> HH:MM   " — one time column for rails and floats
	timelineProseMin = 20 // narrowest prose budget, matching wrapWidth's own floor
	timelineBodyCap  = 4  // flowed rows of a comment/outcome before it is elided
	timelinePairGap  = 2  // columns between a label and the right-hand principal
	timelineMeasure  = 88 // widest comfortable column for a log that has a right margin
)

// timelineFillBody selects how much of an entry's prose the log shows. Flip
// this one constant to revert.
//
// When true, the log fills the whole timelineBodyCap budget from the body,
// flowing block after block until the budget is spent: every row shown carries
// real content, and the count held back is in one unit, display rows. When
// false it restores the older rule — show only the leading paragraph, whatever
// its length, and hold back everything after the first blank line.
//
// The older rule reasoned from git's commit subject, but a commit subject is a
// format with a contract and a ledger comment is not. The prevailing house
// style opens with a one-line headline, which that rule reads as the whole
// thesis: the four-row budget went unspent while the substance was held back.
// Sampled 2026-09-10 across four jobhunt tasks, 10 of 41 comments showed under
// 90 characters while hiding the rest — the worst showed 45 characters and held
// back 128 lines, on a terminal with room for roughly 1,200.
const timelineFillBody = true

// timelineWidth is the log's frame: rules, right-aligned actors and flowed prose
// all land on it, so the stream reads as one column rather than full-bleed text
// with a rule floating at RuleWidth. A card is a narrow object and keeps
// RuleWidth; a log is a stream with a right-hand column and needs the room.
func timelineWidth() int {
	if w := wrapWidth(); w < timelineMeasure {
		return w
	}
	return timelineMeasure
}

// StyledEntry is the flattened view model for one merged-timeline entry. It is
// decoupled from any storage or wire struct so the renderer stays presentation
// only; the CLI populates it from its own timeline projection.
type StyledEntry struct {
	// Label is the row's headline: what happened. Rendered in Accent.
	Label string
	// Accent is the SGR parameter string for Label; empty leaves it unpainted.
	Accent string
	// ID is the entry's own public identifier (C-xxxxx, PE-xxxxx) when it has
	// one, rendered dim beside the label.
	ID string
	// From is the seat an entry came FROM, rendered ahead of the label in the
	// actor color. It is a separate field rather than text folded into Label so
	// the two keep their own colors: an actor stays an actor wherever on the row
	// it is drawn, and neither has to nest an SGR run inside the other.
	From string
	// Principal is the actor, rendered right-aligned. Every row answers "who".
	Principal string
	// Body is prose flowed under the label — a comment or an outcome. Empty for
	// ticks.
	Body string
	// Timestamp is the entry's RFC3339 server time.
	Timestamp string
	// TaskID and TaskPath attach the entry to a task. Empty means the entry
	// floats: it belongs to the project itself.
	TaskID   string
	TaskPath string
	// TaskState is the state this entry moved its task to, when it moved one. It
	// colors the run header's dot.
	TaskState string
}

// TimelineWriter renders a merged timeline incrementally. The log arrives in
// cursor pages and, under --follow, in a poll at a time, so day headers and run
// headers have to survive across calls: a run that continues into the next batch
// keeps its rail instead of restating its header.
type TimelineWriter struct {
	w        io.Writer
	project  string
	day      string
	lastTask string
	opened   bool
}

// NewTimelineWriter returns a writer that will head the log with project before
// its first batch.
func NewTimelineWriter(w io.Writer, project string) *TimelineWriter {
	return &TimelineWriter{w: w, project: project}
}

// Write draws one batch of entries. Entries are never reordered — only
// chronologically adjacent entries sharing a task fuse into one run, so the
// merge order the server delivered survives exactly.
func (t *TimelineWriter) Write(entries []StyledEntry) {
	if len(entries) == 0 {
		return
	}
	if !t.opened {
		t.opened = true
		if t.project != "" {
			_, _ = io.WriteString(t.w, Paint(ColDir, t.project)+"\n")
			_, _ = io.WriteString(t.w, Paint(ColRule, strings.Repeat("─", timelineWidth()))+"\n")
		}
	}
	for i := 0; i < len(entries); {
		runDay := timelineDay(entries[i].Timestamp)
		runTask := entries[i].TaskID
		j := i + 1
		for j < len(entries) &&
			entries[j].TaskID == runTask &&
			timelineDay(entries[j].Timestamp) == runDay {
			j++
		}
		newDay := runDay != t.day
		if newDay {
			_, _ = io.WriteString(t.w, "\n"+Paint(ColSection, runDay)+"\n")
			t.day = runDay
		}
		// A run split across batches keeps its rail: only restate the header when
		// the task actually changed or the day turned.
		resumed := i == 0 && !newDay && runTask != "" && runTask == t.lastTask
		renderTimelineRun(t.w, entries[i:j], resumed)
		t.lastTask = runTask
		i = j
	}
}

// RenderStyledTimeline writes a complete timeline in one call. It is the
// single-batch form of TimelineWriter, kept for callers that already hold every
// entry.
func RenderStyledTimeline(w io.Writer, project string, entries []StyledEntry) {
	if len(entries) == 0 {
		if project != "" {
			fmt.Fprintf(w, "%s\n", Paint(ColDim, "no timeline entries in "+project))
		}
		return
	}
	NewTimelineWriter(w, project).Write(entries)
}

// renderTimelineRun draws one run: either a task's header plus its railed rows,
// or a stretch of floating project-level facts.
func renderTimelineRun(w io.Writer, run []StyledEntry, resumed bool) {
	railed := run[0].TaskID != ""
	switch {
	case railed && !resumed:
		renderRunHeader(w, run)
	case !railed:
		_, _ = io.WriteString(w, "\n")
	}

	for i, entry := range run {
		glyph := timelineFloat
		contRail := " "
		if railed {
			glyph = timelineRail
			contRail = timelineRail
			if i == len(run)-1 {
				glyph = timelineRailEnd
				contRail = " "
			}
		}
		lead := "  " + Paint(ColRule, glyph) + " " +
			Paint(ColDim, timelineClock(entry.Timestamp)) + "   "
		// Only a live rail is painted; a closed run continues under plain space
		// rather than a colored blank.
		cont := "  " + contRail + strings.Repeat(" ", timelineGutter-3)
		if contRail == timelineRail {
			cont = "  " + Paint(ColRule, contRail) + strings.Repeat(" ", timelineGutter-3)
		}

		_, _ = io.WriteString(w, lead+timelineRow(entry, timelineWidth()-timelineGutter)+"\n")
		renderTimelineBody(w, cont, entry.Body)
	}
}

// renderRunHeader prints a task run's header in the task card's own grammar:
// dim id, a state-colored dot, the task slug, and the containing path pushed to
// the right margin. Rows answer "who"; headers answer "where".
func renderRunHeader(w io.Writer, run []StyledEntry) {
	head := run[0]
	state := ""
	for _, entry := range run {
		if entry.TaskState != "" {
			state = entry.TaskState
		}
	}
	dot := Paint(ColDim, timelineRunDot)
	if state != "" {
		dot = Paint(StateColor(state), timelineRunDot)
	}
	left := Paint(ColDim, head.TaskID) + "  " + dot + " " + Paint(ColHeading, timelineSlug(head.TaskPath))
	_, _ = io.WriteString(w, "\n"+padPair(left, Paint(ColDim, timelineParent(head.TaskPath)))+"\n")
}

// timelineRow lays out one row: the originating seat when the entry names one,
// the label, its own id when it has one, and the actor at the right margin.
func timelineRow(entry StyledEntry, width int) string {
	left := Paint(entry.Accent, entry.Label)
	if entry.From != "" {
		left = Paint(ColDir, entry.From) + " " + left
	}
	if entry.ID != "" && entry.ID != entry.Label {
		left += "  " + Paint(ColDim, entry.ID)
	}
	right := ""
	if entry.Principal != "" {
		right = Paint(ColDir, entry.Principal)
	}
	return padPairWidth(left, right, width)
}

// renderTimelineBody flows an entry's prose under its label. Only the leading
// paragraph is shown, capped at timelineBodyCap lines: ledger prose opens with
// its thesis, the same reason git shows a commit subject rather than the whole
// message. The remainder is counted, never silently dropped.
//
// Prose flows to the full terminal (timelineProseWidth), NOT to timelineWidth,
// and the four-line cap and the elided count are therefore measured against the
// reader's own terminal: a wider window holds back fewer lines. The log's
// 88-column frame exists so the day rule and the right-aligned actor have one
// edge to land on — a right-hand column needs something to align to. A comment
// body has no right-hand column, so that frame buys it nothing and only costs
// it lines: on a wide terminal the same paragraph was being broken into twice
// the rows for no structural reason. The two measures are independent, and this
// is the one place they part.
func renderTimelineBody(w io.Writer, cont, body string) {
	shown, elided := timelineBodyRows(cont, body)
	for _, line := range shown {
		_, _ = io.WriteString(w, line+"\n")
	}
	if elided > 0 {
		_, _ = io.WriteString(w, cont+Paint(ColRule, fmt.Sprintf("… %d more lines", elided))+"\n")
	}
}

// timelineBodyRows resolves a body into the rows to print and the count held
// back, under whichever rule timelineFillBody selects.
func timelineBodyRows(cont, body string) ([]string, int) {
	if !timelineFillBody {
		paragraph, rest := timelineLead(body)
		if paragraph == "" {
			return nil, 0
		}
		lines := timelineFlow(cont, paragraph)
		if len(lines) > timelineBodyCap {
			return lines[:timelineBodyCap], rest + len(lines) - timelineBodyCap
		}
		return lines, rest
	}
	var rows []string
	for _, block := range timelineBlocks(body) {
		rows = append(rows, timelineFlow(cont, block)...)
	}
	if len(rows) > timelineBodyCap {
		return rows[:timelineBodyCap], len(rows) - timelineBodyCap
	}
	return rows, 0
}

// timelineFlow wraps one already-joined block to the prose budget and returns
// its rows, prefix included.
func timelineFlow(cont, block string) []string {
	var buf strings.Builder
	emitFlowWidth(&buf, cont, cont, block, timelineProseWidth())
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// timelineBlocks splits a body at its blank lines and joins each block onto one
// line. The joining is not cosmetic: the wrapper breaks on spaces and treats a
// newline as an ordinary character, so a block reaches it as one line or not at
// all.
func timelineBlocks(body string) []string {
	var blocks []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, strings.TrimSpace(strings.Join(current, " ")))
			current = nil
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

// timelineProseWidth is the column budget for flowed timeline prose: the whole
// terminal less the one-column safety margin that keeps the terminal from
// re-wrapping our lines, and NO upper ceiling.
//
// It deliberately does not use wrapWidth. That helper caps flowed text at 120
// columns, a number written for CARD prose — where the card's own 64-column
// frame decides the measure and the cap never binds. Inherited into a log whose
// prose is otherwise unframed, it would be the only thing standing between a
// comment and the terminal the reader chose, and there is no argument on record
// for it doing that job here.
func timelineProseWidth() int {
	if w := detectedWidth() - 1; w > timelineProseMin {
		return w
	}
	return timelineProseMin
}

// timelineLead splits prose into its leading paragraph and a count of the lines
// held back.
func timelineLead(body string) (string, int) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", 0
	}
	lines := strings.Split(body, "\n")
	end := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			end = i
			break
		}
	}
	lead := strings.TrimSpace(strings.Join(lines[:end], " "))
	rest := 0
	for _, line := range lines[end:] {
		if strings.TrimSpace(line) != "" {
			rest++
		}
	}
	return lead, rest
}

// padPair sets left at the margin and right against the log frame.
func padPair(left, right string) string {
	return padPairWidth(left, right, timelineWidth())
}

// padPairWidth sets left at the margin and right against width, collapsing to a
// minimum gap when the terminal cannot hold both.
func padPairWidth(left, right string, width int) string {
	if right == "" {
		return left
	}
	gap := width - visibleWidth(left) - visibleWidth(right)
	if gap < timelinePairGap {
		gap = timelinePairGap
	}
	return left + strings.Repeat(" ", gap) + right
}

// timelineDay renders an entry's calendar day as a day header, e.g.
// "Sun 2026-09-07 CDT". Times are shown in the reader's zone, so the header
// names that zone once per day rather than leaving a bare wall time ambiguous —
// and it is the day header, not every row, because the zone is a property of
// the whole log. Unparseable timestamps head their own group under the raw
// value rather than being dropped.
func timelineDay(ts string) string {
	parsed, ok := ParseTimestamp(ts)
	if !ok {
		return ShortStamp(ts)
	}
	return parsed.In(DisplayLocation()).Format("Mon 2006-01-02 MST")
}

// timelineClock renders an entry's wall time in the reader's zone. It falls back
// to a fixed-width blank so an unparseable timestamp keeps the gutter aligned.
func timelineClock(ts string) string {
	parsed, ok := ParseTimestamp(ts)
	if !ok {
		return "     "
	}
	return parsed.In(DisplayLocation()).Format("15:04")
}

// timelineSlug is the last path segment — the task's own name.
func timelineSlug(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// timelineParent is everything above the task, shown once per run so a row
// never has to repeat it.
func timelineParent(path string) string {
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		return path[:idx]
	}
	return ""
}

// StyledEvent is the flattened view model for one project event's detail card.
type StyledEvent struct {
	ID          string
	Type        string
	Summary     string
	Source      string
	Node        string
	Principal   string
	ScopeRef    string
	Project     string
	TaskID      string
	TaskPath    string
	OccurredAt  string
	CreatedAt   string
	Idempotency string
	Payload     json.RawMessage
}

// RenderStyledEvent writes one project event as a card in the task card's
// grammar: an identity header, an aligned metadata trailer, and § sections over
// full-width rules. It is also the timeline's expansion — one renderer, two
// entry points — so pressing into a log row and running `wrkp show` agree.
func RenderStyledEvent(w io.Writer, e StyledEvent) {
	renderEventHeader(w, e)
	eventSection(w, "§ Summary", func() {
		emitFlow(w, bodyIndent, bodyIndent, e.Summary)
	})
	if rows := decodePayloadRows(e.Payload); len(rows) > 0 {
		eventSection(w, "§ Payload", func() { renderKeyValues(w, rows) })
	}
	if rows := eventProvenance(e); len(rows) > 0 {
		eventSection(w, "§ Provenance", func() { renderKeyValues(w, rows) })
	}
}

// renderEventHeader mirrors the task card header: dim id, an accent glyph, the
// bold dotted type, then metadata aligned under the title.
func renderEventHeader(w io.Writer, e StyledEvent) {
	marker := Paint(ColMarker, timelineFloat)
	left := Paint(ColDim, e.ID) + "  " + marker + " " + Paint(ColHeading, e.Type)
	// A card is framed by RuleWidth, not the log's wider measure, so the project
	// lands on the same right edge as the dividers below it.
	_, _ = io.WriteString(w, padPairWidth(left, Paint(ColDim, e.Project), RuleWidth)+"\n")

	indent := strings.Repeat(" ", len(e.ID)+3)
	sep := Paint(ColRule, " · ")

	var meta []string
	if e.Source != "" {
		meta = append(meta, Paint(ColCode, e.Source))
	}
	if e.Principal != "" {
		meta = append(meta, Paint(ColDir, e.Principal))
	}
	if e.Node != "" {
		meta = append(meta, Paint(ColDim, e.Node))
	}
	if len(meta) > 0 {
		_, _ = io.WriteString(w, indent+strings.Join(meta, sep)+"\n")
	}

	var trailer []string
	if e.OccurredAt != "" {
		age, on := formatUpdatedAge(e.OccurredAt)
		seg := "occurred " + age
		if on != "" {
			seg += Paint(ColDim, " "+on)
		}
		trailer = append(trailer, seg)
	}
	if e.TaskID != "" {
		label := e.TaskID
		if slug := timelineSlug(e.TaskPath); slug != "" {
			label += " " + slug
		}
		trailer = append(trailer, Paint(ColMarker, label))
	}
	if len(trailer) > 0 {
		_, _ = io.WriteString(w, indent+strings.Join(trailer, sep)+"\n")
	}
	_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n")
}

func eventSection(w io.Writer, label string, body func()) {
	_, _ = io.WriteString(w, "\n"+Paint(ColSection, label)+"\n")
	_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n\n")
	body()
}

type keyValue struct{ Key, Value string }

// eventProvenance surfaces the fields that identify where a fact came from.
// ScopeRef is the reason the card exists: it names the seat that produced the
// event even when the payload claims no task at all.
func eventProvenance(e StyledEvent) []keyValue {
	var rows []keyValue
	if e.ScopeRef != "" {
		rows = append(rows, keyValue{"produced by", e.ScopeRef})
	}
	if e.Idempotency != "" {
		rows = append(rows, keyValue{"idempotency", e.Idempotency})
	}
	if e.CreatedAt != "" && e.CreatedAt != e.OccurredAt {
		rows = append(rows, keyValue{"recorded", e.CreatedAt})
	}
	return rows
}

// decodePayloadRows flattens a payload object into aligned rows. Scalars print
// bare; nested arrays and objects collapse to compact JSON on one line. A
// payload that is not an object — which validation forbids but a future writer
// could still deliver — renders as a single raw row rather than disappearing.
func decodePayloadRows(payload json.RawMessage) []keyValue {
	if len(payload) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return []keyValue{{"payload", strings.TrimSpace(string(payload))}}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]keyValue, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, keyValue{key, scalarText(fields[key])})
	}
	return rows
}

// scalarText renders a JSON value for a key/value row: strings unquoted,
// numbers and booleans verbatim, everything else compact on one line.
func scalarText(raw json.RawMessage) string {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		return strconv.FormatBool(flag)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return compact.String()
}

// emitFlowPlain wraps literal text under a hanging indent WITHOUT running it
// through inline markdown styling. Payload values are data — a sha, a ref, an
// idempotency key — and must never have underscores or backticks reinterpreted
// as markup.
func emitFlowPlain(w io.Writer, firstPrefix, contPrefix, raw string) {
	maxW := wrapWidth()
	firstBudget := maxW - visibleWidth(firstPrefix)
	contBudget := maxW - visibleWidth(contPrefix)
	if firstBudget < 8 {
		firstBudget = 8
	}
	if contBudget < 8 {
		contBudget = 8
	}
	for i, line := range wrapStyled(raw, firstBudget, contBudget) {
		prefix := contPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		_, _ = io.WriteString(w, prefix+line+"\n")
	}
}

// renderKeyValues prints aligned key/value rows under a section, flowing long
// values under a hanging indent so nothing is truncated away.
func renderKeyValues(w io.Writer, rows []keyValue) {
	width := 0
	for _, row := range rows {
		if n := len(row.Key); n > width {
			width = n
		}
	}
	for _, row := range rows {
		// Pad outside the paint so no SGR run covers bare alignment space.
		key := Paint(ColDim, row.Key) + strings.Repeat(" ", width-len(row.Key))
		first := bodyIndent + key + "  "
		cont := bodyIndent + strings.Repeat(" ", width) + "  "
		emitFlowPlain(w, first, cont, row.Value)
	}
}

// StyledType is the flattened view model for one observed project-event type.
// Like StyledEntry it is decoupled from any wire struct so the renderer stays
// presentation only.
type StyledType struct {
	Type          string
	Count         int64
	LastCreatedAt string
}

// RenderStyledTypes draws the observed foreign fact types for a project (T-08224).
// `types` was the one wrkp read left out of the porcelain pass, so it emitted a
// raw JSON array between two styled siblings.
//
// The empty case is the interesting one and is NOT an error: a project with no
// project_events has simply never had a producer post to it. Saying "[]", or
// saying nothing at all, is indistinguishable from a broken command — so name the
// project and the cause.
func RenderStyledTypes(w io.Writer, project string, types []StyledType) {
	if project != "" {
		_, _ = io.WriteString(w, Paint(ColDir, project)+"\n")
		_, _ = io.WriteString(w, Paint(ColRule, strings.Repeat("─", typesWidth(types)))+"\n")
	}
	if len(types) == 0 {
		_, _ = io.WriteString(w, "\n")
		target := "this project"
		if project != "" {
			target = project
		}
		_, _ = io.WriteString(w, Paint(ColDim, "no facts have been posted to "+target)+"\n")
		_, _ = io.WriteString(w, Paint(ColDim, "install a producer (wrkp git hooks) or post one with wrkp post")+"\n")
		return
	}
	_, _ = io.WriteString(w, "\n")
	for _, t := range types {
		left := Paint(ColMarker, "◆ ") + t.Type
		right := Paint(ColDim, fmt.Sprintf("%d", t.Count)+" · "+typesAge(t.LastCreatedAt))
		_, _ = io.WriteString(w, "  "+padPairWidth(left, right, typesWidth(types)-2)+"\n")
	}
}

// typesWidth frames the type list the way timelineWidth frames a stream: wide
// enough for the right-hand column, never wider than the terminal.
func typesWidth(types []StyledType) int {
	want := RuleWidth
	for _, t := range types {
		if n := len(t.Type) + 24; n > want {
			want = n
		}
	}
	if w := wrapWidth(); w < want {
		return w
	}
	return want
}

// typesAge renders the last-seen stamp as a relative age, reusing the same clock
// the task cards use so WRKQ_NOW keeps --pretty deterministic.
func typesAge(ts string) string {
	parsed, ok := ParseTimestamp(ts)
	if !ok {
		return ts
	}
	elapsed := NowUTC().Sub(parsed)
	if elapsed < 0 {
		elapsed = 0
	}
	return FormatDuration(elapsed) + " ago"
}
