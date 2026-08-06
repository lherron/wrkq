package wrkfapi

import "github.com/lherron/wrkq/internal/workflow"

// SuspensionResolveParams is the RPC/CLI input to the atomic resolution command.
// The matching suspension id is the only gate; disposition is one of resume,
// close, or cancel; explanation is recorded free text; expectRevision is the
// ordinary CAS precondition.
type SuspensionResolveParams struct {
	SuspensionID   string `json:"suspensionId"`
	Disposition    string `json:"disposition"`
	Explanation    string `json:"explanation,omitempty"`
	ExpectRevision *int64 `json:"expectRevision,omitempty"`
	Role           string `json:"role,omitempty"`
	PrincipalRef   string `json:"principal_ref,omitempty"`
}

// SuspensionResolveResult is the resolution outcome: the cleared suspension id,
// the disposition applied, the instance's new state/revision, the resolution
// event id, and any disposition effects created.
type SuspensionResolveResult struct {
	Task             string                            `json:"task,omitempty"`
	InstanceID       string                            `json:"instanceId"`
	SuspensionID     string                            `json:"suspensionId"`
	Disposition      string                            `json:"disposition"`
	State            workflow.State                    `json:"state"`
	Revision         int64                             `json:"revision"`
	EventID          string                            `json:"eventId"`
	Effects          []workflow.Effect                 `json:"effects"`
	TerminalizedRuns []workflow.TerminalizedRunSummary `json:"terminalizedRuns,omitempty"`
	Instance         *workflow.Instance                `json:"instance,omitempty"`
}
