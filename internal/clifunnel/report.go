package clifunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ExitUsage is the exit code for a caller mistake in how a command was invoked
// (CLI standard §5): unknown flag or command, wrong argument count, missing
// required flag, unrecognized --output mode.
const ExitUsage = 2

// ExitCode returns ExitUsage for a usage error and fallback otherwise. Each
// binary's exit helper calls it after resolving its own explicit exit codes.
func ExitCode(err error, fallback int) int {
	var usage *UsageError
	if errors.As(err, &usage) {
		return ExitUsage
	}
	return fallback
}

// ErrorMode is how Report renders an error.
type ErrorMode int

const (
	// ErrorText is "Error: <message>" plus hint lines and a "code:" line.
	ErrorText ErrorMode = iota
	// ErrorJSON is the indented {"error":{...}} envelope.
	ErrorJSON
	// ErrorNDJSON is the same envelope on one line.
	ErrorNDJSON
)

// StructuredError is the --json error envelope's body. It follows the handoff
// family's shape: a dedicated machine-readable code beside the message, never
// concatenated into it (CLI standard §4).
type StructuredError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// ErrorModeFor reads the caller's requested output shape from argv: --json,
// --ndjson, or --output json|ndjson before any `--` terminator. It reads argv
// rather than parsed flags so a flag-parse failure is rendered in the mode the
// caller asked for too.
func ErrorModeFor(args []string) ErrorMode {
	mode := ErrorText
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		value := ""
		switch {
		case a == "--json" || a == "--json=true":
			mode = ErrorJSON
		case a == "--ndjson" || a == "--ndjson=true":
			mode = ErrorNDJSON
		case a == "--output" && i+1 < len(args):
			value = args[i+1]
			i++
		case strings.HasPrefix(a, "--output="):
			value = strings.TrimPrefix(a, "--output=")
		}
		switch value {
		case "json":
			mode = ErrorJSON
		case "ndjson":
			mode = ErrorNDJSON
		}
	}
	return mode
}

// Structure splits err into its code, first-line message, and trailing hint
// lines. family ("WRKQ", "WRKF") names the codes for errors that carry none:
// <family>_USAGE for usage errors, <family>_NOT_FOUND for CLI-built not-found,
// <family>_ERROR otherwise. coded reports whether the code came from the error
// itself rather than being synthesized here.
func Structure(err error, family string) (s StructuredError, coded bool) {
	var usage *UsageError
	var notFound *NotFoundError
	var withCode interface{ Code() string }
	switch {
	case errors.As(err, &usage):
		s.Code = family + "_USAGE"
	case errors.As(err, &withCode) && withCode.Code() != "":
		s.Code, coded = withCode.Code(), true
	case errors.As(err, &notFound):
		s.Code = family + "_NOT_FOUND"
	default:
		s.Code = family + "_ERROR"
	}
	first, rest, _ := strings.Cut(err.Error(), "\n")
	if coded {
		first = strings.Replace(first, s.Code+": ", "", 1)
	}
	s.Message = first
	hints := strings.Split(rest, "\n")
	for i, line := range hints {
		hints[i] = strings.TrimPrefix(line, "hint: ")
	}
	s.Hint = strings.TrimSpace(strings.Join(hints, "\n"))
	return s, coded
}

// Report writes err to w once, in mode. Text mode keeps the message and its
// hint lines as they were and moves an error's own code out of the message
// onto a trailing "code:" line; the JSON modes write the StructuredError
// envelope.
func Report(w io.Writer, err error, mode ErrorMode, family string) {
	s, coded := Structure(err, family)
	switch mode {
	case ErrorJSON, ErrorNDJSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false) // hints carry <placeholders>
		if mode == ErrorJSON {
			enc.SetIndent("", "  ")
		}
		_ = enc.Encode(map[string]StructuredError{"error": s})
	default:
		text := err.Error()
		if coded {
			_, rest, hasRest := strings.Cut(text, "\n")
			text = s.Message
			if hasRest {
				text += "\n" + rest
			}
			text += "\ncode: " + s.Code
		}
		fmt.Fprintf(w, "Error: %s\n", text)
	}
}
