package workflow

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/db"
)

type TransitionDecisionInput struct {
	Instance           *Instance
	Template           *Template
	Transition         TransitionSpec
	Task               *taskDoc
	Evidence           []Evidence
	Obligations        []Obligation
	Checks             map[string]CheckRun
	Facts              map[string]interface{}
	Role               string
	PrincipalRef       string
	RoleQuery          queryer
	DependencyQuery    rowsQueryer
	CheckDatabase      *db.DB
	RequireRoleBinding bool
}

type TransitionDecision struct {
	Legal         bool
	Outcome       *OutcomeCase
	ExpectedState *State
	Blockers      []Blocker
	FollowUps     []TransitionFollowUp
}

type TransitionFollowUp struct {
	Kind    string
	Ref     string
	CheckID string
	Blocker Blocker
}

func (s *Service) EvaluateTransitionDecision(input TransitionDecisionInput) (TransitionDecision, error) {
	decision := TransitionDecision{Blockers: []Blocker{}, FollowUps: []TransitionFollowUp{}}
	if input.Instance == nil {
		return decision, fmt.Errorf("workflow instance is required")
	}
	if input.Template == nil {
		return decision, fmt.Errorf("workflow template is required")
	}
	tr := input.Transition
	if !stateMatches(*input.Instance, tr.From) {
		decision.Blockers = []Blocker{{Kind: "state", Ref: tr.ID, Message: fmt.Sprintf("transition %s cannot run from current state", tr.ID)}}
		return decision, nil
	}
	if !roleAllowed(input.Role, tr.By) {
		decision.Blockers = []Blocker{{Kind: "role", Ref: input.Role, Message: "role is not allowed for transition"}}
		return decision, nil
	}
	if input.RequireRoleBinding {
		if input.RoleQuery == nil {
			return decision, fmt.Errorf("role binding query is required")
		}
		if !roleBindingAllowed(input.RoleQuery, input.Instance, input.Template, input.Role, input.PrincipalRef) {
			decision.Blockers = []Blocker{{Kind: "role_binding", Ref: input.Role, Message: "role is not allowed for transition"}}
			return decision, nil
		}
	}

	blockers := transitionBlockers(tr, input.Evidence, input.Obligations, taskDocHashOrEmpty(input.Task))
	if len(blockers) > 0 {
		decision.Blockers = blockers
		decision.FollowUps = transitionFollowUps(blockers)
		return decision, nil
	}

	facts := input.Facts
	if facts == nil {
		facts = taskFacts(input.Task)
	}
	checks := input.Checks
	if checks == nil {
		checks = map[string]CheckRun{}
	}
	blockers = checkCommitBlockers(input.Template, tr, input.Evidence, facts, input.Instance, input.Task, input.Evidence, input.Obligations, checks, input.PrincipalRef, input.Role, input.CheckDatabase)
	if len(blockers) > 0 {
		decision.Blockers = blockers
		decision.FollowUps = transitionFollowUps(blockers)
		return decision, nil
	}
	if blockers := separationOfDutyBlockers(tr, input.Evidence, input.PrincipalRef); len(blockers) > 0 {
		decision.Blockers = blockers
		return decision, nil
	}

	ctx := evalContext{Evidence: input.Evidence, Obligations: input.Obligations, Checks: checks, Facts: input.Facts, Task: input.Task, State: input.Instance.State()}
	for _, guard := range tr.Guards {
		if !evalPredicate(guard, ctx) {
			decision.Blockers = []Blocker{{Kind: "guard", Message: "transition guard failed"}}
			return decision, nil
		}
	}

	for i := range tr.Outcomes {
		out := &tr.Outcomes[i]
		if evalPredicate(out.When, ctx) {
			decision.Outcome = out
			decision.ExpectedState = &out.To
			break
		}
	}
	if decision.Outcome == nil {
		decision.Blockers = []Blocker{{Kind: "outcome", Message: "no transition outcome matched"}}
		return decision, nil
	}

	if decision.Outcome.To.Status == "closed" && input.DependencyQuery != nil {
		depBlockers, err := taskRelationBlockers(input.DependencyQuery, input.Instance.TaskUUID)
		if err != nil {
			return decision, err
		}
		if len(depBlockers) > 0 {
			decision.Blockers = depBlockers
			return decision, nil
		}
	}
	if blockers := postconditionBlockers(input.Instance, tr, decision.Outcome, input.Evidence, input.Obligations, checks, input.Task); len(blockers) > 0 {
		decision.Blockers = blockers
		return decision, nil
	}
	decision.Legal = true
	return decision, nil
}

func transitionFollowUps(blockers []Blocker) []TransitionFollowUp {
	var out []TransitionFollowUp
	addedEvidence := map[string]bool{}
	for _, b := range blockers {
		switch b.Kind {
		case "evidence":
			out = append(out, TransitionFollowUp{Kind: "collect_evidence", Ref: b.Ref, Blocker: b})
		case "obligation":
			out = append(out, TransitionFollowUp{Kind: "satisfy_obligation", Ref: b.Ref, Blocker: b})
		case "check_input":
			if strings.HasPrefix(b.Ref, "evidence:") {
				kind := strings.TrimPrefix(b.Ref, "evidence:")
				if addedEvidence[kind] {
					continue
				}
				addedEvidence[kind] = true
				out = append(out, TransitionFollowUp{Kind: "collect_check_evidence", Ref: kind, Blocker: b})
			}
		case "check", "stale_check":
			checkID := strings.TrimPrefix(b.Ref, "check:")
			out = append(out, TransitionFollowUp{Kind: "run_check", Ref: b.Ref, CheckID: checkID, Blocker: b})
		}
	}
	return out
}

func transitionDecisionError(instanceID, transitionID, role string, decision TransitionDecision) error {
	if len(decision.Blockers) == 0 {
		return transitionBlockedError(instanceID, transitionID, nil)
	}
	first := decision.Blockers[0]
	switch first.Kind {
	case "state":
		return fmt.Errorf("%s", first.Message)
	case "role", "role_binding":
		return roleDeniedError(instanceID, transitionID, role)
	default:
		return transitionBlockedError(instanceID, transitionID, decision.Blockers)
	}
}
