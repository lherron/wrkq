package domain

import "testing"

func TestNormalizePromiseReviewAt(t *testing.T) {
	got, err := NormalizePromiseReviewAt("2026-08-24T00:30:00+01:00")
	if err != nil {
		t.Fatalf("NormalizePromiseReviewAt: %v", err)
	}
	if got != "2026-08-23T23:30:00Z" {
		t.Fatalf("normalized review_at = %q, want 2026-08-23T23:30:00Z", got)
	}
}

func TestValidatePromiseFields(t *testing.T) {
	task, container := "task", "container"
	tests := []struct {
		name      string
		owner     string
		subject   string
		reviewAt  string
		state     PromiseState
		task      *string
		container *string
	}{
		{name: "valid", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen},
		{name: "missing owner", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen},
		{name: "blank subject", owner: "agent:cody", subject: "  ", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen},
		{name: "invalid timestamp", owner: "agent:cody", subject: "Review rollout", reviewAt: "tomorrow", state: PromiseStateOpen},
		{name: "invalid state", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: "ready"},
		{name: "two targets", owner: "agent:cody", subject: "Review rollout", reviewAt: "2026-08-23T23:30:00Z", state: PromiseStateOpen, task: &task, container: &container},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidatePromiseFields(tt.owner, tt.subject, tt.reviewAt, tt.state, tt.task, tt.container)
			if tt.name == "valid" && err != nil {
				t.Fatalf("valid fields rejected: %v", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("invalid fields accepted")
			}
		})
	}
}
