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

func ResolveWorkflowPolicy(tpl *Template) WorkflowPolicy {
	if tpl != nil && tpl.ID == "wrkq-simple-task" {
		return simpleTaskWorkflowPolicy{}
	}
	return defaultWorkflowPolicy{}
}

func (defaultWorkflowPolicy) ValidateEvidence(AddEvidenceParams, *parsedEvidenceFacts) error {
	return nil
}

func (defaultWorkflowPolicy) OnEvidenceAdded(*sql.Tx, *Instance, *Evidence) error {
	return nil
}

func (defaultWorkflowPolicy) ProjectObligations(_ *Service, _ *Instance, obligations []Obligation, _ []Evidence, _ bool) []Obligation {
	return obligations
}

type simpleTaskWorkflowPolicy struct{}

func (simpleTaskWorkflowPolicy) ValidateEvidence(params AddEvidenceParams, facts *parsedEvidenceFacts) error {
	if params.Kind == "operator_resolution" {
		return validateOperatorResolutionReason(params.Summary, facts)
	}
	return nil
}

func (simpleTaskWorkflowPolicy) OnEvidenceAdded(tx *sql.Tx, inst *Instance, ev *Evidence) error {
	if inst == nil || ev == nil {
		return nil
	}
	if ev.Kind == "delegated_task_manifest" && len(ev.Data) > 0 {
		if err := createDelegatedTaskClosureObligationsTx(tx, inst.ID, ev.ID, string(ev.Data)); err != nil {
			return err
		}
	}
	if ev.Kind == "coordinator_runbook" && len(ev.Data) > 0 {
		if err := createCoordinatorSmokeExecutionObligationTx(tx, inst.ID, ev.ID); err != nil {
			return err
		}
	}
	if ev.Kind == "completion_claim" {
		if err := createObserverCompletionReviewObligationTx(tx, inst.ID, ev.ID); err != nil {
			return err
		}
	}
	if ev.Kind == "observer_completion_review" {
		_, _ = tx.Exec(`
			UPDATE workflow_effects
			SET status = 'delivered', delivered_at = COALESCE(delivered_at, ?), updated_at = ?
			WHERE instance_id = ? AND kind = 'request_observer_review' AND status IN ('pending','failed','leased')
		`, ev.ProducedAt, ev.ProducedAt, inst.ID)
	}
	return nil
}

func (simpleTaskWorkflowPolicy) ProjectObligations(s *Service, inst *Instance, obligations []Obligation, evidence []Evidence, includeClosed bool) []Obligation {
	obligations = s.withDelegatedTaskClosureState(obligations, includeClosed)
	obligations = withCoordinatorSmokeExecutionState(obligations, evidence, includeClosed)
	obligations = withObserverCompletionReviewState(obligations, evidence, includeClosed)
	obligations = append(obligations, coordinatorSmokeExecutionObligations(inst, evidence, obligations, includeClosed)...)
	obligations = append(obligations, observerCompletionReviewObligations(inst, evidence, obligations, includeClosed)...)
	obligations = append(obligations, s.delegatedTaskClosureObligations(inst, evidence, obligations, includeClosed)...)
	return obligations
}
