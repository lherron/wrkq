package wrkfapi

type watchCursor struct {
	Kind       string `json:"kind"`
	InstanceID string `json:"instanceId"`
	RunID      string `json:"runId,omitempty"`
	Seq        int64  `json:"seq"`
}
