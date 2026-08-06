package workflow

import "encoding/json"

// LedgerEntry is an immutable, machine-queryable forensic event.  Its body is
// deliberately free-form: only refs vocabulary is a platform convention.
type LedgerEntry struct {
	Seq               int64           `json:"seq"`
	UUID              string          `json:"uuid"`
	InstanceID        string          `json:"instanceId"`
	TaskID            string          `json:"taskId"`
	TS                string          `json:"ts"`
	Kind              string          `json:"kind"`
	AboutPrincipalRef string          `json:"aboutPrincipalRef"`
	WrittenBy         string          `json:"writtenBy"`
	Body              json.RawMessage `json:"body"`
}

type AppendLedgerParams struct {
	TaskID            string          `json:"taskId"`
	Kind              string          `json:"kind"`
	AboutPrincipalRef string          `json:"aboutPrincipalRef"`
	Body              json.RawMessage `json:"body,omitempty"`
	WrittenBy         string          `json:"-"`
}

type ListLedgerParams struct {
	TaskID            string `json:"taskId,omitempty"`
	AboutPrincipalRef string `json:"aboutPrincipalRef,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Since             string `json:"since,omitempty"`
	Until             string `json:"until,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	Cursor            string `json:"cursor,omitempty"`
}

type LedgerListResult struct {
	Entries    []LedgerEntry `json:"entries"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type ledgerCursor struct {
	TS         string `json:"ts"`
	InstanceID string `json:"instanceId"`
	Seq        int64  `json:"seq"`
}
