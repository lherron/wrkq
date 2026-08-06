//go:build wrkq_local

package workflow

import "database/sql"

// WorkflowPolicy owns template-specific workflow behavior that the generic
// evidence and ledger paths should not embed directly.
type WorkflowPolicy interface {
	ValidateEvidence(params AddEvidenceParams, facts *parsedEvidenceFacts) error
	OnEvidenceAdded(tx *sql.Tx, inst *Instance, ev *Evidence) error
	ProjectObligations(s *Service, inst *Instance, obligations []Obligation, evidence []Evidence, includeClosed bool) []Obligation
}

type defaultWorkflowPolicy struct{}

type workflowPolicyKey struct {
	templateID string
	version    string
}

type simpleTaskWorkflowPolicy struct{}