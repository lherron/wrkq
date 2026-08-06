package workflow

import "encoding/json"

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

type ObligationStatusOptions struct {
	PrincipalRef string
	Role         string
}

type EffectDelivery struct {
	Effect   *Effect         `json:"effect"`
	Binding  *Run            `json:"binding,omitempty"`
	Receipt  json.RawMessage `json:"receipt,omitempty"`
	ExitCode int             `json:"exitCode"`
	Stdout   string          `json:"stdout,omitempty"`
	Stderr   string          `json:"stderr,omitempty"`
}

type effectRenderContext struct {
	instance  Instance
	outcomeID string
	runID     string
	sequence  int64
}

type instanceRevision struct {
	revision int64
}

type runRowScanner interface {
	Scan(dest ...interface{}) error
}
