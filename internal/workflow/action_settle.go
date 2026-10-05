//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/webhooks"
)

func (s *Service) SettleAction(p SettleActionParams) (*SettleActionResult, error) {
	runID := strings.TrimSpace(firstNonEmptyAction(p.ActionRunID, p.RunID))
	if runID == "" {
		return nil, validationError("runId", "runId is required", "runId", nil, "supply the claimed run id")
	}
	resultStatus := strings.TrimSpace(p.Result)
	if resultStatus == "" {
		return nil, validationError("result", "result is required", "terminal result", []string{"completed", "semantic_blocked", "operational_failed", "operator_required", "cancelled"}, "supply the terminal action result")
	}
	var out *SettleActionResult
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		run, err := claimedRunByIDTx(tx, runID)
		if err != nil {
			return err
		}
		if isTerminalRunStatus(run.Status) {
			if run.Status == "superseded" && run.SupersededByRunID != "" {
				return supersededSettleError(run.ID, run.SupersededByRunID)
			}
			replayed, err := replaySettledActionTx(tx, run, p)
			if err != nil {
				return err
			}
			out = replayed
			return nil
		}
		now := s.now().UTC()
		downgrade := isDowngradeSettlementResult(resultStatus)
		if downgrade {
			if err := validateSettleDowngradeAuthority(run, p); err != nil {
				return err
			}
		} else if err := validateSettleOwnership(run, p); err != nil {
			return err
		}
		inst, err := instanceByIDQuery(tx, run.InstanceID)
		if err != nil {
			return err
		}
		tpl, _, err := showTemplateTx(tx, inst.TemplateID+"@"+inst.TemplateVersion)
		if err != nil {
			return err
		}
		actionSpec, ok := tpl.ExecutableActions[run.Action]
		if !ok {
			return validationError("action", "run action is not declared as executable", "executable action", []string{run.Action}, "claim a v2 executable action before settling")
		}
		if actionSpec.Role != "" && actionSpec.Role != run.Role {
			return actionLeaseConflictError(run.ID)
		}
		transitionID := strings.TrimSpace(p.TransitionID)
		if downgrade {
			if p.TransitionMode == TransitionExplicit && transitionID != "" {
				return validationError("transition", "downgrade settlement cannot apply a workflow transition", "no transition", nil, "omit transition or set transition=false when settling a non-completed action result")
			}
			transitionID = ""
		} else {
			switch p.TransitionMode {
			case TransitionSkip:
				transitionID = ""
			case TransitionExplicit:
				if transitionID == "" {
					return validationError("transition", "transition id is required", "transition id", nil, "supply transition or omit it for the executable action transition")
				}
			default:
				transitionID = strings.TrimSpace(actionSpec.Transition)
			}
		}
		expectedEvidenceKind := strings.TrimSpace(actionSpec.ResultEvidenceKind)
		if downgrade {
			expectedEvidenceKind = "failure_result"
		}
		if expectedEvidenceKind == "" {
			expectedEvidenceKind = actionDefaultEvidenceKind(run.Action)
		}
		var evidence *Evidence
		if p.Evidence != nil {
			kind := strings.TrimSpace(p.Evidence.Kind)
			if kind == "" {
				kind = expectedEvidenceKind
			}
			if kind != expectedEvidenceKind {
				return validationError("evidence.kind", "settlement evidence kind must match settlement result kind", expectedEvidenceKind, []string{expectedEvidenceKind}, "use implement/verify result evidence for completed settlements and failure_result for downgrade settlements")
			}
			if !downgrade {
				if err := validateSettleEvidenceFacts(tx, tpl, actionSpec, run, p.Evidence); err != nil {
					return err
				}
			}
			evidence, err = s.addActionEvidenceTx(tx, inst, tpl, AddEvidenceParams{
				InstanceID:     inst.ID,
				Kind:           kind,
				Ref:            firstNonEmptyAction(p.Evidence.Ref, "wrkf-action:"+run.ID),
				Summary:        p.Evidence.Summary,
				Facts:          p.Evidence.Facts,
				Data:           p.Evidence.Data,
				PrincipalRef:   run.AgentRef,
				Role:           run.Role,
				RunID:          run.ID,
				ContentHash:    p.Evidence.ContentHash,
				Build:          nil,
				IdempotencyKey: firstNonEmptyAction(p.Evidence.IdempotencyKey, "wrkf-action:"+run.ID+":settle:evidence:"+kind),
			})
			if err != nil {
				return err
			}
		} else if downgrade {
			return validationError("evidence", "downgrade settlement evidence is required", "failure_result evidence", nil, "include failure_result evidence explaining why the action cannot be completed")
		} else if transitionID != "" {
			return validationError("evidence", "settlement evidence is required when applying a transition", "run-linked evidence", nil, "include evidence for the executable action result kind")
		}

		var transition map[string]interface{}
		if transitionID != "" {
			transition, err = s.applyActionTransitionTx(tx, inst, tpl, transitionID, run.AgentRef, run.Role, run.ID)
			if err != nil {
				return err
			}
		} else if evidence != nil {
			if err := s.refreshInstanceContextTx(tx, inst, run.AgentRef); err != nil {
				return err
			}
		}

		summary := settleTerminalSummary(p, evidence)
		completedAt := now.Format(time.RFC3339)
		_, err = tx.Exec(`
			UPDATE workflow_runs
			SET status = ?, terminal_result = ?, completed_at = ?,
			    lease_token = NULL
			WHERE id = ? AND status = 'active'
		`, resultStatus, summary, completedAt, run.ID)
		if err != nil {
			return err
		}
		run.Status = resultStatus
		run.TerminalSummary = summary
		run.CompletedAt = completedAt
		run.LeaseToken = ""
		out = &SettleActionResult{
			Run:         workflowRunAttemptFromClaimed(run),
			Evidence:    evidence,
			Transition:  transition,
			Effects:     transitionEffectsFromMap(transition),
			Obligations: transitionObligationsFromMap(transition),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out != nil && out.Transition != nil {
		updated, ok := out.Transition["instance"].(Instance)
		eventID, _ := out.Transition["eventId"].(string)
		if ok && updated.Suspension != nil && eventID != "" {
			meta, metaErr := s.workflowEventMetadataByID(eventID)
			if metaErr == nil {
				transitionID, _ := out.Transition["transition"].(string)
				key := fmt.Sprintf("wrkf-action:%s:settle:transition:%s", out.Run.ID, transitionID)
				ctx := workflowSuspensionWebhookContext(meta, updated, out.Run.AgentRef, out.Run.Role, out.Run.ID, updated.Revision-1, updated.Revision, key)
				webhooks.DispatchTaskEvent(s.db, updated.TaskUUID, ctx)
			}
		}
	}
	if out != nil && out.Transition != nil {
		transitionID, _ := out.Transition["transition"].(string)
		transition, deliverErr := s.deliverBuiltinTransitionEffects(out.Transition, transitionID)
		if deliverErr != nil {
			return out, deliverErr
		}
		out.Transition = transition
		out.Effects = transitionEffectsFromMap(transition)
	}
	return out, nil
}

func validateSettleOwnership(run *claimedRun, p SettleActionParams) error {
	if run.Status != "active" {
		return actionLeaseConflictError(run.ID)
	}
	if strings.TrimSpace(p.OwnerToken) == "" || strings.TrimSpace(p.OwnerToken) != run.LeaseToken {
		return actionLeaseConflictError(run.ID)
	}
	if p.OwnerGeneration <= 0 || p.OwnerGeneration != run.OwnerGeneration {
		return actionLeaseConflictError(run.ID)
	}
	return nil
}

func validateSettleDowngradeAuthority(run *claimedRun, p SettleActionParams) error {
	if run.Status != "active" {
		return actionLeaseConflictError(run.ID)
	}
	if strings.TrimSpace(p.OwnerToken) == "" || strings.TrimSpace(p.OwnerToken) != run.LeaseToken {
		return actionLeaseConflictError(run.ID)
	}
	if p.OwnerGeneration <= 0 || p.OwnerGeneration != run.OwnerGeneration {
		return actionLeaseConflictError(run.ID)
	}
	return nil
}

func isDowngradeSettlementResult(result string) bool {
	return strings.TrimSpace(result) != "completed"
}

func settleTerminalSummary(p SettleActionParams, evidence *Evidence) string {
	if strings.TrimSpace(p.TerminalSummary) != "" {
		return strings.TrimSpace(p.TerminalSummary)
	}
	if evidence != nil && strings.TrimSpace(evidence.Summary) != "" {
		return strings.TrimSpace(evidence.Summary)
	}
	return strings.TrimSpace(p.Result)
}

func replaySettledActionTx(tx *sql.Tx, run *claimedRun, p SettleActionParams) (*SettleActionResult, error) {
	if run.Status != strings.TrimSpace(p.Result) {
		return nil, idempotencyMismatchError(run.ID)
	}
	var evidence *Evidence
	if p.Evidence != nil {
		kind := strings.TrimSpace(p.Evidence.Kind)
		if kind == "" {
			kind = actionDefaultEvidenceKind(run.Action)
		}
		ev, err := settledEvidenceForRunTx(tx, run.ID, kind)
		if err != nil {
			return nil, err
		}
		if ev == nil || !actionEvidenceReplayMatches(ev, p.Evidence) {
			return nil, idempotencyMismatchError(run.ID)
		}
		evidence = ev
	}
	expectedSummary := settleTerminalSummary(p, evidence)
	if expectedSummary != "" && run.TerminalSummary != expectedSummary {
		return nil, idempotencyMismatchError(run.ID)
	}
	transition, err := settledTransitionForRunTx(tx, run.ID)
	if err != nil {
		return nil, err
	}
	return &SettleActionResult{
		Run:         workflowRunAttemptFromClaimed(run),
		Evidence:    evidence,
		Transition:  transition,
		Effects:     transitionEffectsFromMap(transition),
		Obligations: transitionObligationsFromMap(transition),
	}, nil
}

func settledEvidenceForRunTx(tx *sql.Tx, runID, kind string) (*Evidence, error) {
	items, err := queryEvidence(tx, `WHERE run_id = ? AND kind = ? ORDER BY produced_at, id`, runID, kind)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func settledTransitionForRunTx(tx *sql.Tx, runID string) (map[string]interface{}, error) {
	var raw string
	err := tx.QueryRow(`
		SELECT COALESCE(result_json,'')
		FROM workflow_events
		WHERE run_id = ? AND type IN ('workflow.transitioned', 'workflow.suspended')
		ORDER BY seq DESC
		LIMIT 1
	`, runID).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func actionEvidenceReplayMatches(ev *Evidence, in *ActionEvidenceInput) bool {
	if ev == nil || in == nil {
		return ev == nil && in == nil
	}
	if strings.TrimSpace(in.Summary) != "" && strings.TrimSpace(in.Summary) != strings.TrimSpace(ev.Summary) {
		return false
	}
	if strings.TrimSpace(in.Facts) != "" && canonicalJSONForCompare(in.Facts) != canonicalJSONForCompare(string(ev.Facts)) {
		return false
	}
	if strings.TrimSpace(in.Data) != "" && canonicalJSONForCompare(in.Data) != canonicalJSONForCompare(string(ev.Data)) {
		return false
	}
	if strings.TrimSpace(in.ContentHash) != "" && strings.TrimSpace(in.ContentHash) != strings.TrimSpace(ev.ContentHash) {
		return false
	}
	return true
}

func canonicalJSONForCompare(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func validateSettleEvidenceFacts(tx *sql.Tx, tpl *Template, actionSpec ExecutableActionSpec, run *claimedRun, evidence *ActionEvidenceInput) error {
	facts := map[string]interface{}{}
	if evidence != nil && strings.TrimSpace(evidence.Facts) != "" {
		if err := json.Unmarshal([]byte(evidence.Facts), &facts); err != nil {
			return validationError("evidence.facts", "facts must be valid JSON"+jsonLocationSuffix(err), "valid JSON", nil, "fix the JSON syntax in facts")
		}
	}
	if actionSpec.SettleValidation == nil {
		return nil
	}
	var source *Evidence
	for _, rule := range actionSpec.SettleValidation.Rules {
		if !settleValidationRuleMatches(facts, rule.WhenFacts) {
			continue
		}
		if missing := missingRequiredFacts(facts, rule.RequiredFacts); len(missing) > 0 {
			return validationError("evidence.facts", "settlement is missing template-declared required facts", "declared settlement facts", missing, "supply every fact declared by the action settlement contract")
		}
		if rule.IdentityFact != "" {
			if strings.TrimSpace(run.SourceIdentity) == "" {
				return validationError("source", "settlement requires a bound source identity", "bound source identity", nil, "claim a candidate with a source identity")
			}
			if actual := stringFact(facts, rule.IdentityFact); actual != run.SourceIdentity {
				return validationError("evidence.facts."+rule.IdentityFact, "settlement identity does not match the bound source identity", run.SourceIdentity, []string{actual}, "copy the lane-computed source identity into the declared settlement fact")
			}
		}
		if rule.LinkageFact != "" || len(rule.EchoFields) > 0 {
			if source == nil {
				var err error
				source, err = sourceEvidenceForSettleValidation(tx, tpl, actionSpec, run)
				if err != nil {
					return err
				}
			}
			if rule.LinkageFact != "" && stringFact(facts, rule.LinkageFact) != source.ID {
				return validationError("evidence.facts."+rule.LinkageFact, "settlement linkage does not cite the consumed source evidence", source.ID, []string{stringFact(facts, rule.LinkageFact)}, "copy the source evidence id from the claim binding")
			}
			if len(rule.EchoFields) > 0 {
				sourceFacts := evidenceFactsMap(*source)
				for _, echo := range rule.EchoFields {
					if actual, expected := stringFact(facts, echo.Fact), stringFact(sourceFacts, echo.SourceFact); actual != expected {
						return validationError("evidence.facts."+echo.Fact, "settlement fact does not match the declared source evidence echo", expected, []string{actual}, "copy the declared source evidence fact verbatim")
					}
				}
			}
		}
		for _, constraint := range rule.ValueConstraints {
			expected := constraint.Equals
			if constraint.EqualsFact != "" {
				expected = stringFact(facts, constraint.EqualsFact)
			}
			if actual := stringFact(facts, constraint.Fact); actual != expected {
				return validationError("evidence.facts."+constraint.Fact, "settlement fact does not satisfy the declared value constraint", expected, []string{actual}, "supply the template-declared value")
			}
		}
	}
	return nil
}

func settleValidationRuleMatches(facts map[string]interface{}, when map[string]string) bool {
	for fact, expected := range when {
		if stringFact(facts, fact) != expected {
			return false
		}
	}
	return true
}

func sourceEvidenceForSettleValidation(tx *sql.Tx, tpl *Template, actionSpec ExecutableActionSpec, run *claimedRun) (*Evidence, error) {
	if strings.TrimSpace(run.SourceEvidenceID) == "" {
		return nil, validationError("source", "settlement requires claimed source evidence", "source evidence", nil, "claim a candidate with a source evidence binding")
	}
	source, err := evidenceByID(tx, run.SourceEvidenceID)
	if err != nil {
		return nil, err
	}
	if source.InstanceID != run.InstanceID {
		return nil, validationError("source", "claimed source evidence belongs to a different workflow instance", "same-instance source evidence", []string{source.ID}, "claim a candidate from the current workflow instance")
	}
	if actionSpec.SourceBinding == nil {
		return nil, validationError("source", "settlement contract requires a declared source binding", "source binding", nil, "declare the action source binding in the template")
	}
	sourceAction := strings.TrimSpace(actionSpec.SourceBinding.Action)
	sourceSpec, ok := tpl.ExecutableActions[sourceAction]
	if !ok {
		return nil, validationError("source", "settlement source binding references an undeclared action", "declared source action", []string{sourceAction}, "fix the template source binding")
	}
	if source.Kind != sourceSpec.ResultEvidenceKind {
		return nil, validationError("source", "claimed source evidence has the wrong declared kind", sourceSpec.ResultEvidenceKind, []string{source.Kind}, "claim a candidate from the declared source action")
	}
	return source, nil
}

// addActionEvidenceTx records settlement evidence inside the settling
// transaction; the caller refreshes the instance context.
func (s *Service) addActionEvidenceTx(tx *sql.Tx, inst *Instance, tpl *Template, params AddEvidenceParams) (*Evidence, error) {
	ev, _, err := insertEvidenceTx(tx, inst, tpl, params, s.now().UTC().Format(time.RFC3339))
	return ev, err
}

func (s *Service) refreshInstanceContextTx(tx *sql.Tx, inst *Instance, actor string) error {
	task, err := loadTaskDoc(tx, inst.TaskUUID)
	if err != nil {
		return err
	}
	inst.TaskDocEtag = fmt.Sprint(task.ETag)
	inst.TaskDocHash = taskDocHash(task)
	inst.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE workflow_instances SET task_doc_etag = ?, task_doc_hash = ?, updated_at = ? WHERE id = ?`,
		inst.TaskDocEtag, inst.TaskDocHash, inst.UpdatedAt, inst.ID); err != nil {
		return err
	}
	return updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, actor)
}

func (s *Service) applyActionTransitionTx(tx *sql.Tx, inst *Instance, tpl *Template, transitionID, actor, role, runID string) (TransitionResult, error) {
	key := fmt.Sprintf("wrkf-action:%s:settle:transition:%s", runID, transitionID)
	opts := TransitionOptions{PrincipalRef: actor, Role: role, IdempotencyKey: key, RunID: runID}
	requestHash := transitionRequestHash("", inst.ID, transitionID, opts)
	if replayed, err := replayTransitionResult(tx, inst.ID, key, requestHash); err != nil {
		return nil, err
	} else if replayed != nil {
		return replayed, nil
	}
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return nil, err
	}
	task, err := loadTaskDoc(tx, inst.TaskUUID)
	if err != nil {
		return nil, err
	}
	ev, err := listInstanceEvidence(tx, inst.ID)
	if err != nil {
		return nil, err
	}
	obl, err := listObligations(tx, inst.ID, true)
	if err != nil {
		return nil, err
	}
	checks := map[string]CheckRun{}
	for _, checkID := range tr.Checks {
		if latest, ok := latestCheckFor(s.db, inst.ID, tr.ID, checkID); ok {
			checks[checkID] = latest
		}
	}
	decision, err := s.EvaluateTransitionDecision(TransitionDecisionInput{
		Instance:           inst,
		Template:           tpl,
		Transition:         *tr,
		Task:               task,
		Evidence:           ev,
		Obligations:        obl,
		Checks:             checks,
		Role:               role,
		PrincipalRef:       actor,
		RoleQuery:          tx,
		DependencyQuery:    tx,
		CheckDatabase:      s.db,
		RequireRoleBinding: true,
	})
	if err != nil {
		return nil, err
	}
	if !decision.Legal {
		return nil, transitionDecisionError(inst.ID, transitionID, role, decision)
	}
	chosen := decision.Outcome

	updated, result, _, err := commitTransitionOutcomeTx(tx, transitionCommit{
		inst: inst, task: task, outcome: chosen, transitionID: transitionID, resultTask: inst.TaskRef,
		principalRef: actor, role: role, runID: runID, idempotencyKey: key, requestHash: requestHash,
		now: s.now().UTC().Format(time.RFC3339), expectedRevision: inst.Revision,
	})
	if err != nil {
		return nil, err
	}
	*inst = updated
	return result, nil
}

func transitionEffectsFromMap(result map[string]interface{}) []Effect {
	if result == nil {
		return nil
	}
	effects, _ := result["effects"].([]Effect)
	return effects
}

func transitionObligationsFromMap(result map[string]interface{}) []Obligation {
	if result == nil {
		return nil
	}
	obligations, _ := result["obligations"].([]Obligation)
	return obligations
}
