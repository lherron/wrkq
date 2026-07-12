package workflow

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

func (s *Service) ListObligations(taskSelector string, includeClosed bool) ([]Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	policy := ResolveWorkflowPolicy(tpl)
	obl, err := listObligationsForInstance(s.db, inst.ID, includeClosed)
	if err != nil {
		return nil, err
	}
	ev, err := listEvidenceForInstance(s.db, inst.ID)
	if err != nil {
		return nil, err
	}
	obl = policy.ProjectObligations(s, inst, obl, ev, includeClosed)
	return obl, nil
}

func (s *Service) withDelegatedTaskClosureState(obl []Obligation, includeClosed bool) []Obligation {
	var out []Obligation
	for _, o := range obl {
		if o.Kind != "await_subordinate_closure" {
			out = append(out, o)
			continue
		}
		taskID := firstTaskIDFromObligation(o)
		if taskID == "" {
			out = append(out, o)
			continue
		}
		state, title, found := s.lookupTaskState(taskID)
		if found && taskStateTerminal(state) {
			o.Status = "satisfied"
			if o.SatisfiedByEvidenceID == "" {
				o.Reason = fmt.Sprintf("Subordinate task %s reached terminal state=%s", taskID, state)
			}
		} else {
			o.Status = "open"
			if found {
				o.Reason = fmt.Sprintf("Await subordinate task %s terminal state before workflow closure evidence; current state=%s", taskID, state)
			}
		}
		data := map[string]interface{}{
			"task":        taskID,
			"taskState":   state,
			"taskFound":   found,
			"satisfiedBy": fmt.Sprintf("wrkq:%s.state in [completed,cancelled]", taskID),
		}
		if title != "" {
			data["taskTitle"] = title
		}
		raw, _ := json.Marshal(data)
		o.Data = json.RawMessage(raw)
		if o.Status != "open" && !includeClosed {
			continue
		}
		out = append(out, o)
	}
	return out
}

func listObligationsForInstance(database *db.DB, instanceID string, includeClosed bool) ([]Obligation, error) {
	query := `
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_principal_ref, owner_actor, ''),
		       COALESCE(obligee_role,''), COALESCE(obligee_principal_ref, obligee_actor, ''), COALESCE(waive_role,''), COALESCE(waive_principal_ref, waive_actor, ''), COALESCE(no_self_waive,1),
		       blocking, status, COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''),
		       COALESCE(resolved_by_principal_ref, resolved_by_actor, ''), COALESCE(resolved_by_role,''), COALESCE(resolved_at,''), created_at, updated_at
		FROM workflow_obligations WHERE instance_id = ?`
	if !includeClosed {
		query += ` AND status = 'open'`
	}
	query += ` ORDER BY created_at, id`
	rows, err := database.Query(query, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObligations(rows)
}

func listObligationsTx(tx *sql.Tx, instanceID string, includeClosed bool) ([]Obligation, error) {
	query := `
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_principal_ref, owner_actor, ''),
		       COALESCE(obligee_role,''), COALESCE(obligee_principal_ref, obligee_actor, ''), COALESCE(waive_role,''), COALESCE(waive_principal_ref, waive_actor, ''), COALESCE(no_self_waive,1),
		       blocking, status, COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''),
		       COALESCE(resolved_by_principal_ref, resolved_by_actor, ''), COALESCE(resolved_by_role,''), COALESCE(resolved_at,''), created_at, updated_at
		FROM workflow_obligations WHERE instance_id = ?`
	if !includeClosed {
		query += ` AND status = 'open'`
	}
	query += ` ORDER BY created_at, id`
	rows, err := tx.Query(query, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObligations(rows)
}

func scanObligations(rows *sql.Rows) ([]Obligation, error) {
	var out []Obligation
	for rows.Next() {
		var o Obligation
		var blocking int
		var noSelfWaive int
		if err := rows.Scan(&o.ID, &o.InstanceID, &o.Kind, &o.OwnerRole, &o.OwnerPrincipalRef, &o.ObligeeRole, &o.ObligeePrincipalRef, &o.WaiveRole, &o.WaivePrincipalRef, &noSelfWaive, &blocking, &o.Status, &o.Reason, &o.SatisfiedByEvidenceID, &o.ResolvedByPrincipalRef, &o.ResolvedByRole, &o.ResolvedAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Blocking = blocking == 1
		o.NoSelfWaive = noSelfWaive == 1
		out = append(out, o)
	}
	return out, rows.Err()
}

type delegatedTaskManifestData struct {
	Tasks []delegatedTaskManifestTask `json:"tasks"`
}

type delegatedTaskManifestTask struct {
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
	Handle string `json:"handle"`
	Agent  string `json:"agent"`
}

type coordinatorRunbookData struct {
	LockedAt              string `json:"lockedAt"`
	LockedAfterEvidenceID string `json:"lockedAfterEvidenceId"`
	Scope                 string `json:"scope"`
	ExecutableBy          string `json:"executableBy"`
	Steps                 []struct {
		ID string `json:"id"`
	} `json:"steps"`
}

type coordinatorSmokeExecutionData struct {
	RunbookEvidenceID string `json:"runbookEvidenceId"`
	Executions        []struct {
		StepID        string `json:"stepId"`
		Verdict       string `json:"verdict"`
		ActualOutcome string `json:"actualOutcome"`
	} `json:"executions"`
}

type completionClaimData struct {
	SupersedesClaimEvidenceID string `json:"supersedesClaimEvidenceId"`
	AddressesReviewEvidenceID string `json:"addressesReviewEvidenceId"`
}

type observerCompletionReviewData struct {
	ReviewedClaimEvidenceID string   `json:"reviewedClaimEvidenceId"`
	ClaimEvidenceID         string   `json:"claimEvidenceId"`
	Verdict                 string   `json:"verdict"`
	FollowUpTaskIDs         []string `json:"followUpTaskIds"`
}

func withObserverCompletionReviewState(obl []Obligation, ev []Evidence, includeClosed bool) []Obligation {
	var out []Obligation
	for _, o := range obl {
		if o.Kind != "await_observer_completion_review" {
			out = append(out, o)
			continue
		}
		claimID := firstClaimIDFromObligation(o)
		if claimID == "" {
			out = append(out, o)
			continue
		}
		reviewID, verdict, ok := observerReviewForClaim(claimID, ev)
		if ok {
			o.Status = "satisfied"
			o.Reason = fmt.Sprintf("Observer review %s recorded verdict=%s for completion claim %s", reviewID, verdict, claimID)
		} else {
			o.Status = "open"
			o.Reason = fmt.Sprintf("External observer must review completion claim %s before report_complete", claimID)
		}
		raw, _ := json.Marshal(map[string]interface{}{"claimEvidenceId": claimID, "reviewEvidenceId": reviewID, "verdict": verdict})
		o.Data = json.RawMessage(raw)
		if o.Status != "open" && !includeClosed {
			continue
		}
		out = append(out, o)
	}
	return out
}

func observerCompletionReviewObligations(inst *Instance, ev []Evidence, existing []Obligation, includeClosed bool) []Obligation {
	if inst == nil {
		return nil
	}
	existingClaim := map[string]bool{}
	for _, o := range existing {
		if o.Kind != "await_observer_completion_review" {
			continue
		}
		if id := firstClaimIDFromObligation(o); id != "" {
			existingClaim[id] = true
		}
	}
	var out []Obligation
	for _, e := range ev {
		if e.Kind != "completion_claim" || existingClaim[e.ID] {
			continue
		}
		status := "open"
		reviewID, verdict, ok := observerReviewForClaim(e.ID, ev)
		if ok {
			status = "satisfied"
		}
		if status != "open" && !includeClosed {
			continue
		}
		data := map[string]interface{}{"claimEvidenceId": e.ID, "reviewEvidenceId": reviewID, "verdict": verdict}
		var claim completionClaimData
		if len(e.Data) > 0 && json.Unmarshal(e.Data, &claim) == nil {
			data["supersedesClaimEvidenceId"] = claim.SupersedesClaimEvidenceID
			data["addressesReviewEvidenceId"] = claim.AddressesReviewEvidenceID
		}
		raw, _ := json.Marshal(data)
		reason := fmt.Sprintf("External observer must review completion claim %s before report_complete", e.ID)
		if status == "satisfied" {
			reason = fmt.Sprintf("Observer review %s recorded verdict=%s for completion claim %s", reviewID, verdict, e.ID)
		}
		out = append(out, Obligation{
			ID:         "computed_await_observer_completion_review_" + sanitizeIDPart(e.ID),
			InstanceID: inst.ID,
			Kind:       "await_observer_completion_review",
			OwnerRole:  "observer",
			Blocking:   false,
			Status:     status,
			Reason:     reason,
			Data:       json.RawMessage(raw),
			CreatedAt:  e.ProducedAt,
			UpdatedAt:  e.ProducedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func observerReviewForClaim(claimID string, ev []Evidence) (string, string, bool) {
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if e.Kind != "observer_completion_review" || len(e.Data) == 0 {
			continue
		}
		var data observerCompletionReviewData
		if err := json.Unmarshal(e.Data, &data); err != nil {
			continue
		}
		reviewed := strings.TrimSpace(data.ReviewedClaimEvidenceID)
		if reviewed == "" {
			reviewed = strings.TrimSpace(data.ClaimEvidenceID)
		}
		if reviewed == claimID && strings.TrimSpace(data.Verdict) != "" {
			return e.ID, data.Verdict, true
		}
	}
	return "", "", false
}

func withCoordinatorSmokeExecutionState(obl []Obligation, ev []Evidence, includeClosed bool) []Obligation {
	var out []Obligation
	for _, o := range obl {
		if o.Kind != "await_coordinator_smoke_execution" {
			out = append(out, o)
			continue
		}
		runbookID := firstEvidenceIDFromObligation(o)
		if runbookID == "" {
			out = append(out, o)
			continue
		}
		execID, ok := coordinatorSmokeExecutionSatisfied(runbookID, ev)
		if ok {
			o.Status = "satisfied"
			o.Reason = fmt.Sprintf("Coordinator smoke execution %s satisfies runbook %s", execID, runbookID)
		} else {
			o.Status = "open"
			o.Reason = fmt.Sprintf("Execute coordinator runbook %s and record coordinator_smoke_execution before report_complete", runbookID)
		}
		raw, _ := json.Marshal(map[string]interface{}{"runbookEvidenceId": runbookID, "executionEvidenceId": execID})
		o.Data = json.RawMessage(raw)
		if o.Status != "open" && !includeClosed {
			continue
		}
		out = append(out, o)
	}
	return out
}

func coordinatorSmokeExecutionObligations(inst *Instance, ev []Evidence, existing []Obligation, includeClosed bool) []Obligation {
	if inst == nil {
		return nil
	}
	existingRunbook := map[string]bool{}
	for _, o := range existing {
		if o.Kind != "await_coordinator_smoke_execution" {
			continue
		}
		if id := firstEvidenceIDFromObligation(o); id != "" {
			existingRunbook[id] = true
		}
	}
	var out []Obligation
	for _, e := range ev {
		if e.Kind != "coordinator_runbook" || len(e.Data) == 0 || existingRunbook[e.ID] {
			continue
		}
		status := "open"
		execID, ok := coordinatorSmokeExecutionSatisfied(e.ID, ev)
		if ok {
			status = "satisfied"
		}
		if status != "open" && !includeClosed {
			continue
		}
		data := map[string]interface{}{"runbookEvidenceId": e.ID, "executionEvidenceId": execID}
		var rb coordinatorRunbookData
		if json.Unmarshal(e.Data, &rb) == nil {
			data["lockedAt"] = rb.LockedAt
			data["lockedAfterEvidenceId"] = rb.LockedAfterEvidenceID
			data["scope"] = rb.Scope
			data["steps"] = len(rb.Steps)
		}
		raw, _ := json.Marshal(data)
		reason := fmt.Sprintf("Execute coordinator runbook %s and record coordinator_smoke_execution before report_complete", e.ID)
		if status == "satisfied" {
			reason = fmt.Sprintf("Coordinator smoke execution %s satisfies runbook %s", execID, e.ID)
		}
		out = append(out, Obligation{
			ID:         "computed_await_coordinator_smoke_execution_" + sanitizeIDPart(e.ID),
			InstanceID: inst.ID,
			Kind:       "await_coordinator_smoke_execution",
			OwnerRole:  "coordinator",
			Blocking:   false,
			Status:     status,
			Reason:     reason,
			Data:       json.RawMessage(raw),
			CreatedAt:  e.ProducedAt,
			UpdatedAt:  e.ProducedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func coordinatorSmokeExecutionSatisfied(runbookID string, ev []Evidence) (string, bool) {
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if e.Kind != "coordinator_smoke_execution" || len(e.Data) == 0 {
			continue
		}
		var data coordinatorSmokeExecutionData
		if err := json.Unmarshal(e.Data, &data); err != nil || data.RunbookEvidenceID != runbookID || len(data.Executions) == 0 {
			continue
		}
		ok := true
		for _, ex := range data.Executions {
			if ex.Verdict == "pass" {
				continue
			}
			if ex.Verdict == "skip-with-reason" && len(strings.TrimSpace(ex.ActualOutcome)) >= 40 {
				continue
			}
			ok = false
			break
		}
		if ok {
			return e.ID, true
		}
	}
	return "", false
}

func (s *Service) delegatedTaskClosureObligations(inst *Instance, ev []Evidence, existing []Obligation, includeClosed bool) []Obligation {
	if inst == nil {
		return nil
	}
	existingTask := map[string]bool{}
	for _, o := range existing {
		if o.Kind != "await_subordinate_closure" {
			continue
		}
		for _, taskID := range extractTaskIDsFromText(o.ID + " " + o.Reason) {
			existingTask[taskID] = true
		}
	}
	latest := map[string]struct {
		evidence Evidence
		task     delegatedTaskManifestTask
	}{}
	for _, e := range ev {
		if e.Kind != "delegated_task_manifest" || len(e.Data) == 0 {
			continue
		}
		tasks, err := parseDelegatedTaskManifestTasks(e.Data)
		if err != nil {
			continue
		}
		for _, task := range tasks {
			taskID := strings.TrimSpace(task.ID)
			if taskID == "" {
				taskID = strings.TrimSpace(task.TaskID)
			}
			if taskID == "" {
				continue
			}
			latest[taskID] = struct {
				evidence Evidence
				task     delegatedTaskManifestTask
			}{evidence: e, task: task}
		}
	}
	if len(latest) == 0 {
		return nil
	}
	var out []Obligation
	for taskID, item := range latest {
		if existingTask[taskID] {
			continue
		}
		state, title, found := s.lookupTaskState(taskID)
		status := "open"
		if found && taskStateTerminal(state) {
			status = "satisfied"
		}
		if status != "open" && !includeClosed {
			continue
		}
		data := map[string]interface{}{
			"task":             taskID,
			"taskState":        state,
			"taskFound":        found,
			"satisfiedBy":      fmt.Sprintf("wrkq:%s.state in [completed,cancelled]", taskID),
			"sourceEvidenceId": item.evidence.ID,
		}
		if title != "" {
			data["taskTitle"] = title
		}
		if item.task.Handle != "" {
			data["handle"] = item.task.Handle
		}
		if item.task.Agent != "" {
			data["agent"] = item.task.Agent
		}
		raw, _ := json.Marshal(data)
		reason := fmt.Sprintf("Await subordinate task %s terminal state before workflow closure evidence", taskID)
		if found {
			reason = fmt.Sprintf("Await subordinate task %s terminal state before workflow closure evidence; current state=%s", taskID, state)
		}
		out = append(out, Obligation{
			ID:         "computed_await_subordinate_closure_" + sanitizeIDPart(taskID),
			InstanceID: inst.ID,
			Kind:       "await_subordinate_closure",
			OwnerRole:  "coordinator",
			Blocking:   false,
			Status:     status,
			Reason:     reason,
			Data:       json.RawMessage(raw),
			CreatedAt:  item.evidence.ProducedAt,
			UpdatedAt:  item.evidence.ProducedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func createDelegatedTaskClosureObligationsTx(tx *sql.Tx, instanceID, evidenceID, data string) error {
	tasks, err := parseDelegatedTaskManifestTasks(json.RawMessage(data))
	if err != nil {
		return nil
	}
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			taskID = strings.TrimSpace(task.TaskID)
		}
		if taskID == "" {
			continue
		}
		var count int
		if err := tx.QueryRow(`
			SELECT COUNT(1)
			FROM workflow_obligations
			WHERE instance_id = ? AND kind = 'await_subordinate_closure' AND reason LIKE ?
		`, instanceID, "%"+taskID+"%").Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
		if err != nil {
			return err
		}
		reason := fmt.Sprintf("Await subordinate task %s terminal state before workflow closure evidence; source evidence=%s", taskID, evidenceID)
		_, err = tx.Exec(`
			INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
			VALUES (?, ?, 'await_subordinate_closure', 'coordinator', 0, 'open', ?)
		`, id, instanceID, reason)
		if err != nil {
			return err
		}
	}
	return nil
}

func createCoordinatorSmokeExecutionObligationTx(tx *sql.Tx, instanceID, evidenceID string) error {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(1)
		FROM workflow_obligations
		WHERE instance_id = ? AND kind = 'await_coordinator_smoke_execution' AND reason LIKE ?
	`, instanceID, "%"+evidenceID+"%").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
	if err != nil {
		return err
	}
	reason := fmt.Sprintf("Execute coordinator runbook %s and record coordinator_smoke_execution before report_complete", evidenceID)
	_, err = tx.Exec(`
		INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
		VALUES (?, ?, 'await_coordinator_smoke_execution', 'coordinator', 0, 'open', ?)
	`, id, instanceID, reason)
	return err
}

func createObserverCompletionReviewObligationTx(tx *sql.Tx, instanceID, evidenceID string) error {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(1)
		FROM workflow_obligations
		WHERE instance_id = ? AND kind = 'await_observer_completion_review' AND reason LIKE ?
	`, instanceID, "%"+evidenceID+"%").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
	if err != nil {
		return err
	}
	reason := fmt.Sprintf("External observer must review completion claim %s before report_complete", evidenceID)
	_, err = tx.Exec(`
		INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
		VALUES (?, ?, 'await_observer_completion_review', 'observer', 0, 'open', ?)
	`, id, instanceID, reason)
	return err
}

func parseDelegatedTaskManifestTasks(data json.RawMessage) ([]delegatedTaskManifestTask, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var manifest delegatedTaskManifestData
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return manifest.Tasks, nil
}

func (s *Service) lookupTaskState(taskID string) (string, string, bool) {
	var state, title string
	err := s.db.QueryRow(`SELECT state, title FROM tasks WHERE id = ?`, taskID).Scan(&state, &title)
	if err != nil {
		return "", "", false
	}
	return state, title, true
}

func taskStateTerminal(state string) bool {
	switch state {
	case "completed", "cancelled":
		return true
	default:
		return false
	}
}

func extractTaskIDsFromText(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r != '-' && r != '_' && r != ':' && r != '.' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	})
	var out []string
	for _, f := range fields {
		if strings.HasPrefix(f, "T-") {
			out = append(out, f)
		}
	}
	return out
}

func firstTaskIDFromObligation(o Obligation) string {
	if len(o.Data) > 0 {
		var data struct {
			Task   string `json:"task"`
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(o.Data, &data); err == nil {
			if strings.TrimSpace(data.Task) != "" {
				return strings.TrimSpace(data.Task)
			}
			if strings.TrimSpace(data.TaskID) != "" {
				return strings.TrimSpace(data.TaskID)
			}
		}
	}
	for _, taskID := range extractTaskIDsFromText(o.ID + " " + o.Reason) {
		return taskID
	}
	return ""
}

func firstEvidenceIDFromObligation(o Obligation) string {
	if len(o.Data) > 0 {
		var data struct {
			RunbookEvidenceID string `json:"runbookEvidenceId"`
			EvidenceID        string `json:"evidenceId"`
		}
		if err := json.Unmarshal(o.Data, &data); err == nil {
			if strings.TrimSpace(data.RunbookEvidenceID) != "" {
				return strings.TrimSpace(data.RunbookEvidenceID)
			}
			if strings.TrimSpace(data.EvidenceID) != "" {
				return strings.TrimSpace(data.EvidenceID)
			}
		}
	}
	for _, field := range strings.FieldsFunc(o.ID+" "+o.Reason, func(r rune) bool {
		return r != '_' && r != '-' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	}) {
		if strings.HasPrefix(field, "ev_") {
			return field
		}
	}
	return ""
}

func firstClaimIDFromObligation(o Obligation) string {
	if len(o.Data) > 0 {
		var data struct {
			ClaimEvidenceID string `json:"claimEvidenceId"`
		}
		if err := json.Unmarshal(o.Data, &data); err == nil && strings.TrimSpace(data.ClaimEvidenceID) != "" {
			return strings.TrimSpace(data.ClaimEvidenceID)
		}
	}
	for _, field := range strings.FieldsFunc(o.ID+" "+o.Reason, func(r rune) bool {
		return r != '_' && r != '-' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	}) {
		if strings.HasPrefix(field, "ev_") {
			return field
		}
	}
	return ""
}

func sanitizeIDPart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return strings.Trim(b.String(), "_")
}

func (s *Service) ShowObligation(id string) (*Obligation, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_principal_ref, owner_actor, ''),
		       COALESCE(obligee_role,''), COALESCE(obligee_principal_ref, obligee_actor, ''), COALESCE(waive_role,''), COALESCE(waive_principal_ref, waive_actor, ''), COALESCE(no_self_waive,1),
		       blocking, status, COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''),
		       COALESCE(resolved_by_principal_ref, resolved_by_actor, ''), COALESCE(resolved_by_role,''), COALESCE(resolved_at,''), created_at, updated_at
		FROM workflow_obligations WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	obl, err := scanObligations(rows)
	if err != nil {
		return nil, err
	}
	if len(obl) == 0 {
		return nil, fmt.Errorf("obligation not found: %s", id)
	}
	return &obl[0], nil
}

type ObligationStatusOptions struct {
	PrincipalRef string
	Role         string
}

func (s *Service) SetObligationStatus(taskSelector, id, status, evidenceID, reason string) (*Obligation, error) {
	return s.SetObligationStatusWithAuthority(taskSelector, id, status, evidenceID, reason, ObligationStatusOptions{PrincipalRef: "system:wrkf", Role: "system"})
}

func (s *Service) SetObligationStatusWithAuthority(taskSelector, id, status, evidenceID, reason string, opts ObligationStatusOptions) (*Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	if status != "satisfied" && status != "waived" && status != "cancelled" {
		return nil, fmt.Errorf("invalid obligation status: %s", status)
	}
	now := s.now().Format(time.RFC3339)
	var out *Obligation
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		current, err := selectObligationTx(tx, id, inst.ID)
		if err != nil {
			return err
		}
		if !obligationStatusAllowed(*current, status, opts.PrincipalRef, opts.Role) {
			return roleDeniedError(inst.ID, "obligation:"+id+":"+status, opts.Role)
		}
		_, err = tx.Exec(`
			UPDATE workflow_obligations
			SET status = ?, satisfied_by_evidence_id = COALESCE(NULLIF(?, ''), satisfied_by_evidence_id),
			    reason = COALESCE(NULLIF(?, ''), reason), resolved_by_actor = ?, resolved_by_principal_ref = ?, resolved_by_role = ?, resolved_at = ?, updated_at = ?
			WHERE id = ? AND instance_id = ?
		`, status, evidenceID, reason, nullIfEmpty(opts.PrincipalRef), nullIfEmpty(opts.PrincipalRef), nullIfEmpty(opts.Role), now, now, id, inst.ID)
		if err != nil {
			return err
		}
		out, err = selectObligationTx(tx, id, inst.ID)
		return err
	})
	return out, err
}

func selectObligationTx(tx *sql.Tx, id, instanceID string) (*Obligation, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_principal_ref, owner_actor, ''),
		       COALESCE(obligee_role,''), COALESCE(obligee_principal_ref, obligee_actor, ''), COALESCE(waive_role,''), COALESCE(waive_principal_ref, waive_actor, ''), COALESCE(no_self_waive,1),
		       blocking, status, COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''),
		       COALESCE(resolved_by_principal_ref, resolved_by_actor, ''), COALESCE(resolved_by_role,''), COALESCE(resolved_at,''), created_at, updated_at
		FROM workflow_obligations WHERE id = ? AND instance_id = ?
	`, id, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	list, err := scanObligations(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("obligation not found: %s", id)
	}
	return &list[0], nil
}

func obligationStatusAllowed(o Obligation, status, actor, role string) bool {
	actor = strings.TrimSpace(actor)
	role = strings.TrimSpace(role)
	if role == "system" || role == "supervisor" {
		return true
	}
	switch status {
	case "satisfied":
		if o.OwnerPrincipalRef != "" {
			return actor != "" && actor == o.OwnerPrincipalRef
		}
		if o.OwnerRole != "" {
			return role == o.OwnerRole
		}
		return actor != ""
	case "waived", "cancelled":
		if o.NoSelfWaive && actor != "" && o.OwnerPrincipalRef != "" && actor == o.OwnerPrincipalRef {
			return false
		}
		if o.WaivePrincipalRef != "" {
			return actor != "" && actor == o.WaivePrincipalRef
		}
		if o.WaiveRole != "" {
			return role == o.WaiveRole
		}
		return false
	default:
		return false
	}
}

func (s *Service) CreateObligation(taskSelector, kind, ownerRole, ownerActor string, blocking bool, reason string) (*Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var out *Obligation
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
		if err != nil {
			return err
		}
		blockingInt := 0
		if blocking {
			blockingInt = 1
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_obligations (
				id, instance_id, kind, owner_role, owner_actor,
				obligee_role, waive_role, no_self_waive, blocking, status, reason
			) VALUES (?, ?, ?, ?, ?, 'workflow', 'system', 1, ?, 'open', ?)
		`, id, inst.ID, kind, nullIfEmpty(ownerRole), nullIfEmpty(ownerActor), blockingInt, nullIfEmpty(reason))
		if err != nil {
			return err
		}
		out, err = selectObligationTx(tx, id, inst.ID)
		return err
	})
	return out, err
}

func (s *Service) ListEffects(taskSelector string, all bool) ([]Effect, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	return listEffectsForInstance(s.db, inst.ID, all)
}

func listEffectsForInstance(database *db.DB, instanceID string, all bool) ([]Effect, error) {
	query := `
		SELECT id, instance_id, revision, COALESCE(sequence,0), kind, payload_json, status, idempotency_key, COALESCE(semantic_key,''), attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''), COALESCE(receipt_json,''),
		       created_at, updated_at
		FROM workflow_effects WHERE instance_id = ?`
	if !all {
		query += ` AND status IN ('pending','leased','failed','delivered')`
	}
	query += ` ORDER BY COALESCE(sequence, 9223372036854775807), created_at, id`
	rows, err := database.Query(query, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEffects(rows)
}

func scanEffects(rows *sql.Rows) ([]Effect, error) {
	var out []Effect
	for rows.Next() {
		var e Effect
		var payload, receipt string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Revision, &e.Sequence, &e.Kind, &payload, &e.Status, &e.IdempotencyKey, &e.SemanticKey, &e.Attempts, &e.LeasedBy, &e.LeasedUntil, &e.DeliveredAt, &e.LastError, &receipt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		if receipt != "" {
			e.Receipt = json.RawMessage(receipt)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) ShowEffect(id string) (*Effect, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, revision, COALESCE(sequence,0), kind, payload_json, status, idempotency_key, COALESCE(semantic_key,''), attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''), COALESCE(receipt_json,''),
		       created_at, updated_at
		FROM workflow_effects WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	effects, err := scanEffects(rows)
	if err != nil {
		return nil, err
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return &effects[0], nil
}

func (s *Service) ClaimEffects(adapter string, limit int, leaseMs int64, taskSelector, kind string) (*EffectClaim, error) {
	if adapter == "" {
		adapter = "wrkf-effect-claim"
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than zero")
	}
	if leaseMs <= 0 {
		return nil, fmt.Errorf("leaseMs must be greater than zero")
	}
	var taskUUID string
	if taskSelector != "" {
		resolved, _, err := selectors.ResolveTask(s.db, taskSelector)
		if err != nil {
			return nil, err
		}
		taskUUID = resolved
	}

	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	nowText := now.Format(time.RFC3339)
	expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
	claim := &EffectClaim{Effects: []Effect{}, LeaseToken: token, LeaseExpiresAt: expiresAt}

	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		query := `
			SELECT e.id
			FROM workflow_effects e
			JOIN workflow_instances i ON i.id = e.instance_id
			WHERE (
				e.status IN ('pending', 'failed')
				OR (e.status = 'leased' AND COALESCE(e.leased_until, '') <= ?)
			)`
		args := []interface{}{nowText}
		if taskUUID != "" {
			query += ` AND i.task_uuid = ?`
			args = append(args, taskUUID)
		}
		if kind != "" {
			query += ` AND e.kind = ?`
			args = append(args, kind)
		}
		query += ` ORDER BY COALESCE(e.sequence, 9223372036854775807), e.created_at, e.id LIMIT ?`
		args = append(args, limit)

		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		updateArgs := []interface{}{adapter, expiresAt, token, nowText}
		for _, id := range ids {
			updateArgs = append(updateArgs, id)
		}
		result, err := tx.Exec(fmt.Sprintf(`
			UPDATE workflow_effects
			SET status = 'leased',
			    leased_by = ?,
			    leased_until = ?,
			    lease_token = ?,
			    updated_at = ?
			WHERE id IN (%s)
		`, placeholders), updateArgs...)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected != int64(len(ids)) {
			return leaseConflictError("", token)
		}

		effects, err := effectsByLeaseTokenTx(tx, token)
		if err != nil {
			return err
		}
		claim.Effects = effects
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func effectsByLeaseTokenTx(tx *sql.Tx, token string) ([]Effect, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, revision, COALESCE(sequence,0), kind, payload_json, status, idempotency_key, COALESCE(semantic_key,''), attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''), COALESCE(receipt_json,''),
		       created_at, updated_at
		FROM workflow_effects
		WHERE lease_token = ?
		ORDER BY COALESCE(sequence, 9223372036854775807), created_at, id
	`, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEffects(rows)
}

func newLeaseToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "lease_" + hex.EncodeToString(b[:]), nil
}

func (s *Service) AckEffect(id, leaseToken string) (*Effect, error) {
	return s.AckEffectWithReceipt(id, leaseToken, nil)
}

func (s *Service) AckEffectWithReceipt(id, leaseToken string, receipt json.RawMessage) (*Effect, error) {
	if len(receipt) > 0 && !json.Valid(receipt) {
		return nil, fmt.Errorf("invalid effect receipt JSON")
	}
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'delivered',
		    delivered_at = ?,
		    attempts = attempts + 1,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    last_error = NULL,
		    receipt_json = COALESCE(NULLIF(?, ''), receipt_json),
		    updated_at = ?
		WHERE id = ?
		  AND status = 'leased'
		  AND lease_token = ?
		  AND leased_until > ?
	`, now, string(receipt), now, id, leaseToken, now)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, leaseConflictError(id, leaseToken)
	}
	return s.ShowEffect(id)
}

func (s *Service) ForceAckEffect(id string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'delivered',
		    delivered_at = ?,
		    attempts = attempts + 1,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    last_error = NULL,
		    updated_at = ?
		WHERE id = ?
	`, now, now, id)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return s.ShowEffect(id)
}

type EffectDelivery struct {
	Effect   *Effect         `json:"effect"`
	Binding  *Run            `json:"binding,omitempty"`
	Receipt  json.RawMessage `json:"receipt,omitempty"`
	ExitCode int             `json:"exitCode"`
	Stdout   string          `json:"stdout,omitempty"`
	Stderr   string          `json:"stderr,omitempty"`
}

const transitionBuiltinEffectAdapter = "wrkf-transition-builtin"

var engineOwnedBuiltinEffectKinds = map[string]struct{}{
	"set_task_state": {},
}

func isEngineOwnedBuiltinEffect(kind string) bool {
	_, ok := engineOwnedBuiltinEffectKinds[kind]
	return ok
}

func (s *Service) DeliverEffect(id, adapter string, catalog *HookCatalog, templateDir string) (*EffectDelivery, error) {
	current, err := s.ShowEffect(id)
	if err != nil {
		return nil, err
	}
	if current.Status == "delivered" {
		return &EffectDelivery{Effect: current}, nil
	}
	if current.Kind == "set_task_state" {
		return s.deliverSetTaskStateEffect(current, adapter)
	}
	if catalog == nil {
		instForCatalog, err := s.instanceByID(current.InstanceID)
		if err != nil {
			return nil, err
		}
		stored, err := s.storedHookCatalog(instForCatalog.TemplateID, instForCatalog.TemplateVersion)
		if err != nil {
			return nil, err
		}
		catalog = stored
	}
	if catalog == nil {
		return nil, fmt.Errorf("hook catalog is required for effect delivery")
	}
	handler, ok := catalog.EffectHandlers[current.Kind]
	if !ok {
		if h, hookOK := catalog.Hooks["effect_"+current.Kind]; hookOK {
			handler, ok = h, true
		}
	}
	if !ok {
		return nil, fmt.Errorf("no effect handler registered for %s", current.Kind)
	}

	claim, err := s.claimEffectByID(id, adapter, 60_000)
	if err != nil {
		return nil, err
	}
	if claim == nil || len(claim.Effects) == 0 {
		return nil, leaseConflictError(id, "")
	}
	eff := &claim.Effects[0]
	if eff.Status == "delivered" {
		return &EffectDelivery{Effect: eff}, nil
	}
	inst, err := s.instanceByID(eff.InstanceID)
	if err != nil {
		return nil, err
	}
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	role := effectRole(eff)
	if role == "" {
		return nil, fmt.Errorf("effect %s has no role binding target", eff.ID)
	}
	binding, err := s.latestRunForRole(inst.ID, role)
	if err != nil {
		return nil, err
	}
	ev, _ := listEvidenceForInstance(s.db, inst.ID)
	input := map[string]interface{}{
		"effect":   eff,
		"instance": inst,
		"binding":  binding,
		"evidence": ev,
	}
	if task != nil {
		input["task"] = map[string]interface{}{"id": task.ID, "uuid": task.UUID, "title": task.Title, "state": task.State, "taskRef": "wrkq:" + task.ID}
	}
	inputJSON, _ := json.Marshal(input)
	exit, stdout, stderr, runErr := runHook(handler, templateDir, inputJSON)
	if runErr != nil && exit < 0 {
		return nil, runErr
	}
	out := &EffectDelivery{Effect: eff, Binding: binding, ExitCode: exit, Stdout: string(stdout), Stderr: string(stderr)}
	if json.Valid(stdout) {
		out.Receipt = json.RawMessage(stdout)
	}
	if exit != 0 {
		failed, failErr := s.FailEffect(id, claim.LeaseToken, strings.TrimSpace(string(stderr)), true)
		if failErr == nil {
			out.Effect = failed
		}
		return out, fmt.Errorf("effect handler failed with exit %d: %s", exit, strings.TrimSpace(string(stderr)))
	}
	delivered, err := s.AckEffectWithReceipt(id, claim.LeaseToken, out.Receipt)
	if err != nil {
		return nil, err
	}
	out.Effect = delivered
	return out, nil
}

func (s *Service) deliverSetTaskStateEffect(current *Effect, adapter string) (*EffectDelivery, error) {
	claim, err := s.claimEffectByID(current.ID, adapter, 60_000)
	if err != nil {
		return nil, err
	}
	if claim == nil || len(claim.Effects) == 0 {
		return nil, leaseConflictError(current.ID, "")
	}
	eff := &claim.Effects[0]
	if eff.Status == "delivered" {
		return &EffectDelivery{Effect: eff}, nil
	}
	var spec EffectSpec
	if err := json.Unmarshal(eff.Payload, &spec); err != nil {
		failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, "invalid set_task_state payload: "+err.Error(), false)
		if failErr != nil {
			return nil, failErr
		}
		return &EffectDelivery{Effect: failed, ExitCode: 1}, err
	}
	target, _ := spec.Data["state"].(string)
	target = strings.TrimSpace(target)
	targetState, err := domain.ParseState(target)
	if err != nil {
		failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, err.Error(), false)
		if failErr != nil {
			return nil, failErr
		}
		return &EffectDelivery{Effect: failed, ExitCode: 1, Stderr: err.Error()}, err
	}
	inst, err := s.instanceByID(eff.InstanceID)
	if err != nil {
		return nil, err
	}
	before, err := loadTaskDoc(s.db, inst.TaskUUID)
	if err != nil {
		return nil, err
	}
	newETag := before.ETag
	target = string(targetState)
	alreadyApplied := before.State == target
	if !alreadyApplied {
		newETag, err = store.New(s.db).Tasks.UpdateFieldsWithViaAttribution(workflowSystemAttribution, inst.TaskUUID, map[string]interface{}{"state": target}, before.ETag, "wrkf.effect:set_task_state")
		if err != nil {
			failed, failErr := s.FailEffect(eff.ID, claim.LeaseToken, err.Error(), true)
			if failErr != nil {
				return nil, failErr
			}
			return &EffectDelivery{Effect: failed, ExitCode: 1, Stderr: err.Error()}, err
		}
	}
	receiptMap := map[string]interface{}{
		"kind":           "set_task_state.receipt",
		"taskUuid":       inst.TaskUUID,
		"taskRef":        inst.TaskRef,
		"from":           before.State,
		"to":             target,
		"previousEtag":   before.ETag,
		"newEtag":        newETag,
		"alreadyApplied": alreadyApplied,
		"effectId":       eff.ID,
		"semanticKey":    eff.SemanticKey,
	}
	receipt, _ := json.Marshal(receiptMap)
	delivered, err := s.AckEffectWithReceipt(eff.ID, claim.LeaseToken, json.RawMessage(receipt))
	if err != nil {
		return nil, err
	}
	return &EffectDelivery{Effect: delivered, Receipt: json.RawMessage(receipt), ExitCode: 0, Stdout: string(receipt)}, nil
}

func (s *Service) deliverBuiltinTransitionEffects(result map[string]interface{}, transitionID string) (map[string]interface{}, error) {
	if result == nil {
		return result, nil
	}
	// workflow_events.result_json remains the transition commit record. This
	// post-commit pass refreshes the returned result; effect list/show are the
	// durable delivery truth for builtin effect terminal status and receipts.
	effects, err := transitionResultEffects(result["effects"])
	if err != nil {
		return result, err
	}
	if len(effects) == 0 {
		return result, nil
	}

	eventID, _ := result["eventId"].(string)
	for i := range effects {
		if !isEngineOwnedBuiltinEffect(effects[i].Kind) {
			continue
		}
		effectID := effects[i].ID
		delivery, deliverErr := s.DeliverEffect(effectID, transitionBuiltinEffectAdapter, nil, "")
		current := &effects[i]
		if delivery != nil && delivery.Effect != nil {
			current = delivery.Effect
		} else if shown, showErr := s.ShowEffect(effectID); showErr == nil {
			current = shown
		}
		effects[i] = *current
		result["effects"] = effects

		if deliverErr != nil || current.Status != "delivered" {
			cause := deliverErr
			if cause == nil {
				cause = fmt.Errorf("builtin effect %s finished with status %s", current.ID, current.Status)
			}
			return result, &transitionEffectDeliveryError{
				transitionID: transitionID,
				eventID:      eventID,
				effectID:     current.ID,
				kind:         current.Kind,
				status:       current.Status,
				err:          cause,
				result:       result,
			}
		}
	}
	return result, nil
}

func transitionResultEffects(v interface{}) ([]Effect, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []Effect:
		out := make([]Effect, len(x))
		copy(out, x)
		return out, nil
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return nil, err
		}
		var out []Effect
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// workflowSystemAttribution is the principal-only attribution the wrkf engine
// uses for its own system writes (e.g. set_task_state effects).
var workflowSystemAttribution = attribution.Attribution{PrincipalRef: "agent:wrkf-system"}

func (s *Service) claimEffectByID(id, adapter string, leaseMs int64) (*EffectClaim, error) {
	if adapter == "" {
		adapter = "wrkf-effect-deliver"
	}
	if leaseMs <= 0 {
		return nil, fmt.Errorf("leaseMs must be greater than zero")
	}
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	nowText := now.Format(time.RFC3339)
	expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
	claim := &EffectClaim{Effects: []Effect{}, LeaseToken: token, LeaseExpiresAt: expiresAt}
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRow(`SELECT status FROM workflow_effects WHERE id = ?`, id).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("effect not found: %s", id)
			}
			return err
		}
		if status == "delivered" {
			effects, err := effectByIDTx(tx, id)
			if err != nil {
				return err
			}
			claim.Effects = effects
			claim.LeaseToken = ""
			claim.LeaseExpiresAt = ""
			return nil
		}
		result, err := tx.Exec(`
			UPDATE workflow_effects
			SET status = 'leased',
			    leased_by = ?,
			    leased_until = ?,
			    lease_token = ?,
			    updated_at = ?
			WHERE id = ?
			  AND (
				status IN ('pending', 'failed')
				OR (status = 'leased' AND COALESCE(leased_until, '') <= ?)
			  )
		`, adapter, expiresAt, token, nowText, id, nowText)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected != 1 {
			return effectNotDeliverableError(id, status)
		}
		effects, err := effectsByLeaseTokenTx(tx, token)
		if err != nil {
			return err
		}
		claim.Effects = effects
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

func effectByIDTx(tx *sql.Tx, id string) ([]Effect, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, revision, COALESCE(sequence,0), kind, payload_json, status, idempotency_key, COALESCE(semantic_key,''), attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''), COALESCE(receipt_json,''),
		       created_at, updated_at
		FROM workflow_effects
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEffects(rows)
}

func (s *Service) latestRunForRole(instanceID, role string) (*Run, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs
		WHERE instance_id = ? AND role = ? AND status = 'active' AND COALESCE(delivery_ref,'') != ''
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, instanceID, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, fmt.Errorf("role %s is not bound; run wrkf run bind TASK %s HANDLE", role, role)
	}
	var r Run
	if err := rows.Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt); err != nil {
		return nil, err
	}
	return &r, rows.Err()
}

func effectRole(eff *Effect) string {
	if eff == nil || len(eff.Payload) == 0 {
		return ""
	}
	var payload struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(eff.Payload, &payload)
	return payload.Role
}

func nextEffectSequenceTx(tx *sql.Tx, instanceID string) (int64, error) {
	var seq int64
	err := tx.QueryRow(`SELECT COALESCE(MAX(sequence), 0) + 1 FROM workflow_effects WHERE instance_id = ?`, instanceID).Scan(&seq)
	return seq, err
}

type effectRenderContext struct {
	instance  Instance
	outcomeID string
	runID     string
	sequence  int64
}

var unresolvedEffectTokenRE = regexp.MustCompile(`\{[A-Za-z][A-Za-z0-9_]*\}`)

func effectRenderReplacements(ctx effectRenderContext, kind string) map[string]string {
	return map[string]string{
		"{instanceId}":                 ctx.instance.ID,
		"{taskUuid}":                   ctx.instance.TaskUUID,
		"{taskRef}":                    ctx.instance.TaskRef,
		"{revision}":                   fmt.Sprint(ctx.instance.Revision),
		"{outcome}":                    ctx.outcomeID,
		"{kind}":                       kind,
		"{sequence}":                   fmt.Sprint(ctx.sequence),
		"{runId}":                      ctx.runID,
		"{sourceImplementActionRunId}": ctx.runID,
	}
}

func renderEffectTemplateString(value string, replacements map[string]string) (string, error) {
	rendered := value
	for token, replacement := range replacements {
		if strings.Contains(rendered, token) && replacement == "" {
			return "", fmt.Errorf("effect template token %s resolved empty", token)
		}
		rendered = strings.ReplaceAll(rendered, token, replacement)
	}
	if unresolved := unresolvedEffectTokenRE.FindString(rendered); unresolved != "" {
		return "", fmt.Errorf("unresolved effect template token %s", unresolved)
	}
	return rendered, nil
}

func renderEffectTemplateValue(value interface{}, replacements map[string]string) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return renderEffectTemplateString(v, replacements)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			rendered, err := renderEffectTemplateValue(v[i], replacements)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, child := range v {
			renderedKey, err := renderEffectTemplateString(key, replacements)
			if err != nil {
				return nil, err
			}
			rendered, err := renderEffectTemplateValue(child, replacements)
			if err != nil {
				return nil, err
			}
			out[renderedKey] = rendered
		}
		return out, nil
	default:
		return value, nil
	}
}

func renderEffectSpec(ef EffectSpec, ctx effectRenderContext) (EffectSpec, string, error) {
	rendered := ef
	replacements := effectRenderReplacements(ctx, ef.Kind)
	var err error
	rendered.Kind, err = renderEffectTemplateString(rendered.Kind, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	replacements = effectRenderReplacements(ctx, rendered.Kind)
	rendered.Role, err = renderEffectTemplateString(rendered.Role, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	rendered.Reason, err = renderEffectTemplateString(rendered.Reason, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	rendered.SemanticKey, err = renderEffectTemplateString(rendered.SemanticKey, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	if rendered.Data != nil {
		data, err := renderEffectTemplateValue(rendered.Data, replacements)
		if err != nil {
			return EffectSpec{}, "", err
		}
		rendered.Data = data.(map[string]interface{})
	}
	semanticKey, err := effectSemanticKey(ctx, rendered)
	if err != nil {
		return EffectSpec{}, "", err
	}
	return rendered, semanticKey, nil
}

func effectSemanticKey(ctx effectRenderContext, ef EffectSpec) (string, error) {
	key := strings.TrimSpace(ef.SemanticKey)
	if key == "" && ef.Data != nil {
		if raw, ok := ef.Data["semanticKey"]; ok {
			if s, ok := raw.(string); ok {
				key = strings.TrimSpace(s)
			}
		}
	}
	if key == "" {
		key = fmt.Sprintf("rev:%d:outcome:%s:seq:%d:kind:%s", ctx.instance.Revision, ctx.outcomeID, ctx.sequence, ef.Kind)
	}
	return renderEffectTemplateString(key, effectRenderReplacements(ctx, ef.Kind))
}

func (s *Service) FailEffect(id, leaseToken, reason string, retryable bool) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'failed',
		    attempts = attempts + 1,
		    last_error = ?,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    updated_at = ?
		WHERE id = ?
		  AND status = 'leased'
		  AND lease_token = ?
		  AND leased_until > ?
	`, reason, now, id, leaseToken, now)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, leaseConflictError(id, leaseToken)
	}
	return s.ShowEffect(id)
}

func (s *Service) ForceFailEffect(id, reason string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	result, err := s.db.Exec(`
		UPDATE workflow_effects
		SET status = 'failed',
		    attempts = attempts + 1,
		    last_error = ?,
		    leased_by = NULL,
		    leased_until = NULL,
		    lease_token = NULL,
		    updated_at = ?
		WHERE id = ?
	`, reason, now, id)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected != 1 {
		return nil, fmt.Errorf("effect not found: %s", id)
	}
	return s.ShowEffect(id)
}

func (s *Service) RetryEffect(id string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'pending', leased_by = NULL, leased_until = NULL, lease_token = NULL, last_error = NULL, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	return s.ShowEffect(id)
}

func (s *Service) Transition(taskSelector, transitionID string, opts TransitionOptions) (TransitionResult, error) {
	return s.TransitionForSelectors(taskSelector, "", transitionID, opts)
}

func (s *Service) TransitionForSelectors(taskSelector, instanceID, transitionID string, opts TransitionOptions) (TransitionResult, error) {
	if err := s.EnsureBuiltinTemplateForSelectors(taskSelector, instanceID, opts.PrincipalRef); err != nil {
		return nil, err
	}
	requestHash := transitionRequestHash(taskSelector, instanceID, transitionID, opts)
	var result TransitionResult
	var webhookCtx *webhooks.EventContext
	var webhookTaskUUID string
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		resultTaskSelector := taskSelector
		if strings.TrimSpace(resultTaskSelector) == "" {
			resultTaskSelector = inst.TaskRef
		}
		if opts.IdempotencyKey != "" {
			replayed, err := replayTransitionResult(tx, inst.ID, opts.IdempotencyKey, requestHash)
			if err != nil {
				return err
			}
			if replayed != nil {
				result = replayed
				return nil
			}
		}
		if opts.ExpectRevision != nil && *opts.ExpectRevision != inst.Revision {
			return staleRevisionError(inst.ID, *opts.ExpectRevision, inst.Revision)
		}
		tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
		if err != nil {
			return err
		}
		tr, err := findTransition(tpl, transitionID)
		if err != nil {
			return err
		}
		task, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		ev, err := listEvidenceTx(tx, inst.ID)
		if err != nil {
			return err
		}
		obl, err := listObligationsTx(tx, inst.ID, true)
		if err != nil {
			return err
		}
		checks := map[string]CheckRun{}
		if opts.RunChecks {
			for _, checkID := range tr.Checks {
				check, ok := tpl.Checks[checkID]
				if !ok {
					return fmt.Errorf("check not found: %s", checkID)
				}
				cr, err := s.executeCheck(inst, tr, checkID, check, opts.PrincipalRef, opts.Role, opts.HookCatalog, opts.TemplateDir, false)
				if err != nil {
					return err
				}
				checks[checkID] = *cr
			}
		} else {
			for _, checkID := range tr.Checks {
				if latest, ok := latestCheckFor(s.db, inst.ID, tr.ID, checkID); ok {
					checks[checkID] = latest
				}
			}
		}
		for _, id := range opts.CheckIDs {
			c, err := s.ShowCheckRun(id)
			if err != nil {
				return err
			}
			checks[c.CheckID] = *c
		}
		decision, err := s.EvaluateTransitionDecision(TransitionDecisionInput{
			Instance:           inst,
			Template:           tpl,
			Transition:         *tr,
			Task:               task,
			Evidence:           ev,
			Obligations:        obl,
			Checks:             checks,
			Role:               opts.Role,
			PrincipalRef:       opts.PrincipalRef,
			RoleQuery:          tx,
			DependencyQuery:    tx,
			CheckDatabase:      s.db,
			RequireRoleBinding: true,
		})
		if err != nil {
			return err
		}
		if !decision.Legal {
			return transitionDecisionError(inst.ID, transitionID, opts.Role, decision)
		}
		chosen := decision.Outcome
		if opts.DryRun {
			result = map[string]interface{}{"dryRun": true, "transition": transitionID, "outcome": chosen.ID, "state": chosen.To}
			return nil
		}

		nextRevision := inst.Revision + 1
		now := s.now().Format(time.RFC3339)
		updated := *inst
		updated.Status = chosen.To.Status
		updated.Phase = chosen.To.Phase
		updated.Outcome = chosen.To.Outcome
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
			    updated_at = ?, closed_at = ?
			WHERE id = ? AND revision = ?
		`, updated.Status, nullIfEmpty(updated.Phase), nullIfEmpty(updated.Outcome), updated.Revision, updated.TaskDocEtag, updated.TaskDocHash, updated.UpdatedAt, nullIfEmpty(updated.ClosedAt), updated.ID, inst.Revision)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			actual, loadErr := instanceRevisionTx(tx, updated.ID)
			if loadErr != nil {
				return loadErr
			}
			expected := inst.Revision
			if opts.ExpectRevision != nil {
				expected = *opts.ExpectRevision
			}
			return staleRevisionError(updated.ID, expected, actual.revision)
		}

		createdObligations := make([]Obligation, 0, len(chosen.Obligations))
		for _, ob := range chosen.Obligations {
			id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
			if err != nil {
				return err
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
				return err
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
				return err
			}
			seq, err := nextEffectSequenceTx(tx, updated.ID)
			if err != nil {
				return err
			}
			renderedEffect, semanticKey, err := renderEffectSpec(ef, effectRenderContext{
				instance: updated, outcomeID: chosen.ID, runID: opts.RunID, sequence: seq,
			})
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(renderedEffect)
			key := fmt.Sprintf("%s:%s", updated.ID, semanticKey)
			_, err = tx.Exec(`
				INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)
			`, id, updated.ID, updated.Revision, seq, renderedEffect.Kind, string(payload), key, semanticKey, now, now)
			if err != nil {
				return err
			}
			createdEffects = append(createdEffects, Effect{
				ID: id, InstanceID: updated.ID, Revision: updated.Revision, Sequence: seq, Kind: renderedEffect.Kind, Payload: json.RawMessage(payload),
				Status: "pending", IdempotencyKey: key, SemanticKey: semanticKey, CreatedAt: now, UpdatedAt: now,
			})
		}
		eventID, err := nextSeqID(tx, "workflow_event_seq", "wfe")
		if err != nil {
			return err
		}
		result = transitionResultMap(taskSelector, updated, eventID, createdEffects, createdObligations)
		result["task"] = resultTaskSelector
		result["idempotent"] = false
		result["transition"] = transitionID
		result["outcome"] = chosen.ID
		eventPayload := map[string]interface{}{"transition": transitionID, "outcome": chosen.ID, "from": inst.State(), "to": updated.State()}
		resultJSON, _ := json.Marshal(result)
		eventMeta, err := insertTransitionEventWithID(tx, eventID, updated.ID, opts.PrincipalRef, opts.Role, opts.RunID, inst.Revision, updated.Revision, opts.IdempotencyKey, requestHash, string(resultJSON), task.ETag, updated.TaskDocHash, eventPayload)
		if err != nil {
			return err
		}
		ctx := workflowTransitionWebhookContext(eventMeta, updated, opts.PrincipalRef, opts.Role, opts.RunID, transitionID, chosen.ID, inst.Revision, updated.Revision, opts.IdempotencyKey, inst.State(), updated.State())
		webhookCtx = &ctx
		webhookTaskUUID = updated.TaskUUID
		return updateTaskWorkflowMeta(tx, updated.TaskUUID, updated, opts.PrincipalRef)
	})
	if err != nil {
		return nil, err
	}
	if webhookCtx != nil && webhookTaskUUID != "" {
		webhooks.DispatchTaskEvent(s.db, webhookTaskUUID, *webhookCtx)
	}
	if result != nil {
		result, err = s.deliverBuiltinTransitionEffects(result, transitionID)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

type instanceRevision struct {
	revision int64
}

func instanceRevisionTx(tx *sql.Tx, instanceID string) (instanceRevision, error) {
	var out instanceRevision
	err := tx.QueryRow(`SELECT revision FROM workflow_instances WHERE id = ?`, instanceID).Scan(&out.revision)
	return out, err
}

func transitionRequestHash(taskSelector, instanceID, transitionID string, opts TransitionOptions) string {
	req := struct {
		Task           string   `json:"task"`
		InstanceID     string   `json:"instanceId,omitempty"`
		Transition     string   `json:"transition"`
		PrincipalRef   string   `json:"principal_ref,omitempty"`
		Role           string   `json:"role,omitempty"`
		ExpectRevision *int64   `json:"expectRevision,omitempty"`
		IdempotencyKey string   `json:"idempotencyKey,omitempty"`
		CheckIDs       []string `json:"checkIds,omitempty"`
		RunChecks      bool     `json:"runChecks,omitempty"`
		DryRun         bool     `json:"dryRun,omitempty"`
	}{
		Task: taskSelector, InstanceID: instanceID, Transition: transitionID, PrincipalRef: opts.PrincipalRef, Role: opts.Role,
		ExpectRevision: opts.ExpectRevision, IdempotencyKey: opts.IdempotencyKey,
		CheckIDs: append([]string(nil), opts.CheckIDs...), RunChecks: opts.RunChecks, DryRun: opts.DryRun,
	}
	b, _ := json.Marshal(req)
	return Hash(b)
}

func replayTransitionResult(tx *sql.Tx, instanceID, key, requestHash string) (map[string]interface{}, error) {
	var storedHash, resultJSON string
	err := tx.QueryRow(`
		SELECT COALESCE(request_hash,''), COALESCE(result_json,'')
		FROM workflow_events
		WHERE instance_id = ? AND idempotency_key = ?
	`, instanceID, key).Scan(&storedHash, &resultJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if storedHash != requestHash {
		return nil, idempotencyMismatchError(key)
	}
	if strings.TrimSpace(resultJSON) == "" {
		return nil, fmt.Errorf("idempotency result missing for key %q", key)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(resultJSON), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func transitionResultMap(taskSelector string, updated Instance, eventID string, effects []Effect, obligations []Obligation) map[string]interface{} {
	return map[string]interface{}{
		"task":        taskSelector,
		"instanceId":  updated.ID,
		"state":       updated.State(),
		"revision":    updated.Revision,
		"eventId":     eventID,
		"effects":     effects,
		"obligations": obligations,
		"instance":    updated,
	}
}

func insertTransitionEventWithID(tx *sql.Tx, id, instanceID, actor, role, runID string, observed, next int64, key, requestHash, resultJSON string, taskETag int64, taskHash string, payload interface{}) (workflowEventMetadata, error) {
	var seq int64
	_ = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_events WHERE instance_id = ?`, instanceID).Scan(&seq)
	payloadJSON, _ := json.Marshal(payload)
	prevHash := previousEventHashTx(tx, instanceID)
	eventHash := chainedEventHash(prevHash, payloadJSON)
	_, err := tx.Exec(`
		INSERT INTO workflow_events (
			id, instance_id, seq, schema_version, type, actor, principal_ref, role, run_id,
			observed_revision, next_revision, task_doc_etag, task_doc_hash,
			idempotency_key, request_hash, result, result_json, payload_json, prev_event_hash, event_hash
		) VALUES (?, ?, ?, 'wrkf.workflow-event.v0', 'workflow.transitioned', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'committed', ?, ?, ?, ?)
	`, id, instanceID, seq, emptyToNil(actor), emptyToNil(actor), emptyToNil(role), emptyToNil(runID), observed, next, fmt.Sprint(taskETag), taskHash, emptyToNil(key), nullIfEmpty(requestHash), nullIfEmpty(resultJSON), string(payloadJSON), nullIfEmpty(prevHash), eventHash)
	if err != nil {
		return workflowEventMetadata{}, err
	}
	var createdAt string
	if err := tx.QueryRow(`SELECT created_at FROM workflow_events WHERE id = ?`, id).Scan(&createdAt); err != nil {
		return workflowEventMetadata{}, err
	}
	return workflowEventMetadata{
		ID:            id,
		Seq:           seq,
		SchemaVersion: "wrkf.workflow-event.v0",
		Type:          "workflow.transitioned",
		CreatedAt:     createdAt,
		Payload:       payload,
	}, nil
}

func (s *Service) ShowCheckRun(id string) (*CheckRun, error) {
	var c CheckRun
	var exit sql.NullInt64
	var hook, outcome, code, summary, facts, actor, role, runID, completed sql.NullString
	err := s.db.QueryRow(`
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
		FROM workflow_check_runs WHERE id = ?
	`, id).Scan(&c.ID, &c.InstanceID, &c.TransitionID, &c.CheckID, &hook, &c.InputHash, &exit, &c.Verdict, &outcome, &code, &summary, &facts, &actor, &role, &runID, &c.StartedAt, &completed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("check run not found: %s", id)
		}
		return nil, err
	}
	c.HookID = hook.String
	if exit.Valid {
		v := int(exit.Int64)
		c.ExitCode = &v
	}
	c.Outcome, c.Code, c.Summary = outcome.String, code.String, summary.String
	if facts.Valid {
		c.Facts = json.RawMessage(facts.String)
	}
	c.PrincipalRef, c.Role, c.RunID, c.CompletedAt = actor.String, role.String, runID.String, completed.String
	return &c, nil
}

func (s *Service) ListCheckRuns(taskSelector, transitionID string) ([]CheckRun, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
		FROM workflow_check_runs WHERE instance_id = ?`
	args := []interface{}{inst.ID}
	if transitionID != "" {
		query += ` AND transition_id = ?`
		args = append(args, transitionID)
	}
	query += ` ORDER BY started_at, id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CheckRun
	for rows.Next() {
		var c CheckRun
		var exit sql.NullInt64
		var hook, outcome, code, summary, facts, actor, role, runID, completed sql.NullString
		if err := rows.Scan(&c.ID, &c.InstanceID, &c.TransitionID, &c.CheckID, &hook, &c.InputHash, &exit, &c.Verdict, &outcome, &code, &summary, &facts, &actor, &role, &runID, &c.StartedAt, &completed); err != nil {
			return nil, err
		}
		c.HookID = hook.String
		if exit.Valid {
			v := int(exit.Int64)
			c.ExitCode = &v
		}
		c.Outcome, c.Code, c.Summary = outcome.String, code.String, summary.String
		if facts.Valid {
			c.Facts = json.RawMessage(facts.String)
		}
		c.PrincipalRef, c.Role, c.RunID, c.CompletedAt = actor.String, role.String, runID.String, completed.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func listEvidenceForInstance(database *db.DB, instanceID string) ([]Evidence, error) {
	rows, err := database.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE instance_id = ? ORDER BY produced_at, id
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvidenceRows(rows)
}

func (s *Service) StartRun(taskSelector, role, actor string, opts StartRunOptions) (*Run, error) {
	return s.StartRunForSelectors(taskSelector, "", role, actor, opts)
}

func (s *Service) StartRunForSelectors(taskSelector, instanceID, role, actor string, opts StartRunOptions) (*Run, error) {
	var run *Run
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		requestHash := runStartRequestHash(inst.ID, role, actor, opts)
		if opts.IdempotencyKey != "" {
			existing, existingHash, err := selectRunByInstanceIdempotencyKey(tx, inst.ID, opts.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing != nil {
				if existingHash != requestHash {
					return idempotencyMismatchError(opts.IdempotencyKey)
				}
				run = existing
				return nil
			}
		}

		id, err := nextSeqID(tx, "workflow_run_seq", "run")
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_runs (
				id, instance_id, role, actor, principal_ref, delivery_ref, lane, external_run_ref,
				status, started_at, idempotency_key, request_hash, action,
				lease_owner, lease_token, lease_expires_at, heartbeat_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, inst.ID, role, actor, actor, nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), nullIfEmpty(opts.ExternalRunRef), now, nullIfEmpty(opts.IdempotencyKey), nullIfEmpty(requestHash), nullIfEmpty(opts.Action), nullIfEmpty(opts.LeaseOwner), nullIfEmpty(opts.LeaseToken), nullIfEmpty(opts.LeaseExpiresAt), nullIfEmpty(opts.HeartbeatAt))
		if err != nil {
			if isRunUniqueConflict(err) {
				return idempotencyMismatchError(opts.IdempotencyKey)
			}
			return err
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_role_bindings (instance_id, role, actor, principal_ref, delivery_ref, lane, binding_mode, bound_at)
			VALUES (?, ?, ?, ?, ?, ?, 'auto', ?)
			ON CONFLICT(instance_id, role, actor) DO UPDATE SET
				principal_ref = excluded.principal_ref,
				delivery_ref = COALESCE(excluded.delivery_ref, workflow_role_bindings.delivery_ref),
				lane = COALESCE(excluded.lane, workflow_role_bindings.lane)
		`, inst.ID, role, actor, actor, nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), now)
		if err != nil {
			return err
		}
		run = &Run{ID: id, InstanceID: inst.ID, Role: role, PrincipalRef: actor, DeliveryRef: opts.DeliveryRef, Lane: opts.Lane, ExternalRunRef: opts.ExternalRunRef, Action: opts.Action, Status: "active", StartedAt: now, LeaseOwner: opts.LeaseOwner, LeaseToken: opts.LeaseToken, LeaseExpiresAt: opts.LeaseExpiresAt, HeartbeatAt: opts.HeartbeatAt}
		if _, err := insertEventReturning(tx, inst.ID, "workflow.run_started", actor, role, id, inst.Revision, inst.Revision, opts.IdempotencyKey, taskDocEtagInt(inst), inst.TaskDocHash, runLifecyclePayload(run)); err != nil {
			return err
		}
		return nil
	})
	return run, err
}

func (s *Service) BindExternal(id, externalRunRef string, opts BindExternalOptions) (*Run, error) {
	if strings.TrimSpace(externalRunRef) == "" {
		return nil, fmt.Errorf("externalRunRef is required")
	}
	var run *Run
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := selectRunByID(tx, id)
		if err != nil {
			return err
		}
		if current.ExternalRunRef != "" && current.ExternalRunRef != externalRunRef {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		if opts.DeliveryRef != "" && current.DeliveryRef != "" && current.DeliveryRef != opts.DeliveryRef {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		if opts.Lane != "" && current.Lane != "" && current.Lane != opts.Lane {
			return idempotencyMismatchError(opts.IdempotencyKey)
		}
		deliveryRef := current.DeliveryRef
		if opts.DeliveryRef != "" {
			deliveryRef = opts.DeliveryRef
		}
		lane := current.Lane
		if opts.Lane != "" {
			lane = opts.Lane
		}
		_, err = tx.Exec(`
			UPDATE workflow_runs
			SET external_run_ref = ?, delivery_ref = ?, lane = ?
			WHERE id = ?
		`, externalRunRef, nullIfEmpty(deliveryRef), nullIfEmpty(lane), id)
		if err != nil {
			if isRunUniqueConflict(err) {
				return idempotencyMismatchError(opts.IdempotencyKey)
			}
			return err
		}
		_, err = tx.Exec(`
			UPDATE workflow_role_bindings
			SET delivery_ref = COALESCE(NULLIF(?, ''), delivery_ref), lane = COALESCE(NULLIF(?, ''), lane)
			WHERE instance_id = ? AND role = ? AND principal_ref = ?
		`, deliveryRef, lane, current.InstanceID, current.Role, current.PrincipalRef)
		if err != nil {
			return err
		}
		current.ExternalRunRef = externalRunRef
		current.DeliveryRef = deliveryRef
		current.Lane = lane
		run = current
		return nil
	})
	return run, err
}

func (s *Service) FinishRun(id, status, summary string) (*Run, error) {
	if status == "" {
		status = "completed"
	}
	now := s.now().Format(time.RFC3339)
	var run *Run
	if err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := selectRunByID(tx, id)
		if err != nil {
			return err
		}
		if isTerminalRunStatus(current.Status) {
			if current.Status == status && current.TerminalResult == summary {
				run = current
				return nil
			}
			return idempotencyMismatchError(id)
		}
		if _, err := tx.Exec(`UPDATE workflow_runs SET status = ?, terminal_result = ?, completed_at = ?, lease_token = NULL WHERE id = ?`, status, summary, now, id); err != nil {
			return err
		}
		current.Status = status
		current.TerminalResult = summary
		current.CompletedAt = now
		current.LeaseToken = ""
		inst, err := instanceByIDQuery(tx, current.InstanceID)
		if err != nil {
			return err
		}
		if _, err := insertEventReturning(tx, current.InstanceID, "workflow.run_finished", current.PrincipalRef, current.Role, current.ID, inst.Revision, inst.Revision, "", taskDocEtagInt(inst), inst.TaskDocHash, runLifecyclePayload(current)); err != nil {
			return err
		}
		run = current
		return nil
	}); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) FailRun(id, summary string) (*Run, error) {
	return s.FinishRun(id, "failed", summary)
}

func (s *Service) ShowRun(id string) (*Run, error) {
	var r Run
	err := s.db.QueryRow(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs WHERE id = ?
	`, id).Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, err
	}
	return &r, nil
}

type runRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanRun(scanner runRowScanner) (*Run, error) {
	var r Run
	err := scanner.Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func selectRunByID(tx *sql.Tx, id string) (*Run, error) {
	run, err := scanRun(tx.QueryRow(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs WHERE id = ?
	`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, err
	}
	return run, nil
}

func selectRunByInstanceIdempotencyKey(tx *sql.Tx, instanceID, key string) (*Run, string, error) {
	var r Run
	var requestHash string
	err := tx.QueryRow(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,''), COALESCE(request_hash,'')
		FROM workflow_runs WHERE instance_id = ? AND idempotency_key = ?
	`, instanceID, key).Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt, &requestHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}
	return &r, requestHash, nil
}

func runStartRequestHash(instanceID, role, actor string, opts StartRunOptions) string {
	payload := struct {
		InstanceID      string `json:"instanceId"`
		Role            string `json:"role"`
		PrincipalRef    string `json:"principal_ref"`
		DeliveryRef     string `json:"deliveryRef,omitempty"`
		Lane            string `json:"lane,omitempty"`
		ExternalRunRef  string `json:"externalRunRef,omitempty"`
		Action          string `json:"action,omitempty"`
		LeaseOwner      string `json:"leaseOwner,omitempty"`
		LeaseMs         int64  `json:"leaseMs,omitempty"`
		LeaseRequested  bool   `json:"leaseRequested,omitempty"`
		IdempotencySalt string `json:"idempotencySalt"`
	}{
		InstanceID:      instanceID,
		Role:            role,
		PrincipalRef:    actor,
		DeliveryRef:     opts.DeliveryRef,
		Lane:            opts.Lane,
		ExternalRunRef:  opts.ExternalRunRef,
		Action:          opts.Action,
		LeaseOwner:      opts.LeaseOwner,
		LeaseMs:         opts.LeaseMs,
		LeaseRequested:  opts.LeaseOwner != "" || opts.LeaseExpiresAt != "",
		IdempotencySalt: "workflow.run.start.v1",
	}
	b, _ := json.Marshal(payload)
	return Hash(b)
}

func isTerminalRunStatus(status string) bool {
	return status != "" && status != "active"
}

func isRunUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") &&
		(strings.Contains(msg, "workflow_runs.external_run_ref") ||
			strings.Contains(msg, "workflow_runs.instance_id") ||
			strings.Contains(msg, "workflow_runs.idempotency_key"))
}

func (s *Service) ListRuns(taskSelector string) ([]Run, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       COALESCE(action,''), status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,''),
		       COALESCE(lease_owner,''), COALESCE(lease_token,''), COALESCE(lease_expires_at,''), COALESCE(heartbeat_at,'')
		FROM workflow_runs WHERE instance_id = ? ORDER BY started_at, id
	`, inst.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.InstanceID, &r.Role, &r.PrincipalRef, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Action, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult, &r.LeaseOwner, &r.LeaseToken, &r.LeaseExpiresAt, &r.HeartbeatAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) SupervisorCall(taskSelector, reason string) (*Effect, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var effect *Effect
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		key := fmt.Sprintf("%s:%d:supervisor:%s", inst.ID, inst.Revision, id)
		_, err = tx.Exec(`INSERT INTO workflow_effects (id, instance_id, revision, kind, payload_json, status, idempotency_key) VALUES (?, ?, ?, 'supervisor_call', ?, 'pending', ?)`, id, inst.ID, inst.Revision, string(payload), key)
		if err != nil {
			return err
		}
		effect = &Effect{ID: id, InstanceID: inst.ID, Revision: inst.Revision, Kind: "supervisor_call", Payload: payload, Status: "pending", IdempotencyKey: key}
		return nil
	})
	return effect, err
}

func (s *Service) SupervisorEscalate(taskSelector, reason string) (*Effect, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var effect *Effect
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		key := fmt.Sprintf("%s:%d:escalate:%s", inst.ID, inst.Revision, id)
		_, err = tx.Exec(`INSERT INTO workflow_effects (id, instance_id, revision, kind, payload_json, status, idempotency_key) VALUES (?, ?, ?, 'supervisor_escalation', ?, 'pending', ?)`, id, inst.ID, inst.Revision, string(payload), key)
		if err != nil {
			return err
		}
		effect = &Effect{ID: id, InstanceID: inst.ID, Revision: inst.Revision, Kind: "supervisor_escalation", Payload: payload, Status: "pending", IdempotencyKey: key}
		return nil
	})
	return effect, err
}

func (s *Service) DiffTemplateFiles(oldPath, newPath string) (map[string]interface{}, error) {
	oldTpl, _, oldHash, err := LoadTemplateFile(oldPath)
	if err != nil {
		return nil, err
	}
	newTpl, _, newHash, err := LoadTemplateFile(newPath)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"old":      map[string]string{"id": oldTpl.ID, "version": oldTpl.Version, "hash": oldHash},
		"new":      map[string]string{"id": newTpl.ID, "version": newTpl.Version, "hash": newHash},
		"sameHash": oldHash == newHash,
	}, nil
}

func StateMap(st State) map[string]string {
	out := map[string]string{"status": st.Status}
	if st.Phase != "" {
		out["phase"] = st.Phase
	}
	if st.Outcome != "" {
		out["outcome"] = st.Outcome
	}
	return out
}

func JoinErrors(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}
