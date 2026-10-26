package domain

import (
	"strings"
	"testing"
)

// TestPromiseFieldsRefuseEachInvalidFieldAndNormalizeReviewAt: a valid promise
// comes back with review_at normalized to UTC; each invalid field is refused
// with its own message.
//
// rewrite-held (§1): the surface is wrkq.promise.create / `wrkq promise`
// argv (workrpc, rpccli), where only the invalid-timestamp row has a twin.
func TestPromiseFieldsRefuseEachInvalidFieldAndNormalizeReviewAt(t *testing.T) {
	task, container := "task", "container"
	tests := []struct {
		name         string
		owner        string
		subject      string
		reviewAt     string
		state        PromiseState
		task         *string
		container    *string
		wantReviewAt string
		wantErr      string
	}{
		{name: "valid normalizes offset review_at to UTC", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-24T00:30:00+01:00", state: PromiseStateOpen, wantReviewAt: "2026-08-23T23:30:00Z"},
		{name: "missing owner", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen, wantErr: "owner_principal_ref is required"},
		{name: "blank subject", owner: "agent:cody", subject: "  ", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen, wantErr: "promise subject is required"},
		{name: "invalid timestamp", owner: "agent:cody", subject: "Review rollout", reviewAt: "tomorrow", state: PromiseStateOpen, wantErr: "invalid timestamp format"},
		{name: "invalid state", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: "ready", wantErr: "invalid promise state \"ready\""},
		{name: "two targets", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen, task: &task, container: &container, wantErr: "at most one task or container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidatePromiseFields(tt.owner, tt.subject, tt.reviewAt, tt.state, tt.task, tt.container)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("valid fields rejected: %v", err)
				}
				if got != tt.wantReviewAt {
					t.Fatalf("review_at = %q, want %q", got, tt.wantReviewAt)
				}
				return
			}
			if err == nil {
				t.Fatalf("invalid fields accepted (review_at %q)", got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not name %q", err.Error(), tt.wantErr)
			}
		})
	}
}
