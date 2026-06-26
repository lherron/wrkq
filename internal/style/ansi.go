// Package style holds wrkq's shared terminal-presentation primitives: ANSI
// coloring, the markdown renderer, the styled task card, and the relative-time
// helpers. It is pure presentation — no DB, store, or API coupling — so both the
// legacy CLI (internal/cli) and the RPC-backed mirror (internal/rpccli) render
// through ONE copy. Byte-parity between the two binaries is therefore structural,
// not something the parity harness has to chase.
package style

import (
	"os"
	"regexp"
)

// Palette. Color is reserved for STRUCTURE — headings, code, links, quote bars,
// tree branches, state accents — while prose recedes so it reads like text, not
// a syntax dump. Values are raw SGR parameter strings (see Paint).
const (
	ColHeading   = "1"    // bold — inline emphasis fallback
	ColSection   = "1;35" // bold magenta — section headers (body landmarks)
	ColCode      = "36"   // cyan — inline code & fenced blocks
	ColLink      = "4"    // underline — link text
	ColRule      = "2"    // faint — horizontal rules, quote bars, branches
	ColMarker    = "33"   // amber — list bullets/numbers (structure)
	ColDim       = "2"    // tree branches, IDs, secondary metadata
	ColDir       = "1;34" // container slug — bold blue
	ColStateOpen = "33"   // amber — active/needs attention
	ColStateWIP  = "36"   // cyan — in progress
	ColStateStop = "31"   // red — blocked
	ColDone      = "32"   // green — completed / all done
)

// RuleWidth is the column span for horizontal rules and section dividers.
const RuleWidth = 64

// ColorEnabled reports whether to emit ANSI styling. It is auto-detected once
// from stdout (honoring NO_COLOR and requiring an interactive terminal) so piped
// output is clean. Binaries or tests may override it before rendering. Note that
// --pretty forces the styled LAYOUT but never flips this — color stays a function
// of the terminal, which is what keeps non-TTY --pretty deterministic (and thus
// byte-parity provable).
var ColorEnabled = detectColor()

func detectColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Paint wraps s in an ANSI SGR sequence when color is enabled; otherwise it
// returns s unchanged (so the styled layout degrades cleanly to plain text).
func Paint(code, s string) string {
	if !ColorEnabled || code == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// StateColor maps a task state to its subtle accent color.
func StateColor(state string) string {
	switch state {
	case "open":
		return ColStateOpen
	case "in_progress":
		return ColStateWIP
	case "blocked":
		return ColStateStop
	case "completed":
		return ColDone
	default: // draft and anything else recede
		return ColDim
	}
}

var ansiSeq = regexp.MustCompile(`\033\[[0-9;]*m`)

// StripANSI removes SGR sequences so visible width can be measured.
func StripANSI(s string) string {
	return ansiSeq.ReplaceAllString(s, "")
}
