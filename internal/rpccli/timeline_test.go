//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// Off a terminal, `wrkq timeline <container>` prints the server's
// wrkq.container.timelineView composite unchanged (indented): every field the
// server sent — null campaign links and the snapshot fence included — survives.
func TestTimelineJSONIsTheServerCompositeUnchanged(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.mustRun(t, "set", f.residentID, "--outcome", "shipped")
	catalog := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(catalog, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatalf("write hook catalog: %v", err)
	}
	t.Setenv("WRKF_HOOK_CATALOG", catalog)

	// The campaign and the plain project above it, by P- id.
	campaignID := f.friendlyID(t, f.campaignAUUID)
	for _, container := range []string{campaignID, f.friendlyID(t, f.projectAUUID)} {
		t.Run(container, func(t *testing.T) {
			out, err := runCampaignCLI(t, f.dbPath, "timeline", container)
			if err != nil {
				t.Fatalf("wrkq timeline %s: %v\n%s", container, err, out)
			}
			tr := inProcessTransport(t, f.dbPath)
			defer func() { _ = tr.Close() }()
			raw, err := tr.Call(t.Context(), "wrkq.container.timelineView", map[string]any{"container": container})
			if err != nil {
				t.Fatalf("timelineView %s: %v", container, err)
			}
			var cli, server any
			if err := json.Unmarshal([]byte(out), &cli); err != nil {
				t.Fatalf("decode CLI timeline: %v\n%s", err, out)
			}
			if err := json.Unmarshal(raw, &server); err != nil {
				t.Fatalf("decode server timeline: %v", err)
			}
			if !reflect.DeepEqual(cli, server) {
				t.Fatalf("CLI timeline differs from the server composite:\ncli:    %s\nserver: %s", out, raw)
			}
			if !strings.HasPrefix(out, "{\n  \"") {
				t.Fatalf("timeline JSON is not two-space indented: %q", out[:min(len(out), 40)])
			}
			// Guard against both sides being trivially empty: the snapshot fence is
			// always present, and the campaign's page carries the outcome entry.
			if !strings.Contains(out, `"snapshotEventId"`) {
				t.Fatalf("timeline JSON dropped snapshotEventId:\n%s", out)
			}
			if container == campaignID && !strings.Contains(out, `"shipped"`) {
				t.Fatalf("campaign timeline JSON lacks the outcome entry:\n%s", out)
			}
		})
	}
}
