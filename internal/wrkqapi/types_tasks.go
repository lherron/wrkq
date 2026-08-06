package wrkqapi

// restoreOptions carries the per-task restore mutation (target state + the
// optional move / field updates / comment). Mirrors internal/cli restoreTaskOptions.
type restoreOptions struct {
	targetState          string
	newProjectUUID       *string
	newSlug              *string
	newTitle             string
	newDescription       string
	newPriority          int
	newLabels            string
	assigneePrincipalRef *string
	comment              string
}

// rowScanner abstracts *sql.Row and *sql.Rows for scanTaskRow.
type rowScanner interface {
	Scan(dest ...any) error
}
