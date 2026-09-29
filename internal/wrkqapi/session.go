package wrkqapi

import "github.com/lherron/wrkq/internal/scope"

// SessionRef is caller-supplied attribution for the runtime that wrote a comment.
type SessionRef struct {
	HostSessionID string `json:"hostSessionId"`
	Generation    int64  `json:"generation"`
}

func (s SessionRef) Valid() bool {
	return scope.ValidHostSession(s.HostSessionID, s.Generation)
}
