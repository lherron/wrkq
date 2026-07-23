package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type campaignValidation struct {
	taskUUIDs         []string
	enrollmentChange  bool
	residentAdmission bool
	move              bool
	containers        bool
}

// validateEffectiveMembershipTx validates the post-mutation resident-or-enrolled
// campaign set. Callers invoke it inside their transaction after staging the full
// mutation, so an error rolls everything back. Moving into the enrolled campaign
// atomically removes the now-redundant enrollment.
func validateEffectiveMembershipTx(tx *sql.Tx, v campaignValidation) error {
	for _, taskUUID := range v.taskUUIDs {
		var residentUUID string
		var enrolledUUID, residentState, enrolledState sql.NullString
		if err := tx.QueryRow(`
			SELECT t.project_uuid, t.campaign_uuid, resident.campaign_state, enrolled.campaign_state
			  FROM tasks t
			  LEFT JOIN containers resident ON resident.uuid = t.project_uuid
			  LEFT JOIN containers enrolled ON enrolled.uuid = t.campaign_uuid
			 WHERE t.uuid = ?
		`, taskUUID).Scan(&residentUUID, &enrolledUUID, &residentState, &enrolledState); err != nil {
			return fmt.Errorf("failed to validate campaign membership for task %s: %w", taskUUID, err)
		}
		if v.enrollmentChange && enrolledUUID.Valid &&
			(!enrolledState.Valid ||
				(enrolledState.String != CampaignStateDraft && enrolledState.String != CampaignStateActive)) {
			return fmt.Errorf("campaign enrollment target must be a draft or active campaign")
		}
		if v.residentAdmission && residentState.Valid &&
			(residentState.String == CampaignStateCompleted || residentState.String == CampaignStateCancelled) {
			return fmt.Errorf("terminal campaign cannot accept new resident members")
		}
		if !residentState.Valid || !enrolledUUID.Valid {
			continue
		}
		if residentUUID == enrolledUUID.String {
			if _, err := tx.Exec("UPDATE tasks SET campaign_uuid = NULL WHERE uuid = ?", taskUUID); err != nil {
				return fmt.Errorf("failed to clear redundant campaign enrollment: %w", err)
			}
			continue
		}
		if v.move {
			return fmt.Errorf("task is enrolled in a different campaign; unenroll it before moving into campaign %s", residentUUID)
		}
		return fmt.Errorf("task resident in campaign %s cannot enroll in foreign campaign %s", residentUUID, enrolledUUID.String)
	}

	if v.containers {
		// Container mutations can change effective membership for every task in
		// that container at once (most importantly plain -> campaign
		// conversion). Re-run the exclusivity half of the validator over the
		// resulting graph so conversion cannot strand resident tasks in a
		// foreign campaign.
		var taskUUID, residentUUID, enrolledUUID string
		err := tx.QueryRow(`
			SELECT t.uuid, t.project_uuid, t.campaign_uuid
			  FROM tasks t
			  JOIN containers resident ON resident.uuid = t.project_uuid
			 WHERE resident.campaign_state IS NOT NULL
			   AND t.campaign_uuid IS NOT NULL
			   AND t.campaign_uuid != t.project_uuid
			 LIMIT 1
		`).Scan(&taskUUID, &residentUUID, &enrolledUUID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to validate campaign conversion membership: %w", err)
		}
		if err == nil {
			return fmt.Errorf(
				"task %s resident in campaign %s cannot remain enrolled in foreign campaign %s; unenroll it before conversion",
				taskUUID, residentUUID, enrolledUUID,
			)
		}

		var nested string
		err = tx.QueryRow(`
			WITH RECURSIVE ancestors(campaign_uuid, ancestor_uuid) AS (
				SELECT c.uuid, c.parent_uuid FROM containers c WHERE c.campaign_state IS NOT NULL
				UNION ALL
				SELECT a.campaign_uuid, p.parent_uuid
				  FROM ancestors a JOIN containers p ON p.uuid = a.ancestor_uuid
				 WHERE a.ancestor_uuid IS NOT NULL
			)
			SELECT a.campaign_uuid
			  FROM ancestors a JOIN containers parent ON parent.uuid = a.ancestor_uuid
			 WHERE parent.campaign_state IS NOT NULL
			 LIMIT 1
		`).Scan(&nested)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to validate nested campaigns: %w", err)
		}
		if err == nil {
			return fmt.Errorf("campaign containers cannot be nested under another campaign")
		}
	}
	return nil
}

// ValidateTaskResidentAdmissionTx rejects a newly inserted task whose resident
// container is a terminal campaign. Callers outside store use this after staging
// an insert in the same transaction so rejection rolls the insert back.
func ValidateTaskResidentAdmissionTx(tx *sql.Tx, taskUUID string) error {
	return validateEffectiveMembershipTx(tx, campaignValidation{
		taskUUIDs:         []string{taskUUID},
		residentAdmission: true,
	})
}

// StampTaskCampaignContext adds production-time resident container and effective
// campaign fields to an event payload. campaign_uuid is always explicit.
func StampTaskCampaignContext(tx *sql.Tx, taskUUID string, payload map[string]any) error {
	var containerUUID string
	var enrolledUUID, residentState sql.NullString
	if err := tx.QueryRow(`
		SELECT t.project_uuid, t.campaign_uuid, resident.campaign_state
		  FROM tasks t
		  LEFT JOIN containers resident ON resident.uuid = t.project_uuid
		 WHERE t.uuid = ?
	`, taskUUID).Scan(&containerUUID, &enrolledUUID, &residentState); err != nil {
		return fmt.Errorf("failed to load task campaign context: %w", err)
	}
	payload["container_uuid"] = containerUUID
	switch {
	case residentState.Valid:
		payload["campaign_uuid"] = containerUUID
	case enrolledUUID.Valid:
		payload["campaign_uuid"] = enrolledUUID.String
	default:
		payload["campaign_uuid"] = nil
	}
	return nil
}

func stampTaskCampaignJSON(tx *sql.Tx, taskUUID string, payload map[string]any) (string, error) {
	if err := StampTaskCampaignContext(tx, taskUUID, payload); err != nil {
		return "", err
	}
	b, err := json.Marshal(payload)
	return string(b), err
}
