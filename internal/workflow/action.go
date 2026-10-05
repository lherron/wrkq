//go:build wrkq_local

package workflow

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const actionListMaxLimit = 1000

// StartAction ensures a workflow instance exists for the task, then creates (or
// idempotently replays) one wrkf run for a semantic action.
func (s *Service) StartAction(p StartActionParams) (*ActionRun, error) {
	action := strings.TrimSpace(p.Action)
	if action == "" {
		return nil, validationError("action", "action is required", "action", nil, "supply an action such as triage|implement|review|verify")
	}
	if strings.TrimSpace(p.Task) == "" && strings.TrimSpace(p.InstanceID) == "" {
		return nil, validationError("selector", "task or instanceId is required", "task or instanceId", nil, "supply task or instanceId")
	}
	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = defaultRoleForAction(action)
	}
	lane := strings.TrimSpace(p.Lane)
	if lane == "" {
		lane = defaultLaneForAction(action)
	}
	leaseOwner := strings.TrimSpace(p.LeaseOwner)
	leaseRequested := leaseOwner != "" || p.LeaseMs > 0
	if leaseRequested && leaseOwner == "" {
		return nil, validationError("leaseOwner", "leaseOwner is required when leaseMs is supplied", "leaseOwner", nil, "supply leaseOwner and leaseMs together")
	}
	if leaseRequested && p.LeaseMs <= 0 {
		return nil, validationError("leaseMs", "leaseMs must be greater than zero when leaseOwner is supplied", "positive milliseconds", nil, "supply leaseOwner and leaseMs together")
	}
	leaseToken, leaseExpiresAt, heartbeatAt := "", "", ""
	if leaseRequested {
		var err error
		leaseToken, err = newLeaseToken()
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		leaseExpiresAt = now.Add(time.Duration(p.LeaseMs) * time.Millisecond).Format(time.RFC3339)
		heartbeatAt = now.Format(time.RFC3339)
	}

	inst, err := s.resolveActiveInstance(p.Task, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		if strings.TrimSpace(p.InstanceID) != "" {
			return nil, fmt.Errorf("workflow instance not found: %s", p.InstanceID)
		}
		workflowRef := strings.TrimSpace(p.Workflow)
		if workflowRef == "" {
			if _, _, err := s.EnsureBuiltinTemplate(DefaultActionWorkflowTemplateRef, p.PrincipalRef); err != nil {
				return nil, err
			}
			workflowRef = DefaultActionWorkflowTemplateRef
		} else if _, builtinErr := builtinTemplateData(workflowRef); builtinErr == nil {
			if _, _, err := s.EnsureBuiltinTemplate(workflowRef, p.PrincipalRef); err != nil {
				return nil, err
			}
		}
		attached, err := s.AttachTask(p.Task, workflowRef, p.PrincipalRef)
		if err != nil {
			return nil, err
		}
		inst = attached
	}

	run, err := s.StartRunForSelectors("", inst.ID, role, p.PrincipalRef, StartRunOptions{
		IdempotencyKey: p.IdempotencyKey,
		DeliveryRef:    p.DeliveryRef,
		Lane:           lane,
		ExternalRunRef: normalizeExternalRunRef(p.ExternalRunRef),
		Action:         action,
		LeaseOwner:     leaseOwner,
		LeaseToken:     leaseToken,
		LeaseExpiresAt: leaseExpiresAt,
		HeartbeatAt:    heartbeatAt,
		LeaseMs:        p.LeaseMs,
	})
	if err != nil {
		return nil, err
	}
	return s.toActionRunWithOptions(run, inst, actionRunViewOptions{includeLeaseToken: leaseRequested})
}

// BindActionExternal binds an action run to an external runtime run (normally
// HRC), standardizing HRC refs as hrc:<runId>.
func (s *Service) BindActionExternal(p BindActionExternalParams) (*ActionRun, error) {
	if strings.TrimSpace(p.ActionRunID) == "" {
		return nil, validationError("actionRunId", "actionRunId is required", "actionRunId", nil, "supply the action run id")
	}
	run, err := s.BindExternal(p.ActionRunID, normalizeExternalRunRef(p.ExternalRunRef), BindExternalOptions{
		DeliveryRef:    p.DeliveryRef,
		Lane:           p.Lane,
		IdempotencyKey: p.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return s.toActionRun(run, nil)
}

// CompleteAction records run-linked evidence, optionally applies the matching
// transition, then finishes the run. Replays are side-effect free.
func (s *Service) CompleteAction(p CompleteActionParams) (*ActionCompleteResult, error) {
	if strings.TrimSpace(p.ActionRunID) == "" {
		return nil, validationError("actionRunId", "actionRunId is required", "actionRunId", nil, "supply the action run id")
	}
	run, err := s.ShowRun(p.ActionRunID)
	if err != nil {
		return nil, err
	}
	if err := s.validateActionSettlement(run, p.LeaseToken, "complete"); err != nil {
		return nil, err
	}
	inst, err := instanceByIDQuery(s.db, run.InstanceID)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	actionSpec, hasActionSpec := tpl.ExecutableActions[run.Action]

	result := &ActionCompleteResult{}
	if p.Evidence != nil {
		defaultKind := actionDefaultEvidenceKind(run.Action)
		if hasActionSpec && strings.TrimSpace(actionSpec.ResultEvidenceKind) != "" {
			defaultKind = strings.TrimSpace(actionSpec.ResultEvidenceKind)
		}
		kind := strings.TrimSpace(p.Evidence.Kind)
		if kind == "" {
			kind = defaultKind
		}
		if hasActionSpec && kind == strings.TrimSpace(actionSpec.ResultEvidenceKind) {
			if err := withImmediateTx(s.db, func(tx *sql.Tx) error {
				claimed, err := claimedRunByIDTx(tx, run.ID)
				if err != nil {
					return err
				}
				return validateSettleEvidenceFacts(tx, tpl, actionSpec, claimed, p.Evidence)
			}); err != nil {
				return nil, err
			}
		}
		ev, err := s.addActionEvidence(run, p.Evidence, defaultKind)
		if err != nil {
			return nil, err
		}
		result.Evidence = ev
	}

	if p.TransitionMode != TransitionSkip {
		transitionID := strings.TrimSpace(p.TransitionID)
		if p.TransitionMode == TransitionDefault {
			resolved, err := s.defaultTransitionFor(inst, run.Role)
			if err != nil {
				return nil, err
			}
			transitionID = resolved
		}
		if transitionID != "" {

			fresh, err := instanceByIDQuery(s.db, run.InstanceID)
			if err != nil {
				return nil, err
			}
			key := p.TransitionIdempotencyKey
			if key == "" {
				key = fmt.Sprintf("wrkf-action:%s:transition:%s", run.ID, transitionID)
			}
			out, err := s.TransitionForSelectors("", fresh.ID, transitionID, TransitionOptions{
				PrincipalRef:   run.PrincipalRef,
				Role:           run.Role,
				IdempotencyKey: key,
				RunID:          run.ID,
			})
			if err != nil {
				return nil, err
			}
			result.Transition = out
		}
	}

	finished, err := s.FinishRun(run.ID, "completed", p.RunSummary)
	if err != nil {
		return nil, err
	}
	ar, err := s.toActionRun(finished, inst)
	if err != nil {
		return nil, err
	}
	result.Run = ar
	return result, nil
}

// FailAction terminally fails an action run, optionally recording failure
// evidence. It never applies a success transition.
func (s *Service) FailAction(p FailActionParams) (*ActionRun, error) {
	if strings.TrimSpace(p.ActionRunID) == "" {
		return nil, validationError("actionRunId", "actionRunId is required", "actionRunId", nil, "supply the action run id")
	}
	if strings.TrimSpace(p.Summary) == "" {
		return nil, validationError("summary", "summary is required", "summary", nil, "supply a failure summary")
	}
	run, err := s.ShowRun(p.ActionRunID)
	if err != nil {
		return nil, err
	}
	if err := s.validateActionSettlement(run, p.LeaseToken, "fail"); err != nil {
		return nil, err
	}
	if p.Evidence != nil {
		if _, err := s.addActionEvidence(run, p.Evidence, "failure_result"); err != nil {
			return nil, err
		}
	}
	failed, err := s.FailRun(run.ID, p.Summary)
	if err != nil {
		return nil, err
	}
	return s.toActionRun(failed, nil)
}

func (s *Service) HeartbeatAction(p HeartbeatActionParams) (*ActionRun, error) {
	if strings.TrimSpace(p.ActionRunID) == "" {
		return nil, validationError("actionRunId", "actionRunId is required", "actionRunId", nil, "supply the action run id")
	}
	if strings.TrimSpace(p.LeaseToken) == "" {
		return nil, actionLeaseConflictError(p.ActionRunID)
	}
	leaseMs := p.LeaseMs
	if leaseMs <= 0 {
		leaseMs = 5 * 60 * 1000
	}
	now := s.now().UTC()
	expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
	heartbeatAt := now.Format(time.RFC3339)
	var run *Run
	if err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := selectRunByID(tx, p.ActionRunID)
		if err != nil {
			return err
		}
		if current.Status != "active" || current.LeaseToken == "" || current.LeaseToken != strings.TrimSpace(p.LeaseToken) || !leaseStillCurrent(current.LeaseExpiresAt, now) {
			return actionLeaseConflictError(p.ActionRunID)
		}
		if _, err := tx.Exec(`UPDATE workflow_runs SET lease_expires_at = ?, heartbeat_at = ? WHERE id = ?`, expiresAt, heartbeatAt, current.ID); err != nil {
			return err
		}
		current.LeaseExpiresAt = expiresAt
		current.HeartbeatAt = heartbeatAt
		run = current
		return nil
	}); err != nil {
		return nil, err
	}
	return s.toActionRunWithOptions(run, nil, actionRunViewOptions{includeLeaseToken: true})
}

// ShowAction returns the canonical view of one action run, including run-linked
// evidence and transition events.
func (s *Service) ShowAction(actionRunID string) (*ActionRun, error) {
	if strings.TrimSpace(actionRunID) == "" {
		return nil, validationError("actionRunId", "actionRunId is required", "actionRunId", nil, "supply the action run id")
	}
	run, err := s.ShowRun(actionRunID)
	if err != nil {
		return nil, err
	}
	ar, err := s.toActionRun(run, nil)
	if err != nil {
		return nil, err
	}
	evIDs, evKinds, err := s.runEvidence(run.ID)
	if err != nil {
		return nil, err
	}
	ar.EvidenceIDs, ar.EvidenceKinds = evIDs, evKinds
	eventIDs, err := s.runTransitionEvents(run.ID)
	if err != nil {
		return nil, err
	}
	ar.TransitionEventIDs = eventIDs
	return ar, nil
}

// ListActions returns the action runs attached to a task. With
// IncludeClosedInstances it spans all workflow instances of the task.
func (s *Service) ListActions(p ListActionsParams) ([]ActionRun, error) {
	instanceIDs, instByID, err := s.actionListInstances(p)
	if err != nil {
		return nil, err
	}
	if len(instanceIDs) == 0 {
		return []ActionRun{}, nil
	}

	limit := p.Limit
	if limit <= 0 || limit > actionListMaxLimit {
		limit = actionListMaxLimit
	}

	placeholders := make([]string, len(instanceIDs))
	args := make([]interface{}, 0, len(instanceIDs)+3)
	for i, id := range instanceIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	clause := `WHERE instance_id IN (` + strings.Join(placeholders, ",") + `) AND action IS NOT NULL`
	if status := strings.TrimSpace(p.Status); status != "" {
		clause += " AND status = ?"
		args = append(args, status)
	}
	if action := strings.TrimSpace(p.Action); action != "" {
		clause += " AND action = ?"
		args = append(args, action)
	}
	clause += " ORDER BY started_at, id LIMIT ?"
	args = append(args, limit)

	runs, err := queryRuns(s.db, clause, args...)
	if err != nil {
		return nil, err
	}
	out := []ActionRun{}
	for i := range runs {
		ar, err := s.toActionRun(&runs[i], instByID[runs[i].InstanceID])
		if err != nil {
			return nil, err
		}
		out = append(out, *ar)
	}

	for i := range out {
		evIDs, evKinds, err := s.runEvidence(out[i].RunID)
		if err != nil {
			return nil, err
		}
		out[i].EvidenceIDs, out[i].EvidenceKinds = evIDs, evKinds
		eventIDs, err := s.runTransitionEvents(out[i].RunID)
		if err != nil {
			return nil, err
		}
		out[i].TransitionEventIDs = eventIDs
	}
	return out, nil
}

func (s *Service) resolveActiveInstance(taskSelector, instanceID string) (*Instance, error) {
	if strings.TrimSpace(instanceID) != "" {
		inst, err := instanceByIDQuery(s.db, instanceID)
		if err != nil {
			if err == sql.ErrNoRows || strings.Contains(err.Error(), "not found") {
				return nil, nil
			}
			return nil, err
		}
		return inst, nil
	}
	taskUUID, err := resolveTaskUUIDQuery(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	inst, err := latestInstanceByTaskUUIDQuery(s.db, taskUUID)
	if err != nil {
		if err == sql.ErrNoRows || strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	if inst != nil && inst.Status == "closed" {
		return nil, nil
	}
	return inst, nil
}

func (s *Service) addActionEvidence(run *Run, in *ActionEvidenceInput, defaultKind string) (*Evidence, error) {
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = defaultKind
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		ref = fmt.Sprintf("wrkf-action:%s", run.ID)
	}
	key := in.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("wrkf-action:%s:evidence:%s", run.ID, kind)
	}
	return s.AddEvidence(AddEvidenceParams{
		InstanceID:     run.InstanceID,
		Kind:           kind,
		Ref:            ref,
		Summary:        in.Summary,
		Facts:          in.Facts,
		Data:           in.Data,
		PrincipalRef:   run.PrincipalRef,
		Role:           run.Role,
		RunID:          run.ID,
		ContentHash:    in.ContentHash,
		IdempotencyKey: key,
	})
}

// defaultTransitionFor returns the unique transition available from the
// instance's current state for the given role. It returns "" when no transition
// applies (CompleteAction then finishes the run without a transition) and an
// error when more than one applies (the caller must pass an explicit transition).
func (s *Service) defaultTransitionFor(inst *Instance, role string) (string, error) {
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, tr := range tpl.Transitions {
		if transitionFromMatches(*inst, tr) && roleAllowed(role, tr.By) {
			matches = append(matches, tr.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", validationError("transition", "multiple transitions are available; pass an explicit transition", "transition", matches, "set transition to one of the available ids")
	}
}

func (s *Service) toActionRun(run *Run, inst *Instance) (*ActionRun, error) {
	return s.toActionRunWithOptions(run, inst, actionRunViewOptions{})
}

func (s *Service) toActionRunWithOptions(run *Run, inst *Instance, opts actionRunViewOptions) (*ActionRun, error) {
	if inst == nil {
		loaded, err := instanceByIDQuery(s.db, run.InstanceID)
		if err != nil {
			return nil, err
		}
		inst = loaded
	}
	out := &ActionRun{
		ActionRunID:    run.ID,
		RunID:          run.ID,
		Task:           strings.TrimPrefix(inst.TaskRef, "wrkq:"),
		InstanceID:     inst.ID,
		Workflow:       ActionWorkflowRef{ID: inst.TemplateID, Version: inst.TemplateVersion, Hash: inst.TemplateHash},
		Action:         run.Action,
		Role:           run.Role,
		PrincipalRef:   run.PrincipalRef,
		Lane:           run.Lane,
		DeliveryRef:    run.DeliveryRef,
		ExternalRunRef: run.ExternalRunRef,
		Status:         run.Status,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
		TerminalResult: run.TerminalResult,
		LeaseOwner:     run.LeaseOwner,
		LeaseExpiresAt: run.LeaseExpiresAt,
		HeartbeatAt:    run.HeartbeatAt,
	}
	if opts.includeLeaseToken {
		out.LeaseToken = run.LeaseToken
	}
	return out, nil
}

func (s *Service) validateActionSettlement(run *Run, token, op string) error {
	if run.Status == "completed" && op == "complete" {
		return nil
	}
	if run.Status != "active" {
		return actionLeaseConflictError(run.ID)
	}
	if run.LeaseToken == "" {
		return nil
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(token) != run.LeaseToken {
		return actionLeaseConflictError(run.ID)
	}
	return nil
}

func leaseStillCurrent(expiresAt string, now time.Time) bool {
	if strings.TrimSpace(expiresAt) == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(expiresAt))
	if err != nil {
		return false
	}
	return t.After(now)
}

func (s *Service) runEvidence(runID string) ([]string, []string, error) {
	rows, err := s.db.Query(`SELECT id, kind FROM workflow_evidence WHERE run_id = ? ORDER BY produced_at, id`, runID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids, kinds []string
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		kinds = append(kinds, kind)
	}
	return ids, kinds, rows.Err()
}

func (s *Service) runTransitionEvents(runID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM workflow_events WHERE run_id = ? AND type = 'workflow.transitioned' ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) actionListInstances(p ListActionsParams) ([]string, map[string]*Instance, error) {
	byID := map[string]*Instance{}
	if id := strings.TrimSpace(p.InstanceID); id != "" {
		inst, err := instanceByIDQuery(s.db, id)
		if err != nil {
			if err == sql.ErrNoRows || strings.Contains(err.Error(), "not found") {
				return nil, byID, nil
			}
			return nil, nil, err
		}
		byID[inst.ID] = inst
		return []string{inst.ID}, byID, nil
	}
	taskUUID, err := resolveTaskUUIDQuery(s.db, p.Task)
	if err != nil {
		return nil, nil, err
	}
	query := `
		SELECT ` + instanceSelectColumns + `
		FROM workflow_instances WHERE task_uuid = ?`
	if !p.IncludeClosedInstances {
		query += " AND status != 'closed'"
	}
	query += " ORDER BY created_at, id"
	rows, err := s.db.Query(query, taskUUID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		inst, err := scanInstanceRow(rows)
		if err != nil {
			return nil, nil, err
		}
		byID[inst.ID] = inst
		ids = append(ids, inst.ID)
	}
	return ids, byID, rows.Err()
}

func actionDefaultEvidenceKind(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return "action_result"
	}
	return action + "_result"
}

func defaultRoleForAction(action string) string {
	switch action {
	case "triage":
		return "triager"
	case "implement":
		return "implementer"
	case "review":
		return "reviewer"
	case "verify":
		return "tester"
	case "landing":
		return "release_manager"
	default:
		return action
	}
}

func defaultLaneForAction(action string) string {
	switch action {
	case "triage":
		return "triage"
	case "implement":
		return "implementation"
	case "review":
		return "review"
	case "verify":
		return "verify"
	default:
		return action
	}
}

// normalizeExternalRunRef standardizes HRC bindings as hrc:<runId>. A ref that
// already carries a scheme (contains ":") is preserved; a bare value is treated
// as an HRC run id and prefixed. Empty stays empty.
func normalizeExternalRunRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, ":") {
		return ref
	}
	return "hrc:" + ref
}
