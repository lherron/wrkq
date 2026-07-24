package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/webhooks"
)

// action.go — low-ceremony wrkf.action.* surface.
//
// A wrkf "action" is a single semantic task-lifecycle step (triage, implement,
// verify, review, ...). The action API is a thin composition over the existing
// wrkf primitives — run.start, run.bindExternal, evidence.add, transition.apply,
// run.finish/fail — not a second ledger. It does not touch tasks.state directly
// and never reads or writes legacy task scalar linkage fields.

// ActionWorkflowRef identifies the workflow template backing an action run.
type ActionWorkflowRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    string `json:"hash,omitempty"`
}

// ActionRun is the canonical semantic view of one action run. ActionRunID and
// RunID are the same durable workflow_runs.id; both names are preserved so
// callers can use existing wrkf run language.
type ActionRun struct {
	ActionRunID        string            `json:"actionRunId"`
	RunID              string            `json:"runId"`
	Task               string            `json:"task"`
	InstanceID         string            `json:"instanceId"`
	Workflow           ActionWorkflowRef `json:"workflow"`
	Action             string            `json:"action"`
	Role               string            `json:"role"`
	PrincipalRef       string            `json:"principal_ref,omitempty"`
	Lane               string            `json:"lane,omitempty"`
	DeliveryRef        string            `json:"deliveryRef,omitempty"`
	ExternalRunRef     string            `json:"externalRunRef,omitempty"`
	Status             string            `json:"status"`
	StartedAt          string            `json:"startedAt"`
	CompletedAt        string            `json:"completedAt,omitempty"`
	TerminalResult     string            `json:"terminalResult,omitempty"`
	LeaseOwner         string            `json:"leaseOwner,omitempty"`
	LeaseToken         string            `json:"leaseToken,omitempty"`
	LeaseExpiresAt     string            `json:"leaseExpiresAt,omitempty"`
	HeartbeatAt        string            `json:"heartbeatAt,omitempty"`
	EvidenceIDs        []string          `json:"evidenceIds,omitempty"`
	EvidenceKinds      []string          `json:"evidenceKinds,omitempty"`
	TransitionEventIDs []string          `json:"transitionEventIds,omitempty"`
}

// ActionEvidenceInput is the optional evidence recorded by complete/fail.
type ActionEvidenceInput struct {
	Kind           string
	Ref            string
	Summary        string
	Facts          string
	Data           string
	ContentHash    string
	IdempotencyKey string
}

// StartActionParams drives StartAction.
type StartActionParams struct {
	Task           string
	InstanceID     string
	Workflow       string
	Action         string
	Role           string
	PrincipalRef   string
	Lane           string
	DeliveryRef    string
	ExternalRunRef string
	IdempotencyKey string
	LeaseOwner     string
	LeaseMs        int64
}

// BindActionExternalParams drives BindActionExternal.
type BindActionExternalParams struct {
	ActionRunID    string
	ExternalRunRef string
	DeliveryRef    string
	Lane           string
	IdempotencyKey string
}

// TransitionMode selects how CompleteAction handles the workflow transition.
type TransitionMode int

const (
	// TransitionDefault resolves the unique transition available from the
	// current state for the run's role.
	TransitionDefault TransitionMode = iota
	// TransitionSkip records evidence and finishes the run without a transition.
	TransitionSkip
	// TransitionExplicit applies the caller-supplied transition id.
	TransitionExplicit
)

// CompleteActionParams drives CompleteAction.
type CompleteActionParams struct {
	ActionRunID              string
	LeaseToken               string
	Evidence                 *ActionEvidenceInput
	TransitionMode           TransitionMode
	TransitionID             string
	TransitionIdempotencyKey string
	RunSummary               string
}

// FailActionParams drives FailAction.
type FailActionParams struct {
	ActionRunID string
	LeaseToken  string
	Summary     string
	Evidence    *ActionEvidenceInput
}

type HeartbeatActionParams struct {
	ActionRunID string
	LeaseToken  string
	LeaseMs     int64
}

type ClaimActionParams struct {
	Task             string             `json:"task,omitempty"`
	InstanceID       string             `json:"instanceId,omitempty"`
	Prefer           ActionClaimPrefer  `json:"prefer,omitempty"`
	RunnerID         string             `json:"runnerId"`
	AgentRef         string             `json:"agentRef"`
	ScopeRef         string             `json:"scopeRef,omitempty"`
	Capabilities     []RunnerCapability `json:"capabilities,omitempty"`
	LeaseMs          int64              `json:"leaseMs"`
	WorkspaceRoot    string             `json:"workspaceRoot,omitempty"`
	IdempotencyKey   string             `json:"idempotencyKey,omitempty"`
	PriorRun         *string            `json:"priorRun"`
	PriorRunProvided bool               `json:"-"`
}

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

type ActionClaimPrefer struct {
	InstanceID        string `json:"instanceId,omitempty"`
	SemanticActionKey string `json:"semanticActionKey,omitempty"`
	Action            string `json:"action,omitempty"`
}

type RunnerCapability struct {
	HandlerContract   string   `json:"handlerContract,omitempty"`
	HandlerID         string   `json:"handlerId,omitempty"`
	HandlerVersion    string   `json:"handlerVersion,omitempty"`
	Actions           []string `json:"actions,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	SideEffectClasses []string `json:"sideEffectClasses,omitempty"`
	WorkspaceModes    []string `json:"workspaceModes,omitempty"`
}

type ClaimActionResult struct {
	Binding *FencedRunBinding `json:"binding,omitempty"`
}

type ActionClaimEvidenceRecord struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Summary    string `json:"summary,omitempty"`
	ProducedAt string `json:"producedAt"`
}

type ActionClaimPredecessor struct {
	RunID             string                      `json:"runId"`
	Owner             string                      `json:"owner,omitempty"`
	ClaimedAt         string                      `json:"claimedAt"`
	HeartbeatAt       string                      `json:"heartbeatAt,omitempty"`
	ExpiresAt         string                      `json:"expiresAt,omitempty"`
	SettleStatus      string                      `json:"settleStatus"`
	Settled           bool                        `json:"settled"`
	TerminalResult    string                      `json:"terminalResult,omitempty"`
	SideEffectClasses []string                    `json:"sideEffectClasses"`
	ExternalRunRef    string                      `json:"externalRunRef,omitempty"`
	WorkspaceRef      string                      `json:"workspaceRef,omitempty"`
	EvidenceWritten   []ActionClaimEvidenceRecord `json:"evidenceWritten"`
}

type SettleActionParams struct {
	ActionRunID     string               `json:"actionRunId,omitempty"`
	RunID           string               `json:"runId,omitempty"`
	OwnerToken      string               `json:"ownerToken,omitempty"`
	OwnerGeneration int64                `json:"ownerGeneration,omitempty"`
	Result          string               `json:"result"`
	Evidence        *ActionEvidenceInput `json:"evidence,omitempty"`
	TransitionMode  TransitionMode       `json:"-"`
	TransitionID    string               `json:"-"`
	TerminalSummary string               `json:"terminalSummary,omitempty"`
}

type SettleActionResult struct {
	Run         WorkflowRunAttempt     `json:"run"`
	Evidence    *Evidence              `json:"evidence,omitempty"`
	Transition  map[string]interface{} `json:"transition,omitempty"`
	Effects     []Effect               `json:"effects,omitempty"`
	Obligations []Obligation           `json:"obligations,omitempty"`
}

type FencedRunBinding struct {
	Run       WorkflowRunAttempt `json:"run"`
	Task      ActionTaskBinding  `json:"task"`
	Instance  Instance           `json:"instance"`
	Authority ActionRunAuthority `json:"authority"`
}

type WorkflowRunAttempt struct {
	ID                string               `json:"id"`
	InstanceID        string               `json:"instanceId"`
	SemanticActionKey string               `json:"semanticActionKey"`
	Action            string               `json:"action"`
	Role              string               `json:"role"`
	Attempt           int64                `json:"attempt"`
	Status            string               `json:"status"`
	AgentRef          string               `json:"agentRef,omitempty"`
	ScopeRef          string               `json:"scopeRef,omitempty"`
	HandlerContract   string               `json:"handlerContract,omitempty"`
	HandlerID         string               `json:"handlerId,omitempty"`
	HandlerVersion    string               `json:"handlerVersion,omitempty"`
	ExternalRunRef    string               `json:"externalRunRef,omitempty"`
	WorkspaceRef      string               `json:"workspaceRef,omitempty"`
	Source            *ActionSourceBinding `json:"source,omitempty"`
	StartedAt         string               `json:"startedAt"`
	CompletedAt       string               `json:"completedAt,omitempty"`
	TerminalSummary   string               `json:"terminalSummary,omitempty"`
	PredecessorRunID  string               `json:"predecessorRunId,omitempty"`
}

type ActionTaskBinding struct {
	UUID string `json:"uuid"`
	Ref  string `json:"ref"`
	Path string `json:"path,omitempty"`
}

type ActionRunAuthority struct {
	RunnerID        string `json:"runnerId"`
	OwnerToken      string `json:"ownerToken"`
	OwnerGeneration int64  `json:"ownerGeneration"`
	ClaimedAt       string `json:"claimedAt"`
	HeartbeatAt     string `json:"heartbeatAt,omitempty"`
	LeaseExpiresAt  string `json:"leaseExpiresAt"`
}

// ActionCompleteResult is the committed result of CompleteAction.
type ActionCompleteResult struct {
	Run        *ActionRun             `json:"run"`
	Evidence   *Evidence              `json:"evidence,omitempty"`
	Transition map[string]interface{} `json:"transition,omitempty"`
}

// ListActionsParams drives ListActions.
type ListActionsParams struct {
	Task                   string
	InstanceID             string
	IncludeClosedInstances bool
	Status                 string
	Action                 string
	Limit                  int
}

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
			if _, _, err := s.EnsureBuiltinTemplate(BuiltinSimpleTaskTemplateRef, p.PrincipalRef); err != nil {
				return nil, err
			}
			workflowRef = BuiltinSimpleTaskTemplateRef
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
		// Existing predecessors are reported below with their full review record.
		// A first-ever claim must still state the null CAS value explicitly.
		p.PriorRun = nil
	}
	var binding *FencedRunBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		task := strings.TrimSpace(p.Task)
		instanceID := strings.TrimSpace(firstNonEmptyAction(p.InstanceID, p.Prefer.InstanceID))
		// workspaceRef is an opaque reported fact on the run record: recorded at
		// claim time, surfaced in run readback, never interpreted by the engine.
		workspaceRoot := strings.TrimSpace(p.WorkspaceRoot)
		inst, err := resolveInstanceSelectors(tx, task, instanceID)
		if err != nil {
			return err
		}
		// Suspended-write gate (door 3 of 3). Refuse before candidate and
		// predecessor evaluation so suspension wins over succession.
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

type claimedRun struct {
	ID                    string
	InstanceID            string
	SemanticActionKey     string
	Action                string
	Role                  string
	Attempt               int64
	Status                string
	AgentRef              string
	ScopeRef              string
	HandlerContract       string
	HandlerID             string
	HandlerVersion        string
	ExternalRunRef        string
	WorkspaceRef          string
	SourceRunID           string
	SourceEvidenceID      string
	SourceIdentity        string
	StartedAt             string
	CompletedAt           string
	TerminalSummary       string
	LeaseOwner            string
	LeaseToken            string
	LeaseExpiresAt        string
	HeartbeatAt           string
	OwnerGeneration       int64
	SupersededByRunID     string
	SideEffectClassesJSON string
	PredecessorRunID      string
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
		SELECT id, instance_id, COALESCE(semantic_action_key,''), COALESCE(action,''), role, COALESCE(attempt,1),
		       status, COALESCE(agent_ref, principal_ref, actor, ''), COALESCE(scope_ref,''), COALESCE(handler_contract,''),
		       COALESCE(handler_id,''), COALESCE(handler_version,''), COALESCE(external_run_ref,''), COALESCE(workspace_ref,''),
		       COALESCE(source_run_id,''), COALESCE(source_evidence_id,''), COALESCE(source_identity,''),
		       started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,''), COALESCE(owner_generation,0),
		       COALESCE(superseded_by_run_id,''), COALESCE(side_effect_classes_json,'[]'), COALESCE(predecessor_run_id,'')
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
		SELECT id, instance_id, COALESCE(semantic_action_key,''), COALESCE(action,''), role, COALESCE(attempt,1),
		       status, COALESCE(agent_ref, principal_ref, actor, ''), COALESCE(scope_ref,''), COALESCE(handler_contract,''),
		       COALESCE(handler_id,''), COALESCE(handler_version,''), COALESCE(external_run_ref,''), COALESCE(workspace_ref,''),
		       COALESCE(source_run_id,''), COALESCE(source_evidence_id,''), COALESCE(source_identity,''),
		       started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,''), COALESCE(owner_generation,0),
		       COALESCE(superseded_by_run_id,''), COALESCE(side_effect_classes_json,'[]'), COALESCE(predecessor_run_id,'')
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

func scanClaimedRun(scanner runRowScanner) (*claimedRun, error) {
	var r claimedRun
	err := scanner.Scan(
		&r.ID, &r.InstanceID, &r.SemanticActionKey, &r.Action, &r.Role, &r.Attempt,
		&r.Status, &r.AgentRef, &r.ScopeRef, &r.HandlerContract, &r.HandlerID, &r.HandlerVersion,
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
		HandlerID:         run.HandlerID,
		HandlerVersion:    run.HandlerVersion,
		ExternalRunRef:    run.ExternalRunRef,
		WorkspaceRef:      run.WorkspaceRef,
		Source:            claimedRunSource(run),
		StartedAt:         run.StartedAt,
		CompletedAt:       run.CompletedAt,
		TerminalSummary:   run.TerminalSummary,
		PredecessorRunID:  run.PredecessorRunID,
	}
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

func settledEvidenceForRunTx(tx *sql.Tx, runID, kind string) (*Evidence, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence
		WHERE run_id = ? AND kind = ?
		ORDER BY produced_at, id
	`, runID, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanEvidenceRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
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
	source, err := evidenceByIDTx(tx, run.SourceEvidenceID)
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

func evidenceByIDTx(tx *sql.Tx, id string) (*Evidence, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items, err := scanEvidenceRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("evidence not found: %s", id)
	}
	return &items[0], nil
}

func firstNonEmptyAction(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) addActionEvidenceTx(tx *sql.Tx, inst *Instance, tpl *Template, params AddEvidenceParams) (*Evidence, error) {
	policy := ResolveWorkflowPolicy(tpl)
	var kindSpec *KindSpec
	if spec, ok := tpl.EvidenceKinds[params.Kind]; ok {
		kindSpec = &spec
	}
	if err := validateProducibleBy(params.Kind, kindSpec, params.Role); err != nil {
		return nil, err
	}
	facts, err := parseAndValidateEvidenceFacts(params.Kind, params.Facts, kindSpec)
	if err != nil {
		return nil, err
	}
	if err := policy.ValidateEvidence(params, facts); err != nil {
		return nil, err
	}
	var dataArg interface{}
	var dataRaw json.RawMessage
	if strings.TrimSpace(params.Data) != "" {
		if !json.Valid([]byte(params.Data)) {
			return nil, validationError("data", "data must be valid JSON"+jsonLocationSuffix(json.Unmarshal([]byte(params.Data), new(json.RawMessage))), "valid JSON", nil, "fix the JSON syntax in --data")
		}
		dataArg = params.Data
		dataRaw = json.RawMessage(params.Data)
	}
	var factsRaw json.RawMessage
	if facts != nil {
		factsRaw = facts.Raw
	}
	requestHash := ""
	if params.IdempotencyKey != "" {
		requestHash = evidenceAddRequestHash(params, factsRaw, dataRaw)
		replayed, err := replayEvidenceResult(tx, inst.ID, params.IdempotencyKey, requestHash)
		if err != nil {
			return nil, err
		}
		if replayed != nil {
			return replayed, nil
		}
	}
	task, err := loadTaskDoc(tx, inst.TaskUUID)
	if err != nil {
		return nil, err
	}
	if kindSpec != nil && len(kindSpec.LinkageRefs) > 0 {
		existing, err := listEvidenceTx(tx, inst.ID)
		if err != nil {
			return nil, err
		}
		if err := validateLinkageRefs(existing, kindSpec, dataRaw); err != nil {
			return nil, err
		}
	}
	id, err := nextSeqID(tx, "workflow_evidence_seq", "ev")
	if err != nil {
		return nil, err
	}
	taskHashAtProduction := taskDocHash(task)
	source := map[string]interface{}{"type": "external_ref", "ref": params.Ref, "taskHashAtProduction": taskHashAtProduction}
	if len(dataRaw) > 0 {
		source["dataHash"] = Hash(dataRaw)
	}
	if strings.TrimSpace(params.ContentHash) != "" {
		source["contentHash"] = strings.TrimSpace(params.ContentHash)
	}
	sourceJSON, _ := json.Marshal(source)
	var factsArg interface{}
	if facts != nil {
		factsArg = string(facts.Raw)
	}
	now := s.now().UTC().Format(time.RFC3339)
	_, err = tx.Exec(`
		INSERT INTO workflow_evidence (id, instance_id, kind, ref, summary, facts_json, data_json, source_json, actor, principal_ref, role, run_id, task_etag_at_production, task_hash_at_production, produced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, inst.ID, params.Kind, params.Ref, nullIfEmpty(params.Summary), factsArg, dataArg, string(sourceJSON), emptyToNil(params.PrincipalRef), emptyToNil(params.PrincipalRef), emptyToNil(params.Role), emptyToNil(params.RunID), fmt.Sprint(task.ETag), taskHashAtProduction, now)
	if err != nil {
		return nil, err
	}
	ev := &Evidence{ID: id, InstanceID: inst.ID, Kind: params.Kind, Ref: params.Ref, Summary: params.Summary, Facts: factsRaw, Data: dataRaw, Source: sourceJSON, PrincipalRef: params.PrincipalRef, Role: params.Role, RunID: params.RunID, ContentHash: strings.TrimSpace(params.ContentHash), TaskEtagAtProduction: fmt.Sprint(task.ETag), TaskHashAtProduction: taskHashAtProduction, ProducedAt: now}
	if err := policy.OnEvidenceAdded(tx, inst, ev); err != nil {
		return nil, err
	}
	if params.IdempotencyKey != "" {
		if err := storeEvidenceResult(tx, inst.ID, params.IdempotencyKey, requestHash, ev); err != nil {
			return nil, err
		}
	}
	return ev, nil
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
	ev, err := listEvidenceTx(tx, inst.ID)
	if err != nil {
		return nil, err
	}
	obl, err := listObligationsTx(tx, inst.ID, true)
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

	// Suspended-write gate (door 2 of 3). A suspended instance rejects the
	// write; reads and inspection are unaffected. This is the entire fencing
	// story — a pre-park worker's settle bounces here.
	if inst.Suspension != nil {
		return nil, suspendedWriteError(inst)
	}

	eventID, err := nextSeqID(tx, "workflow_event_seq", "wfe")
	if err != nil {
		return nil, err
	}
	nextRevision := inst.Revision + 1
	now := s.now().UTC().Format(time.RFC3339)
	updated := *inst
	if chosen.To != nil {
		updated.Status = chosen.To.Status
		updated.Phase = chosen.To.Phase
		updated.Outcome = chosen.To.Outcome
	} else {
		if err := applySuspendOutcomeTx(tx, &updated, chosen.Suspend, eventID, now); err != nil {
			return nil, err
		}
	}
	updated.Revision = nextRevision
	updated.UpdatedAt = now
	if updated.Status == "closed" {
		updated.ClosedAt = now
	} else {
		updated.ClosedAt = ""
	}
	updated.TaskDocEtag = fmt.Sprint(task.ETag)
	updated.TaskDocHash = taskDocHash(task)
	res, err := tx.Exec(`
		UPDATE workflow_instances
		SET status = ?, phase = ?, outcome = ?, revision = ?, task_doc_etag = ?, task_doc_hash = ?,
		    updated_at = ?, closed_at = ?, suspension_id = ?, suspension_reason = ?, suspension_at = ?, suspension_cause_ref = ?
		WHERE id = ? AND revision = ?
	`, updated.Status, nullIfEmpty(updated.Phase), nullIfEmpty(updated.Outcome), updated.Revision, updated.TaskDocEtag, updated.TaskDocHash,
		updated.UpdatedAt, nullIfEmpty(updated.ClosedAt), suspensionID(updated.Suspension), suspensionReason(updated.Suspension), suspensionAt(updated.Suspension), suspensionCauseRef(updated.Suspension), updated.ID, inst.Revision)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		actual, loadErr := instanceRevisionTx(tx, updated.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		return nil, staleRevisionError(updated.ID, inst.Revision, actual.revision)
	}

	createdObligations := make([]Obligation, 0, len(chosen.Obligations))
	for _, ob := range chosen.Obligations {
		id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
		if err != nil {
			return nil, err
		}
		blocking := 0
		if ob.Blocking {
			blocking = 1
		}
		noSelfWaive := true
		if ob.NoSelfWaive != nil {
			noSelfWaive = *ob.NoSelfWaive
		}
		noSelfWaiveInt := 0
		if noSelfWaive {
			noSelfWaiveInt = 1
		}
		obligeeRole := strings.TrimSpace(ob.ObligeeRole)
		if obligeeRole == "" {
			obligeeRole = "workflow"
		}
		waiveRole := strings.TrimSpace(ob.WaiveRole)
		if waiveRole == "" && strings.TrimSpace(ob.WaivePrincipalRef) == "" {
			waiveRole = "system"
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_obligations (
				id, instance_id, kind, owner_role, owner_actor, owner_principal_ref, obligee_role, obligee_actor, obligee_principal_ref,
				waive_role, waive_actor, waive_principal_ref, no_self_waive, blocking, status, reason, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?)
		`, id, updated.ID, ob.Kind, nullIfEmpty(ob.OwnerRole), nullIfEmpty(ob.OwnerPrincipalRef), nullIfEmpty(ob.OwnerPrincipalRef), nullIfEmpty(obligeeRole), nullIfEmpty(ob.ObligeePrincipalRef), nullIfEmpty(ob.ObligeePrincipalRef), nullIfEmpty(waiveRole), nullIfEmpty(ob.WaivePrincipalRef), nullIfEmpty(ob.WaivePrincipalRef), noSelfWaiveInt, blocking, nullIfEmpty(ob.Reason), now, now)
		if err != nil {
			return nil, err
		}
		createdObligations = append(createdObligations, Obligation{
			ID: id, InstanceID: updated.ID, Kind: ob.Kind, OwnerRole: ob.OwnerRole, OwnerPrincipalRef: ob.OwnerPrincipalRef,
			ObligeeRole: obligeeRole, ObligeePrincipalRef: ob.ObligeePrincipalRef, WaiveRole: waiveRole, WaivePrincipalRef: ob.WaivePrincipalRef,
			NoSelfWaive: noSelfWaive, Blocking: ob.Blocking, Status: "open", Reason: ob.Reason, CreatedAt: now, UpdatedAt: now,
		})
	}
	createdEffects := make([]Effect, 0, len(chosen.Effects))
	for _, ef := range chosen.Effects {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return nil, err
		}
		seq, err := nextEffectSequenceTx(tx, updated.ID)
		if err != nil {
			return nil, err
		}
		renderedEffect, semanticKey, err := renderEffectSpec(ef, effectRenderContext{
			instance: updated, outcomeID: chosen.ID, runID: runID, sequence: seq,
		})
		if err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(renderedEffect)
		effectKey := fmt.Sprintf("%s:%s", updated.ID, semanticKey)
		_, err = tx.Exec(`
			INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)
		`, id, updated.ID, updated.Revision, seq, renderedEffect.Kind, string(payload), effectKey, semanticKey, now, now)
		if err != nil {
			return nil, err
		}
		createdEffects = append(createdEffects, Effect{
			ID: id, InstanceID: updated.ID, Revision: updated.Revision, Sequence: seq, Kind: renderedEffect.Kind, Payload: json.RawMessage(payload),
			Status: "pending", IdempotencyKey: effectKey, SemanticKey: semanticKey, CreatedAt: now, UpdatedAt: now,
		})
	}
	result := transitionResultMap(inst.TaskRef, updated, eventID, createdEffects, createdObligations)
	result["transition"] = transitionID
	result["outcome"] = chosen.ID
	result["idempotent"] = false
	resultJSON, _ := json.Marshal(result)
	eventPayload := map[string]interface{}{"transition": transitionID, "outcome": chosen.ID, "from": inst.State(), "to": updated.State()}
	eventType := "workflow.transitioned"
	if chosen.Suspend != nil {
		eventType = "workflow.suspended"
		eventPayload["suspension"] = updated.Suspension
		eventPayload["beforeRevision"] = inst.Revision
		eventPayload["afterRevision"] = updated.Revision
	}
	if _, err := insertWorkflowMutationEventWithID(tx, eventType, eventID, updated.ID, actor, role, runID, inst.Revision, updated.Revision, key, requestHash, string(resultJSON), task.ETag, updated.TaskDocHash, eventPayload); err != nil {
		return nil, err
	}
	if chosen.Suspend == nil {
		if err := updateTaskWorkflowMeta(tx, updated.TaskUUID, updated, actor); err != nil {
			return nil, err
		}
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
			// Re-resolve the instance immediately before applying so the
			// transition runs against current state. We rely on transition.apply's
			// own invariants rather than supplying a possibly-stale context hash.
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
	query := `
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs
		WHERE instance_id IN (` + strings.Join(placeholders, ",") + `) AND action IS NOT NULL`
	if status := strings.TrimSpace(p.Status); status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if action := strings.TrimSpace(p.Action); action != "" {
		query += " AND action = ?"
		args = append(args, action)
	}
	query += " ORDER BY started_at, id LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ActionRun{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt); err != nil {
			return nil, err
		}
		ar, err := s.toActionRun(&r, instByID[r.InstanceID])
		if err != nil {
			return nil, err
		}
		out = append(out, *ar)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

// ── helpers ──────────────────────────────────────────────────────────────────

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

type actionRunViewOptions struct {
	includeLeaseToken bool
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
