package rpccli

import (
	"encoding/json"
	"testing"
)

func TestCompletionNoticeListsOnlyUnfinishedSubtasks(t *testing.T) {
	got, err := completionSubtaskNotice(json.RawMessage(`{"id":"T-12345","subtasks":[{"id":"T-12345.preview","state":"open"},{"id":"T-12345.diagram","state":"completed"},{"id":"T-12345.draft","state":"draft"},{"id":"T-12345.cancel","state":"cancelled"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "T-12345.preview" || got[1] != "T-12345.draft" {
		t.Fatalf("notice: %v", got)
	}
}
