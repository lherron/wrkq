package rpccli

// exit.go mirrors internal/cli/helpers.go's exit-code error mechanism so the
// mirror binary reproduces the legacy CLI's DISTINCT process exit codes (e.g.
// handoff's 2/3/4/5/6) and its "structured error already written to stderr"
// suppression. Commands that own their structured-error rendering (handoff)
// return exitErrorReported so main.go does NOT reprint a generic "Error:" line —
// preserving byte-for-byte stderr parity with legacy `wrkq`.

type cliExitError struct {
	code     int
	err      error
	reported bool
}

func (e cliExitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e cliExitError) Unwrap() error { return e.err }

// exitErrorReported returns an error that exits with code and signals that the
// command already wrote its own structured error to stderr.
func exitErrorReported(code int, err error) error {
	return cliExitError{code: code, err: err, reported: true}
}

// ExitCodeForError returns the process exit code represented by err (1 by
// default; the encoded code for a mirror exit error).
func ExitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(cliExitError); ok {
		return e.code
	}
	return 1
}

// ErrorAlreadyReported reports whether a command already wrote its own structured
// error to stderr (so main.go must not reprint a generic "Error:" line).
func ErrorAlreadyReported(err error) bool {
	if e, ok := err.(cliExitError); ok {
		return e.reported
	}
	return false
}
