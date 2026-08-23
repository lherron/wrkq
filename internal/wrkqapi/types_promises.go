package wrkqapi

// WrkqPromiseSubjectRef identifies the optional live resource watched by a
// promise. Subject remains the durable text snapshot when this reference is
// detached or its target is purged.
type WrkqPromiseSubjectRef struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

// WrkqPromise is the stable promise resource DTO.
type WrkqPromise struct {
	UUID                  string                 `json:"uuid"`
	ID                    string                 `json:"id"`
	OwnerPrincipalRef     string                 `json:"ownerPrincipalRef"`
	Subject               string                 `json:"subject"`
	ReviewQuestion        *string                `json:"reviewQuestion,omitempty"`
	SubjectRef            *WrkqPromiseSubjectRef `json:"subjectRef"`
	ReviewAt              string                 `json:"reviewAt"`
	State                 string                 `json:"state"`
	ClosedAt              *string                `json:"closedAt,omitempty"`
	LastReviewedAt        *string                `json:"lastReviewedAt,omitempty"`
	LastReviewNote        *string                `json:"lastReviewNote,omitempty"`
	Meta                  map[string]any         `json:"meta"`
	ETag                  int64                  `json:"etag"`
	CreatedAt             string                 `json:"createdAt"`
	UpdatedAt             string                 `json:"updatedAt"`
	CreatedByPrincipalRef string                 `json:"createdByPrincipalRef"`
	UpdatedByPrincipalRef string                 `json:"updatedByPrincipalRef"`
}

type WrkqPromiseListResult struct {
	Items []WrkqPromise `json:"items"`
}

// PromiseAddParams creates an accepted promise. Exactly one of ReviewAt or
// ReviewIn is required. When OwnerPrincipalRef differs from the caller,
// OnBehalf must be true.
type PromiseAddParams struct {
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
}

type PromiseShowParams struct {
	Promise string `json:"promise"`
}

// PromiseListParams defaults OwnerPrincipalRef to the effective caller. Reads
// for an explicitly named owner are unrestricted.
type PromiseListParams struct {
	OwnerPrincipalRef string `json:"ownerPrincipalRef,omitempty"`
	State             string `json:"state,omitempty"`
	Task              string `json:"task,omitempty"`
	Container         string `json:"container,omitempty"`
	PrincipalRef      string `json:"principalRef,omitempty"`
}

type PromiseReadyParams struct {
	OwnerPrincipalRef string `json:"ownerPrincipalRef,omitempty"`
	PrincipalRef      string `json:"principalRef,omitempty"`
}

// PromiseEditParams edits content and/or the next review instant. ReviewAt and
// ReviewIn are mutually exclusive; omitting both leaves reviewAt unchanged.
type PromiseEditParams struct {
	Promise        string          `json:"promise"`
	Subject        *string         `json:"subject,omitempty"`
	ReviewQuestion *string         `json:"reviewQuestion,omitempty"`
	ReviewAt       string          `json:"reviewAt,omitempty"`
	ReviewIn       string          `json:"reviewIn,omitempty"`
	Meta           *map[string]any `json:"meta,omitempty"`
	IfMatch        int64           `json:"ifMatch,omitempty"`
	PrincipalRef   string          `json:"principalRef,omitempty"`
}

// PromiseReviewParams is used by renew, resolve, and abandon. Renew requires
// exactly one review time; resolve/abandon ignore both review fields.
type PromiseReviewParams struct {
	Promise      string  `json:"promise"`
	ReviewAt     string  `json:"reviewAt,omitempty"`
	ReviewIn     string  `json:"reviewIn,omitempty"`
	Note         *string `json:"note,omitempty"`
	IfMatch      int64   `json:"ifMatch,omitempty"`
	PrincipalRef string  `json:"principalRef,omitempty"`
}

// PromiseRetargetParams powers attach and detach. Attach requires exactly one
// of Task or Container; detach ignores both.
type PromiseRetargetParams struct {
	Promise      string `json:"promise"`
	Task         string `json:"task,omitempty"`
	Container    string `json:"container,omitempty"`
	IfMatch      int64  `json:"ifMatch,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
}

// PromiseDeleteParams supports root `wrkq rm PR-...`: absent/soft/abandon
// deliberately abandons the promise, while purge hard-deletes it.
type PromiseDeleteParams struct {
	Promise      string `json:"promise"`
	Mode         string `json:"mode,omitempty"`
	IfMatch      int64  `json:"ifMatch,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
}
