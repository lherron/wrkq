package wrkqapi

import (
	"reflect"
	"testing"
)

// TestTaskBlockedViewDTOFingerprint guards the `check blocked` compat projection
// shape (same rationale as TestCatViewDTOFingerprint): a field/tag drift in the
// BlockedResult-equivalent DTO is a PROTOCOL CONTRACT change and must be a
// deliberate update, not a silent regression.
func TestTaskBlockedViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqTaskBlockedView{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqTaskBlockedEntry{}))
	const want = "WrkqTaskBlockedView{task_id,task_uuid,is_blocked,blockers}\n" +
		"WrkqTaskBlockedEntry{id,uuid,slug,title,state}"
	if got != want {
		t.Errorf("check blocked DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestInboxViewDTOFingerprint guards the `check-inbox` compat list projection
// shape. The row order/tags must match the legacy inboxTask struct exactly.
func TestInboxViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqInboxView{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqInboxEntry{}))
	const want = "WrkqInboxView{items}\n" +
		"WrkqInboxEntry{type,uuid,id,slug,title,path,state,omitempty,priority,omitempty,kind,omitempty,due_at,omitempty,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,etag}"
	if got != want {
		t.Errorf("check-inbox DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}
