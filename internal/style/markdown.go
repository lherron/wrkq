package style

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

var (
	mdLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	mdItalic = regexp.MustCompile(`\*([^*\s][^*]*?)\*|(?:^|[\s(])_([^_\s][^_]*?)_(?:$|[\s).,!?])`)
	mdBullet = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	mdNumber = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	mdHead   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	// mdLabel matches a standalone "Section:" line — the de facto section headers
	// in wrkq task bodies, which authors write as plain text rather than markdown
	// headings. Kept tight (short, capitalized, no code) so it promotes real
	// labels without bolding stray prose that ends in a colon.
	mdLabel = regexp.MustCompile(`^[A-Z][\w ./()'-]{0,38}:$`)
)

// RenderMarkdown writes md to w as styled terminal text, line-prefixed by
// indent. When color is disabled it degrades to lightly cleaned plain text, so
// the same path is safe for NO_COLOR and for non-TTY --pretty.
func RenderMarkdown(w io.Writer, md, indent string) {
	lines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	inFence := false
	wrote := false // suppress leading blank lines and the first heading's top margin
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fenced code blocks: emit verbatim in code color, no inline parsing.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			_, _ = io.WriteString(w, indent+Paint(ColRule, "│ ")+"\n")
			wrote = true
			continue
		}
		if inFence {
			_, _ = io.WriteString(w, indent+Paint(ColRule, "│ ")+Paint(ColCode, line)+"\n")
			wrote = true
			continue
		}

		// Horizontal rule.
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			_, _ = io.WriteString(w, indent+Paint(ColRule, strings.Repeat("─", RuleWidth))+"\n")
			wrote = true
			continue
		}

		// Headings. h1/h2 carry the section color; deeper ones recede.
		if m := mdHead.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			text := styleInline(strings.TrimSpace(m[2]))
			code := ColSection
			if level >= 3 {
				code = "1;2" // deeper headings recede (bold + faint)
			}
			if wrote {
				_, _ = io.WriteString(w, "\n")
			}
			_, _ = io.WriteString(w, indent+Paint(code, text)+"\n")
			if level == 1 {
				_, _ = io.WriteString(w, indent+Paint(ColRule, strings.Repeat("─", min(RuleWidth, len(StripANSI(text)))))+"\n")
			}
			wrote = true
			continue
		}

		// Blockquote. Continuations keep the quote bar.
		if strings.HasPrefix(trimmed, ">") {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			bar := indent + Paint(ColRule, "▏ ")
			emitFlow(w, bar, bar, body)
			wrote = true
			continue
		}

		// Bulleted list. Continuations hang under the text, clear of the marker.
		if m := mdBullet.FindStringSubmatch(line); m != nil {
			first := indent + m[1] + Paint(ColMarker, "•") + " "
			cont := indent + m[1] + "  "
			emitFlow(w, first, cont, m[2])
			wrote = true
			continue
		}

		// Numbered list. Continuations hang under the text, clear of the number.
		if m := mdNumber.FindStringSubmatch(line); m != nil {
			first := indent + m[1] + Paint(ColMarker, m[2]+".") + " "
			cont := indent + m[1] + strings.Repeat(" ", len(m[2])+2)
			emitFlow(w, first, cont, m[3])
			wrote = true
			continue
		}

		// Standalone "Section:" label — promote to a colored section header.
		if line == trimmed && !strings.ContainsRune(trimmed, '`') && mdLabel.MatchString(trimmed) {
			_, _ = io.WriteString(w, indent+Paint(ColSection, trimmed)+"\n")
			wrote = true
			continue
		}

		// Plain paragraph / blank line.
		if trimmed == "" {
			if wrote {
				_, _ = io.WriteString(w, "\n")
			}
			continue
		}
		emitFlow(w, indent, indent, line)
		wrote = true
	}
}

// styleInline applies inline markdown styling. Code spans are resolved first so
// emphasis markers inside them are left untouched.
func styleInline(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			b.WriteString(styleEmphasis(s))
			break
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			b.WriteString(styleEmphasis(s))
			break
		}
		b.WriteString(styleEmphasis(s[:i]))
		b.WriteString(Paint(ColCode, s[i+1:i+1+j]))
		s = s[i+1+j+1:]
	}
	return b.String()
}

// styleEmphasis handles links, bold, and italic in a code-free segment.
func styleEmphasis(s string) string {
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdLink.FindStringSubmatch(m)
		return Paint(ColLink, parts[1]) + Paint(ColRule, " ("+parts[2]+")")
	})
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdBold.FindStringSubmatch(m)
		text := parts[1]
		if text == "" {
			text = parts[2]
		}
		return Paint("1", text)
	})
	s = mdItalic.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdItalic.FindStringSubmatch(m)
		// Preserve any leading/trailing boundary char the pattern captured.
		lead, trail := "", ""
		if len(m) > 0 && (m[0] == ' ' || m[0] == '(') {
			lead = string(m[0])
		}
		if n := len(m); n > 0 && (m[n-1] == ' ' || m[n-1] == ')' || m[n-1] == '.' || m[n-1] == ',' || m[n-1] == '!' || m[n-1] == '?') {
			trail = string(m[n-1])
		}
		text := parts[1]
		if text == "" {
			text = parts[2]
		}
		return lead + Paint("3", text) + trail
	})
	return s
}

// detectedWidth is the terminal column count, resolved once at startup. Falls
// back to COLUMNS, then a sane default, when the size can't be read. Both
// binaries resolve it identically (same stdout / env), so wrapped --pretty
// output stays byte-identical between them.
var detectedWidth = func() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 1 {
		return w
	}
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 1 {
		return c
	}
	return 100
}()

// wrapWidth is the visible column budget for flowing text: the terminal width
// less a one-column safety margin (so the terminal never re-wraps our lines),
// capped for readability on very wide terminals.
func wrapWidth() int {
	w := detectedWidth - 1
	if w > 120 {
		w = 120
	}
	if w < 20 {
		w = 20
	}
	return w
}

// visibleWidth counts the rendered columns of s, ignoring ANSI SGR sequences.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(StripANSI(s))
}

// emitFlow word-wraps raw markdown text to the terminal width and re-applies the
// line prefix so wrapped continuations stay under the margin instead of falling
// back to column 0. firstPrefix leads the first line; contPrefix leads every
// continuation (a hanging indent or a quote bar). Text is styled first, then
// wrapped ANSI-aware, so inline spans (code, bold, links) survive a break.
func emitFlow(w io.Writer, firstPrefix, contPrefix, raw string) {
	maxW := wrapWidth()
	firstBudget := maxW - visibleWidth(firstPrefix)
	contBudget := maxW - visibleWidth(contPrefix)
	if firstBudget < 8 {
		firstBudget = 8
	}
	if contBudget < 8 {
		contBudget = 8
	}

	lines := wrapStyled(styleInline(raw), firstBudget, contBudget)
	for i, line := range lines {
		prefix := contPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		_, _ = io.WriteString(w, prefix+line+"\n")
	}
}

// styledWords splits an already-styled string into standalone words at visible
// spaces. Each word re-opens the SGR active at its start and closes it at its
// end, so a multi-word span (e.g. a code span containing spaces) can break
// across lines without leaking color or resurrecting markup.
func styledWords(s string) []string {
	var words []string
	var cur strings.Builder
	active := ""        // current open SGR params, "" when none
	hasVisible := false // whether cur holds any printable runes yet
	flush := func() {
		if hasVisible {
			out := cur.String()
			if active != "" {
				out += "\x1b[0m"
			}
			words = append(words, out)
		}
		cur.Reset()
		hasVisible = false
		if active != "" {
			cur.WriteString("\x1b[" + active + "m") // re-open for the next word
		}
	}
	for i := 0; i < len(s); {
		if s[i] == 0x1b { // ANSI SGR sequence: copy verbatim, track active state
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			seq := s[i : j+1]
			cur.WriteString(seq)
			if seq == "\x1b[0m" {
				active = ""
			} else if len(seq) >= 4 {
				active = seq[2 : len(seq)-1]
			}
			i = j + 1
			continue
		}
		if s[i] == ' ' {
			flush()
			i++
			continue
		}
		cur.WriteByte(s[i]) // bytes pass through; multibyte runes copy intact
		hasVisible = true
		i++
	}
	flush()
	return words
}

// wrapStyled greedily packs styled words into lines within the given budgets,
// measuring width by visible columns only.
func wrapStyled(s string, firstBudget, contBudget int) []string {
	words := styledWords(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	curWidth := 0
	budget := firstBudget
	for _, wd := range words {
		ww := visibleWidth(wd)
		switch {
		case cur == "":
			cur, curWidth = wd, ww
		case curWidth+1+ww <= budget:
			cur += " " + wd
			curWidth += 1 + ww
		default:
			lines = append(lines, cur)
			cur, curWidth = wd, ww
			budget = contBudget
		}
	}
	lines = append(lines, cur)
	return lines
}
