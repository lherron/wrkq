package client

// CommentService exposes task comment creation.
type CommentService struct{ client *Client }

type CommentAddOptions struct {
	Kind           *string
	Meta           map[string]any
	IdempotencyKey string
}

func (s CommentService) Add(task, body string, options ...CommentAddOptions) (*Comment, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	params := struct {
		Task           string         `json:"task"`
		Body           string         `json:"body"`
		Kind           *string        `json:"kind,omitempty"`
		Meta           map[string]any `json:"meta,omitempty"`
		Actor          string         `json:"actor,omitempty"`
		IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	}{task, body, opts.Kind, opts.Meta, principal, opts.IdempotencyKey}
	var out Comment
	if err := s.client.call("wrkq.comment.add", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
