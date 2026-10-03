//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/db"
)

func checkCommitBlockers(tpl *Template, tr TransitionSpec, ev []Evidence, facts map[string]interface{}, inst *Instance, task *taskDoc, currentEv []Evidence, currentObl []Obligation, checks map[string]CheckRun, actor, role string, database *db.DB) []Blocker {
	var blockers []Blocker
	haveEvidence := map[string]bool{}
	for _, e := range ev {
		haveEvidence[e.Kind] = true
	}
	for _, checkID := range tr.Checks {
		check, ok := tpl.Checks[checkID]
		if !ok {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: "transition references missing check"})
			continue
		}
		for _, kind := range checkRequiredEvidenceKinds(checkID, check, facts) {
			if !haveEvidence[kind] {
				blockers = append(blockers, Blocker{Kind: "check_input", Ref: "evidence:" + kind, Message: fmt.Sprintf("%s requires %s evidence before check %s can pass", tr.ID, kind, checkID)})
			}
		}
		cr, ok := checks[checkID]
		if !ok && database != nil {
			cr, ok = latestCheckFor(database, inst.ID, tr.ID, checkID)
		}
		if !ok {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s requires latest %s check to pass", tr.ID, checkID)})
			continue
		}
		currentHash := currentCheckInputHash(inst, &tr, cr.PrincipalRef, cr.Role, task, currentEv, currentObl)
		if cr.InputHash != "" && currentHash != "" && cr.InputHash != currentHash {
			blockers = append(blockers, Blocker{Kind: "stale_check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s check %s was produced from stale inputs", tr.ID, checkID)})
			continue
		}
		if cr.Verdict != "pass" {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s requires latest %s check to pass", tr.ID, checkID)})
		}
	}
	return blockers
}

func checkRequiredEvidenceKinds(checkID string, check CheckSpec, facts map[string]interface{}) []string {
	set := map[string]bool{}
	add := func(kind string) {
		if strings.TrimSpace(kind) != "" {
			set[kind] = true
		}
	}
	add(check.EvidenceKind)
	for _, kind := range check.EvidenceKinds {
		add(kind)
	}
	switch check.HookID {
	case "plan_ready":
		add("source_spec")
		add("decomposition_plan")
	case "architect_verdict":
		add("architect_verdict")
	case "delegated_tasks_recorded":
		add("delegated_task_manifest")
	case "branch_ready":
		if factPathBool(facts, "branch.required") {
			add("branch_evidence")
		}
	case "stacked_terminal":
		add("stacked_terminal")
	case "red_verified":
		add("red_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "impl_verified":
		add("impl_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "live_smoke_verified":
		add("live_smoke_evidence")
	case "coordinator_smoke_verified":
		add("coordinator_runbook")
		add("coordinator_smoke_execution")
		add("impl_evidence")
	case "observer_review_verdict":
		add("completion_claim")
		add("observer_completion_review")
	case "cleanup_verified":
		add("cleanup_evidence")
	case "report_ready":
		add("report_evidence")
	}
	switch checkID {
	case "plan_ready":
		add("source_spec")
		add("decomposition_plan")
	case "architect_verdict":
		add("architect_verdict")
	case "delegated_tasks_recorded":
		add("delegated_task_manifest")
	case "branch_ready":
		if factPathBool(facts, "branch.required") {
			add("branch_evidence")
		}
	case "stacked_terminal":
		add("stacked_terminal")
	case "red_verified":
		add("red_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "impl_verified":
		add("impl_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "live_smoke_verified":
		add("live_smoke_evidence")
	case "coordinator_smoke_verified":
		add("coordinator_runbook")
		add("coordinator_smoke_execution")
		add("impl_evidence")
	case "observer_review_verdict":
		add("completion_claim")
		add("observer_completion_review")
	case "cleanup_verified":
		add("cleanup_evidence")
	case "report_ready":
		add("report_evidence")
	}
	out := make([]string, 0, len(set))
	for kind := range set {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func factPathBool(facts map[string]interface{}, path string) bool {
	var current interface{} = facts
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]interface{})
		if !ok {
			return false
		}
		current, ok = m[part]
		if !ok {
			return false
		}
	}
	b, _ := current.(bool)
	return b
}

func separationOfDutyBlockers(tr TransitionSpec, ev []Evidence, actor string) []Blocker {
	if tr.SeparationOfDuty == nil {
		return nil
	}
	var blockers []Blocker
	for _, kind := range tr.SeparationOfDuty.DistinctPrincipalFromEvidence {
		latest, ok := latestEvidenceByKind(ev, kind)
		if !ok || strings.TrimSpace(actor) == "" || strings.TrimSpace(latest.PrincipalRef) == "" {
			continue
		}
		if latest.PrincipalRef == actor {
			blockers = append(blockers, Blocker{Kind: "separation_of_duty", Ref: latest.ID, Message: fmt.Sprintf("transition principal must differ from %s evidence producer", kind)})
		}
	}
	for _, pair := range tr.SeparationOfDuty.EvidencePrincipalPairsDistinct {
		left, leftOK := latestEvidenceByKind(ev, pair.LeftKind)
		right, rightOK := latestEvidenceByKind(ev, pair.RightKind)
		if !leftOK || !rightOK || strings.TrimSpace(left.PrincipalRef) == "" || strings.TrimSpace(right.PrincipalRef) == "" {
			continue
		}
		if left.PrincipalRef == right.PrincipalRef {
			blockers = append(blockers, Blocker{Kind: "separation_of_duty", Ref: left.ID + ":" + right.ID, Message: fmt.Sprintf("%s and %s evidence must be produced by different principals", pair.LeftKind, pair.RightKind)})
		}
	}
	return blockers
}

func postconditionBlockers(inst *Instance, tr TransitionSpec, chosen *OutcomeCase, ev []Evidence, obl []Obligation, checks map[string]CheckRun, task *taskDoc) []Blocker {
	if chosen == nil || len(tr.Postconditions) == 0 {
		return nil
	}
	nextState := inst.State()
	if chosen.To != nil {
		nextState = *chosen.To
	}
	ctx := evalContext{Evidence: ev, Obligations: obl, Checks: checks, Facts: taskFacts(task), Task: task, State: nextState}
	var blockers []Blocker
	for i, post := range tr.Postconditions {
		if !evalPredicate(post, ctx) {
			blockers = append(blockers, Blocker{Kind: "postcondition", Ref: fmt.Sprintf("%s:%d", tr.ID, i), Message: "transition postcondition failed"})
		}
	}
	return blockers
}

func taskDocHashOrEmpty(task *taskDoc) string {
	if task == nil {
		return ""
	}
	return taskDocHash(task)
}

func transitionBlockers(tr TransitionSpec, ev []Evidence, obl []Obligation, currentTaskHash string) []Blocker {
	var blockers []Blocker
	for _, o := range obl {
		if o.Blocking && o.Status == "open" {
			blockers = append(blockers, Blocker{Kind: "obligation", Ref: o.ID, Message: "blocking obligation is open"})
		}
	}
	for _, req := range tr.Requires {
		if req.Evidence != nil {
			match := matchEvidenceRequirement(ev, *req.Evidence)
			if !match.OK {
				blockers = append(blockers, Blocker{Kind: "evidence", Ref: req.Evidence.Kind, Message: match.Detail})
				continue
			}
			if match.Latest != nil && evidenceStaleForTask(*match.Latest, currentTaskHash) {
				blockers = append(blockers, Blocker{Kind: "stale_evidence", Ref: match.Latest.ID, Message: fmt.Sprintf("required evidence %s is stale for current task document", match.Latest.ID)})
			}
		}
		if req.Obligation != nil {
			found := false
			want := req.Obligation.Status
			if want == "" {
				want = "satisfied"
			}
			for _, o := range obl {
				if req.Obligation.ID != "" && o.ID != req.Obligation.ID {
					continue
				}
				if req.Obligation.Kind != "" && o.Kind != req.Obligation.Kind {
					continue
				}
				if o.Status == want {
					found = true
				}
			}
			if !found {
				ref := req.Obligation.ID
				if ref == "" {
					ref = req.Obligation.Kind
				}
				blockers = append(blockers, Blocker{Kind: "obligation", Ref: ref, Message: "required obligation status is missing"})
			}
		}
	}
	return blockers
}

func evidenceStaleForTask(e Evidence, currentTaskHash string) bool {
	if strings.TrimSpace(currentTaskHash) == "" || strings.TrimSpace(e.TaskHashAtProduction) == "" {
		return false
	}
	return e.TaskHashAtProduction != currentTaskHash
}

func latestCheckFor(database *db.DB, instanceID, transitionID, checkID string) (CheckRun, bool) {
	var c CheckRun
	var exit sql.NullInt64
	var hook, outcome, code, summary, facts, actor, role, runID, completed sql.NullString
	err := database.QueryRow(`
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
		FROM workflow_check_runs
		WHERE instance_id = ? AND transition_id = ? AND check_id = ?
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, instanceID, transitionID, checkID).Scan(&c.ID, &c.InstanceID, &c.TransitionID, &c.CheckID, &hook, &c.InputHash, &exit, &c.Verdict, &outcome, &code, &summary, &facts, &actor, &role, &runID, &c.StartedAt, &completed)
	if err != nil {
		return c, false
	}
	c.HookID = hook.String
	if exit.Valid {
		v := int(exit.Int64)
		c.ExitCode = &v
	}
	c.Outcome = outcome.String
	c.Code = code.String
	c.Summary = summary.String
	if facts.Valid {
		c.Facts = json.RawMessage(facts.String)
	}
	c.PrincipalRef = actor.String
	c.Role = role.String
	c.RunID = runID.String
	c.CompletedAt = completed.String
	return c, true
}

func filterOpenObligations(in []Obligation) []Obligation {
	out := []Obligation{}
	for _, o := range in {
		if o.Status == "open" {
			out = append(out, o)
		}
	}
	return out
}

func filterPendingEffects(in []Effect) []Effect {
	out := []Effect{}
	for _, e := range in {
		if e.Status == "pending" || e.Status == "leased" {
			out = append(out, e)
		}
	}
	return out
}
