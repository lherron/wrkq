package workflow

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

func (s *Service) ListObligations(taskSelector string, includeClosed bool) ([]Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	obl, err := listObligationsForInstance(s.db, inst.ID, includeClosed)
	if err != nil {
		return nil, err
	}
	ev, err := listEvidenceForInstance(s.db, inst.ID)
	if err != nil {
		return nil, err
	}
	obl = s.withDelegatedTaskClosureState(obl, includeClosed)
	obl = withCoordinatorSmokeExecutionState(obl, ev, includeClosed)
	obl = withObserverCompletionReviewState(obl, ev, includeClosed)
	obl = append(obl, coordinatorSmokeExecutionObligations(inst, ev, obl, includeClosed)...)
	obl = append(obl, observerCompletionReviewObligations(inst, ev, obl, includeClosed)...)
	obl = append(obl, s.delegatedTaskClosureObligations(inst, ev, obl, includeClosed)...)
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
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_actor,''), blocking, status,
		       COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''), created_at, updated_at
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
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_actor,''), blocking, status,
		       COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''), created_at, updated_at
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
		if err := rows.Scan(&o.ID, &o.InstanceID, &o.Kind, &o.OwnerRole, &o.OwnerActor, &blocking, &o.Status, &o.Reason, &o.SatisfiedByEvidenceID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Blocking = blocking == 1
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
		SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_actor,''), blocking, status,
		       COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''), created_at, updated_at
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

func (s *Service) SetObligationStatus(taskSelector, id, status, evidenceID, reason string) (*Obligation, error) {
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
		_, err := tx.Exec(`
			UPDATE workflow_obligations
			SET status = ?, satisfied_by_evidence_id = COALESCE(NULLIF(?, ''), satisfied_by_evidence_id),
			    reason = COALESCE(NULLIF(?, ''), reason), updated_at = ?
			WHERE id = ? AND instance_id = ?
		`, status, evidenceID, reason, now, id, inst.ID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(`
			SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_actor,''), blocking, status,
			       COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''), created_at, updated_at
			FROM workflow_obligations WHERE id = ?
		`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		list, err := scanObligations(rows)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("obligation not found: %s", id)
		}
		out = &list[0]
		return nil
	})
	return out, err
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
			INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, owner_actor, blocking, status, reason)
			VALUES (?, ?, ?, ?, ?, ?, 'open', ?)
		`, id, inst.ID, kind, nullIfEmpty(ownerRole), nullIfEmpty(ownerActor), blockingInt, nullIfEmpty(reason))
		if err != nil {
			return err
		}
		rows, err := tx.Query(`
			SELECT id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_actor,''), blocking, status,
			       COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''), created_at, updated_at
			FROM workflow_obligations WHERE id = ?
		`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		list, err := scanObligations(rows)
		if err != nil {
			return err
		}
		out = &list[0]
		return nil
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
		SELECT id, instance_id, revision, kind, payload_json, status, idempotency_key, attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''),
		       created_at, updated_at
		FROM workflow_effects WHERE instance_id = ?`
	if !all {
		query += ` AND status IN ('pending','leased','failed','delivered')`
	}
	query += ` ORDER BY created_at, id`
	rows, err := database.Query(query, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEffects(rows)
}

func listEffectsTx(tx *sql.Tx, instanceID string, all bool) ([]Effect, error) {
	query := `
		SELECT id, instance_id, revision, kind, payload_json, status, idempotency_key, attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''),
		       created_at, updated_at
		FROM workflow_effects WHERE instance_id = ?`
	if !all {
		query += ` AND status IN ('pending','leased','failed','delivered')`
	}
	query += ` ORDER BY created_at, id`
	rows, err := tx.Query(query, instanceID)
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
		var payload string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Revision, &e.Kind, &payload, &e.Status, &e.IdempotencyKey, &e.Attempts, &e.LeasedBy, &e.LeasedUntil, &e.DeliveredAt, &e.LastError, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) ShowEffect(id string) (*Effect, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, revision, kind, payload_json, status, idempotency_key, attempts,
		       COALESCE(leased_by,''), COALESCE(leased_until,''), COALESCE(delivered_at,''), COALESCE(last_error,''),
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

func (s *Service) AckEffect(id, adapter string) (*Effect, error) {
	current, err := s.ShowEffect(id)
	if err != nil {
		return nil, err
	}
	if current.Kind == "request_observer_review" && current.Status == "pending" {
		return nil, fmt.Errorf("request_observer_review must be delivered with wrkf effect deliver before ack")
	}
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'delivered', delivered_at = ?, attempts = attempts + 1, leased_by = ?, updated_at = ? WHERE id = ?`, now, adapter, now, id); err != nil {
		return nil, err
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

func (s *Service) DeliverEffect(id, adapter string, catalog *HookCatalog, templateDir string) (*EffectDelivery, error) {
	eff, err := s.ShowEffect(id)
	if err != nil {
		return nil, err
	}
	if eff.Status == "delivered" {
		return &EffectDelivery{Effect: eff}, nil
	}
	if eff.Status != "pending" && eff.Status != "failed" {
		return nil, fmt.Errorf("effect %s is not deliverable from status %s", id, eff.Status)
	}
	if catalog == nil {
		return nil, fmt.Errorf("hook catalog is required for effect delivery")
	}
	handler, ok := catalog.EffectHandlers[eff.Kind]
	if !ok {
		if h, hookOK := catalog.Hooks["effect_"+eff.Kind]; hookOK {
			handler, ok = h, true
		}
	}
	if !ok {
		return nil, fmt.Errorf("no effect handler registered for %s", eff.Kind)
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
		return out, fmt.Errorf("effect handler failed with exit %d: %s", exit, strings.TrimSpace(string(stderr)))
	}
	now := s.now().Format(time.RFC3339)
	if adapter == "" {
		adapter = "wrkf-effect-deliver"
	}
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'delivered', delivered_at = ?, attempts = attempts + 1, leased_by = ?, last_error = NULL, updated_at = ? WHERE id = ?`, now, adapter, now, id); err != nil {
		return nil, err
	}
	delivered, err := s.ShowEffect(id)
	if err != nil {
		return nil, err
	}
	out.Effect = delivered
	return out, nil
}

func (s *Service) latestRunForRole(instanceID, role string) (*Run, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,'')
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
	if err := rows.Scan(&r.ID, &r.InstanceID, &r.Role, &r.Actor, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult); err != nil {
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

func (s *Service) FailEffect(id, reason string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'failed', attempts = attempts + 1, last_error = ?, updated_at = ? WHERE id = ?`, reason, now, id); err != nil {
		return nil, err
	}
	return s.ShowEffect(id)
}

func (s *Service) RetryEffect(id string) (*Effect, error) {
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_effects SET status = 'pending', leased_by = NULL, leased_until = NULL, last_error = NULL, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	return s.ShowEffect(id)
}

func (s *Service) Transition(taskSelector, transitionID string, opts TransitionOptions) (map[string]interface{}, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	if opts.ExpectRevision != nil && *opts.ExpectRevision != inst.Revision {
		return nil, fmt.Errorf("workflow revision mismatch: expected %d, got %d", *opts.ExpectRevision, inst.Revision)
	}
	if opts.ContextHash != "" && opts.ContextHash != inst.ContextHash {
		return nil, fmt.Errorf("workflow context hash mismatch")
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return nil, err
	}
	if !stateMatches(*inst, tr.From) {
		return nil, fmt.Errorf("transition %s cannot run from current state", transitionID)
	}
	if !roleAllowed(opts.Role, tr.By) {
		return nil, fmt.Errorf("role %s is not allowed for transition %s", opts.Role, transitionID)
	}
	if opts.IdempotencyKey != "" {
		var existing string
		err := s.db.QueryRow(`SELECT id FROM workflow_events WHERE instance_id = ? AND idempotency_key = ?`, inst.ID, opts.IdempotencyKey).Scan(&existing)
		if err == nil {
			return map[string]interface{}{"idempotent": true, "eventId": existing, "instance": inst, "state": inst.State(), "revision": inst.Revision}, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	ev, _ := s.ListEvidence(taskSelector)
	obl, _ := s.ListObligations(taskSelector, true)
	blockers := transitionBlockers(*tr, ev, obl)
	if len(blockers) > 0 {
		return nil, fmt.Errorf("transition is blocked: %s", blockers[0].Message)
	}
	checks := map[string]CheckRun{}
	if opts.RunChecks {
		for _, checkID := range tr.Checks {
			check, ok := tpl.Checks[checkID]
			if !ok {
				return nil, fmt.Errorf("check not found: %s", checkID)
			}
			cr, err := s.executeCheck(inst, tr, checkID, check, opts.Actor, opts.Role, opts.HookCatalog, opts.TemplateDir, true)
			if err != nil {
				return nil, err
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
			return nil, err
		}
		checks[c.CheckID] = *c
	}
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	for _, guard := range tr.Guards {
		if !evalPredicate(guard, evalContext{Evidence: ev, Obligations: obl, Checks: checks, Task: task}) {
			return nil, fmt.Errorf("transition guard failed")
		}
	}
	var chosen *OutcomeCase
	for i := range tr.Outcomes {
		out := &tr.Outcomes[i]
		if evalPredicate(out.When, evalContext{Evidence: ev, Obligations: obl, Checks: checks, Task: task}) {
			chosen = out
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("no transition outcome matched")
	}
	if opts.DryRun {
		return map[string]interface{}{"dryRun": true, "transition": transitionID, "outcome": chosen.ID, "state": chosen.To}, nil
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
	}
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		task, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		updated.TaskDocEtag = fmt.Sprint(task.ETag)
		updated.TaskDocHash = taskDocHash(task)
		updated.ContextHash = contextHash(updated.TemplateHash, updated.State(), updated.Revision, updated.TaskDocHash, ev, obl, nil)
		_, err = tx.Exec(`
			UPDATE workflow_instances
			SET status = ?, phase = ?, outcome = ?, revision = ?, context_hash = ?, task_doc_etag = ?, task_doc_hash = ?,
			    updated_at = ?, closed_at = ?
			WHERE id = ?
		`, updated.Status, nullIfEmpty(updated.Phase), nullIfEmpty(updated.Outcome), updated.Revision, updated.ContextHash, updated.TaskDocEtag, updated.TaskDocHash, updated.UpdatedAt, nullIfEmpty(updated.ClosedAt), updated.ID)
		if err != nil {
			return err
		}
		for _, ob := range chosen.Obligations {
			id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
			if err != nil {
				return err
			}
			blocking := 0
			if ob.Blocking {
				blocking = 1
			}
			_, err = tx.Exec(`
				INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, owner_actor, blocking, status, reason)
				VALUES (?, ?, ?, ?, ?, ?, 'open', ?)
			`, id, updated.ID, ob.Kind, nullIfEmpty(ob.OwnerRole), nullIfEmpty(ob.OwnerActor), blocking, nullIfEmpty(ob.Reason))
			if err != nil {
				return err
			}
		}
		for _, ef := range chosen.Effects {
			id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(ef)
			key := fmt.Sprintf("%s:%d:%s:%s", updated.ID, updated.Revision, chosen.ID, id)
			_, err = tx.Exec(`
				INSERT INTO workflow_effects (id, instance_id, revision, kind, payload_json, status, idempotency_key)
				VALUES (?, ?, ?, ?, ?, 'pending', ?)
			`, id, updated.ID, updated.Revision, ef.Kind, string(payload), key)
			if err != nil {
				return err
			}
		}
		if err := insertEvent(tx, updated.ID, "workflow.transitioned", opts.Actor, opts.Role, "", inst.Revision, updated.Revision, opts.IdempotencyKey, task.ETag, updated.TaskDocHash, updated.ContextHash, map[string]interface{}{"transition": transitionID, "outcome": chosen.ID, "from": inst.State(), "to": updated.State()}); err != nil {
			return err
		}
		return updateTaskWorkflowMeta(tx, updated.TaskUUID, updated, opts.Actor)
	})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"idempotent": false, "transition": transitionID, "outcome": chosen.ID, "state": updated.State(), "revision": updated.Revision, "instance": updated}, nil
}

func (s *Service) ShowCheckRun(id string) (*CheckRun, error) {
	var c CheckRun
	var exit sql.NullInt64
	var hook, outcome, code, summary, facts, actor, role, runID, completed sql.NullString
	err := s.db.QueryRow(`
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
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
	c.Actor, c.Role, c.RunID, c.CompletedAt = actor.String, role.String, runID.String, completed.String
	return &c, nil
}

func (s *Service) ListCheckRuns(taskSelector, transitionID string) ([]CheckRun, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	query := `
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
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
		c.Actor, c.Role, c.RunID, c.CompletedAt = actor.String, role.String, runID.String, completed.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func listEvidenceForInstance(database *db.DB, instanceID string) ([]Evidence, error) {
	rows, err := database.Query(`SELECT id, kind, ref, COALESCE(summary,''), COALESCE(data_json,''), source_json, produced_at FROM workflow_evidence WHERE instance_id = ? ORDER BY produced_at, id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var data, source string
		if err := rows.Scan(&e.ID, &e.Kind, &e.Ref, &e.Summary, &data, &source, &e.ProducedAt); err != nil {
			return nil, err
		}
		if data != "" {
			e.Data = json.RawMessage(data)
		}
		if source != "" {
			e.Source = json.RawMessage(source)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) StartRun(taskSelector, role, actor, deliveryRef, lane string) (*Run, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var run *Run
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_run_seq", "run")
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_runs (id, instance_id, role, actor, delivery_ref, lane, status, started_at)
			VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
		`, id, inst.ID, role, actor, nullIfEmpty(deliveryRef), nullIfEmpty(lane), now)
		if err != nil {
			return err
		}
		run = &Run{ID: id, InstanceID: inst.ID, Role: role, Actor: actor, DeliveryRef: deliveryRef, Lane: lane, Status: "active", StartedAt: now}
		return nil
	})
	return run, err
}

func (s *Service) FinishRun(id, status, summary string) (*Run, error) {
	if status == "" {
		status = "completed"
	}
	now := s.now().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE workflow_runs SET status = ?, terminal_result = ?, completed_at = ? WHERE id = ?`, status, summary, now, id); err != nil {
		return nil, err
	}
	return s.ShowRun(id)
}

func (s *Service) ShowRun(id string) (*Run, error) {
	var r Run
	err := s.db.QueryRow(`
		SELECT id, instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,'')
		FROM workflow_runs WHERE id = ?
	`, id).Scan(&r.ID, &r.InstanceID, &r.Role, &r.Actor, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("run not found: %s", id)
		}
		return nil, err
	}
	return &r, nil
}

func (s *Service) ListRuns(taskSelector string) ([]Run, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), COALESCE(external_run_ref,''),
		       status, started_at, COALESCE(completed_at,''), COALESCE(terminal_result,'')
		FROM workflow_runs WHERE instance_id = ? ORDER BY started_at, id
	`, inst.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.InstanceID, &r.Role, &r.Actor, &r.DeliveryRef, &r.Lane, &r.ExternalRunRef, &r.Status, &r.StartedAt, &r.CompletedAt, &r.TerminalResult); err != nil {
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
