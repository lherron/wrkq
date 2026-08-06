//go:build wrkq_local

package workflow

type actionCandidateQueryer interface {
	queryer
	rowsQueryer
}

type staleContextFreshnessCandidate struct {
	ReverifyAction string
	Reason         string
	Candidate      ActionCandidate
}