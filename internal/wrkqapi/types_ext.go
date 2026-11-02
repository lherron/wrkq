package wrkqapi

import (
	"bytes"
	"encoding/json"
)

// ExtSubject selects exactly one public subject identity.
type ExtSubject struct {
	Task      string `json:"task,omitempty"`
	Container string `json:"container,omitempty"`
	Room      string `json:"room,omitempty"`
}
type ExtSubjectRef struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	State string `json:"state"`
}
type WrkqExt struct {
	Subject               string         `json:"subject"`
	NS                    string         `json:"ns"`
	Kind                  string         `json:"kind"`
	ID                    string         `json:"id"`
	Body                  map[string]any `json:"body"`
	ETag                  int64          `json:"etag"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt"`
	UpdatedByPrincipalRef string         `json:"updatedByPrincipalRef"`
	UpdatedByScopeRef     string         `json:"updatedByScopeRef,omitempty"`
}
type WrkqExtListItem struct {
	WrkqExt
	Subject ExtSubjectRef `json:"subject"`
}
type ExtPutParams struct {
	Subject      ExtSubject `json:"subject"`
	NS           string     `json:"ns"`
	Kind         string     `json:"kind"`
	ID           string     `json:"id"`
	Body         any        `json:"body"`
	IfMatch      *int64     `json:"ifMatch,omitempty"`
	PrincipalRef string     `json:"principalRef,omitempty"`
}
type ExtPutResult struct {
	Ext     WrkqExt `json:"ext"`
	Created bool    `json:"created"`
}
type ExtShowParams struct {
	Subject ExtSubject `json:"subject"`
	NS      string     `json:"ns"`
	Kind    string     `json:"kind"`
	ID      string     `json:"id"`
}
type ExtShowResult struct {
	Ext WrkqExt `json:"ext"`
}
type ExtDeleteParams struct {
	Subject      ExtSubject `json:"subject"`
	NS           string     `json:"ns"`
	Kind         string     `json:"kind"`
	ID           string     `json:"id"`
	IfMatch      *int64     `json:"ifMatch,omitempty"`
	PrincipalRef string     `json:"principalRef,omitempty"`
}
type ExtDeleteResult struct {
	Deleted bool `json:"deleted"`
}
type ExtListViewParams struct {
	Subject       *ExtSubject `json:"subject,omitempty"`
	NS            string      `json:"ns,omitempty"`
	Kind          string      `json:"kind,omitempty"`
	SubjectStates []string    `json:"subjectStates,omitempty"`
	Limit         int         `json:"limit,omitempty"`
	Cursor        string      `json:"cursor,omitempty"`
}
type WrkqExtListView struct {
	Items      []WrkqExtListItem `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// UnmarshalJSON preserves the numeric values in writer-owned JSON bodies.
func (p *ExtPutParams) UnmarshalJSON(data []byte) error {
	type plain ExtPutParams
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(p))
}
