//go:build wrkq_local

package workflow

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func findTransition(tpl *Template, id string) (*TransitionSpec, error) {
	for i := range tpl.Transitions {
		if tpl.Transitions[i].ID == id {
			return &tpl.Transitions[i], nil
		}
	}
	return nil, fmt.Errorf("transition not found: %s", id)
}

func roleAllowed(role string, by []string) bool {
	role = strings.TrimSpace(role)
	if role == "" {
		return false
	}
	for _, b := range by {
		if b == role {
			return true
		}
	}
	return false
}

func roleBindingAllowed(q queryer, inst *Instance, tpl *Template, role, actor string) bool {
	role = strings.TrimSpace(role)
	actor = strings.TrimSpace(actor)
	if role == "" || actor == "" {
		return false
	}
	if role == "system" || role == "supervisor" {
		return true
	}
	if tpl != nil {
		if spec, ok := tpl.Roles[role]; ok && len(spec.Principals) > 0 {
			for _, allowed := range spec.Principals {
				if allowed == "*" || allowed == actor {
					return true
				}
			}
			return false
		}
	}
	if inst == nil {
		return false
	}
	var count int
	if err := q.QueryRow(`SELECT COUNT(*) FROM workflow_role_bindings WHERE instance_id = ? AND role = ?`, inst.ID, role).Scan(&count); err != nil {
		return false
	}
	if count == 0 {

		return true
	}
	var matched int
	if err := q.QueryRow(`SELECT COUNT(*) FROM workflow_role_bindings WHERE instance_id = ? AND role = ? AND principal_ref = ?`, inst.ID, role, actor).Scan(&matched); err != nil {
		return false
	}
	return matched > 0
}

func (s *Service) transitionOwners(inst *Instance, tpl *Template, tr TransitionSpec, requestedRole string) ([]ActionOwner, []Blocker) {
	roles, blockers := transitionCandidateRoles(tr, requestedRole)
	if len(blockers) > 0 {
		return nil, blockers
	}
	var owners []ActionOwner
	var bindingBlockers []Blocker
	seen := map[string]bool{}
	for _, role := range roles {
		roleOwners, err := s.ownersForRole(inst, tpl, role)
		if err != nil {
			bindingBlockers = append(bindingBlockers, Blocker{Kind: "role_binding", Ref: role, Message: err.Error()})
			continue
		}
		if len(roleOwners) == 0 {
			bindingBlockers = append(bindingBlockers, Blocker{Kind: "role_binding", Ref: role, Message: fmt.Sprintf("role %s has no eligible bound principals", role)})
			continue
		}
		for _, owner := range roleOwners {
			key := owner.Role + "\x00" + owner.PrincipalRef + "\x00" + owner.DeliveryRef + "\x00" + owner.Lane
			if seen[key] {
				continue
			}
			seen[key] = true
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 && len(bindingBlockers) > 0 {
		return nil, bindingBlockers
	}
	return owners, nil
}

func transitionCandidateRoles(tr TransitionSpec, requestedRole string) ([]string, []Blocker) {
	requestedRole = strings.TrimSpace(requestedRole)
	if requestedRole != "" {
		if !roleAllowed(requestedRole, tr.By) {
			return nil, []Blocker{{Kind: "role", Ref: requestedRole, Message: "role is not allowed for transition"}}
		}
		return []string{requestedRole}, nil
	}
	roles := make([]string, 0, len(tr.By))
	seen := map[string]bool{}
	add := func(role string) {
		role = strings.TrimSpace(role)
		if role == "" || seen[role] || !roleAllowed(role, tr.By) {
			return
		}
		seen[role] = true
		roles = append(roles, role)
	}
	if tr.Responsibility != nil {
		add(tr.Responsibility.Role)
	}
	for _, role := range tr.By {
		add(role)
	}
	return roles, nil
}

func (s *Service) ownersForRole(inst *Instance, tpl *Template, role string) ([]ActionOwner, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	if role == "system" || role == "supervisor" {
		return []ActionOwner{{Role: role}}, nil
	}
	if tpl != nil {
		if spec, ok := tpl.Roles[role]; ok && len(spec.Principals) > 0 {
			owners := make([]ActionOwner, 0, len(spec.Principals))
			for _, actor := range spec.Principals {
				actor = strings.TrimSpace(actor)
				if actor == "" {
					continue
				}
				owner := ActionOwner{Role: role}
				if actor != "*" {
					owner.PrincipalRef = actor
				}
				owners = append(owners, owner)
			}
			return owners, nil
		}
	}
	if inst == nil {
		return nil, fmt.Errorf("workflow instance is required")
	}
	rows, err := s.db.Query(`
		SELECT COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,'')
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ?
		ORDER BY bound_at DESC, principal_ref
	`, inst.ID, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var owners []ActionOwner
	for rows.Next() {
		var owner ActionOwner
		owner.Role = role
		if err := rows.Scan(&owner.PrincipalRef, &owner.DeliveryRef, &owner.Lane); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(owners) == 0 {

		return []ActionOwner{{Role: role}}, nil
	}
	return owners, nil
}

func transitionActionID(transitionID string, owner ActionOwner, ownerCount int) string {
	id := "transition_" + transitionID
	if ownerCount <= 1 {
		return id
	}
	suffix := owner.Role
	if owner.PrincipalRef != "" {
		suffix += "_" + owner.PrincipalRef
	}
	return id + "_" + sanitizeActionID(suffix)
}

func sanitizeActionID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func transitionCommand(taskRef, transitionID string, owner ActionOwner, revision int64) string {
	cmd := fmt.Sprintf("wrkf transition %s %s --role %s", strings.TrimPrefix(taskRef, "wrkq:"), transitionID, owner.Role)
	if owner.PrincipalRef != "" {
		cmd += fmt.Sprintf(" --principal-ref %s", owner.PrincipalRef)
	}
	cmd += fmt.Sprintf(" --expect-revision %d", revision)
	return cmd
}

func evalPredicate(p Predicate, ctx evalContext) bool {
	if p.Always != nil {
		return *p.Always
	}
	if p.Otherwise != nil {
		return *p.Otherwise
	}
	if len(p.All) > 0 {
		for _, child := range p.All {
			if !evalPredicate(child, ctx) {
				return false
			}
		}
		return true
	}
	if len(p.Any) > 0 {
		for _, child := range p.Any {
			if evalPredicate(child, ctx) {
				return true
			}
		}
		return false
	}
	if p.Not != nil {
		return !evalPredicate(*p.Not, ctx)
	}
	if p.EvidenceExists != nil {
		return matchEvidenceRequirement(ctx.Evidence, EvidenceRequirementSpec{Kind: p.EvidenceExists.Kind, Facts: p.EvidenceExists.Facts}).OK
	}
	if p.ObligationStatus != nil {
		for _, o := range ctx.Obligations {
			if p.ObligationStatus.ID != "" && o.ID != p.ObligationStatus.ID {
				continue
			}
			if p.ObligationStatus.Kind != "" && o.Kind != p.ObligationStatus.Kind {
				continue
			}
			if o.Status == p.ObligationStatus.Is {
				return true
			}
		}
		return false
	}
	if p.CheckVerdict != nil {
		c, ok := ctx.Checks[p.CheckVerdict.Check]
		return ok && c.Verdict == p.CheckVerdict.Is
	}
	if p.CheckOutcome != nil {
		c, ok := ctx.Checks[p.CheckOutcome.Check]
		return ok && c.Outcome == p.CheckOutcome.Is
	}
	if p.FactEquals != nil {
		return reflect.DeepEqual(resolveFact(ctx, p.FactEquals.Path), p.FactEquals.Value)
	}
	return false
}

func resolveFact(ctx evalContext, path string) interface{} {
	if ctx.Task != nil {
		switch path {
		case "task.state":
			return ctx.Task.State
		case "task.id":
			return ctx.Task.ID
		case "task.has_specification":

			return strings.TrimSpace(ctx.Task.Specification) != ""
		}
	}
	switch path {
	case "workflow.status":
		return ctx.State.Status
	case "workflow.phase":
		return ctx.State.Phase
	case "workflow.outcome":
		return ctx.State.Outcome
	}
	if ctx.Facts != nil {
		return ctx.Facts[path]
	}
	return nil
}

func (s *Service) Next(taskSelector, role string) (*NextActionResponse, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	if err := s.EnsureInstanceBuiltinTemplate(inst, ""); err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	ev, _ := s.ListEvidence(taskSelector, "")
	obl, _ := s.ListObligations(taskSelector, true)
	eff, _ := s.ListEffects(taskSelector, false)
	openObl := filterOpenObligations(obl)
	pendingEff := filterPendingEffects(eff)
	resp := &NextActionResponse{Actions: []NextAction{}, BlockedTransitions: []BlockedTransition{}, OpenObligations: openObl, PendingEffects: pendingEff}
	resp.Instance.ID = inst.ID
	resp.Instance.TaskRef = inst.TaskRef
	resp.Instance.Template.ID = inst.TemplateID
	resp.Instance.Template.Version = inst.TemplateVersion
	resp.Instance.Template.Hash = inst.TemplateHash
	resp.Instance.State = inst.State()
	resp.Instance.Revision = inst.Revision
	resp.Instance.TaskDoc.Etag = inst.TaskDocEtag
	resp.Instance.TaskDoc.Hash = inst.TaskDocHash
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	if task != nil {
		resp.Instance.Stale = taskDocHash(task) != inst.TaskDocHash
	}
	if inst.Status == "closed" {
		resp.Actions = []NextAction{}
		return resp, nil
	}
	for _, o := range openObl {
		switch o.Kind {
		case "await_subordinate_closure":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "await_" + o.ID,
				Kind:  "await_subordinate_closure",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  85,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"do not record closure_evidence for a subordinate until its wrkq task reaches a terminal state"},
					Warnings: []string{},
				},
			})
		case "await_coordinator_smoke_execution":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "execute_" + o.ID,
				Kind:  "execute_coordinator_smoke",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  86,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"execute the locked runbook with fresh artifacts before recording coordinator_smoke_execution"},
					Warnings: []string{},
				},
			})
		case "await_observer_completion_review":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "request_" + o.ID,
				Kind:  "request_observer_review",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "observer"},
				Rank:  92,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard: []string{
						"observer must be outside the coordinator loop",
						"observer must judge against the original task body, not only coordinator-authored criteria",
						"do not accept coordinator claims that requested functionality can be bypassed without an explicit human or supervisor override",
					},
					Warnings: []string{},
				},
			})
		case "address_observer_rejection":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "address_" + o.ID,
				Kind:  "address_observer_rejection",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  91,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"submit a revised completion_claim referencing the rejected claim and observer review"},
					Warnings: []string{},
				},
			})
		}
	}
	for _, e := range pendingEff {
		if e.Status != "pending" && e.Status != "failed" {
			continue
		}
		if e.Kind != "request_observer_review" {
			continue
		}
		role := effectRole(&e)
		if role == "" {
			continue
		}
		binding, bindErr := s.latestRunForRole(inst.ID, role)
		if bindErr != nil {
			resp.Actions = append(resp.Actions, NextAction{
				ID:         "bind_" + role,
				Kind:       "bind_role",
				Mode:       "deterministic",
				Owner:      ActionOwner{Role: "coordinator"},
				Rank:       95,
				Why:        fmt.Sprintf("effect %s requires a bound %s delivery handle", e.ID, role),
				Command:    fmt.Sprintf("wrkf run bind %s %s <agent@project:%s~%s>", strings.TrimPrefix(inst.TaskRef, "wrkq:"), role, strings.TrimPrefix(inst.TaskRef, "wrkq:"), role),
				Guardrails: Guardrails{Hard: []string{"bind role to a project/task-scoped handle before delivering the effect"}, Warnings: []string{}},
			})
			continue
		}
		resp.Actions = append(resp.Actions, NextAction{
			ID:         "deliver_" + e.ID,
			Kind:       "deliver_effect",
			Mode:       "deterministic",
			Owner:      ActionOwner{Role: "coordinator", PrincipalRef: binding.PrincipalRef, DeliveryRef: binding.DeliveryRef, Lane: binding.Lane},
			Rank:       94,
			Why:        fmt.Sprintf("deliver pending %s effect to %s", e.Kind, binding.DeliveryRef),
			Command:    fmt.Sprintf("wrkf effect deliver %s", e.ID),
			Guardrails: Guardrails{Hard: []string{"deliver through wrkf effect handler; do not hand-compose an out-of-band dispatch"}, Warnings: []string{}},
		})
	}
	for _, tr := range tpl.Transitions {
		if !transitionFromMatches(*inst, tr) {
			continue
		}
		owners, ownerBlockers := s.transitionOwners(inst, tpl, tr, role)
		ownerRole := ""
		if len(owners) > 0 {
			ownerRole = owners[0].Role
		} else if role != "" {
			ownerRole = role
		} else if tr.Responsibility != nil && tr.Responsibility.Role != "" {
			ownerRole = tr.Responsibility.Role
		} else if len(tr.By) > 0 {
			ownerRole = tr.By[0]
		}
		if len(ownerBlockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: ownerBlockers})
			continue
		}
		checks := map[string]CheckRun{}
		for _, checkID := range tr.Checks {
			if latest, ok := latestCheckFor(s.db, inst.ID, tr.ID, checkID); ok {
				checks[checkID] = latest
			}
		}
		decision, err := s.EvaluateTransitionDecision(TransitionDecisionInput{
			Instance:        inst,
			Template:        tpl,
			Transition:      tr,
			Task:            task,
			Evidence:        ev,
			Obligations:     obl,
			Checks:          checks,
			Facts:           taskFacts(task),
			Role:            ownerRole,
			DependencyQuery: s.db,
			CheckDatabase:   s.db,
		})
		if err != nil {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: []Blocker{{Kind: "task_dependency", Message: err.Error()}}})
			continue
		}
		if !decision.Legal {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: decision.Blockers})
			resp.Actions = append(resp.Actions, transitionFollowUpActions(inst, tr.ID, ownerRole, decision.FollowUps)...)
			continue
		}
		expected := *decision.ExpectedState
		for _, owner := range owners {
			ownerDecision, err := s.EvaluateTransitionDecision(TransitionDecisionInput{
				Instance:        inst,
				Template:        tpl,
				Transition:      tr,
				Task:            task,
				Evidence:        ev,
				Obligations:     obl,
				Checks:          checks,
				Facts:           taskFacts(task),
				Role:            owner.Role,
				PrincipalRef:    owner.PrincipalRef,
				DependencyQuery: s.db,
				CheckDatabase:   s.db,
			})
			if err != nil {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: owner.Role, BlocksOn: []Blocker{{Kind: "task_dependency", Message: err.Error()}}})
				continue
			}
			if !ownerDecision.Legal {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: owner.Role, BlocksOn: ownerDecision.Blockers})
				continue
			}
			resp.Actions = append(resp.Actions, NextAction{ID: transitionActionID(tr.ID, owner, len(owners)), Kind: "transition", Mode: "deterministic", Owner: owner, Rank: 100, Why: "transition is legal and prerequisites are satisfied", Command: transitionCommand(inst.TaskRef, tr.ID, owner, inst.Revision), ExpectedState: &expected, Guardrails: Guardrails{Hard: []string{"provide expected revision"}, Warnings: []string{}}})
		}
	}
	sort.SliceStable(resp.Actions, func(i, j int) bool { return resp.Actions[i].Rank > resp.Actions[j].Rank })
	return resp, nil
}

func transitionFollowUpActions(inst *Instance, transitionID, ownerRole string, followUps []TransitionFollowUp) []NextAction {
	var actions []NextAction
	for _, followUp := range followUps {
		b := followUp.Blocker
		switch followUp.Kind {
		case "collect_evidence":
			actions = append(actions, NextAction{ID: "collect_" + followUp.Ref, Kind: "collect_evidence", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 80, Why: b.Message, Unblocks: []string{transitionID}, Guardrails: Guardrails{Hard: []string{"reference source truth as evidence"}, Warnings: []string{}}})
		case "satisfy_obligation":
			actions = append(actions, NextAction{ID: "satisfy_" + followUp.Ref, Kind: "satisfy_obligation", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 50, Why: b.Message, Unblocks: []string{transitionID}, Guardrails: Guardrails{Hard: []string{"satisfy or waive the blocking obligation"}, Warnings: []string{}}})
		case "collect_check_evidence":
			actions = append(actions, NextAction{ID: "collect_" + followUp.Ref, Kind: "collect_evidence", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 80, Why: b.Message, Unblocks: []string{transitionID}, Guardrails: Guardrails{Hard: []string{"reference source truth as evidence before running the check"}, Warnings: []string{}}})
		case "run_check":
			actions = append(actions, NextAction{ID: "run_check_" + followUp.CheckID, Kind: "run_check", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 90, Why: b.Message, Unblocks: []string{transitionID}, Command: fmt.Sprintf("wrkf check run %s %s", strings.TrimPrefix(inst.TaskRef, "wrkq:"), transitionID), Guardrails: Guardrails{Hard: []string{"do not commit stale check results"}, Warnings: []string{}}})
		}
	}
	return actions
}
