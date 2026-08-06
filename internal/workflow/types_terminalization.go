package workflow

// TerminalizedRunSummary is the token-free readback for a run whose authority
// ended because its workflow instance was explicitly terminalized.
type TerminalizedRunSummary struct {
	RunID          string `json:"runId"`
	Status         string `json:"status"`
	CompletedAt    string `json:"completedAt"`
	TerminalResult string `json:"terminalResult"`
}
