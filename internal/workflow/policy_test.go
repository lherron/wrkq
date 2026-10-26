//go:build wrkq_local

package workflow

import (
	"database/sql"
	"strings"
	"testing"
)

type recordingWorkflowPolicy struct {
	validateCalls    int
	addedEvidenceIDs []string
	projectCalls     []bool
	validateErr      error
	insertObligation bool
}

func installWorkflowPolicyForTest(templateID, version string, policy WorkflowPolicy) func() {
	id := strings.TrimSpace(templateID)
	ver := strings.TrimSpace(version)
	if policy == nil {
		policy = defaultWorkflowPolicy{}
	}

	workflowPolicyRegistry.Lock()
	defer workflowPolicyRegistry.Unlock()
	if ver == "" {
		previous, hadPrevious := workflowPolicyRegistry.fallback[id]
		workflowPolicyRegistry.fallback[id] = policy
		return func() {
			workflowPolicyRegistry.Lock()
			defer workflowPolicyRegistry.Unlock()
			if hadPrevious {
				workflowPolicyRegistry.fallback[id] = previous
			} else {
				delete(workflowPolicyRegistry.fallback, id)
			}
		}
	}

	key := workflowPolicyKey{templateID: id, version: ver}
	previous, hadPrevious := workflowPolicyRegistry.exact[key]
	workflowPolicyRegistry.exact[key] = policy
	return func() {
		workflowPolicyRegistry.Lock()
		defer workflowPolicyRegistry.Unlock()
		if hadPrevious {
			workflowPolicyRegistry.exact[key] = previous
		} else {
			delete(workflowPolicyRegistry.exact, key)
		}
	}
}

func (p *recordingWorkflowPolicy) ValidateEvidence(AddEvidenceParams, *parsedEvidenceFacts) error {
	p.validateCalls++
	return p.validateErr
}

func (p *recordingWorkflowPolicy) OnEvidenceAdded(tx *sql.Tx, inst *Instance, ev *Evidence) error {
	p.addedEvidenceIDs = append(p.addedEvidenceIDs, ev.ID)
	if !p.insertObligation {
		return nil
	}
	id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
		VALUES (?, ?, 'policy_side_effect', 'agent', 0, 'open', 'created by policy hook')
	`, id, inst.ID)
	return err
}

func (p *recordingWorkflowPolicy) ProjectObligations(_ *Service, inst *Instance, obligations []Obligation, _ []Evidence, includeClosed bool) []Obligation {
	p.projectCalls = append(p.projectCalls, includeClosed)
	obligations = append(obligations, Obligation{
		ID:         "policy_projected_open",
		InstanceID: inst.ID,
		Kind:       "policy_projected",
		OwnerRole:  "agent",
		Status:     "open",
	})
	if includeClosed {
		obligations = append(obligations, Obligation{
			ID:         "policy_projected_satisfied",
			InstanceID: inst.ID,
			Kind:       "policy_projected",
			OwnerRole:  "agent",
			Status:     "satisfied",
		})
	}
	return obligations
}

func TestWorkflowPolicyRegistryResolution(t *testing.T) {
	if _, ok := ResolveWorkflowPolicy(nil).(defaultWorkflowPolicy); !ok {
		t.Fatalf("nil template should resolve default policy")
	}
	if _, ok := ResolveWorkflowPolicy(&Template{ID: "unregistered", Version: "1"}).(defaultWorkflowPolicy); !ok {
		t.Fatalf("unregistered template should resolve default policy")
	}
	if _, ok := ResolveWorkflowPolicy(&Template{ID: "wrkq-simple-task", Version: "3"}).(simpleTaskWorkflowPolicy); !ok {
		t.Fatalf("built-in simple-task v3 should resolve exact simple-task policy")
	}
	if _, ok := ResolveWorkflowPolicy(&Template{ID: "wrkq-simple-task", Version: "99"}).(defaultWorkflowPolicy); !ok {
		t.Fatalf("unregistered simple-task version should not resolve by implicit ID fallback")
	}

	exact := &recordingWorkflowPolicy{}
	cleanupExact := installWorkflowPolicyForTest("policy-test", "1", exact)
	defer cleanupExact()
	fallback := &recordingWorkflowPolicy{}
	cleanupFallback := installWorkflowPolicyForTest("policy-fallback-test", "", fallback)
	defer cleanupFallback()

	if got := ResolveWorkflowPolicy(&Template{ID: "policy-test", Version: "1"}); got != exact {
		t.Fatalf("exact policy = %T, want registered exact policy", got)
	}
	if _, ok := ResolveWorkflowPolicy(&Template{ID: "policy-test", Version: "2"}).(defaultWorkflowPolicy); !ok {
		t.Fatalf("unregistered version should use default when no explicit fallback exists")
	}
	if got := ResolveWorkflowPolicy(&Template{ID: "policy-fallback-test", Version: "2"}); got != fallback {
		t.Fatalf("fallback policy = %T, want registered ID fallback", got)
	}
}

func TestAddEvidenceDispatchesPolicyValidationSideEffectsAndIdempotency(t *testing.T) {
	svc, taskUUID := setupDirectEvidenceFixture(t)
	policy := &recordingWorkflowPolicy{insertObligation: true}
	cleanup := installWorkflowPolicyForTest("direct_evidence_test", "1", policy)
	defer cleanup()

	first, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector:   taskUUID,
		Kind:           "behavior_note",
		Ref:            "urn:policy:note",
		PrincipalRef:   "agent:policy",
		Role:           "agent",
		IdempotencyKey: "policy-replay",
	})
	if err != nil {
		t.Fatalf("AddEvidence first: %v", err)
	}
	replayed, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector:   taskUUID,
		Kind:           "behavior_note",
		Ref:            "urn:policy:note",
		PrincipalRef:   "agent:policy",
		Role:           "agent",
		IdempotencyKey: "policy-replay",
	})
	if err != nil {
		t.Fatalf("AddEvidence replay: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replayed evidence ID = %s, want %s", replayed.ID, first.ID)
	}
	if policy.validateCalls != 2 {
		t.Fatalf("ValidateEvidence calls = %d, want validation before insert and replay", policy.validateCalls)
	}
	if len(policy.addedEvidenceIDs) != 1 || policy.addedEvidenceIDs[0] != first.ID {
		t.Fatalf("OnEvidenceAdded calls = %v, want exactly first inserted evidence", policy.addedEvidenceIDs)
	}

	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(1) FROM workflow_obligations WHERE kind = 'policy_side_effect'`).Scan(&count); err != nil {
		t.Fatalf("count side-effect obligations: %v", err)
	}
	if count != 1 {
		t.Fatalf("side-effect obligations = %d, want 1", count)
	}
}

func TestListObligationsDispatchesPolicyProjectionAndIncludeClosed(t *testing.T) {
	svc, taskUUID := setupDirectEvidenceFixture(t)
	policy := &recordingWorkflowPolicy{}
	cleanup := installWorkflowPolicyForTest("direct_evidence_test", "1", policy)
	defer cleanup()

	openOnly, err := svc.ListObligations(taskUUID, false)
	if err != nil {
		t.Fatalf("ListObligations open: %v", err)
	}
	if len(openOnly) != 1 || openOnly[0].ID != "policy_projected_open" {
		t.Fatalf("open obligations = %+v, want only projected open obligation", openOnly)
	}

	withClosed, err := svc.ListObligations(taskUUID, true)
	if err != nil {
		t.Fatalf("ListObligations includeClosed: %v", err)
	}
	if len(withClosed) != 2 || withClosed[0].ID != "policy_projected_open" || withClosed[1].ID != "policy_projected_satisfied" {
		t.Fatalf("includeClosed obligations = %+v, want open and satisfied projections", withClosed)
	}
	if len(policy.projectCalls) != 2 || policy.projectCalls[0] || !policy.projectCalls[1] {
		t.Fatalf("ProjectObligations includeClosed calls = %v, want [false true]", policy.projectCalls)
	}
}
