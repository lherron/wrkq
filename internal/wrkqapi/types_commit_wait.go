package wrkqapi

// CommitWaitParams carries an ephemeral wake token, never a durable cursor.
type CommitWaitParams struct {
	Since     string `json:"since,omitempty"`
	TimeoutMs *int   `json:"timeoutMs,omitempty"`
}

type WrkqCommitWait struct {
	Token   string `json:"token"`
	Changed bool   `json:"changed"`
}
