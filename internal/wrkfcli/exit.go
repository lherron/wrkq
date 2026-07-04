package wrkfcli

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

func (e cliExitError) Unwrap() error {
	return e.err
}

func exitError(code int, err error) error {
	return cliExitError{code: code, err: err}
}

func exitErrorReported(code int, err error) error {
	return cliExitError{code: code, err: err, reported: true}
}

func ExitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(cliExitError); ok {
		return e.code
	}
	return 1
}

func errorAlreadyReported(err error) bool {
	if e, ok := err.(cliExitError); ok {
		return e.reported
	}
	return false
}
