package domain

import (
	"fmt"
	"strings"
	"time"
)

// PromiseState is the durable lifecycle state of an attention promise.
// Readiness is derived from ReviewAt and is never stored as a state.
type PromiseState string

const (
	PromiseStateOpen      PromiseState = "open"
	PromiseStateResolved  PromiseState = "resolved"
	PromiseStateAbandoned PromiseState = "abandoned"
)

// Promise is a principal-owned commitment to revisit a durable text subject.
// It may reference at most one live task or container while retaining Subject
// as the permanent snapshot if that target is detached or purged.
type Promise struct {
	UUID                  string       `json:"uuid" db:"uuid"`
	ID                    string       `json:"id" db:"id"`
	OwnerPrincipalRef     string       `json:"owner_principal_ref" db:"owner_principal_ref"`
	Subject               string       `json:"subject" db:"subject"`
	ReviewQuestion        *string      `json:"review_question,omitempty" db:"review_question"`
	SubjectTaskUUID       *string      `json:"subject_task_uuid,omitempty" db:"subject_task_uuid"`
	SubjectContainerUUID  *string      `json:"subject_container_uuid,omitempty" db:"subject_container_uuid"`
	ReviewAt              string       `json:"review_at" db:"review_at"`
	State                 PromiseState `json:"state" db:"state"`
	ClosedAt              *string      `json:"closed_at,omitempty" db:"closed_at"`
	LastReviewedAt        *string      `json:"last_reviewed_at,omitempty" db:"last_reviewed_at"`
	LastReviewNote        *string      `json:"last_review_note,omitempty" db:"last_review_note"`
	Meta                  *string      `json:"meta,omitempty" db:"meta"`
	ETag                  int64        `json:"etag" db:"etag"`
	CreatedAt             string       `json:"created_at" db:"created_at"`
	UpdatedAt             string       `json:"updated_at" db:"updated_at"`
	CreatedByPrincipalRef string       `json:"created_by_principal_ref" db:"created_by_principal_ref"`
	CreatedByScopeRef     *string      `json:"created_by_scope_ref,omitempty" db:"created_by_scope_ref"`
	UpdatedByPrincipalRef string       `json:"updated_by_principal_ref" db:"updated_by_principal_ref"`
	UpdatedByScopeRef     *string      `json:"updated_by_scope_ref,omitempty" db:"updated_by_scope_ref"`
}

// NormalizePromiseReviewAt validates an offset-aware RFC3339 instant using the
// domain timestamp contract and returns the canonical UTC, whole-second form
// required by promise storage and lexical ready queries.
func NormalizePromiseReviewAt(value string) (string, error) {
	parsed, err := ValidateTimestamp(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"), nil
}

// ValidatePromiseState validates the stored promise lifecycle vocabulary.
func ValidatePromiseState(state PromiseState) error {
	switch state {
	case PromiseStateOpen, PromiseStateResolved, PromiseStateAbandoned:
		return nil
	default:
		return fmt.Errorf("invalid promise state %q: must be one of: open, resolved, abandoned", state)
	}
}

// ValidatePromiseFields validates the storage-independent promise invariants
// and returns the canonical review timestamp for the caller to persist.
func ValidatePromiseFields(ownerPrincipalRef, subject, reviewAt string, state PromiseState, taskUUID, containerUUID *string) (string, error) {
	if strings.TrimSpace(ownerPrincipalRef) == "" {
		return "", fmt.Errorf("owner_principal_ref is required")
	}
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("promise subject is required")
	}
	if taskUUID != nil && containerUUID != nil {
		return "", fmt.Errorf("promise may reference at most one task or container")
	}
	if err := ValidatePromiseState(state); err != nil {
		return "", err
	}
	return NormalizePromiseReviewAt(reviewAt)
}
