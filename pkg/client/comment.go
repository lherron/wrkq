package client

import (
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

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
	scopeRef, err := s.client.mutationScopeRef()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	var session *wrkqapi.SessionRef
	if host := scope.SessionRefFromEnv(scopeRef); host != nil {
		session = &wrkqapi.SessionRef{HostSessionID: host.HostSessionID, Generation: host.Generation}
	}
	params := struct {
		Task           string              `json:"task"`
		Body           string              `json:"body"`
		Kind           *string             `json:"kind,omitempty"`
		Meta           map[string]any      `json:"meta,omitempty"`
		Actor          string              `json:"actor,omitempty"`
		ScopeRef       string              `json:"scopeRef,omitempty"`
		Session        *wrkqapi.SessionRef `json:"session,omitempty"`
		IdempotencyKey string              `json:"idempotencyKey,omitempty"`
	}{task, body, opts.Kind, opts.Meta, principal, scopeRef, session, opts.IdempotencyKey}
	var out Comment
	if err := s.client.call("wrkq.comment.add", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
