//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (p *ClaimActionParams) UnmarshalJSON(data []byte) error {
	type plain ClaimActionParams
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*p = ClaimActionParams(decoded)
	_, p.PriorRunProvided = fields["priorRun"]
	return nil
}

func (s *Service) ClaimAction(p ClaimActionParams) (*ClaimActionResult, error) {
	if strings.TrimSpace(p.RunnerID) == "" {
		return nil, validationError("runnerId", "runnerId is required", "runnerId", nil, "supply the runner identity")
	}
	if strings.TrimSpace(p.AgentRef) == "" {
		return nil, validationError("agentRef", "agentRef is required", "agentRef", nil, "supply agentRef")
	}
	if p.LeaseMs <= 0 {
		return nil, validationError("leaseMs", "leaseMs must be greater than zero", "positive milliseconds", nil, "supply leaseMs")
	}
	if !p.PriorRunProvided {

		p.PriorRun = nil
	}
	var binding *FencedRunBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		task := strings.TrimSpace(p.Task)
		instanceID := strings.TrimSpace(firstNonEmptyAction(p.InstanceID, p.Prefer.InstanceID))

		workspaceRoot := strings.TrimSpace(p.WorkspaceRoot)
		inst, err := resolveInstanceSelectors(tx, task, instanceID)
		if err != nil {
			return err
		}

		if inst.Suspension != nil {
			return suspendedWriteError(inst)
		}
		filters := ActionNextFilters{}
		if action := strings.TrimSpace(p.Prefer.Action); action != "" {
			filters.Actions = []string{action}
		}
		next, err := s.actionCandidatesForInstance(tx, inst, ActionNextParams{Filters: filters})
		if err != nil {
			return err
		}
		candidate, ok := selectClaimCandidate(next.Candidates, p.Prefer)
		if !ok {
			binding = nil
			return nil
		}
		if err := validateRunnerCapabilities(candidate, p.Capabilities); err != nil {
			return err
		}
		token, err := newLeaseToken()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		nowText := now.Format(time.RFC3339)
		expiresAt := now.Add(time.Duration(p.LeaseMs) * time.Millisecond).Format(time.RFC3339)
		attempt, err := nextAttemptForSemanticKey(tx, candidate.InstanceID, candidate.SemanticActionKey)
		if err != nil {
			return err
		}
		predecessor, err := latestRunForSemanticKey(tx, candidate.InstanceID, candidate.SemanticActionKey)
		if err != nil {
			return err
		}
		if predecessor != nil {
			record, err := actionClaimPredecessorTx(tx, predecessor)
			if err != nil {
				return err
			}
			if p.PriorRun == nil || strings.TrimSpace(*p.PriorRun) != predecessor.ID {
				return claimRefusedError(record)
			}
		} else {
			if !p.PriorRunProvided {
				return validationError("priorRun", "priorRun is required", "run id or null", nil, "send priorRun null for a first-ever claim")
			}
			if p.PriorRun != nil {
				return validationError("priorRun", "priorRun does not identify an existing predecessor", "null", nil, "send priorRun null for a first-ever claim")
			}
		}
		id, err := nextSeqID(tx, "workflow_run_seq", "run")
		if err != nil {
			return err
		}
		sideEffectJSON, err := json.Marshal(candidate.SideEffectClasses)
		if err != nil {
			return err
		}
		predecessorID := ""
		if predecessor != nil {
			predecessorID = predecessor.ID
		}
		sourceRunID, sourceEvidenceID, sourceIdentity := "", "", ""
		if candidate.Source != nil {
			sourceRunID = candidate.Source.SourceRunID
			sourceEvidenceID = candidate.Source.SourceEvidenceID
			sourceIdentity = candidate.Source.SourceIdentity
		}
		if predecessor != nil {
			if _, err := tx.Exec(`UPDATE workflow_runs SET status = 'superseded', terminal_result = ?, completed_at = ?, lease_token = NULL WHERE id = ?`, "superseded by "+id, nowText, predecessor.ID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`
				INSERT INTO workflow_runs (
					id, instance_id, role, actor, principal_ref, status, started_at,
					idempotency_key, action, lease_owner, lease_token, lease_expires_at, heartbeat_at,
					semantic_action_key, attempt, agent_ref, scope_ref, handler_contract,
					workspace_ref, source_run_id, source_evidence_id, source_identity, owner_generation
					, predecessor_run_id, side_effect_classes_json
				)
				VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
			`, id, candidate.InstanceID, candidate.Role, p.AgentRef, p.AgentRef, nowText, nullIfEmpty(p.IdempotencyKey),
			candidate.Action, p.RunnerID, token, expiresAt, nowText, candidate.SemanticActionKey, attempt,
			p.AgentRef, nullIfEmpty(p.ScopeRef), nullIfEmpty(candidate.HandlerContract), nullIfEmpty(firstNonEmptyAction(workspaceRoot, candidate.WorkspaceRef)),
			nullIfEmpty(sourceRunID), nullIfEmpty(sourceEvidenceID), nullIfEmpty(sourceIdentity), nullIfEmpty(predecessorID), string(sideEffectJSON))
		if err != nil {
			if isRunUniqueConflict(err) {
				return actionLeaseConflictError(candidate.SemanticActionKey)
			}
			return err
		}
		run := &claimedRun{
			ID: id, InstanceID: candidate.InstanceID, SemanticActionKey: candidate.SemanticActionKey,
			Action: candidate.Action, Role: candidate.Role, Attempt: attempt, Status: "active",
			AgentRef: p.AgentRef, ScopeRef: p.ScopeRef, HandlerContract: candidate.HandlerContract,
			WorkspaceRef: firstNonEmptyAction(workspaceRoot, candidate.WorkspaceRef), SourceRunID: sourceRunID, SourceEvidenceID: sourceEvidenceID,
			SourceIdentity: sourceIdentity, StartedAt: nowText, LeaseOwner: p.RunnerID, LeaseToken: token,
			LeaseExpiresAt: expiresAt, HeartbeatAt: nowText, OwnerGeneration: 1, SideEffectClassesJSON: string(sideEffectJSON), PredecessorRunID: predecessorID,
		}
		taskDoc, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		if predecessor != nil {
			if _, err := tx.Exec(`UPDATE workflow_runs SET superseded_by_run_id = ? WHERE id = ?`, id, predecessor.ID); err != nil {
				return err
			}
			if err := appendActionSuccessionLedgerTx(tx, inst, predecessor, run, taskDoc); err != nil {
				return err
			}
		}
		binding = claimedRunBinding(run, inst, taskDoc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ClaimActionResult{Binding: binding}, nil
}

func selectClaimCandidate(candidates []ActionCandidate, prefer ActionClaimPrefer) (ActionCandidate, bool) {
	for _, candidate := range candidates {
		if prefer.SemanticActionKey != "" && candidate.SemanticActionKey != prefer.SemanticActionKey {
			continue
		}
		if prefer.Action != "" && candidate.Action != prefer.Action {
			continue
		}
		return candidate, true
	}
	return ActionCandidate{}, false
}

func validateRunnerCapabilities(candidate ActionCandidate, caps []RunnerCapability) error {
	if len(caps) == 0 {
		return nil
	}
	for _, cap := range caps {
		if capabilityMatches(candidate, cap) {
			return nil
		}
	}
	return validationError("capabilities", "runner capabilities do not match candidate", "matching capability", []string{candidate.Action, candidate.Role, candidate.HandlerContract}, "supply a capability covering the candidate action, role, handler contract, workspace mode, and side effects")
}

func capabilityMatches(candidate ActionCandidate, cap RunnerCapability) bool {
	if strings.TrimSpace(cap.HandlerContract) != "" && strings.TrimSpace(cap.HandlerContract) != candidate.HandlerContract {
		return false
	}
	if len(cap.Actions) > 0 && !matchesAnyFilter(candidate.Action, cap.Actions) {
		return false
	}
	if len(cap.Roles) > 0 && !matchesAnyFilter(candidate.Role, cap.Roles) {
		return false
	}
	if len(cap.WorkspaceModes) > 0 && !matchesAnyFilter(candidate.WorkspaceMode, cap.WorkspaceModes) {
		return false
	}
	if len(cap.SideEffectClasses) > 0 {
		allowed := map[string]bool{}
		for _, c := range cap.SideEffectClasses {
			allowed[strings.TrimSpace(c)] = true
		}
		for _, c := range candidate.SideEffectClasses {
			if !allowed[strings.TrimSpace(c)] {
				return false
			}
		}
	}
	return true
}

func nextAttemptForSemanticKey(tx *sql.Tx, instanceID, semanticKey string) (int64, error) {
	var attempt int64
	err := tx.QueryRow(`
		SELECT COALESCE(MAX(attempt), 0) + 1
		FROM workflow_runs
		WHERE instance_id = ? AND semantic_action_key = ?
	`, instanceID, semanticKey).Scan(&attempt)
	return attempt, err
}

func latestRunForSemanticKey(tx *sql.Tx, instanceID, semanticKey string) (*claimedRun, error) {
	row := tx.QueryRow(`
		SELECT `+claimedRunColumns+`
		FROM workflow_runs
		WHERE instance_id = ? AND semantic_action_key = ?
		ORDER BY attempt DESC, started_at DESC, id DESC
		LIMIT 1
	`, instanceID, semanticKey)
	run, err := scanClaimedRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return run, nil
}

func claimedRunByIDTx(tx *sql.Tx, id string) (*claimedRun, error) {
	row := tx.QueryRow(`
		SELECT `+claimedRunColumns+`
		FROM workflow_runs
		WHERE id = ?
	`, id)
	run, err := scanClaimedRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, err
	}
	return run, nil
}

// claimedRunColumns is the SELECT list scanClaimedRun reads, in scan order.
const claimedRunColumns = `id, instance_id, COALESCE(semantic_action_key,''), COALESCE(action,''), role, COALESCE(attempt,1),
	status, COALESCE(agent_ref, principal_ref, actor, ''), COALESCE(scope_ref,''), COALESCE(handler_contract,''),
	COALESCE(external_run_ref,''), COALESCE(workspace_ref,''),
	COALESCE(source_run_id,''), COALESCE(source_evidence_id,''), COALESCE(source_identity,''),
	started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
	COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,''), COALESCE(owner_generation,0),
	COALESCE(superseded_by_run_id,''), COALESCE(side_effect_classes_json,'[]'), COALESCE(predecessor_run_id,'')`

func scanClaimedRun(scanner runRowScanner) (*claimedRun, error) {
	var r claimedRun
	err := scanner.Scan(
		&r.ID, &r.InstanceID, &r.SemanticActionKey, &r.Action, &r.Role, &r.Attempt,
		&r.Status, &r.AgentRef, &r.ScopeRef, &r.HandlerContract,
		&r.ExternalRunRef, &r.WorkspaceRef, &r.SourceRunID, &r.SourceEvidenceID, &r.SourceIdentity,
		&r.StartedAt, &r.CompletedAt, &r.TerminalSummary, &r.LeaseOwner, &r.LeaseToken,
		&r.LeaseExpiresAt, &r.HeartbeatAt, &r.OwnerGeneration, &r.SupersededByRunID, &r.SideEffectClassesJSON, &r.PredecessorRunID,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func actionClaimPredecessorTx(tx *sql.Tx, run *claimedRun) (*ActionClaimPredecessor, error) {
	record := &ActionClaimPredecessor{
		RunID: run.ID, Owner: run.LeaseOwner, ClaimedAt: run.StartedAt,
		HeartbeatAt: run.HeartbeatAt, ExpiresAt: run.LeaseExpiresAt,
		SettleStatus: run.Status, Settled: isTerminalRunStatus(run.Status),
		TerminalResult: run.TerminalSummary,
		ExternalRunRef: run.ExternalRunRef, WorkspaceRef: run.WorkspaceRef,
		SideEffectClasses: []string{}, EvidenceWritten: []ActionClaimEvidenceRecord{},
	}
	if strings.TrimSpace(run.SideEffectClassesJSON) != "" {
		if err := json.Unmarshal([]byte(run.SideEffectClassesJSON), &record.SideEffectClasses); err != nil {
			return nil, fmt.Errorf("decode predecessor side-effect classes: %w", err)
		}
	}
	rows, err := tx.Query(`SELECT id, kind, ref, COALESCE(summary,''), produced_at FROM workflow_evidence WHERE run_id = ? ORDER BY produced_at, id`, run.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var evidence ActionClaimEvidenceRecord
		if err := rows.Scan(&evidence.ID, &evidence.Kind, &evidence.Ref, &evidence.Summary, &evidence.ProducedAt); err != nil {
			return nil, err
		}
		record.EvidenceWritten = append(record.EvidenceWritten, evidence)
	}
	return record, rows.Err()
}

func appendActionSuccessionLedgerTx(tx *sql.Tx, inst *Instance, predecessor, successor *claimedRun, task *taskDoc) error {
	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM ledger_entry WHERE instance_id = ?`, inst.ID).Scan(&seq); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"predecessorRunId": predecessor.ID,
		"successorRunId":   successor.ID,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO ledger_entry (uuid, instance_id, task_id, seq, ts, kind, about_principal_ref, written_by, body_json)
		VALUES (?, ?, ?, ?, ?, 'workflow.action.succession', ?, ?, ?)`,
		uuid.NewString(), inst.ID, task.ID, seq, successor.StartedAt, successor.AgentRef, successor.AgentRef, string(body))
	return err
}

func workflowRunAttemptFromClaimed(run *claimedRun) WorkflowRunAttempt {
	return WorkflowRunAttempt{
		ID:                run.ID,
		InstanceID:        run.InstanceID,
		SemanticActionKey: run.SemanticActionKey,
		Action:            run.Action,
		Role:              run.Role,
		Attempt:           run.Attempt,
		Status:            run.Status,
		AgentRef:          run.AgentRef,
		ScopeRef:          run.ScopeRef,
		HandlerContract:   run.HandlerContract,
		ExternalRunRef:    run.ExternalRunRef,
		WorkspaceRef:      run.WorkspaceRef,
		Source:            claimedRunSource(run),
		StartedAt:         run.StartedAt,
		CompletedAt:       run.CompletedAt,
		TerminalSummary:   run.TerminalSummary,
		PredecessorRunID:  run.PredecessorRunID,
	}
}

func claimedRunBinding(run *claimedRun, inst *Instance, task *taskDoc) *FencedRunBinding {
	return &FencedRunBinding{
		Run: workflowRunAttemptFromClaimed(run),
		Task: ActionTaskBinding{
			UUID: task.UUID,
			Ref:  strings.TrimPrefix(inst.TaskRef, "wrkq:"),
			Path: task.Slug,
		},
		Instance: *inst,
		Authority: ActionRunAuthority{
			RunnerID:        run.LeaseOwner,
			OwnerToken:      run.LeaseToken,
			OwnerGeneration: run.OwnerGeneration,
			LeaseExpiresAt:  run.LeaseExpiresAt,
			ClaimedAt:       run.StartedAt,
			HeartbeatAt:     run.HeartbeatAt,
		},
	}
}

func claimedRunSource(run *claimedRun) *ActionSourceBinding {
	if run.SourceRunID == "" && run.SourceEvidenceID == "" && run.SourceIdentity == "" {
		return nil
	}
	return &ActionSourceBinding{
		SourceRunID:      run.SourceRunID,
		SourceEvidenceID: run.SourceEvidenceID,
		SourceIdentity:   run.SourceIdentity,
	}
}

func firstNonEmptyAction(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
