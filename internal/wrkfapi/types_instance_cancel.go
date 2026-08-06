package wrkfapi

import "github.com/lherron/wrkq/internal/workflow"

type InstanceCancelParams struct {
	TaskSelector   string `json:"task,omitempty"`
	InstanceID     string `json:"instanceId,omitempty"`
	ExpectRevision *int64 `json:"expectRevision,omitempty"`
	Explanation    string `json:"explanation,omitempty"`
	PrincipalRef   string `json:"principal_ref,omitempty"`
	Role           string `json:"role,omitempty"`
}

type InstanceCancelResult struct {
	Task             string                            `json:"task"`
	InstanceID       string                            `json:"instanceId"`
	State            workflow.State                    `json:"state"`
	Revision         int64                             `json:"revision"`
	EventID          string                            `json:"eventId"`
	Effects          []workflow.Effect                 `json:"effects"`
	TerminalizedRuns []workflow.TerminalizedRunSummary `json:"terminalizedRuns"`
	Instance         *workflow.Instance                `json:"instance,omitempty"`
}
