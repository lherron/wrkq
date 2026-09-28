// Package localseam holds the typed refusal shared by the portable (CGO-free,
// remote-only) CLI builds. It carries no build tag and imports nothing tagged,
// so both the wrkq and wrkf portable seams can return the same error type and
// message (T-07090, T-07092).
package localseam

import "fmt"

// ErrLocalLocatorUnsupported names the refusal so callers and tests can match it
// without string-matching a message.
type ErrLocalLocatorUnsupported struct {
	Binary  string
	Locator string
}

func (e *ErrLocalLocatorUnsupported) Error() string {
	locator := e.Locator
	if locator == "" {
		locator = "(none configured)"
	}
	return fmt.Sprintf(
		"this %s build is remote-only and cannot open the local database %q; "+
			"set WRKQ_DB to an rpc:// endpoint (for example rpc://host:7171), "+
			"or use a %s built with -tags wrkq_local for local-file operation",
		e.Binary, locator, e.Binary)
}

// Refuse returns the typed refusal for binary's portable build.
func Refuse(binary, locator string) error {
	return &ErrLocalLocatorUnsupported{Binary: binary, Locator: locator}
}
