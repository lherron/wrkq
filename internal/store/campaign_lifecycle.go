package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

const (
	CampaignStateDraft     = "draft"
	CampaignStateActive    = "active"
	CampaignStateCompleted = "completed"
	CampaignStateCancelled = "cancelled"

	CampaignCloseNudgeEvent = "container.campaign_close_nudged"
)

// CampaignMemberDiagnostic is a stable, human-actionable member projection used
// by the close guard. Membership is resident or enrolled.
type CampaignMemberDiagnostic struct {
	UUID       string
	ID         string
	Path       string
	State      string
	Membership string
}

// CampaignCloseBlockedError reports the open members that must be
// dispositioned before a completed close. MissingOutcomes is deliberately
// non-blocking and is included for display alongside the blocking set.
type CampaignCloseBlockedError struct {
	Stragglers      []CampaignMemberDiagnostic
	MissingOutcomes []CampaignMemberDiagnostic
}

func (e *CampaignCloseBlockedError) Error() string {
	return fmt.Sprintf("campaign close blocked by %d open member(s)", len(e.Stragglers))
}

// CampaignTransitionResult is the committed campaign state event plus
// non-blocking diagnostics captured in the same transaction.
type CampaignTransitionResult struct {
	ETag            int64
	PreviousState   *string
	CampaignState   string
	MissingOutcomes []CampaignMemberDiagnostic
	EventID         int64
	EventTimestamp  string
}

// ConvertCampaignWithAttribution adorns a plain container as a draft or active
// campaign. The conversion, optional content bodies, effective-membership
// validation, container.updated snapshot, and campaign-state event are one
// write transaction.
func (cs *ContainerStore) ConvertCampaignWithAttribution(
	attr attribution.Attribution,
	containerUUID string,
	targetState string,
	description, specification, labels *string,
	ifMatch int64,
) (*CampaignTransitionResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if targetState == "" {
		targetState = CampaignStateActive
	}
	if targetState != CampaignStateDraft && targetState != CampaignStateActive {
		return nil, fmt.Errorf("campaign conversion state must be draft or active")
	}
	var result *CampaignTransitionResult
	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var currentETag int64
		var currentState sql.NullString
		var kind string
		if err := tx.QueryRow(
			"SELECT etag, campaign_state, kind FROM containers WHERE uuid = ?", containerUUID,
		).Scan(&currentETag, &currentState, &kind); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to load campaign container: %w", err)
		}
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}
		if kind == string(domain.ContainerKindRoot) {
			return fmt.Errorf("root container cannot be converted to a campaign")
		}
		if currentState.Valid {
			return fmt.Errorf("container is already a campaign in state %s", currentState.String)
		}

		setClauses := []string{
			"campaign_state = ?",
			"etag = etag + 1",
			"updated_by_principal_ref = ?",
			"updated_by_scope_ref = ?",
		}
		args := []interface{}{targetState, attr.PrincipalRef, scopeSQL(attr)}
		contentChanged := false
		snapshotFields := map[string]interface{}{}
		if description != nil {
			setClauses = append(setClauses, "description = ?")
			args = append(args, *description)
			contentChanged = true
			snapshotFields["description"] = *description
		}
		if specification != nil {
			setClauses = append(setClauses, "specification = ?")
			if strings.TrimSpace(*specification) == "" {
				args = append(args, nil)
			} else {
				args = append(args, *specification)
			}
			contentChanged = true
			snapshotFields["specification"] = *specification
		}
		if labels != nil {
			setClauses = append(setClauses, "labels = ?")
			args = append(args, *labels)
			contentChanged = true
			snapshotFields["labels"] = *labels
		}
		args = append(args, containerUUID)
		if _, err := tx.Exec(
			"UPDATE containers SET "+strings.Join(setClauses, ", ")+" WHERE uuid = ?", args...,
		); err != nil {
			return fmt.Errorf("failed to convert campaign: %w", err)
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{containers: true}); err != nil {
			return err
		}

		newETag := currentETag + 1
		if contentChanged {
			snapshot, err := containerContentSnapshot(tx, containerUUID, snapshotFields)
			if err != nil {
				return err
			}
			if err := logContainerEvent(tx, ew, attr, containerUUID, "container.updated", newETag, snapshot); err != nil {
				return err
			}
		}
		meta, err := logCampaignStateEvent(tx, ew, attr, containerUUID, nil, targetState, newETag)
		if err != nil {
			return err
		}
		result = &CampaignTransitionResult{
			ETag:           newETag,
			CampaignState:  targetState,
			EventID:        meta.ID,
			EventTimestamp: meta.Timestamp,
		}
		return nil
	})
	if err == nil && result != nil {
		webhooks.DispatchCampaignTransition(
			cs.store.db,
			containerUUID,
			result.PreviousState,
			result.CampaignState,
			events.EventMetadata{ID: result.EventID, Timestamp: result.EventTimestamp},
			attr.PrincipalRef,
		)
	}
	return result, err
}

// TransitionCampaignWithAttribution activates a draft campaign or declares a
// draft/active campaign terminal. The active->completed guard reads effective
// resident+enrolled membership and writes campaign_state under the same
// BEGIN IMMEDIATE transaction.
func (cs *ContainerStore) TransitionCampaignWithAttribution(
	attr attribution.Attribution,
	containerUUID, targetState string,
	ifMatch int64,
) (*CampaignTransitionResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if targetState != CampaignStateActive &&
		targetState != CampaignStateCompleted &&
		targetState != CampaignStateCancelled {
		return nil, fmt.Errorf("campaign target state must be active, completed, or cancelled")
	}

	var result *CampaignTransitionResult
	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var currentETag int64
		var currentState sql.NullString
		if err := tx.QueryRow(
			"SELECT etag, campaign_state FROM containers WHERE uuid = ?", containerUUID,
		).Scan(&currentETag, &currentState); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to load campaign container: %w", err)
		}
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}
		if !currentState.Valid {
			return fmt.Errorf("container is not a campaign; convert it first")
		}
		switch currentState.String {
		case CampaignStateDraft:
			if targetState != CampaignStateActive && targetState != CampaignStateCancelled {
				return fmt.Errorf("draft campaign can only be activated or cancelled")
			}
		case CampaignStateActive:
			if targetState != CampaignStateCompleted && targetState != CampaignStateCancelled {
				return fmt.Errorf("active campaign can only be completed or cancelled")
			}
		default:
			return fmt.Errorf("campaign is %s; terminal campaigns cannot transition", currentState.String)
		}

		missing := []CampaignMemberDiagnostic{}
		if currentState.String == CampaignStateActive && targetState == CampaignStateCompleted {
			members, err := campaignMemberDiagnosticsTx(tx, containerUUID)
			if err != nil {
				return err
			}
			stragglers, completedMissing := classifyCampaignMembers(members)
			missing = completedMissing
			if len(stragglers) > 0 {
				return &CampaignCloseBlockedError{
					Stragglers:      stragglers,
					MissingOutcomes: missing,
				}
			}
		}

		if _, err := tx.Exec(`
			UPDATE containers
			   SET campaign_state = ?,
			       etag = etag + 1,
			       updated_by_principal_ref = ?,
			       updated_by_scope_ref = ?
			 WHERE uuid = ?
		`, targetState, attr.PrincipalRef, scopeSQL(attr), containerUUID); err != nil {
			return fmt.Errorf("failed to close campaign: %w", err)
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{containers: true}); err != nil {
			return err
		}

		newETag := currentETag + 1
		previous := currentState.String
		meta, err := logCampaignStateEvent(tx, ew, attr, containerUUID, &previous, targetState, newETag)
		if err != nil {
			return err
		}
		result = &CampaignTransitionResult{
			ETag:            newETag,
			PreviousState:   &previous,
			CampaignState:   targetState,
			MissingOutcomes: missing,
			EventID:         meta.ID,
			EventTimestamp:  meta.Timestamp,
		}
		return nil
	})
	if err == nil && result != nil {
		webhooks.DispatchCampaignTransition(
			cs.store.db,
			containerUUID,
			result.PreviousState,
			result.CampaignState,
			events.EventMetadata{ID: result.EventID, Timestamp: result.EventTimestamp},
			attr.PrincipalRef,
		)
	}
	return result, err
}

type campaignMemberRow struct {
	CampaignMemberDiagnostic
	outcome sql.NullString
}

func campaignMemberDiagnosticsTx(tx *sql.Tx, campaignUUID string) ([]campaignMemberRow, error) {
	rows, err := tx.Query(`
		SELECT t.uuid, t.id, COALESCE(tp.path, t.slug), t.state, t.outcome,
		       CASE WHEN t.project_uuid = ? THEN 'resident' ELSE 'enrolled' END
		  FROM tasks t
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		 WHERE t.project_uuid = ? OR t.campaign_uuid = ?
		 ORDER BY t.id
	`, campaignUUID, campaignUUID, campaignUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to load campaign members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	members := []campaignMemberRow{}
	for rows.Next() {
		var member campaignMemberRow
		if err := rows.Scan(
			&member.UUID, &member.ID, &member.Path, &member.State,
			&member.outcome, &member.Membership,
		); err != nil {
			return nil, fmt.Errorf("failed to scan campaign member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate campaign members: %w", err)
	}
	return members, nil
}

func classifyCampaignMembers(members []campaignMemberRow) ([]CampaignMemberDiagnostic, []CampaignMemberDiagnostic) {
	stragglers := []CampaignMemberDiagnostic{}
	missing := []CampaignMemberDiagnostic{}
	for _, row := range members {
		switch row.State {
		case "completed", "cancelled", "archived", "deleted":
			if row.State == "completed" && (!row.outcome.Valid || strings.TrimSpace(row.outcome.String) == "") {
				missing = append(missing, row.CampaignMemberDiagnostic)
			}
		default:
			// idea and draft are intentionally open here.
			stragglers = append(stragglers, row.CampaignMemberDiagnostic)
		}
	}
	return stragglers, missing
}

func logCampaignStateEvent(
	tx *sql.Tx,
	ew *events.Writer,
	attr attribution.Attribution,
	containerUUID string,
	from *string,
	to string,
	etag int64,
) (events.EventMetadata, error) {
	payload := map[string]interface{}{
		"campaign_uuid":  containerUUID,
		"container_uuid": containerUUID,
		"from":           from,
		"to":             to,
	}
	return logContainerEventReturning(
		tx, ew, attr, containerUUID, "container.campaign_state_changed", etag, payload,
	)
}

func logContainerEvent(
	tx *sql.Tx,
	ew *events.Writer,
	attr attribution.Attribution,
	containerUUID, eventType string,
	etag int64,
	payload map[string]interface{},
) error {
	_, err := logContainerEventReturning(tx, ew, attr, containerUUID, eventType, etag, payload)
	return err
}

func logContainerEventReturning(
	tx *sql.Tx,
	ew *events.Writer,
	attr attribution.Attribution,
	containerUUID, eventType string,
	etag int64,
	payload map[string]interface{},
) (events.EventMetadata, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to marshal %s payload: %w", eventType, err)
	}
	payloadString := string(payloadJSON)
	meta, err := ew.LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "container",
		ResourceUUID: &containerUUID,
		EventType:    eventType,
		ETag:         &etag,
		Payload:      &payloadString,
	})
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to log %s event: %w", eventType, err)
	}
	return meta, nil
}

func campaignUUIDForTaskTx(tx *sql.Tx, taskUUID string) (string, error) {
	var residentUUID string
	var enrolledUUID, residentState, enrolledState sql.NullString
	if err := tx.QueryRow(`
		SELECT t.project_uuid, t.campaign_uuid, resident.campaign_state, enrolled.campaign_state
		  FROM tasks t
		  LEFT JOIN containers resident ON resident.uuid = t.project_uuid
		  LEFT JOIN containers enrolled ON enrolled.uuid = t.campaign_uuid
		 WHERE t.uuid = ?
	`, taskUUID).Scan(&residentUUID, &enrolledUUID, &residentState, &enrolledState); err != nil {
		return "", fmt.Errorf("failed to load task campaign for close nudge: %w", err)
	}
	if residentState.Valid && residentState.String == CampaignStateActive {
		return residentUUID, nil
	}
	if enrolledUUID.Valid && enrolledState.Valid && enrolledState.String == CampaignStateActive {
		return enrolledUUID.String, nil
	}
	return "", nil
}

// maybeLogCampaignCloseNudgeForTask emits the decision nudge after the task's
// terminal event, while the task mutation transaction still owns the writer
// lock. It never changes campaign_state and never creates a comment.
func maybeLogCampaignCloseNudgeForTask(
	tx *sql.Tx,
	ew *events.Writer,
	attr attribution.Attribution,
	taskUUID string,
) error {
	campaignUUID, err := campaignUUIDForTaskTx(tx, taskUUID)
	if err != nil {
		return err
	}
	return maybeLogCampaignCloseNudge(tx, ew, attr, campaignUUID)
}

func maybeLogCampaignCloseNudge(
	tx *sql.Tx,
	ew *events.Writer,
	attr attribution.Attribution,
	campaignUUID string,
) error {
	if campaignUUID == "" {
		return nil
	}
	var state string
	var etag int64
	if err := tx.QueryRow(
		"SELECT campaign_state, etag FROM containers WHERE uuid = ?", campaignUUID,
	).Scan(&state, &etag); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to load campaign for close nudge: %w", err)
	}
	if state != CampaignStateActive {
		return nil
	}
	var openMembers int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		  FROM tasks
		 WHERE (project_uuid = ? OR campaign_uuid = ?)
		   AND state NOT IN ('completed','cancelled','archived','deleted')
	`, campaignUUID, campaignUUID).Scan(&openMembers); err != nil {
		return fmt.Errorf("failed to count open campaign members: %w", err)
	}
	if openMembers != 0 {
		return nil
	}
	payload := map[string]interface{}{
		"campaign_uuid":  campaignUUID,
		"container_uuid": campaignUUID,
		"reason":         "all_members_terminal",
		"prompt":         "all members terminal — close?",
	}
	return logContainerEvent(tx, ew, attr, campaignUUID, CampaignCloseNudgeEvent, etag, payload)
}
