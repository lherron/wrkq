package workflow

type WatchTarget struct {
	Kind       string `json:"kind"`
	Selector   string `json:"selector"`
	InstanceID string `json:"instanceId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	TaskRef    string `json:"taskRef,omitempty"`
}

type WatchSnapshot struct {
	Target   WatchTarget `json:"target"`
	Until    string      `json:"until"`
	Met      bool        `json:"met"`
	Class    string      `json:"class"`
	ExitCode int         `json:"exitCode"`
	Status   string      `json:"status,omitempty"`
	Phase    string      `json:"phase,omitempty"`
	Outcome  string      `json:"outcome,omitempty"`
	Instance *Instance   `json:"instance,omitempty"`
	Run      *Run        `json:"run,omitempty"`
}
