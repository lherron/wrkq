//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/id"
)

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
		reason := fmt.Sprintf("Await subordinate task %s terminal state before workflow closure evidence; source evidence=%s", taskID, evidenceID)
		if err := insertObligationOnceTx(tx, instanceID, "await_subordinate_closure", "coordinator", taskID, reason); err != nil {
			return err
		}
	}
	return nil
}

func createCoordinatorSmokeExecutionObligationTx(tx *sql.Tx, instanceID, evidenceID string) error {
	reason := fmt.Sprintf("Execute coordinator runbook %s and record coordinator_smoke_execution before report_complete", evidenceID)
	return insertObligationOnceTx(tx, instanceID, "await_coordinator_smoke_execution", "coordinator", evidenceID, reason)
}

func createObserverCompletionReviewObligationTx(tx *sql.Tx, instanceID, evidenceID string) error {
	reason := fmt.Sprintf("External observer must review completion claim %s before report_complete", evidenceID)
	return insertObligationOnceTx(tx, instanceID, "await_observer_completion_review", "observer", evidenceID, reason)
}

// insertObligationOnceTx records an open, non-blocking obligation of kind
// unless one already names subject in its reason.
func insertObligationOnceTx(tx *sql.Tx, instanceID, kind, ownerRole, subject, reason string) error {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(1)
		FROM workflow_obligations
		WHERE instance_id = ? AND kind = ? AND reason LIKE ?
	`, instanceID, kind, "%"+subject+"%").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
		VALUES (?, ?, ?, ?, 0, 'open', ?)
	`, id, instanceID, kind, ownerRole, reason)
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
	return id.FindTaskIDs(text)
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
	return firstEvidenceTokenInObligationText(o)
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
	return firstEvidenceTokenInObligationText(o)
}

// firstEvidenceTokenInObligationText is the legacy fallback for obligations
// that predate structured data: the first ev_ token in the id or reason.
func firstEvidenceTokenInObligationText(o Obligation) string {
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
