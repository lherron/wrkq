package client

// PromiseService exposes promise lifecycle methods.
type PromiseService struct{ client *Client }

type PromiseAddOptions struct {
	OwnerPrincipalRef string
	OnBehalf          bool
	Subject           string
	ReviewQuestion    *string
	Task              string
	Container         string
	ReviewAt          string
	ReviewIn          string
	Meta              map[string]any
}

type PromiseListOptions struct {
	OwnerPrincipalRef string
	State             string
	Task              string
	Container         string
}

type PromiseRenewOptions struct {
	ReviewAt string
	ReviewIn string
	Note     *string
	IfMatch  int64
}

type PromiseResolveOptions struct {
	Note    *string
	IfMatch int64
}

func (s PromiseService) Add(opts PromiseAddOptions) (*Promise, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	params := struct {
		OwnerPrincipalRef string         `json:"ownerPrincipalRef,omitempty"`
		OnBehalf          bool           `json:"onBehalf,omitempty"`
		Subject           string         `json:"subject,omitempty"`
		ReviewQuestion    *string        `json:"reviewQuestion,omitempty"`
		Task              string         `json:"task,omitempty"`
		Container         string         `json:"container,omitempty"`
		ReviewAt          string         `json:"reviewAt,omitempty"`
		ReviewIn          string         `json:"reviewIn,omitempty"`
		Meta              map[string]any `json:"meta,omitempty"`
		PrincipalRef      string         `json:"principalRef,omitempty"`
	}{opts.OwnerPrincipalRef, opts.OnBehalf, opts.Subject, opts.ReviewQuestion, opts.Task, opts.Container, opts.ReviewAt, opts.ReviewIn, opts.Meta, principal}
	var out Promise
	if err := s.client.call("wrkq.promise.add", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s PromiseService) List(options ...PromiseListOptions) (*PromiseListResult, error) {
	opts := first(options)
	params := struct {
		OwnerPrincipalRef string `json:"ownerPrincipalRef,omitempty"`
		State             string `json:"state,omitempty"`
		Task              string `json:"task,omitempty"`
		Container         string `json:"container,omitempty"`
		PrincipalRef      string `json:"principalRef,omitempty"`
	}{opts.OwnerPrincipalRef, opts.State, opts.Task, opts.Container, s.client.principalRef}
	var out PromiseListResult
	if err := s.client.call("wrkq.promise.list", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s PromiseService) Renew(promise string, options ...PromiseRenewOptions) (*Promise, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	params := struct {
		Promise      string  `json:"promise"`
		ReviewAt     string  `json:"reviewAt,omitempty"`
		ReviewIn     string  `json:"reviewIn,omitempty"`
		Note         *string `json:"note,omitempty"`
		IfMatch      int64   `json:"ifMatch,omitempty"`
		PrincipalRef string  `json:"principalRef,omitempty"`
	}{promise, opts.ReviewAt, opts.ReviewIn, opts.Note, opts.IfMatch, principal}
	var out Promise
	if err := s.client.call("wrkq.promise.renew", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s PromiseService) Resolve(promise string, options ...PromiseResolveOptions) (*Promise, error) {
	principal, err := s.client.mutationPrincipal()
	if err != nil {
		return nil, err
	}
	opts := first(options)
	params := struct {
		Promise      string  `json:"promise"`
		Note         *string `json:"note,omitempty"`
		IfMatch      int64   `json:"ifMatch,omitempty"`
		PrincipalRef string  `json:"principalRef,omitempty"`
	}{promise, opts.Note, opts.IfMatch, principal}
	var out Promise
	if err := s.client.call("wrkq.promise.resolve", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
