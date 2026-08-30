package client

// TaskService exposes the task methods needed by external coordinators.
type TaskService struct{ client *Client }

type TaskListOptions struct {
	Path           string   `json:"path,omitempty"`
	States         []string `json:"state,omitempty"`
	Kinds          []string `json:"kind,omitempty"`
	Assignee       string   `json:"assignee,omitempty"`
	ClaimedBy      string   `json:"claimedBy,omitempty"`
	ClaimedNode    string   `json:"claimedNode,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	IncludeDeleted bool     `json:"includeDeleted,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	Direction      string   `json:"direction,omitempty"`
	Recursive      bool     `json:"recursive,omitempty"`
	Summary        bool     `json:"summary,omitempty"`
}

type TaskUpdateOptions struct {
	ExpectETag      *int64
	ClaimScope      string
	ClaimToken      string
	ClaimGeneration int64
	IdempotencyKey  string
}

func (s TaskService) Show(task string) (*Task, error) {
	var out Task
	if err := s.client.call("wrkq.task.show", struct {
		Task string `json:"task"`
	}{Task: task}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s TaskService) List(options ...TaskListOptions) (*TaskListResult, error) {
	opts := first(options)
	var out TaskListResult
	if err := s.client.call("wrkq.task.list", opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s TaskService) Update(task string, patch TaskPatch, options ...TaskUpdateOptions) (*Task, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	params := struct {
		Task            string    `json:"task"`
		Patch           TaskPatch `json:"patch"`
		ExpectETag      *int64    `json:"expectEtag,omitempty"`
		Actor           string    `json:"actor,omitempty"`
		ClaimScope      string    `json:"claimScope,omitempty"`
		ClaimToken      string    `json:"claimToken,omitempty"`
		ClaimGeneration int64     `json:"claimGeneration,omitempty"`
		IdempotencyKey  string    `json:"idempotencyKey,omitempty"`
	}{
		Task:            task,
		Patch:           patch,
		ExpectETag:      opts.ExpectETag,
		Actor:           principal,
		ClaimScope:      opts.ClaimScope,
		ClaimToken:      opts.ClaimToken,
		ClaimGeneration: opts.ClaimGeneration,
		IdempotencyKey:  opts.IdempotencyKey,
	}
	var out Task
	if err := s.client.call("wrkq.task.update", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func first[T any](values []T) T {
	var zero T
	if len(values) == 0 {
		return zero
	}
	return values[0]
}
