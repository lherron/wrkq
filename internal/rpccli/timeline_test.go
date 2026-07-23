package rpccli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRenderTimelineHumanShowsLatestOutcomeAndAmendment(t *testing.T) {
	specification := "ratified spec"
	first, amended := "first", "final"
	view := containerTimelineView{
		Entries: []timelineEntry{
			{
				Type: "task.outcome", EventID: 10,
				TaskUUID: "task-uuid", TaskID: "T-00001", TaskPath: "project/task",
				Outcome: &struct {
					Text *string `json:"text"`
				}{Text: &first},
			},
			{
				Type: "task.outcome", EventID: 11,
				TaskUUID: "task-uuid", TaskID: "T-00001", TaskPath: "project/task",
				Outcome: &struct {
					Text *string `json:"text"`
				}{Text: &amended},
			},
		},
	}
	view.Container.Path = "project"
	view.Container.Specification = &specification
	view.Campaign = &struct {
		State    string `json:"state"`
		Archived bool   `json:"archived"`
	}{State: "active"}
	view.Rollup.Terminal = 1
	view.Rollup.Total = 2

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	renderTimelineHuman(cmd, view)

	got := output.String()
	for _, want := range []string{
		"Timeline: project\n",
		"Campaign: active\n",
		"Progress: 1/2 terminal\n",
		"Specification: ratified spec\n",
		"project/task: final (amended)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeline human output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "project/task: first") {
		t.Fatalf("timeline human output retained superseded outcome:\n%s", got)
	}
}

func TestWriteTimelineJSONPreservesFullServerComposite(t *testing.T) {
	raw := []byte(`{"container":{"uuid":"container-uuid","path":"project","description":"brief"},"campaign":null,"entries":[{"type":"task.state","eventId":7,"campaignUuid":null,"containerUuid":"container-uuid","taskState":{"state":"completed","sourceEventType":"task.updated"}}],"snapshotEventId":7}`)
	var output bytes.Buffer
	if err := writeTimelineJSON(&output, raw); err != nil {
		t.Fatalf("write timeline JSON: %v", err)
	}
	for _, want := range []string{
		`"uuid": "container-uuid"`,
		`"campaignUuid": null`,
		`"containerUuid": "container-uuid"`,
		`"snapshotEventId": 7`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("timeline JSON dropped server field %q:\n%s", want, output.String())
		}
	}
}
